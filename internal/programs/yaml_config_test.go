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

// ── Integration: parse real nomad-cluster.yaml ──────────────────────────

func TestParseApplications_NomadClusterYAML(t *testing.T) {
	// Parse the actual built-in nomad-cluster.yaml to verify catalog integrity.
	builtinYAML := readBuiltinYAML(t, "nomad-cluster.yaml")
	apps := ParseApplications(builtinYAML)
	require.NotNil(t, apps, "nomad-cluster.yaml should declare applications")

	// Verify all 4 catalog apps exist
	// Use a map for order-independent check
	keySet := map[string]bool{}
	for _, a := range apps {
		keySet[a.Key] = true
	}
	assert.True(t, keySet["traefik"], "catalog should include traefik")
	assert.True(t, keySet["postgres"], "catalog should include postgres")
	assert.True(t, keySet["nocobase"], "catalog should include nocobase")
	assert.True(t, keySet["github-runner"], "catalog should include github-runner")

	// Verify dependency chain: nocobase → postgres, postgres → traefik
	nocobase := findApp(apps, "nocobase")
	require.NotNil(t, nocobase)
	assert.Contains(t, nocobase.DependsOn, "postgres")

	postgres := findApp(apps, "postgres")
	require.NotNil(t, postgres)
	assert.Contains(t, postgres.DependsOn, "traefik")

	// Traefik should be defaultOn
	traefik := findApp(apps, "traefik")
	require.NotNil(t, traefik)
	assert.True(t, traefik.DefaultOn)

	// All should be workload tier
	for _, a := range apps {
		assert.Equal(t, ApplicationTier("workload"), a.Tier, "app %s should be workload tier", a.Key)
	}

	// Each app with configFields should have at least one required field
	for _, a := range apps {
		if len(a.ConfigFields) > 0 {
			hasRequired := false
			for _, f := range a.ConfigFields {
				if f.Required {
					hasRequired = true
					break
				}
			}
			assert.True(t, hasRequired, "app %s should have at least one required config field", a.Key)
		}
	}
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
