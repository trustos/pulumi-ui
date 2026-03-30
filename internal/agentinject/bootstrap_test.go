package agentinject

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenderAgentBootstrap_ReplacesPlaceholders(t *testing.T) {
	vars := AgentVars{
		NebulaCACert:   "ca-cert-data",
		NebulaHostCert: "host-cert-data",
		NebulaHostKey:  "host-key-data",
		NebulaVersion:  "v1.10.3",
		AgentVersion:   "v1.2.3",
		AgentToken:     "secret-token-123",
	}

	result := string(RenderAgentBootstrap(vars))

	assert.Contains(t, result, "ca-cert-data")
	assert.Contains(t, result, "host-cert-data")
	assert.Contains(t, result, "host-key-data")
	assert.Contains(t, result, "v1.10.3")
	assert.Contains(t, result, "v1.2.3")
	assert.Contains(t, result, "secret-token-123")

	assert.NotContains(t, result, "@@NEBULA_CA_CERT@@")
	assert.NotContains(t, result, "@@NEBULA_HOST_CERT@@")
	assert.NotContains(t, result, "@@NEBULA_HOST_KEY@@")
	assert.NotContains(t, result, "@@NEBULA_VERSION@@")
	assert.NotContains(t, result, "@@AGENT_VERSION@@")
	assert.NotContains(t, result, "@@AGENT_TOKEN@@")
}

func TestRenderAgentBootstrap_ContainsMarker(t *testing.T) {
	result := string(RenderAgentBootstrap(AgentVars{}))
	assert.Contains(t, result, AgentBootstrapMarker)
}

func TestRenderAgentBootstrap_InstallsNebulaBinary(t *testing.T) {
	result := string(RenderAgentBootstrap(AgentVars{NebulaVersion: "v1.10.3"}))
	assert.Contains(t, result, "nebula-linux-")
	assert.Contains(t, result, "tar xz -C /usr/local/bin -f")
	assert.Contains(t, result, "chmod +x /usr/local/bin/nebula")
}

func TestRenderAgentBootstrap_CreatesNebulaService(t *testing.T) {
	result := string(RenderAgentBootstrap(AgentVars{}))
	assert.Contains(t, result, "nebula.service")
	assert.Contains(t, result, "ExecStart=/usr/local/bin/nebula -config /etc/nebula/config.yml")
	assert.Contains(t, result, "systemctl enable nebula")
	assert.Contains(t, result, "systemctl start nebula")
}

func TestRenderAgentBootstrap_NebulaPort41820(t *testing.T) {
	result := string(RenderAgentBootstrap(AgentVars{}))
	assert.Contains(t, result, "port: 41820")
}

func TestRenderAgentBootstrap_NebulaFirewall(t *testing.T) {
	result := string(RenderAgentBootstrap(AgentVars{}))
	assert.Contains(t, result, "group: server")
	assert.Contains(t, result, "port: 41820")
	assert.Contains(t, result, "proto: tcp")
}

func TestRenderAgentBootstrap_NebulaFirewallAllowsSSHFromUserGroup(t *testing.T) {
	result := string(RenderAgentBootstrap(AgentVars{}))
	assert.Contains(t, result, "port: 22")
	assert.Contains(t, result, "group: user")
	// Verify the SSH rule is a complete inbound entry with tcp proto
	assert.Regexp(t, `(?s)inbound:.*port: 22\s+proto: tcp\s+group: user`, result)
}

func TestRenderAgentBootstrap_NebulaFirewallAllowsICMP(t *testing.T) {
	result := string(RenderAgentBootstrap(AgentVars{}))
	assert.Regexp(t, `(?s)inbound:.*port: any\s+proto: icmp\s+host: any`, result)
}

func TestRenderAgentBootstrap_NebulaFirewallHasThreeInboundRules(t *testing.T) {
	result := string(RenderAgentBootstrap(AgentVars{}))
	// Extract the inbound section (between "inbound:" and "EOF")
	inboundIdx := strings.Index(result, "inbound:")
	assert.Greater(t, inboundIdx, 0, "inbound: section must exist")
	eofIdx := strings.Index(result[inboundIdx:], "EOF")
	assert.Greater(t, eofIdx, 0, "EOF after inbound: must exist")
	inboundSection := result[inboundIdx : inboundIdx+eofIdx]

	// Count the "- port:" entries which mark individual rules
	count := strings.Count(inboundSection, "- port:")
	assert.Equal(t, 3, count, "expected exactly 3 inbound firewall rules (server:41820, user:22, icmp:any)")
}

func TestRenderAgentBootstrap_DNATRuleForDockerPorts(t *testing.T) {
	result := string(RenderAgentBootstrap(AgentVars{}))

	// The script must contain an iptables DNAT rule for forwarding Nebula
	// overlay traffic to the node's private IP (Docker dynamic ports).
	assert.Contains(t, result, "iptables -t nat")
	assert.Contains(t, result, "PREROUTING")
	assert.Contains(t, result, "-j DNAT --to-destination")

	// The rule must target the nebula1 interface specifically.
	assert.Contains(t, result, "-i nebula1")

	// The rule must exclude port 41820 (agent port stays on Nebula IP).
	assert.Contains(t, result, "! --dport 41820")

	// The rule must be idempotent: check with -C before appending with -A.
	assert.Contains(t, result, "iptables -t nat -C PREROUTING")
	assert.Contains(t, result, "iptables -t nat -A PREROUTING")
}

func TestRenderAgentBootstrap_AgentDependsOnNebula(t *testing.T) {
	result := string(RenderAgentBootstrap(AgentVars{}))
	assert.Contains(t, result, "After=network-online.target nebula.service")
}

func TestIsComputeResource(t *testing.T) {
	assert.True(t, IsComputeResource("oci:Core/instance:Instance"))
	assert.True(t, IsComputeResource("oci:Core/instanceConfiguration:InstanceConfiguration"))
	assert.False(t, IsComputeResource("oci:Core/vcn:Vcn"))
	assert.False(t, IsComputeResource("oci:Core/subnet:Subnet"))
	assert.False(t, IsComputeResource(""))
}
