package programs

import (
	"bytes"
	_ "embed"
	"fmt"
	"strconv"
	"text/template"

	"github.com/trustos/pulumi-ui/internal/agentinject"
)

//go:embed cloudinit.sh
var cloudInitScript string

// CloudInitData is the template context for rendering cloudinit.sh.
type CloudInitData struct {
	Vars map[string]string // runtime variable substitutions
	Apps map[string]bool   // per-app conditionals
}

// buildCloudInit renders the cloud-init template with the given parameters and
// returns a gzip+base64 encoded string suitable for OCI instance user_data.
//
// If agentBootstrap is non-empty, the result is a multipart MIME message
// composing the program's cloud-init script with the agent bootstrap.
// The agent bootstrap is injected by the engine (not by programs directly).
func buildCloudInit(
	ocpus, memoryGb, nodeCount int,
	nomadVersion, consulVersion string,
	apps map[string]bool,
	extraVars map[string]string,
	agentBootstrap []byte,
) string {
	vars := map[string]string{
		"NOMAD_CLIENT_CPU":       strconv.Itoa(ocpus * 3000),
		"NOMAD_CLIENT_MEMORY":    strconv.Itoa(memoryGb*1024 - 512),
		"NOMAD_BOOTSTRAP_EXPECT": strconv.Itoa(nodeCount),
		"NOMAD_VERSION":          nomadVersion,
		"CONSUL_VERSION":         consulVersion,
	}
	for k, v := range extraVars {
		vars[k] = v
	}

	if apps == nil {
		apps = map[string]bool{
			"docker": true,
			"consul": true,
			"nomad":  true,
		}
	}

	data := CloudInitData{Vars: vars, Apps: apps}

	tmpl, err := template.New("cloudinit").Parse(cloudInitScript)
	if err != nil {
		panic(fmt.Sprintf("cloudinit template parse error: %v", err))
	}

	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, data); err != nil {
		panic(fmt.Sprintf("cloudinit template execute error: %v", err))
	}

	if len(agentBootstrap) > 0 {
		return agentinject.ComposeAndEncode(rendered.Bytes(), agentBootstrap)
	}
	return agentinject.GzipBase64(rendered.Bytes())
}
