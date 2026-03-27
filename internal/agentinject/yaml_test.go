package agentinject

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testAgentVars = AgentVars{
	NebulaCACert:     "test-ca-cert",
	NebulaHostCert:   "test-host-cert",
	NebulaHostKey:    "test-host-key",
	NebulaVersion:    "v1.10.3",
	AgentVersion:     "latest",
	AgentDownloadURL: "",
	AgentToken:       "test-token",
}

func TestInjectIntoYAML_SingleInstance(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      availabilityDomain: AD-1
      shape: VM.Standard.A1.Flex
      metadata:
        ssh_authorized_keys: my-key
`
	result, _, err := InjectIntoYAML(yaml, []AgentVars{testAgentVars})
	require.NoError(t, err)
	assert.Contains(t, result, "user_data")
	assert.NotEqual(t, yaml, result, "YAML should be modified")
}

func TestInjectIntoYAML_NoComputeResource(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: ocid1.compartment
      cidrBlock: 10.0.0.0/16
`
	result, _, err := InjectIntoYAML(yaml, []AgentVars{testAgentVars})
	require.NoError(t, err)
	assert.Equal(t, yaml, result, "non-compute resources should not be modified")
}

func TestInjectIntoYAML_PreservesExistingUserData(t *testing.T) {
	existing := GzipBase64([]byte("#!/bin/bash\necho existing\n"))
	yaml := `name: test
runtime: yaml
resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      metadata:
        user_data: ` + existing + "\n"

	result, _, err := InjectIntoYAML(yaml, []AgentVars{testAgentVars})
	require.NoError(t, err)
	assert.NotEqual(t, yaml, result)

	// Decode the injected user_data and verify both scripts are present
	lines := strings.Split(result, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "user_data:") {
			encoded := strings.TrimPrefix(line, "user_data: ")
			decoded, ok := DecodeUserData(encoded)
			require.True(t, ok)
			assert.Contains(t, string(decoded), "echo existing")
			assert.Contains(t, string(decoded), AgentBootstrapMarker)
			return
		}
	}
	t.Fatal("user_data not found in result")
}

func TestInjectIntoYAML_SkipsAlreadyInjected(t *testing.T) {
	agent := RenderAgentBootstrap(testAgentVars)
	existing := ComposeAndEncode([]byte("#!/bin/bash\necho existing\n"), agent)
	yaml := `name: test
runtime: yaml
resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      metadata:
        user_data: ` + existing + "\n"

	result, _, err := InjectIntoYAML(yaml, []AgentVars{testAgentVars})
	require.NoError(t, err)
	assert.Equal(t, yaml, result, "already-injected YAML should not be modified again")
}

func TestInjectIntoYAML_InstanceConfiguration(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-config:
    type: oci:Core/instanceConfiguration:InstanceConfiguration
    properties:
      compartmentId: ocid1.compartment
      instanceDetails:
        instanceType: compute
        launchDetails:
          shape: VM.Standard.A1.Flex
          metadata:
            ssh_authorized_keys: my-key
`
	result, _, err := InjectIntoYAML(yaml, []AgentVars{testAgentVars})
	require.NoError(t, err)
	assert.Contains(t, result, "user_data")
}

func TestInjectIntoYAML_NoMetadataSection(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      shape: VM.Standard.A1.Flex
`
	result, _, err := InjectIntoYAML(yaml, []AgentVars{testAgentVars})
	require.NoError(t, err)
	assert.Contains(t, result, "metadata")
	assert.Contains(t, result, "user_data")
	assert.NotEqual(t, yaml, result)

	lines := strings.Split(result, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "user_data:") {
			encoded := strings.TrimPrefix(line, "user_data: ")
			decoded, ok := DecodeUserData(encoded)
			require.True(t, ok)
			assert.Contains(t, string(decoded), AgentBootstrapMarker)
			return
		}
	}
	t.Fatal("user_data not found in result")
}

func TestInjectIntoYAML_InstanceConfig_NoMetadata(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-config:
    type: oci:Core/instanceConfiguration:InstanceConfiguration
    properties:
      compartmentId: ocid1.compartment
      instanceDetails:
        instanceType: compute
        launchDetails:
          shape: VM.Standard.A1.Flex
`
	result, _, err := InjectIntoYAML(yaml, []AgentVars{testAgentVars})
	require.NoError(t, err)
	assert.Contains(t, result, "metadata")
	assert.Contains(t, result, "user_data")
}

func TestInjectIntoYAML_InstanceConfig_NoLaunchDetails(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-config:
    type: oci:Core/instanceConfiguration:InstanceConfiguration
    properties:
      compartmentId: ocid1.compartment
      instanceDetails:
        instanceType: compute
`
	result, _, err := InjectIntoYAML(yaml, []AgentVars{testAgentVars})
	require.NoError(t, err)
	assert.Contains(t, result, "launchDetails")
	assert.Contains(t, result, "metadata")
	assert.Contains(t, result, "user_data")
}

func TestInjectIntoYAML_NoProperties(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-instance:
    type: oci:Core/instance:Instance
`
	result, _, err := InjectIntoYAML(yaml, []AgentVars{testAgentVars})
	require.NoError(t, err)
	assert.Equal(t, yaml, result, "instance without properties section cannot be injected")
}

func TestInjectIntoYAML_MetadataWithoutUserData(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      metadata:
        ssh_authorized_keys: my-key
`
	result, _, err := InjectIntoYAML(yaml, []AgentVars{testAgentVars})
	require.NoError(t, err)
	assert.Contains(t, result, "user_data")
	assert.Contains(t, result, "ssh_authorized_keys")
}

func TestInjectIntoYAML_InvalidYAML(t *testing.T) {
	result, _, err := InjectIntoYAML("not: valid: yaml: {{broken", []AgentVars{testAgentVars})
	assert.Error(t, err)
	assert.Contains(t, result, "not: valid: yaml:")
}

func TestInjectIntoYAML_MultipleComputeResources(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  node-1:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      shape: VM.Standard.A1.Flex
  node-2:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      shape: VM.Standard.A1.Flex
`
	result, _, err := InjectIntoYAML(yaml, []AgentVars{testAgentVars})
	require.NoError(t, err)
	// Both instances should have user_data injected
	count := strings.Count(result, "user_data:")
	assert.Equal(t, 2, count, "each instance should get user_data")
}

func TestInjectIntoYAML_MixedComputeTypes(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      shape: VM.Standard.A1.Flex
  my-config:
    type: oci:Core/instanceConfiguration:InstanceConfiguration
    properties:
      compartmentId: ocid1.compartment
      instanceDetails:
        instanceType: compute
        launchDetails:
          shape: VM.Standard.A1.Flex
`
	result, _, err := InjectIntoYAML(yaml, []AgentVars{testAgentVars})
	require.NoError(t, err)
	count := strings.Count(result, "user_data:")
	assert.Equal(t, 2, count, "both Instance and InstanceConfiguration should get user_data")
}

func TestInjectIntoYAML_NonComputeUntouched(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: ocid1.compartment
  my-subnet:
    type: oci:Core/subnet:Subnet
    properties:
      compartmentId: ocid1.compartment
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      shape: VM.Standard.A1.Flex
`
	result, _, err := InjectIntoYAML(yaml, []AgentVars{testAgentVars})
	require.NoError(t, err)
	count := strings.Count(result, "user_data:")
	assert.Equal(t, 1, count, "only compute resource should get user_data")
}

// ---------------------------------------------------------------------------
// Multi-node (per-node cert) injection
// ---------------------------------------------------------------------------

func TestInjectIntoYAML_MultiNodeDistinctCerts(t *testing.T) {
	// Two instances, two distinct AgentVars — each should get its own bootstrap.
	vars0 := AgentVars{NebulaCACert: "ca", NebulaHostCert: "cert-0", NebulaHostKey: "key-0", AgentToken: "tok0"}
	vars1 := AgentVars{NebulaCACert: "ca", NebulaHostCert: "cert-1", NebulaHostKey: "key-1", AgentToken: "tok1"}

	yaml := `name: test
runtime: yaml
resources:
  node-0:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
  node-1:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
`
	result, count, err := InjectIntoYAML(yaml, []AgentVars{vars0, vars1})
	require.NoError(t, err)
	assert.Equal(t, 2, count, "injected count should match number of instances")
	assert.Equal(t, 2, strings.Count(result, "user_data:"), "each node should get user_data")

	// Decode both user_data values and confirm they contain distinct certs.
	lines := strings.Split(result, "\n")
	var userDataValues []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "user_data:") {
			userDataValues = append(userDataValues, strings.TrimPrefix(line, "user_data: "))
		}
	}
	require.Len(t, userDataValues, 2)

	dec0, ok0 := DecodeUserData(userDataValues[0])
	dec1, ok1 := DecodeUserData(userDataValues[1])
	require.True(t, ok0)
	require.True(t, ok1)
	assert.Contains(t, string(dec0), "cert-0", "node-0 should have cert-0")
	assert.Contains(t, string(dec1), "cert-1", "node-1 should have cert-1")
}

func TestInjectIntoYAML_FallbackToLastCertForExtraInstances(t *testing.T) {
	// Single cert, two instances — both get the same (last) cert.
	yaml := `name: test
runtime: yaml
resources:
  node-0:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
  node-1:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
`
	result, count, err := InjectIntoYAML(yaml, []AgentVars{testAgentVars})
	require.NoError(t, err)
	assert.Equal(t, 2, count, "injected count should match number of instances")
	assert.Equal(t, 2, strings.Count(result, "user_data:"), "both instances should still get user_data")
}

func TestInjectIntoYAML_EmptyVarsList(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
`
	result, _, err := InjectIntoYAML(yaml, []AgentVars{})
	require.NoError(t, err)
	assert.Equal(t, yaml, result, "empty vars list should leave YAML unchanged")
}
