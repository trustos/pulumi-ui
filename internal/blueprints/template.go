package blueprints

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"github.com/trustos/pulumi-ui/internal/agentinject"
)

// TemplateContext is passed into every program template at render time.
type TemplateContext struct {
	Config map[string]string
}

// computeConfigRenderer is the pluggable renderer for the {{ computeConfig }}
// template helper. main.go installs the cloud.Registry-backed implementation
// via SetComputeConfigRenderer; unset it falls back to returning empty
// (which is the safe default — no shapeConfig emitted).
//
// Keeping this a package-level var rather than a Renderer struct is a
// deliberate concession: every existing RenderTemplate caller stays
// free-function. Future providers slot in at startup-registration time.
var computeConfigRenderer func(providerID, region, computeType, cpu, memGiB string) string

// SetComputeConfigRenderer installs the process-wide renderer used by the
// {{ computeConfig }} helper. Call once from main.go after building the
// cloud.Registry.
func SetComputeConfigRenderer(fn func(providerID, region, computeType, cpu, memGiB string) string) {
	computeConfigRenderer = fn
}

// buildFuncMap returns the complete template.FuncMap used for all blueprint
// rendering and validation. Keeping a single source ensures they stay in sync.
func buildFuncMap() template.FuncMap {
	fm := sprig.FuncMap()
	fm["instanceOcpus"] = templateInstanceOcpus
	fm["instanceMemoryGb"] = templateInstanceMemoryGb
	fm["cloudInit"] = templateCloudInit
	fm["groupRef"] = templateGroupRef
	fm["gzipBase64"] = templateGzipBase64
	fm["gossipKey"] = templateGossipKey
	fm["computeConfig"] = templateComputeConfig
	return fm
}

// templateComputeConfig is the body of the {{ computeConfig }} helper.
// Returns a YAML fragment like `shapeConfig: { ocpus: X, memoryInGbs: Y }`
// for providers that emit one (e.g. OCI flex shapes); empty string
// otherwise. Empty string is safe to inject inline — blueprints may
// write {{ computeConfig "oci" .Config.region .Config.shape .Config.ocpus .Config.memoryInGbs }}
// on its own line and the blank line parses cleanly as YAML.
func templateComputeConfig(providerID, region, computeType, cpu, memGiB string) string {
	if computeConfigRenderer == nil {
		return ""
	}
	return computeConfigRenderer(providerID, region, computeType, cpu, memGiB)
}

// RenderTemplate renders a Go-templated Pulumi YAML body using the supplied
// config map and the Sprig function library (same as Helm). Custom OCI helper
// functions are also registered.
func RenderTemplate(templateBody string, config map[string]string) (string, error) {
	tmpl, err := template.New("program").
		Delims("{{", "}}").
		Funcs(buildFuncMap()).
		Option("missingkey=error").
		Parse(templateBody)
	if err != nil {
		return "", fmt.Errorf("template parse: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, TemplateContext{Config: config}); err != nil {
		return "", fmt.Errorf("template render: %w", err)
	}

	return buf.String(), nil
}

// SanitizeYAML strips fn::readFile directives from a YAML body so that
// user-defined blueprints cannot read arbitrary files from the server filesystem.
func SanitizeYAML(yamlBody string) string {
	lines := strings.Split(yamlBody, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.Contains(line, "fn::readFile") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// templateInstanceOcpus returns the OCPU count for a given node index and total
// node count, mirroring the Always Free allocation strategy in nomad_cluster.go.
func templateInstanceOcpus(nodeIndex, nodeCount int) int {
	switch nodeCount {
	case 1:
		return 4
	case 2:
		return 2
	case 3:
		if nodeIndex < 2 {
			return 1
		}
		return 2
	case 4:
		return 1
	default:
		return 1
	}
}

// templateInstanceMemoryGb returns the memory (GiB) for a given node given
// that the OCI Always Free A1 pool is 24 GB total across up to 4 OCPUs.
func templateInstanceMemoryGb(nodeIndex, nodeCount int) int {
	ocpus := templateInstanceOcpus(nodeIndex, nodeCount)
	return ocpus * 6
}

// templateCloudInit renders and base64-encodes the cloud-init script for a
// single node. Uses Go template rendering (same as buildCloudInit in
// cloudinit.go). COMPARTMENT_OCID and SUBNET_OCID are resolved at boot time
// via the OCI Instance Metadata Service (IMDS) inside cloudinit.sh.
//
// If config["ocpusPerNode"] and config["memoryGbPerNode"] are set (homogeneous
// InstancePool programs), those values take precedence over the per-node
// distribution logic used by the built-in Go program.
//
// Note: for YAML programs, the agent bootstrap is NOT composed here. The engine
// injects it via post-render YAML transformation (InjectIntoYAML). This
// function produces only the program-specific cloud-init.
func templateCloudInit(nodeIndex int, config map[string]string) string {
	nodeCount, _ := strconv.Atoi(config["nodeCount"])
	if nodeCount == 0 {
		nodeCount = 1 // multi-account blueprint: single node per stack
	}
	ocpus := templateInstanceOcpus(nodeIndex, nodeCount)
	memGb := templateInstanceMemoryGb(nodeIndex, nodeCount)
	if v, err := strconv.Atoi(config["ocpusPerNode"]); err == nil && v > 0 {
		ocpus = v
	}
	if v, err := strconv.Atoi(config["ocpus"]); err == nil && v > 0 {
		ocpus = v
	}
	if v, err := strconv.Atoi(config["memoryGbPerNode"]); err == nil && v > 0 {
		memGb = v
	}
	if v, err := strconv.Atoi(config["memoryInGbs"]); err == nil && v > 0 {
		memGb = v
	}

	// Pass cluster-specific variables for multi-account join logic.
	extraVars := map[string]string{}
	for _, key := range []string{"role", "primaryPrivateIp", "gossipKey", "serverMode", "bootstrapExpect"} {
		if v := config[key]; v != "" {
			extraVars[key] = v
		}
	}

	return buildCloudInit(ocpus, memGb, nodeCount, config["nomadVersion"], config["consulVersion"], nil, extraVars, nil)
}

// templateGroupRef formats a Pulumi OCI IAM policy statement that references
// a group in either the old IDCS domain format ("Allow group Name to ...") or
// the new Identity Domain format ("Allow group 'Domain'/Name to ...").
func templateGroupRef(groupName, domain, statement string) string {
	if domain != "" {
		return fmt.Sprintf("Allow group '%s'/%s to %s", domain, groupName, statement)
	}
	return fmt.Sprintf("Allow group %s to %s", groupName, statement)
}

// templateGzipBase64 compresses a shell script with gzip and returns the
// base64-encoded result, suitable for OCI instance metadata user_data.
// Usage in YAML templates: {{ gzipBase64 "#!/bin/bash\napt update" }}
func templateGzipBase64(script string) string {
	return agentinject.GzipBase64([]byte(script))
}

// templateGossipKey generates a 32-byte random gossip encryption key,
// base64-encoded, suitable for Consul and Nomad gossip encryption.
// Equivalent to `consul keygen` or `nomad operator gossip keyring generate`.
// Usage in YAML templates: {{ gossipKey }}
func templateGossipKey() string {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("gossipKey: failed to generate random key: " + err.Error())
	}
	return base64.StdEncoding.EncodeToString(key)
}
