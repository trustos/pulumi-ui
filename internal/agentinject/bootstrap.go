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
	AgentVersion     string // pulumi-ui-agent binary version (e.g. "v0.1.0")
	AgentDownloadURL string
	AgentToken       string
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
	s = strings.ReplaceAll(s, "@@AGENT_DOWNLOAD_URL@@", vars.AgentDownloadURL)
	s = strings.ReplaceAll(s, "@@AGENT_TOKEN@@", vars.AgentToken)
	return []byte(s)
}

// AgentBootstrapMarker is a comment placed at the top of agent_bootstrap.sh.
// Used by the YAML injector to detect whether a user_data value already
// contains the agent bootstrap (prevents double injection).
const AgentBootstrapMarker = "# PULUMI_UI_AGENT_BOOTSTRAP"
