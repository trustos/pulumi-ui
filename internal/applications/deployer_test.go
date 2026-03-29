package applications

import (
	"bytes"
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
