package blueprints

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	builtins "github.com/trustos/pulumi-ui/blueprints"
)

func TestRenderTemplate_BasicSubstitution(t *testing.T) {
	tmpl := `name: {{ .Config.name }}
runtime: yaml
resources:
  r:
    type: oci:Core/vcn:Vcn
    properties:
      displayName: {{ .Config.name }}`

	result, err := RenderTemplate(tmpl, map[string]string{"name": "test-vcn"})
	require.NoError(t, err)
	assert.Contains(t, result, "displayName: test-vcn")
	assert.Contains(t, result, "name: test-vcn")
}

func TestRenderTemplate_MissingKey(t *testing.T) {
	tmpl := `name: {{ .Config.missing }}`
	_, err := RenderTemplate(tmpl, map[string]string{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing")
}

func TestRenderTemplate_PulumiInterpolationSurvives(t *testing.T) {
	tmpl := `name: test
runtime: yaml
resources:
  subnet:
    type: oci:Core/subnet:Subnet
    properties:
      vcnId: ${my-vcn.id}`

	result, err := RenderTemplate(tmpl, map[string]string{})
	require.NoError(t, err)
	assert.Contains(t, result, "${my-vcn.id}")
}

func TestRenderTemplate_AdSetRoundRobin(t *testing.T) {
	// The new multi-AD gallery templates split `deployAds` (comma-separated)
	// into a list at render time and round-robin via index + mod + len.
	// Sprig covers every primitive — no template-helper change needed.
	tmpl := `name: multi-ad
runtime: yaml
resources:
{{- range $i := until (atoi $.Config.nodeCount) }}
  {{- $ads := splitList "," $.Config.deployAds }}
  node-{{ $i }}:
    type: oci:Core/instance:Instance
    properties:
      availabilityDomain: "{{ index $ads (mod $i (len $ads)) }}"
{{- end }}`

	result, err := RenderTemplate(tmpl, map[string]string{
		"nodeCount": "3",
		"deployAds": "AD-1,AD-3",
	})
	require.NoError(t, err)
	// Round-robin: instance 0 → AD-1, instance 1 → AD-3, instance 2 → AD-1.
	assert.Contains(t, result, "node-0")
	assert.Contains(t, result, "node-1")
	assert.Contains(t, result, "node-2")
	// Count AD occurrences to verify round-robin distribution.
	assert.Equal(t, 2, strings.Count(result, `availabilityDomain: "AD-1"`))
	assert.Equal(t, 1, strings.Count(result, `availabilityDomain: "AD-3"`))
}

func TestRenderTemplate_AdSetSingleAd(t *testing.T) {
	// 3 instances across 1 AD — all three land in the same AD.
	tmpl := `name: single-ad
runtime: yaml
resources:
{{- range $i := until (atoi $.Config.nodeCount) }}
  {{- $ads := splitList "," $.Config.deployAds }}
  node-{{ $i }}:
    type: oci:Core/instance:Instance
    properties:
      availabilityDomain: "{{ index $ads (mod $i (len $ads)) }}"
{{- end }}`

	result, err := RenderTemplate(tmpl, map[string]string{
		"nodeCount": "3",
		"deployAds": "AD-3",
	})
	require.NoError(t, err)
	assert.Equal(t, 3, strings.Count(result, `availabilityDomain: "AD-3"`))
}

func TestRenderTemplate_SprigFunctions(t *testing.T) {
	tmpl := `name: {{ upper .Config.name }}`
	result, err := RenderTemplate(tmpl, map[string]string{"name": "hello"})
	require.NoError(t, err)
	assert.Contains(t, result, "HELLO")
}

func TestApplyConfigDefaults_MergesDefaults(t *testing.T) {
	yaml := `name: test
runtime: yaml
config:
  nodeCount:
    type: integer
    default: "3"
  region:
    type: string
resources:
  r:
    type: oci:Core/vcn:Vcn
`
	cfg := map[string]string{"region": "us-ashburn-1"}
	merged := ApplyConfigDefaults(yaml, cfg)

	assert.Equal(t, "3", merged["nodeCount"])
	assert.Equal(t, "us-ashburn-1", merged["region"])
}

func TestApplyConfigDefaults_UserOverridesDefault(t *testing.T) {
	yaml := `name: test
runtime: yaml
config:
  count:
    type: integer
    default: "3"
resources:
  r:
    type: oci:Core/vcn:Vcn
`
	cfg := map[string]string{"count": "5"}
	merged := ApplyConfigDefaults(yaml, cfg)
	assert.Equal(t, "5", merged["count"])
}

// nomadClusterYAML returns the raw embedded nomad-cluster.yaml template.
func nomadClusterYAML() string {
	return builtins.ReadFile("nomad-cluster.yaml")
}

// nomadClusterDefaults returns a config map with the minimum required fields
// for the nomad-cluster template to render without errors.
func nomadClusterDefaults() map[string]string {
	return map[string]string{
		"compartmentName":        "test-compartment",
		"compartmentDescription": "test description",
		"privateSubnetCidr":      "10.0.2.0/24",
		"sshSourceCidr":          "0.0.0.0/0",
		"shape":                  "VM.Standard.A1.Flex",
		"imageId":                "ocid1.image.oc1.test",
		"ocpusPerNode":           "1",
		"memoryGbPerNode":        "6",
		"bootVolSizeGb":          "50",
		"nomadVersion":           "1.10.3",
		"consulVersion":          "1.21.3",
		"sshPublicKey":           "ssh-ed25519 AAAA test@test",
		"nodeCount":              "2",
	}
}

// TestNomadClusterYAML_NoSkipDynamicGroupConfig verifies that the
// nomad-cluster.yaml template does not contain skipDynamicGroup as a config
// key. The field was removed — dynamic group + policy are now always created.
func TestNomadClusterYAML_NoSkipDynamicGroupConfig(t *testing.T) {
	raw := nomadClusterYAML()

	// Extract the config section (between "config:" and "resources:")
	configIdx := strings.Index(raw, "\nconfig:")
	require.Greater(t, configIdx, 0, "nomad-cluster.yaml must have a config section")

	resourcesIdx := strings.Index(raw, "\nresources:")
	require.Greater(t, resourcesIdx, configIdx, "resources section must follow config")

	configSection := raw[configIdx:resourcesIdx]

	assert.NotContains(t, configSection, "skipDynamicGroup",
		"skipDynamicGroup must not appear as a config key in nomad-cluster.yaml")
}

// TestNomadClusterYAML_IAMAlwaysPresent renders the nomad-cluster template
// with default config and verifies that the dynamic group and policy resources
// are always present (not wrapped in a conditional).
func TestNomadClusterYAML_IAMAlwaysPresent(t *testing.T) {
	raw := nomadClusterYAML()
	cfg := nomadClusterDefaults()
	merged := ApplyConfigDefaults(raw, cfg)

	rendered, err := RenderTemplate(raw, merged)
	require.NoError(t, err, "nomad-cluster template must render with default config")

	assert.Contains(t, rendered, "nomad-cluster-dg",
		"rendered template must contain the dynamic group resource")
	assert.Contains(t, rendered, "nomad-cluster-policy",
		"rendered template must contain the policy resource")

	// Verify the raw template has no conditional around the IAM resources.
	// The old template wrapped them in {{- if ne .Config.skipDynamicGroup "true" }}.
	assert.NotContains(t, raw, `ne .Config.skipDynamicGroup`,
		"raw template must not conditionally gate IAM resources on skipDynamicGroup")
}
