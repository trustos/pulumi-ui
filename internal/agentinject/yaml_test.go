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
	result, err := InjectIntoYAML(yaml, testAgentVars)
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
	result, err := InjectIntoYAML(yaml, testAgentVars)
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

	result, err := InjectIntoYAML(yaml, testAgentVars)
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

	result, err := InjectIntoYAML(yaml, testAgentVars)
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
	result, err := InjectIntoYAML(yaml, testAgentVars)
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
	result, err := InjectIntoYAML(yaml, testAgentVars)
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
	result, err := InjectIntoYAML(yaml, testAgentVars)
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
	result, err := InjectIntoYAML(yaml, testAgentVars)
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
	result, err := InjectIntoYAML(yaml, testAgentVars)
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
	result, err := InjectIntoYAML(yaml, testAgentVars)
	require.NoError(t, err)
	assert.Contains(t, result, "user_data")
	assert.Contains(t, result, "ssh_authorized_keys")
}

func TestInjectIntoYAML_InvalidYAML(t *testing.T) {
	result, err := InjectIntoYAML("not: valid: yaml: {{broken", testAgentVars)
	assert.Error(t, err)
	assert.Contains(t, result, "not: valid: yaml:")
}
