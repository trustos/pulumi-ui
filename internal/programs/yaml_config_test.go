package programs

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseConfigFields_BasicTypes(t *testing.T) {
	yaml := `name: test
runtime: yaml
config:
  name:
    type: string
  count:
    type: integer
    default: "3"
  enabled:
    type: boolean
resources:
  r:
    type: oci:Core/vcn:Vcn
`
	fields, _, err := ParseConfigFields(yaml)
	require.NoError(t, err)
	require.Len(t, fields, 3)

	assert.Equal(t, "name", fields[0].Key)
	assert.Equal(t, "text", fields[0].Type)

	assert.Equal(t, "count", fields[1].Key)
	assert.Equal(t, "number", fields[1].Type)
	assert.Equal(t, "3", fields[1].Default)

	assert.Equal(t, "enabled", fields[2].Key)
	assert.Equal(t, "select", fields[2].Type)
}

func TestParseConfigFields_ConventionOverrides(t *testing.T) {
	yaml := `name: test
runtime: yaml
config:
  imageId:
    type: string
  shape:
    type: string
  sshPublicKey:
    type: string
  compartmentId:
    type: string
  availabilityDomain:
    type: string
resources:
  r:
    type: oci:Core/vcn:Vcn
`
	fields, _, err := ParseConfigFields(yaml)
	require.NoError(t, err)
	require.Len(t, fields, 5)

	assert.Equal(t, "oci-image", fields[0].Type)
	assert.Equal(t, "oci-shape", fields[1].Type)
	assert.Equal(t, "ssh-public-key", fields[2].Type)
	assert.Equal(t, "oci-compartment", fields[3].Type)
	assert.Equal(t, "oci-ad", fields[4].Type)
}

func TestParseConfigFields_UITypeOverride(t *testing.T) {
	yaml := `name: test
runtime: yaml
meta:
  fields:
    targetCompartment:
      ui_type: oci-compartment
      label: Target Compartment
    ad:
      ui_type: oci-ad
      label: Availability Domain
config:
  targetCompartment:
    type: string
  ad:
    type: string
resources:
  r:
    type: oci:Core/vcn:Vcn
`
	fields, _, err := ParseConfigFields(yaml)
	require.NoError(t, err)
	require.Len(t, fields, 2)

	assert.Equal(t, "oci-compartment", fields[0].Type)
	assert.Equal(t, "Target Compartment", fields[0].Label)
	assert.Equal(t, "oci-ad", fields[1].Type)
	assert.Equal(t, "Availability Domain", fields[1].Label)
}

func TestParseConfigFields_Groups(t *testing.T) {
	yaml := `name: test
runtime: yaml
meta:
  groups:
    - key: networking
      label: Networking
      fields: [cidr, subnetCidr]
config:
  cidr:
    type: string
  subnetCidr:
    type: string
  other:
    type: string
resources:
  r:
    type: oci:Core/vcn:Vcn
`
	fields, _, err := ParseConfigFields(yaml)
	require.NoError(t, err)

	assert.Equal(t, "networking", fields[0].Group)
	assert.Equal(t, "Networking", fields[0].GroupLabel)
	assert.Equal(t, "networking", fields[1].Group)
	assert.Equal(t, "", fields[2].Group)
}

func TestParseAgentAccess_True(t *testing.T) {
	yaml := `name: test
runtime: yaml
meta:
  agentAccess: true
config:
  name:
    type: string
resources:
  r:
    type: oci:Core/vcn:Vcn
`
	assert.True(t, ParseAgentAccess(yaml))
}

func TestParseAgentAccess_False(t *testing.T) {
	yaml := `name: test
runtime: yaml
config:
  name:
    type: string
resources:
  r:
    type: oci:Core/vcn:Vcn
`
	assert.False(t, ParseAgentAccess(yaml))
}

func TestParseAgentAccess_ExplicitFalse(t *testing.T) {
	yaml := `name: test
runtime: yaml
meta:
  agentAccess: false
resources:
  r:
    type: oci:Core/vcn:Vcn
`
	assert.False(t, ParseAgentAccess(yaml))
}

func TestStripMetaSection(t *testing.T) {
	yaml := `name: test
runtime: yaml
meta:
  groups:
    - key: g
      label: G
      fields: [a]
config:
  a:
    type: string
resources:
  r:
    type: oci:Core/vcn:Vcn
`
	stripped := stripMetaSection(yaml)
	assert.NotContains(t, stripped, "meta:")
	assert.NotContains(t, stripped, "groups:")
	assert.Contains(t, stripped, "config:")
	assert.Contains(t, stripped, "resources:")
}

// ── ParseApplications tests ─────────────────────────────────────────────

func TestParseApplications_NoMeta(t *testing.T) {
	yaml := `name: test
runtime: yaml
config:
  foo:
    type: string
resources:
  r:
    type: oci:Core/vcn:Vcn
`
	apps := ParseApplications(yaml)
	assert.Nil(t, apps)
}

func TestParseApplications_EmptyApplications(t *testing.T) {
	yaml := `name: test
runtime: yaml
meta:
  agentAccess: true
  applications: []
config:
  foo:
    type: string
resources:
  r:
    type: oci:Core/vcn:Vcn
`
	apps := ParseApplications(yaml)
	assert.Nil(t, apps)
}

func TestParseApplications_SingleApp(t *testing.T) {
	yaml := `name: test
runtime: yaml
meta:
  agentAccess: true
  applications:
    - key: github-runner
      name: GitHub Actions Runner
      description: Self-hosted runner
      tier: workload
      target: first
      required: false
      defaultOn: false
config:
  foo:
    type: string
resources:
  r:
    type: oci:Core/vcn:Vcn
`
	apps := ParseApplications(yaml)
	require.Len(t, apps, 1)
	assert.Equal(t, "github-runner", apps[0].Key)
	assert.Equal(t, "GitHub Actions Runner", apps[0].Name)
	assert.Equal(t, "Self-hosted runner", apps[0].Description)
	assert.Equal(t, ApplicationTier("workload"), apps[0].Tier)
	assert.Equal(t, TargetMode("first"), apps[0].Target)
	assert.False(t, apps[0].Required)
	assert.False(t, apps[0].DefaultOn)
}

func TestParseApplications_WithConfigFields(t *testing.T) {
	yaml := `name: test
runtime: yaml
meta:
  applications:
    - key: runner
      name: Runner
      tier: workload
      target: first
      configFields:
        - key: token
          label: Auth Token
          type: text
          required: true
          description: GitHub PAT
        - key: labels
          label: Labels
          type: text
          required: false
          default: "self-hosted"
config:
  foo:
    type: string
resources:
  r:
    type: oci:Core/vcn:Vcn
`
	apps := ParseApplications(yaml)
	require.Len(t, apps, 1)
	require.Len(t, apps[0].ConfigFields, 2)

	assert.Equal(t, "token", apps[0].ConfigFields[0].Key)
	assert.Equal(t, "Auth Token", apps[0].ConfigFields[0].Label)
	assert.Equal(t, "text", apps[0].ConfigFields[0].Type)
	assert.True(t, apps[0].ConfigFields[0].Required)
	assert.Equal(t, "GitHub PAT", apps[0].ConfigFields[0].Description)

	assert.Equal(t, "labels", apps[0].ConfigFields[1].Key)
	assert.Equal(t, "self-hosted", apps[0].ConfigFields[1].Default)
	assert.False(t, apps[0].ConfigFields[1].Required)
}

func TestParseApplications_WithDependsOn(t *testing.T) {
	yaml := `name: test
runtime: yaml
meta:
  applications:
    - key: traefik
      name: Traefik
      tier: workload
      target: first
      dependsOn: [docker, nomad]
config:
  foo:
    type: string
resources:
  r:
    type: oci:Core/vcn:Vcn
`
	apps := ParseApplications(yaml)
	require.Len(t, apps, 1)
	assert.Equal(t, []string{"docker", "nomad"}, apps[0].DependsOn)
}

func TestParseApplications_MultipleApps(t *testing.T) {
	yaml := `name: test
runtime: yaml
meta:
  applications:
    - key: app1
      name: App 1
      tier: bootstrap
      target: all
      required: true
    - key: app2
      name: App 2
      tier: workload
      target: first
      defaultOn: true
config:
  foo:
    type: string
resources:
  r:
    type: oci:Core/vcn:Vcn
`
	apps := ParseApplications(yaml)
	require.Len(t, apps, 2)
	assert.Equal(t, "app1", apps[0].Key)
	assert.True(t, apps[0].Required)
	assert.Equal(t, ApplicationTier("bootstrap"), apps[0].Tier)
	assert.Equal(t, "app2", apps[1].Key)
	assert.True(t, apps[1].DefaultOn)
	assert.Equal(t, ApplicationTier("workload"), apps[1].Tier)
}

// ── Integration: parse real program YAML files ──────────────────────────

func TestParseApplications_NomadClusterYAML(t *testing.T) {
	// nomad-cluster.yaml is pure infrastructure — no application catalog.
	builtinYAML := readBuiltinYAML(t, "nomad-cluster.yaml")
	apps := ParseApplications(builtinYAML)
	assert.Nil(t, apps, "nomad-cluster.yaml should NOT declare applications (infra only)")
}

func TestParseApplications_NomadFullStackYAML(t *testing.T) {
	// nomad-full-stack.yaml has the full application catalog.
	builtinYAML := readBuiltinYAML(t, "nomad-full-stack.yaml")
	apps := ParseApplications(builtinYAML)
	require.NotNil(t, apps, "nomad-full-stack.yaml should declare applications")

	keySet := map[string]bool{}
	for _, a := range apps {
		keySet[a.Key] = true
	}
	assert.True(t, keySet["traefik"], "catalog should include traefik")
	assert.True(t, keySet["postgres"], "catalog should include postgres")
	assert.True(t, keySet["postgres-backup"], "catalog should include postgres-backup")
	assert.True(t, keySet["nocobase"], "catalog should include nocobase")
	assert.True(t, keySet["github-runner"], "catalog should include github-runner")
	assert.True(t, keySet["pgadmin"], "catalog should include pgadmin")

	// Dependency chain
	nocobase := findApp(apps, "nocobase")
	require.NotNil(t, nocobase)
	assert.Contains(t, nocobase.DependsOn, "postgres")

	pgBackup := findApp(apps, "postgres-backup")
	require.NotNil(t, pgBackup)
	assert.Contains(t, pgBackup.DependsOn, "postgres")

	postgres := findApp(apps, "postgres")
	require.NotNil(t, postgres)
	assert.Contains(t, postgres.DependsOn, "traefik")

	// Port assignments
	traefik := findApp(apps, "traefik")
	require.NotNil(t, traefik)
	assert.True(t, traefik.DefaultOn)
	assert.Equal(t, 8080, traefik.Port)
	assert.Equal(t, 5432, postgres.Port)
	assert.Equal(t, 13000, nocobase.Port)

	// pgAdmin
	pgadmin := findApp(apps, "pgadmin")
	require.NotNil(t, pgadmin)
	assert.Equal(t, 80, pgadmin.Port)
	assert.Contains(t, pgadmin.DependsOn, "postgres")

	// postgres-backup should have a pre-destroy hook
	require.NotEmpty(t, pgBackup.Hooks, "postgres-backup should have lifecycle hooks")
	assert.Equal(t, "pre-destroy", pgBackup.Hooks[0].Trigger)
	assert.Equal(t, "agent-exec", pgBackup.Hooks[0].Type)
	assert.True(t, pgBackup.Hooks[0].ContinueOnError)

	// All should be workload tier with consulEnv
	for _, a := range apps {
		assert.Equal(t, ApplicationTier("workload"), a.Tier, "app %s should be workload tier", a.Key)
		assert.NotEmpty(t, a.ConsulEnv, "app %s should have consulEnv", a.Key)
	}
}

func TestParseApplications_WithConsulEnv(t *testing.T) {
	yaml := `name: test
runtime: yaml
meta:
  applications:
    - key: myapp
      name: My App
      tier: workload
      target: first
      consulEnv:
        NOMAD_TOKEN: "nomad/bootstrap-token"
        DB_PASSWORD: "myapp/db-password"
config:
  foo:
    type: string
resources:
  r:
    type: oci:Core/vcn:Vcn
`
	apps := ParseApplications(yaml)
	require.Len(t, apps, 1)
	require.NotNil(t, apps[0].ConsulEnv)
	assert.Equal(t, "nomad/bootstrap-token", apps[0].ConsulEnv["NOMAD_TOKEN"])
	assert.Equal(t, "myapp/db-password", apps[0].ConsulEnv["DB_PASSWORD"])
}

func TestParseApplications_Secret(t *testing.T) {
	yaml := `name: test
runtime: yaml
meta:
  applications:
    - key: myapp
      name: My App
      tier: workload
      target: first
      configFields:
        - key: dbUser
          label: DB User
          type: text
          required: true
        - key: dbPassword
          label: DB Password
          type: text
          secret: true
        - key: appKey
          label: App Key
          type: text
          secret: true
        - key: domain
          label: Domain
          type: text
config:
  foo:
    type: string
resources:
  r:
    type: oci:Core/vcn:Vcn
`
	apps := ParseApplications(yaml)
	require.Len(t, apps, 1)
	require.Len(t, apps[0].ConfigFields, 4)

	// dbUser — not secret
	assert.False(t, apps[0].ConfigFields[0].Secret, "dbUser should not be secret")
	// dbPassword — secret: true
	assert.True(t, apps[0].ConfigFields[1].Secret, "dbPassword should be secret")
	// appKey — secret: true
	assert.True(t, apps[0].ConfigFields[2].Secret, "appKey should be secret")
	// domain — not secret
	assert.False(t, apps[0].ConfigFields[3].Secret, "domain should not be secret")
}

func TestParseApplications_NoConsulEnv(t *testing.T) {
	yaml := `name: test
runtime: yaml
meta:
  applications:
    - key: simple
      name: Simple
      tier: workload
      target: first
config:
  foo:
    type: string
resources:
  r:
    type: oci:Core/vcn:Vcn
`
	apps := ParseApplications(yaml)
	require.Len(t, apps, 1)
	assert.Empty(t, apps[0].ConsulEnv)
}

func TestApplyConfigDefaults_NomadClusterYAML(t *testing.T) {
	builtinYAML := readBuiltinYAML(t, "nomad-cluster.yaml")
	minimal := map[string]string{"nodeCount": "1"}
	merged := ApplyConfigDefaults(builtinYAML, minimal)

	assert.Equal(t, "1", merged["nodeCount"], "user override should win")
	assert.Equal(t, "nomad-compartment", merged["compartmentName"], "default should be applied")
	assert.Equal(t, "10.0.0.0/16", merged["vcnCidr"], "default should be applied")
	assert.Equal(t, "VM.Standard.A1.Flex", merged["shape"], "default should be applied")
	assert.NotEmpty(t, merged["nomadVersion"], "default should be applied")
}

func TestParseAgentAccess_NomadClusterYAML(t *testing.T) {
	builtinYAML := readBuiltinYAML(t, "nomad-cluster.yaml")
	assert.True(t, ParseAgentAccess(builtinYAML), "nomad-cluster.yaml should have agentAccess: true")
}

// helpers

func readBuiltinYAML(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../../programs/" + name)
	if err != nil {
		t.Skipf("cannot read %s: %v (run tests from repo root)", name, err)
	}
	return string(b)
}

func findApp(apps []ApplicationDef, key string) *ApplicationDef {
	for i := range apps {
		if apps[i].Key == key {
			return &apps[i]
		}
	}
	return nil
}
