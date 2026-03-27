package agentinject

import (
	_ "embed"
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
	return []byte(s)
}

// AgentBootstrapMarker is a comment placed at the top of agent_bootstrap.sh.
// Used by the YAML injector to detect whether a user_data value already
// contains the agent bootstrap (prevents double injection).
const AgentBootstrapMarker = "# PULUMI_UI_AGENT_BOOTSTRAP"
