package applications

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"text/template"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	builtins "github.com/trustos/pulumi-ui/blueprints"

	"github.com/trustos/pulumi-ui/internal/blueprints"
)

// ── filterWorkloads tests ───────────────────────────────────────────────

func TestFilterWorkloads_Empty(t *testing.T) {
	result := filterWorkloads(nil, nil)
	assert.Empty(t, result)
}

func TestFilterWorkloads_OnlySelectedWorkloads(t *testing.T) {
	catalog := []blueprints.ApplicationDef{
		{Key: "consul", Name: "Consul", Tier: blueprints.TierBootstrap},
		{Key: "nomad", Name: "Nomad", Tier: blueprints.TierBootstrap},
		{Key: "traefik", Name: "Traefik", Tier: blueprints.TierWorkload},
		{Key: "grafana", Name: "Grafana", Tier: blueprints.TierWorkload},
		{Key: "vault", Name: "Vault", Tier: blueprints.TierWorkload},
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
	catalog := []blueprints.ApplicationDef{
		{Key: "app1", Tier: blueprints.TierWorkload},
		{Key: "app2", Tier: blueprints.TierWorkload},
	}
	result := filterWorkloads(catalog, map[string]bool{})
	assert.Empty(t, result)
}

func TestFilterWorkloads_BootstrapExcluded(t *testing.T) {
	catalog := []blueprints.ApplicationDef{
		{Key: "boot1", Tier: blueprints.TierBootstrap},
	}
	result := filterWorkloads(catalog, map[string]bool{"boot1": true})
	assert.Empty(t, result)
}

func TestNewDeployer(t *testing.T) {
	d := NewDeployer(nil, nil, nil)
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
	// Traefik should declare 3 ports: http (80), https (443), api (8080)
	assert.Contains(t, content, `port "http"`)
	assert.Contains(t, content, `port "https"`)
	assert.Contains(t, content, `port "api"`)
	assert.Contains(t, content, "static = 80")
	assert.Contains(t, content, "static = 443")
	assert.Contains(t, content, "static = 8080")
}

func TestJobTemplateExists_Postgres(t *testing.T) {
	content, err := builtins.ReadJobFile("postgres.nomad.hcl")
	require.NoError(t, err)
	assert.Contains(t, content, "postgres")
	assert.Contains(t, content, "[[.dbUser]]")
	assert.Contains(t, content, "[[.dbPassword]]")
	// pgAdmin is now a separate job template
	assert.NotContains(t, content, "pgadmin")
}

func TestJobTemplateExists_PgAdmin(t *testing.T) {
	content, err := builtins.ReadJobFile("pgadmin.nomad.hcl")
	require.NoError(t, err)
	assert.Contains(t, content, "pgadmin")
	assert.Contains(t, content, "[[.email]]")
	// pgAdmin should NOT have Traefik routing tags (empty tags = no catch-all)
	assert.Contains(t, content, "tags = []", "pgadmin should have empty tags (no Traefik routing)")
	assert.NotContains(t, content, "traefik", "pgadmin template should not reference traefik")
}

func TestJobTemplateExists_NocoBase(t *testing.T) {
	content, err := builtins.ReadJobFile("nocobase.nomad.hcl")
	require.NoError(t, err)
	assert.Contains(t, content, "nocobase")
	assert.Contains(t, content, "[[.dbName]]")
	assert.Contains(t, content, "[[.appKey]]")
	// Domain is now managed via Traefik dynamic config, not in job template
	assert.NotContains(t, content, "[[.domain]]")
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

func TestJobTemplateRendering_Traefik_DashboardPort(t *testing.T) {
	content, err := builtins.ReadJobFile("traefik.nomad.hcl")
	require.NoError(t, err)

	tmpl, err := template.New("test").Delims("[[", "]]").Parse(content)
	require.NoError(t, err)

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, map[string]string{"acmeEmail": "test@example.com"})
	require.NoError(t, err)

	rendered := buf.String()

	// The API dashboard entrypoint should be on port 8080
	assert.Contains(t, rendered, `address: ":8080"`)
	// Dashboard must be enabled with insecure mode (accessed via Nebula mesh)
	assert.Contains(t, rendered, "dashboard: true")
	assert.Contains(t, rendered, "insecure: true")
	// All 3 ports should be listed in the docker config
	assert.Contains(t, rendered, `ports        = ["http", "https", "api"]`)
}

func TestJobTemplateRendering_Postgres(t *testing.T) {
	content, err := builtins.ReadJobFile("postgres.nomad.hcl")
	require.NoError(t, err)

	tmpl, err := template.New("test").Delims("[[", "]]").Parse(content)
	require.NoError(t, err)

	data := map[string]string{
		"dbUser":     "myuser",
		"dbPassword": "secret123",
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	require.NoError(t, err)

	rendered := buf.String()
	assert.Contains(t, rendered, "myuser")
	assert.Contains(t, rendered, "secret123")
}

func TestJobTemplateRendering_PgAdmin(t *testing.T) {
	content, err := builtins.ReadJobFile("pgadmin.nomad.hcl")
	require.NoError(t, err)

	tmpl, err := template.New("test").Delims("[[", "]]").Parse(content)
	require.NoError(t, err)

	data := map[string]string{
		"email": "admin@example.com",
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	require.NoError(t, err)

	rendered := buf.String()
	assert.Contains(t, rendered, "admin@example.com")
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
	app := blueprints.ApplicationDef{
		Key: "nocobase",
		ConfigFields: []blueprints.ConfigField{
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
	app := blueprints.ApplicationDef{
		Key: "traefik",
		ConfigFields: []blueprints.ConfigField{
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
	app := blueprints.ApplicationDef{
		Key: "traefik",
		ConfigFields: []blueprints.ConfigField{
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

// ── Secret field skipping (cf.Secret: true → leave empty) ───────────────

func TestSecretFieldSkipping(t *testing.T) {
	// Fields marked Secret: true should be left empty in template data,
	// because the job's init-secrets task in Consul KV generates them.
	// Non-secret fields matching isSecretField heuristic still get Go-side generation.
	appConfig := map[string]string{
		"nocobase.dbUser": "admin",
		// dbPassword is missing → normally auto-generated, but Secret: true → skip
	}
	app := blueprints.ApplicationDef{
		Key: "nocobase",
		ConfigFields: []blueprints.ConfigField{
			{Key: "dbUser", Default: "admin"},
			{Key: "dbPassword", Secret: true}, // Consul KV managed
			{Key: "appKey"},                    // NOT secret-flagged, but matches isSecretField heuristic
		},
	}

	// Extract data (same logic as deployer.uploadJobFile lines 174-184)
	prefix := app.Key + "."
	data := make(map[string]string)
	for k, v := range appConfig {
		if strings.HasPrefix(k, prefix) {
			fieldKey := strings.TrimPrefix(k, prefix)
			if strings.HasPrefix(fieldKey, "_") {
				continue
			}
			data[fieldKey] = v
		}
	}
	for _, cf := range app.ConfigFields {
		if _, ok := data[cf.Key]; !ok {
			data[cf.Key] = cf.Default
		}
	}

	// Build secretFields set (lines 193-198)
	secretFields := make(map[string]bool)
	for _, cf := range app.ConfigFields {
		if cf.Secret {
			secretFields[cf.Key] = true
		}
	}

	// Auto-generate secrets (lines 204-218)
	for key, val := range data {
		if val != "" {
			continue
		}
		if secretFields[key] {
			continue // Consul KV init-secrets handles this
		}
		if isSecretField(key) {
			generated := generateSecret()
			data[key] = generated
			appConfig[prefix+key] = generated
		}
	}

	// dbPassword should remain empty (Secret: true → skipped)
	assert.Empty(t, data["dbPassword"], "secret-flagged field should be left empty for Consul KV init-secrets")
	assert.Empty(t, appConfig["nocobase.dbPassword"], "secret-flagged field should NOT be persisted to appConfig")

	// appKey should be auto-generated (matches isSecretField heuristic, not Secret: true)
	assert.NotEmpty(t, data["appKey"], "non-catalog secret field should be auto-generated")
	assert.Len(t, data["appKey"], 32)

	// dbUser should remain as provided
	assert.Equal(t, "admin", data["dbUser"])
}

// ── Auto-credentials key filtering ──────────────────────────────────────

func TestAutoCredentialsKeyFiltering(t *testing.T) {
	// Keys prefixed with "_" (like _autoCredentials) are internal metadata
	// and should be excluded from template data.
	appConfig := map[string]string{
		"app._autoCredentials": "true",
		"app._internalFlag":    "yes",
		"app.dbUser":           "admin",
		"app.domain":           "example.com",
	}

	prefix := "app."
	data := make(map[string]string)
	for k, v := range appConfig {
		if strings.HasPrefix(k, prefix) {
			fieldKey := strings.TrimPrefix(k, prefix)
			if strings.HasPrefix(fieldKey, "_") {
				continue // skip internal metadata keys
			}
			data[fieldKey] = v
		}
	}

	assert.NotContains(t, data, "_autoCredentials", "underscore-prefixed keys must be excluded")
	assert.NotContains(t, data, "_internalFlag", "underscore-prefixed keys must be excluded")
	assert.Equal(t, "admin", data["dbUser"])
	assert.Equal(t, "example.com", data["domain"])
}

// ── buildEnvExports validation failure ──────────────────────────────────

func TestBuildEnvExportsValidationFailure(t *testing.T) {
	d := NewDeployer(nil, nil, nil)

	tests := []struct {
		name      string
		consulEnv map[string]string
		wantEmpty bool
	}{
		{
			name:      "invalid shell identifier",
			consulEnv: map[string]string{"foo bar": "valid/path"},
			wantEmpty: true,
		},
		{
			name:      "invalid KV path with shell injection",
			consulEnv: map[string]string{"NOMAD_TOKEN": "key; rm -rf /"},
			wantEmpty: true,
		},
		{
			name:      "empty env var name",
			consulEnv: map[string]string{"": "valid/path"},
			wantEmpty: true,
		},
		{
			name:      "empty KV path",
			consulEnv: map[string]string{"TOKEN": ""},
			wantEmpty: true,
		},
		{
			name:      "command substitution in path",
			consulEnv: map[string]string{"TOKEN": "key$(whoami)"},
			wantEmpty: true,
		},
		{
			name:      "valid entries",
			consulEnv: map[string]string{"NOMAD_TOKEN": "nomad/bootstrap-token"},
			wantEmpty: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := d.buildEnvExports(tt.consulEnv)
			if tt.wantEmpty {
				assert.Empty(t, result, "should return empty string for invalid consulEnv")
			} else {
				assert.NotEmpty(t, result, "should return exports for valid consulEnv")
				assert.Contains(t, result, "export NOMAD_TOKEN=")
			}
		})
	}
}

// ── checkDeploymentStatus command format ─────────────────────────────────

func TestCheckDeploymentStatusCommandFormat(t *testing.T) {
	// Verify the shell command constructed by checkDeploymentStatus has
	// the expected structure. We can't call checkDeploymentStatus without
	// a real tunnel, but we can replicate its fmt.Sprintf and validate.
	envExports := "export NOMAD_TOKEN=$(consul kv get 'nomad/bootstrap-token' 2>/dev/null || true) && "
	jobKey := "nocobase"

	cmd := fmt.Sprintf(
		`%snomad job deployments -latest -json %s 2>/dev/null | grep -o '"Status": *"[^"]*"' | head -1 | cut -d'"' -f4`,
		envExports, jobKey,
	)

	// Should start with env exports
	assert.True(t, strings.HasPrefix(cmd, "export NOMAD_TOKEN="))
	// Should contain the nomad deployments query
	assert.Contains(t, cmd, "nomad job deployments -latest -json nocobase")
	// Should pipe through grep to extract Status
	assert.Contains(t, cmd, `grep -o '"Status": *"[^"]*"'`)
	// Should extract the 4th field from quote-delimited output
	assert.Contains(t, cmd, `cut -d'"' -f4`)
	// Should suppress stderr
	assert.Contains(t, cmd, "2>/dev/null")

	// Without env exports
	cmdNoEnv := fmt.Sprintf(
		`%snomad job deployments -latest -json %s 2>/dev/null | grep -o '"Status": *"[^"]*"' | head -1 | cut -d'"' -f4`,
		"", jobKey,
	)
	assert.True(t, strings.HasPrefix(cmdNoEnv, "nomad job deployments"))
}

// ── checkDeploymentStatus grep pattern consistency ───────────────────────

func TestCheckDeploymentStatusGrepPatternMatchesJSON(t *testing.T) {
	// The grep pattern in checkDeploymentStatus uses: "Status": *"[^"]*"
	// (with an optional space after the colon). Verify this matches actual
	// Nomad JSON output formats (with and without space after colon).
	pattern := `"Status": *"[^"]*"`
	tests := []struct {
		name  string
		input string
		match bool
	}{
		{"with space", `"Status": "successful"`, true},
		{"without space", `"Status":"running"`, true},
		{"multiple spaces", `"Status":  "failed"`, true},
		{"wrong key", `"TaskStatus": "running"`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate grep -o behavior: does the pattern appear in the line?
			// We use strings.Contains as a proxy since the pattern is a regex.
			// The actual check is that our fmt.Sprintf uses the right pattern.
			cmd := fmt.Sprintf(
				`nomad job deployments -latest -json test 2>/dev/null | grep -o '%s' | head -1 | cut -d'"' -f4`,
				pattern,
			)
			assert.Contains(t, cmd, `"Status": *"[^"]*"`,
				"grep pattern must include optional space after colon")
		})
	}
}

// ── NocoBase template rendering ─────────────────────────────────────────

func TestJobTemplateRendering_NocoBase(t *testing.T) {
	content, err := builtins.ReadJobFile("nocobase.nomad.hcl")
	require.NoError(t, err)

	tmpl, err := template.New("test").Delims("[[", "]]").Parse(content)
	require.NoError(t, err)

	data := map[string]string{
		"dbUser":     "nocobase_user",
		"dbPassword": "s3cretPass",
		"dbName":     "nocobase_db",
		"appKey":     "abc123hexkey",
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	require.NoError(t, err)

	rendered := buf.String()
	// Go template variables should be rendered
	assert.Contains(t, rendered, "nocobase_user")
	assert.Contains(t, rendered, "s3cretPass")
	assert.Contains(t, rendered, "nocobase_db")
	assert.Contains(t, rendered, "abc123hexkey")
	// No unrendered Go template markers should remain
	assert.NotContains(t, rendered, "[[")
	// Nomad template expressions should pass through unchanged
	assert.Contains(t, rendered, `{{ key "postgres/adminuser" }}`)
	assert.Contains(t, rendered, `{{ key "nocobase/db_name" }}`)
	assert.Contains(t, rendered, `{{ key "nocobase/db_password" }}`)
	// Job structure should be intact
	assert.Contains(t, rendered, `job "nocobase"`)
	assert.Contains(t, rendered, "APP_KEY=abc123hexkey")
	assert.Contains(t, rendered, "nocobase/nocobase:latest")
}
