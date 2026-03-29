package applications

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"text/template"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	builtins "github.com/trustos/pulumi-ui/programs"

	"github.com/trustos/pulumi-ui/internal/programs"
)

// ── filterWorkloads tests ───────────────────────────────────────────────

func TestFilterWorkloads_Empty(t *testing.T) {
	result := filterWorkloads(nil, nil)
	assert.Empty(t, result)
}

func TestFilterWorkloads_OnlySelectedWorkloads(t *testing.T) {
	catalog := []programs.ApplicationDef{
		{Key: "consul", Name: "Consul", Tier: programs.TierBootstrap},
		{Key: "nomad", Name: "Nomad", Tier: programs.TierBootstrap},
		{Key: "traefik", Name: "Traefik", Tier: programs.TierWorkload},
		{Key: "grafana", Name: "Grafana", Tier: programs.TierWorkload},
		{Key: "vault", Name: "Vault", Tier: programs.TierWorkload},
	}
	selected := map[string]bool{
		"consul":  true,
		"traefik": true,
		"vault":   true,
	}

	result := filterWorkloads(catalog, selected)
	assert.Len(t, result, 2)
	assert.Equal(t, "traefik", result[0].Key)
	assert.Equal(t, "vault", result[1].Key)
}

func TestFilterWorkloads_NoneSelected(t *testing.T) {
	catalog := []programs.ApplicationDef{
		{Key: "app1", Tier: programs.TierWorkload},
		{Key: "app2", Tier: programs.TierWorkload},
	}
	result := filterWorkloads(catalog, map[string]bool{})
	assert.Empty(t, result)
}

func TestFilterWorkloads_BootstrapExcluded(t *testing.T) {
	catalog := []programs.ApplicationDef{
		{Key: "boot1", Tier: programs.TierBootstrap},
	}
	result := filterWorkloads(catalog, map[string]bool{"boot1": true})
	assert.Empty(t, result)
}

func TestNewDeployer(t *testing.T) {
	d := NewDeployer(nil, nil)
	assert.NotNil(t, d)
}

// ── Job template tests ──────────────────────────────────────────────────

func TestJobTemplateExists_GithubRunner(t *testing.T) {
	content, err := builtins.ReadJobFile("github-runner.nomad.hcl")
	require.NoError(t, err)
	assert.Contains(t, content, "github-runner")
	assert.Contains(t, content, "[[.githubToken]]")
	assert.Contains(t, content, "[[.githubRepo]]")
}

func TestJobTemplateExists_Traefik(t *testing.T) {
	content, err := builtins.ReadJobFile("traefik.nomad.hcl")
	require.NoError(t, err)
	assert.Contains(t, content, "traefik")
	assert.Contains(t, content, "[[.acmeEmail]]")
}

func TestJobTemplateExists_Postgres(t *testing.T) {
	content, err := builtins.ReadJobFile("postgres.nomad.hcl")
	require.NoError(t, err)
	assert.Contains(t, content, "postgres")
	assert.Contains(t, content, "[[.dbUser]]")
	assert.Contains(t, content, "[[.pgadminEmail]]")
}

func TestJobTemplateExists_NocoBase(t *testing.T) {
	content, err := builtins.ReadJobFile("nocobase.nomad.hcl")
	require.NoError(t, err)
	assert.Contains(t, content, "nocobase")
	assert.Contains(t, content, "[[.domain]]")
	assert.Contains(t, content, "[[.dbName]]")
}

func TestJobTemplateNotFound(t *testing.T) {
	_, err := builtins.ReadJobFile("nonexistent.nomad.hcl")
	assert.Error(t, err)
}

func TestJobTemplateRendering_GithubRunner(t *testing.T) {
	content, err := builtins.ReadJobFile("github-runner.nomad.hcl")
	require.NoError(t, err)

	tmpl, err := template.New("test").Delims("[[", "]]").Parse(content)
	require.NoError(t, err)

	data := map[string]string{
		"githubToken":  "ghp_abc123",
		"githubRepo":   "https://github.com/org/repo",
		"runnerLabels": "self-hosted,nomad",
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	require.NoError(t, err)

	rendered := buf.String()
	assert.Contains(t, rendered, "ghp_abc123")
	assert.Contains(t, rendered, "https://github.com/org/repo")
	assert.Contains(t, rendered, "self-hosted,nomad")
	assert.NotContains(t, rendered, "[[")
}

func TestJobTemplateRendering_Traefik(t *testing.T) {
	content, err := builtins.ReadJobFile("traefik.nomad.hcl")
	require.NoError(t, err)

	tmpl, err := template.New("test").Delims("[[", "]]").Parse(content)
	require.NoError(t, err)

	data := map[string]string{
		"acmeEmail": "admin@example.com",
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	require.NoError(t, err)

	rendered := buf.String()
	assert.Contains(t, rendered, "admin@example.com")
	// Nomad template expressions ({{ key "..." }}) should NOT be rendered by Go
	// They use single-brace Nomad syntax, not Go double-brace
	assert.NotContains(t, rendered, "[[.acmeEmail]]")
	// Nomad HCL heredoc syntax (<<EOF) should pass through unchanged
	assert.Contains(t, rendered, "<<EOF")
}

func TestJobTemplateRendering_Postgres(t *testing.T) {
	content, err := builtins.ReadJobFile("postgres.nomad.hcl")
	require.NoError(t, err)

	tmpl, err := template.New("test").Delims("[[", "]]").Parse(content)
	require.NoError(t, err)

	data := map[string]string{
		"dbUser":        "myuser",
		"pgadminEmail":  "admin@example.com",
		"pgadminDomain": "pgadmin.example.com",
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	require.NoError(t, err)

	rendered := buf.String()
	assert.Contains(t, rendered, "myuser")
	assert.Contains(t, rendered, "admin@example.com")
	assert.Contains(t, rendered, "pgadmin.example.com")
}

func TestJobTemplateExists_PostgresBackup(t *testing.T) {
	content, err := builtins.ReadJobFile("postgres-backup.nomad.hcl")
	require.NoError(t, err)
	assert.Contains(t, content, `job "postgres-backup"`)
	assert.Contains(t, content, "[[.backupSchedule]]")
	assert.Contains(t, content, "[[.bucketName]]")
	assert.Contains(t, content, "[[.retentionCount]]")
	assert.Contains(t, content, `type        = "batch"`)
	assert.Contains(t, content, "periodic")
}

// ── Secret auto-generation ──────────────────────────────────────────────

func TestIsSecretField(t *testing.T) {
	assert.True(t, isSecretField("dbPassword"))
	assert.True(t, isSecretField("appKey"))
	assert.True(t, isSecretField("apiSecret"))
	assert.True(t, isSecretField("githubToken"))
	assert.False(t, isSecretField("dbUser"))
	assert.False(t, isSecretField("domain"))
	assert.False(t, isSecretField("acmeEmail"))
	assert.False(t, isSecretField("backupSchedule"))
}

func TestGenerateSecret(t *testing.T) {
	s1 := generateSecret()
	s2 := generateSecret()
	assert.Len(t, s1, 32) // 16 bytes → 32 hex chars
	assert.Len(t, s2, 32)
	assert.NotEqual(t, s1, s2) // should be random
}

// ── AppConfig prefix extraction ─────────────────────────────────────────

func TestAppConfigPrefixExtraction(t *testing.T) {
	appConfig := map[string]string{
		"github-runner.githubToken":  "ghp_abc",
		"github-runner.githubRepo":   "https://github.com/org/repo",
		"github-runner.runnerLabels": "self-hosted",
		"traefik.acmeEmail":          "admin@example.com",
	}

	// Simulate the prefix extraction logic from deployer.uploadJobFile
	prefix := "github-runner."
	data := make(map[string]string)
	for k, v := range appConfig {
		if strings.HasPrefix(k, prefix) {
			data[strings.TrimPrefix(k, prefix)] = v
		}
	}

	assert.Equal(t, "ghp_abc", data["githubToken"])
	assert.Equal(t, "https://github.com/org/repo", data["githubRepo"])
	assert.Equal(t, "self-hosted", data["runnerLabels"])
	assert.NotContains(t, data, "acmeEmail") // belongs to traefik, not github-runner
}

// ── Secret persistence ──────────────────────────────────────────────────

func TestAutoGeneratedSecretPersistedToAppConfig(t *testing.T) {
	// Simulate: appConfig has an empty password field → auto-generate should
	// write the generated value BACK into appConfig with the prefixed key.
	appConfig := map[string]string{
		"nocobase.dbUser": "admin",
		// dbPassword is missing → should be auto-generated
	}
	app := programs.ApplicationDef{
		Key: "nocobase",
		ConfigFields: []programs.ConfigField{
			{Key: "dbUser", Default: "admin"},
			{Key: "dbPassword"},
		},
	}

	// Extract data like the deployer does
	prefix := app.Key + "."
	data := make(map[string]string)
	for k, v := range appConfig {
		if strings.HasPrefix(k, prefix) {
			data[strings.TrimPrefix(k, prefix)] = v
		}
	}
	// Ensure all declared config fields exist in data (with defaults or empty)
	for _, cf := range app.ConfigFields {
		if _, ok := data[cf.Key]; !ok {
			data[cf.Key] = cf.Default
		}
	}

	// Auto-generate secrets
	for key, val := range data {
		if val == "" && isSecretField(key) {
			generated := generateSecret()
			data[key] = generated
			appConfig[prefix+key] = generated
		}
	}

	// The appConfig map should now have the generated password
	assert.NotEmpty(t, appConfig["nocobase.dbPassword"], "auto-generated secret should be persisted to appConfig")
	assert.Len(t, appConfig["nocobase.dbPassword"], 32) // hex-encoded 16 bytes
	assert.Equal(t, "admin", data["dbUser"])
}

// ── Required field validation ───────────────────────────────────────────

func TestRequiredFieldValidation(t *testing.T) {
	// Simulate required field "acmeEmail" being empty
	app := programs.ApplicationDef{
		Key: "traefik",
		ConfigFields: []programs.ConfigField{
			{Key: "acmeEmail", Required: true},
		},
	}
	data := map[string]string{
		"acmeEmail": "", // empty
	}

	// Check required fields (same logic as deployer)
	var validationErr error
	for _, cf := range app.ConfigFields {
		if cf.Required && data[cf.Key] == "" {
			validationErr = fmt.Errorf("required config field %q is empty", cf.Key)
			break
		}
	}

	assert.Error(t, validationErr)
	assert.Contains(t, validationErr.Error(), "acmeEmail")
}

func TestRequiredFieldValidation_WithValue(t *testing.T) {
	app := programs.ApplicationDef{
		Key: "traefik",
		ConfigFields: []programs.ConfigField{
			{Key: "acmeEmail", Required: true},
		},
	}
	data := map[string]string{
		"acmeEmail": "admin@example.com",
	}

	var validationErr error
	for _, cf := range app.ConfigFields {
		if cf.Required && data[cf.Key] == "" {
			validationErr = fmt.Errorf("required config field %q is empty", cf.Key)
			break
		}
	}

	assert.NoError(t, validationErr)
}

// ── Exit code detection ─────────────────────────────────────────────────

func TestExitCodeDetection(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		wantErr bool
		errMsg  string
	}{
		{"success", "some output\n---EXIT:0---\n", false, ""},
		{"failure", "Error: bad\n---EXIT:1---\n", true, "code 1"},
		{"not found", "Error: cmd not found\n---EXIT:127---\n", true, "code 127"},
		{"no marker", "some output without exit code", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the exit code check logic from deployer
			full := tt.output
			var err error
			if idx := strings.Index(full, "---EXIT:"); idx >= 0 {
				marker := full[idx:]
				if end := strings.Index(marker, "---\n"); end > 8 {
					code := marker[8:end]
					if code != "0" {
						err = fmt.Errorf("nomad job run exited with code %s", code)
					}
				} else if !strings.Contains(marker, "---EXIT:0---") {
					err = fmt.Errorf("nomad job run exited with non-zero status")
				}
			}

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// ── ConsulEnv shell command generation ──────────────────────────────────

func TestConsulEnvCommandGeneration(t *testing.T) {
	// Simulate the deployer's consul env export building
	consulEnv := map[string]string{
		"NOMAD_TOKEN": "nomad/bootstrap-token",
		"DB_PASSWORD": "myapp/db-password",
	}

	var envExports string
	for envVar, kvPath := range consulEnv {
		envExports += fmt.Sprintf("export %s=$(consul kv get %s 2>/dev/null || true) && ", envVar, kvPath)
	}

	cmd := envExports + "nomad job run /opt/nomad-jobs/myapp.nomad.hcl"

	assert.Contains(t, cmd, "consul kv get nomad/bootstrap-token")
	assert.Contains(t, cmd, "consul kv get myapp/db-password")
	assert.Contains(t, cmd, "2>/dev/null || true") // optional, doesn't fail
	assert.Contains(t, cmd, "nomad job run /opt/nomad-jobs/myapp.nomad.hcl")
}

func TestIsValidShellIdentifier(t *testing.T) {
	assert.True(t, isValidShellIdentifier("NOMAD_TOKEN"))
	assert.True(t, isValidShellIdentifier("DB_PASSWORD"))
	assert.True(t, isValidShellIdentifier("_private"))
	assert.True(t, isValidShellIdentifier("x"))
	assert.False(t, isValidShellIdentifier(""))
	assert.False(t, isValidShellIdentifier("123"))     // starts with number
	assert.False(t, isValidShellIdentifier("foo bar"))  // space
	assert.False(t, isValidShellIdentifier("foo;bar"))  // semicolon
	assert.False(t, isValidShellIdentifier("$(cmd)"))   // shell injection
}

func TestIsValidKVPath(t *testing.T) {
	assert.True(t, isValidKVPath("nomad/bootstrap-token"))
	assert.True(t, isValidKVPath("postgres/adminuser"))
	assert.True(t, isValidKVPath("my.app/secret_key"))
	assert.False(t, isValidKVPath(""))
	assert.False(t, isValidKVPath("key; rm -rf /"))  // injection
	assert.False(t, isValidKVPath("key$(cmd)"))       // command substitution
	assert.False(t, isValidKVPath("key`cmd`"))         // backtick
	assert.False(t, isValidKVPath("key with spaces"))  // spaces
}

func TestConsulEnvEmpty(t *testing.T) {
	// No consulEnv → no exports, just the nomad command
	consulEnv := map[string]string{}

	var envExports string
	for envVar, kvPath := range consulEnv {
		envExports += fmt.Sprintf("export %s=$(consul kv get %s 2>/dev/null || true) && ", envVar, kvPath)
	}

	cmd := envExports + "nomad job run /opt/nomad-jobs/test.nomad.hcl"

	assert.Equal(t, "nomad job run /opt/nomad-jobs/test.nomad.hcl", cmd)
	assert.NotContains(t, cmd, "consul")
}
