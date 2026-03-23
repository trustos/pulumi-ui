package programs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
