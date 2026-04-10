package agentinject

import (
	_ "embed"
	"fmt"
	"strconv"
	"strings"
)

//go:embed agent_bootstrap.sh
var agentBootstrapScript string

// AgentVars holds the values injected into agent_bootstrap.sh at deploy time.
type AgentVars struct {
	NebulaCACert     string
	NebulaHostCert   string
	NebulaHostKey    string
	NebulaVersion    string // Nebula binary version (e.g. "v1.10.3")
	AgentVersion string // pulumi-ui-agent binary version (e.g. "v0.1.0")
	AgentToken   string
	// NebulaServerVPNIP is the pulumi-ui server's Nebula overlay IP (e.g. "10.42.13.1").
	// Always set when a Nebula PKI exists for the stack. Used so the agent can
	// add the server to its static_host_map and download the agent binary over
	// the Nebula overlay after Nebula starts.
	NebulaServerVPNIP string
	// NebulaServerRealIP is the server's public IP address (e.g. "1.2.3.4").
	// When set, the agent configures the server's Nebula UDP endpoint so the
	// agent can initiate the Nebula handshake. Derived from PULUMI_UI_EXTERNAL_URL
	// or auto-detected at startup.
	NebulaServerRealIP string
}

// RenderAgentBootstrap replaces @@PLACEHOLDER@@ markers in the embedded
// agent_bootstrap.sh with the provided values and returns the rendered script.
func RenderAgentBootstrap(vars AgentVars) []byte {
	s := agentBootstrapScript
	s = strings.ReplaceAll(s, "@@NEBULA_CA_CERT@@", vars.NebulaCACert)
	s = strings.ReplaceAll(s, "@@NEBULA_HOST_CERT@@", vars.NebulaHostCert)
	s = strings.ReplaceAll(s, "@@NEBULA_HOST_KEY@@", vars.NebulaHostKey)
	s = strings.ReplaceAll(s, "@@NEBULA_VERSION@@", vars.NebulaVersion)
	s = strings.ReplaceAll(s, "@@AGENT_VERSION@@", vars.AgentVersion)
	s = strings.ReplaceAll(s, "@@AGENT_TOKEN@@", vars.AgentToken)
	s = strings.ReplaceAll(s, "@@NEBULA_SERVER_VPN_IP@@", vars.NebulaServerVPNIP)
	s = strings.ReplaceAll(s, "@@NEBULA_SERVER_REAL_IP@@", vars.NebulaServerRealIP)
	// Single-node: pool placeholders become no-ops.
	s = strings.ReplaceAll(s, "@@POOL_NODE_COUNT@@", "1")
	s = strings.ReplaceAll(s, "@@MULTI_NODE_CERTS@@", "  : # single node — no extra certs")
	return []byte(s)
}

// RenderAgentBootstrapForPool renders the agent bootstrap script for an
// InstancePool where all instances share one user_data but need distinct
// Nebula identities. All node certs are embedded; at boot time the instance
// reads /etc/pulumi-ui-agent/node_index (written by cloud-init) and selects
// the matching cert.
func RenderAgentBootstrapForPool(allVars []AgentVars) []byte {
	if len(allVars) <= 1 {
		return RenderAgentBootstrap(allVars[0])
	}

	s := agentBootstrapScript
	// Use node-0's values for single-value placeholders (CA, server IPs, versions, token).
	v := allVars[0]
	s = strings.ReplaceAll(s, "@@NEBULA_CA_CERT@@", v.NebulaCACert)
	s = strings.ReplaceAll(s, "@@NEBULA_HOST_CERT@@", v.NebulaHostCert)
	s = strings.ReplaceAll(s, "@@NEBULA_HOST_KEY@@", v.NebulaHostKey)
	s = strings.ReplaceAll(s, "@@NEBULA_VERSION@@", v.NebulaVersion)
	s = strings.ReplaceAll(s, "@@AGENT_VERSION@@", v.AgentVersion)
	s = strings.ReplaceAll(s, "@@AGENT_TOKEN@@", v.AgentToken)
	s = strings.ReplaceAll(s, "@@NEBULA_SERVER_VPN_IP@@", v.NebulaServerVPNIP)
	s = strings.ReplaceAll(s, "@@NEBULA_SERVER_REAL_IP@@", v.NebulaServerRealIP)

	// Pool-specific placeholders.
	s = strings.ReplaceAll(s, "@@POOL_NODE_COUNT@@", strconv.Itoa(len(allVars)))

	// Build the multi-cert block: heredoc for each node's cert + key.
	var b strings.Builder
	for i, nv := range allVars {
		fmt.Fprintf(&b, "  cat > /etc/nebula/host_%d.crt <<'CERT_%d'\n%s\nCERT_%d\n\n", i, i, nv.NebulaHostCert, i)
		fmt.Fprintf(&b, "  cat > /etc/nebula/host_%d.key <<'KEY_%d'\n%s\nKEY_%d\n\n", i, i, nv.NebulaHostKey, i)
		fmt.Fprintf(&b, "  chmod 600 /etc/nebula/host_%d.key\n", i)
	}
	s = strings.ReplaceAll(s, "@@MULTI_NODE_CERTS@@", b.String())

	return []byte(s)
}

// AgentBootstrapMarker is a comment placed at the top of agent_bootstrap.sh.
// Used by the YAML injector to detect whether a user_data value already
// contains the agent bootstrap (prevents double injection).
const AgentBootstrapMarker = "# PULUMI_UI_AGENT_BOOTSTRAP"
