package agentinject

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenderAgentBootstrap_ReplacesPlaceholders(t *testing.T) {
	vars := AgentVars{
		NebulaCACert:     "ca-cert-data",
		NebulaHostCert:   "host-cert-data",
		NebulaHostKey:    "host-key-data",
		AgentVersion:     "v1.2.3",
		AgentDownloadURL: "https://example.com/agent",
		AgentToken:       "secret-token-123",
	}

	result := string(RenderAgentBootstrap(vars))

	assert.Contains(t, result, "ca-cert-data")
	assert.Contains(t, result, "host-cert-data")
	assert.Contains(t, result, "host-key-data")
	assert.Contains(t, result, "v1.2.3")
	assert.Contains(t, result, "https://example.com/agent")
	assert.Contains(t, result, "secret-token-123")

	assert.NotContains(t, result, "@@NEBULA_CA_CERT@@")
	assert.NotContains(t, result, "@@NEBULA_HOST_CERT@@")
	assert.NotContains(t, result, "@@NEBULA_HOST_KEY@@")
	assert.NotContains(t, result, "@@AGENT_VERSION@@")
	assert.NotContains(t, result, "@@AGENT_DOWNLOAD_URL@@")
	assert.NotContains(t, result, "@@AGENT_TOKEN@@")
}

func TestRenderAgentBootstrap_ContainsMarker(t *testing.T) {
	result := string(RenderAgentBootstrap(AgentVars{}))
	assert.Contains(t, result, AgentBootstrapMarker)
}

func TestIsComputeResource(t *testing.T) {
	assert.True(t, IsComputeResource("oci:Core/instance:Instance"))
	assert.True(t, IsComputeResource("oci:Core/instanceConfiguration:InstanceConfiguration"))
	assert.False(t, IsComputeResource("oci:Core/vcn:Vcn"))
	assert.False(t, IsComputeResource("oci:Core/subnet:Subnet"))
	assert.False(t, IsComputeResource(""))
}
