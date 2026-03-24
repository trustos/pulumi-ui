package programs

import (
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
