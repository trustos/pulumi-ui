package oci

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadTestSchema(t *testing.T) map[string]ResourceSchema {
	t.Helper()
	data, err := os.ReadFile("testdata/sample-schema.json")
	require.NoError(t, err)
	s := parseSchema(data)
	require.NotNil(t, s)
	return s
}

func TestParseSchema_BasicResource(t *testing.T) {
	s := loadTestSchema(t)
	inst, ok := s["oci:Core/instance:Instance"]
	require.True(t, ok, "Instance resource should exist")
	assert.Equal(t, "Compute instance", inst.Description)

	assert.True(t, inst.Inputs["compartmentId"].Required)
	assert.True(t, inst.Inputs["shape"].Required)
	assert.False(t, inst.Inputs["metadata"].Required)
	assert.Equal(t, "string", inst.Inputs["compartmentId"].Type)
}

func TestParseSchema_ResolvesRef(t *testing.T) {
	s := loadTestSchema(t)
	inst := s["oci:Core/instance:Instance"]

	vnicDetails := inst.Inputs["createVnicDetails"]
	assert.Equal(t, "object", vnicDetails.Type, "should resolve $ref to object type")
	require.NotNil(t, vnicDetails.Properties, "should have sub-field properties")

	subnetId := vnicDetails.Properties["subnetId"]
	assert.Equal(t, "string", subnetId.Type)
	assert.True(t, subnetId.Required, "subnetId should be required per type definition")
	assert.Equal(t, "OCID of the subnet", subnetId.Description)

	assignPublicIp := vnicDetails.Properties["assignPublicIp"]
	assert.Equal(t, "boolean", assignPublicIp.Type)
	assert.False(t, assignPublicIp.Required)
}

func TestParseSchema_ResolvesRefWithoutExplicitType(t *testing.T) {
	s := loadTestSchema(t)
	inst := s["oci:Core/instance:Instance"]

	sourceDetails := inst.Inputs["sourceDetails"]
	assert.Equal(t, "object", sourceDetails.Type, "$ref-only property should default to object")
	require.NotNil(t, sourceDetails.Properties)

	sourceType := sourceDetails.Properties["sourceType"]
	assert.Equal(t, "string", sourceType.Type)
	assert.True(t, sourceType.Required)
}

func TestParseSchema_ResolvesArrayItemsRef(t *testing.T) {
	s := loadTestSchema(t)
	rt := s["oci:Core/routeTable:RouteTable"]

	routeRules := rt.Inputs["routeRules"]
	assert.Equal(t, "array", routeRules.Type)
	require.NotNil(t, routeRules.Items, "should have items schema")
	assert.Equal(t, "object", routeRules.Items.Type)
	require.NotNil(t, routeRules.Items.Properties)

	dest := routeRules.Items.Properties["destination"]
	assert.Equal(t, "string", dest.Type)
	assert.True(t, dest.Required)

	entityId := routeRules.Items.Properties["networkEntityId"]
	assert.Equal(t, "string", entityId.Type)
	assert.True(t, entityId.Required)
}

func TestParseSchema_ResolvesNestedRef(t *testing.T) {
	s := loadTestSchema(t)
	nsg := s["oci:Core/networkSecurityGroupSecurityRule:NetworkSecurityGroupSecurityRule"]

	tcpOpts := nsg.Inputs["tcpOptions"]
	assert.Equal(t, "object", tcpOpts.Type)
	require.NotNil(t, tcpOpts.Properties)

	destPortRange := tcpOpts.Properties["destinationPortRange"]
	assert.Equal(t, "object", destPortRange.Type)
	require.NotNil(t, destPortRange.Properties, "nested $ref should resolve to sub-fields")

	minPort := destPortRange.Properties["min"]
	assert.Equal(t, "integer", minPort.Type)
	assert.True(t, minPort.Required)

	maxPort := destPortRange.Properties["max"]
	assert.Equal(t, "integer", maxPort.Type)
	assert.True(t, maxPort.Required)
}

func TestParseSchema_OutputProperties(t *testing.T) {
	s := loadTestSchema(t)
	inst := s["oci:Core/instance:Instance"]

	require.NotNil(t, inst.Outputs)
	assert.Equal(t, "string", inst.Outputs["id"].Type)
	assert.Equal(t, "OCID of the instance", inst.Outputs["id"].Description)
	assert.Equal(t, "string", inst.Outputs["publicIp"].Type)
}

func TestParseSchema_PlainObjectNoRef(t *testing.T) {
	s := loadTestSchema(t)
	inst := s["oci:Core/instance:Instance"]

	metadata := inst.Inputs["metadata"]
	assert.Equal(t, "object", metadata.Type)
	assert.Nil(t, metadata.Properties, "plain object without $ref should have no sub-fields")
}

func TestParseSchema_InvalidJSON(t *testing.T) {
	result := parseSchema([]byte(`not json`))
	assert.Nil(t, result)
}

func TestParseSchema_EmptyResources(t *testing.T) {
	result := parseSchema([]byte(`{"resources": {}}`))
	assert.NotNil(t, result)
	assert.Len(t, result, 0)
}

func TestRefToToken(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"#/types/oci:Core/Foo:Foo", "oci:Core/Foo:Foo"},
		{"#/types/", ""},
		{"short", "short"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.expected, refToToken(tt.input), "input: %s", tt.input)
	}
}

func TestFallbackSchema_HasSubFields(t *testing.T) {
	fb := fallbackSchema()

	inst := fb["oci:Core/instance:Instance"]
	require.NotNil(t, inst.Inputs["createVnicDetails"].Properties)
	assert.Contains(t, inst.Inputs["createVnicDetails"].Properties, "subnetId")
	assert.Contains(t, inst.Inputs["createVnicDetails"].Properties, "assignPublicIp")

	require.NotNil(t, inst.Inputs["sourceDetails"].Properties)
	assert.Contains(t, inst.Inputs["sourceDetails"].Properties, "sourceType")

	require.NotNil(t, inst.Inputs["shapeConfig"].Properties)
	assert.Contains(t, inst.Inputs["shapeConfig"].Properties, "ocpus")
	assert.Contains(t, inst.Inputs["shapeConfig"].Properties, "memoryInGbs")

	rt := fb["oci:Core/routeTable:RouteTable"]
	require.NotNil(t, rt.Inputs["routeRules"].Items)
	require.NotNil(t, rt.Inputs["routeRules"].Items.Properties)
	assert.Contains(t, rt.Inputs["routeRules"].Items.Properties, "destination")
	assert.Contains(t, rt.Inputs["routeRules"].Items.Properties, "networkEntityId")

	bs := fb["oci:NetworkLoadBalancer/backendSet:BackendSet"]
	require.NotNil(t, bs.Inputs["healthChecker"].Properties)
	assert.Contains(t, bs.Inputs["healthChecker"].Properties, "protocol")
	assert.Contains(t, bs.Inputs["healthChecker"].Properties, "port")

	nsgRule := fb["oci:Core/networkSecurityGroupSecurityRule:NetworkSecurityGroupSecurityRule"]
	require.NotNil(t, nsgRule.Inputs["tcpOptions"].Properties)
	destRange := nsgRule.Inputs["tcpOptions"].Properties["destinationPortRange"]
	require.NotNil(t, destRange.Properties)
	assert.Contains(t, destRange.Properties, "min")
	assert.Contains(t, destRange.Properties, "max")
}

func TestFallbackSchema_BackwardCompatible(t *testing.T) {
	fb := fallbackSchema()
	for _, res := range fb {
		for _, prop := range res.Inputs {
			assert.NotEmpty(t, prop.Type, "every fallback property should have a type")
		}
	}
}
