package blueprints

import (
	"bytes"
	"strings"
	"testing"
	"text/template"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// renderCloudInitScript renders the embedded cloudinit.sh template with the
// given data and returns the raw script text (no gzip/base64).
func renderCloudInitScript(t *testing.T, data CloudInitData) string {
	t.Helper()

	tmpl, err := template.New("cloudinit").Parse(cloudInitScript)
	require.NoError(t, err, "cloudinit template must parse")

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	require.NoError(t, err, "cloudinit template must render")

	return buf.String()
}

// minimalCloudInitData returns a CloudInitData with the minimum fields needed
// for the template to render without errors.
func minimalCloudInitData() CloudInitData {
	return CloudInitData{
		Vars: map[string]string{
			"NOMAD_CLIENT_CPU":       "3000",
			"NOMAD_CLIENT_MEMORY":    "5632",
			"NOMAD_BOOTSTRAP_EXPECT": "1",
			"NOMAD_VERSION":          "1.7.5",
			"CONSUL_VERSION":         "1.18.1",
		},
		Apps: map[string]bool{
			"docker": true,
			"consul": true,
			"nomad":  true,
		},
	}
}

// TestCloudInit_ExecutionOrder verifies that in the main execution block,
// discover_peers is called AFTER discover_imds. The original bug had
// discover_node_ips called at the top level before setup_os, which meant
// jq wasn't installed and SUBNET_OCID wasn't set.
func TestCloudInit_ExecutionOrder(t *testing.T) {
	rendered := renderCloudInitScript(t, minimalCloudInitData())

	// Locate the "Main execution" section to focus on call order there.
	mainIdx := strings.Index(rendered, "# Main execution")
	require.Greater(t, mainIdx, 0, "rendered script must contain a Main execution section")

	mainBlock := rendered[mainIdx:]

	setupIdx := strings.Index(mainBlock, "setup_os")
	require.Greater(t, setupIdx, -1, "setup_os must appear in main block")

	waitIdx := strings.Index(mainBlock, "wait_for_network")
	require.Greater(t, waitIdx, -1, "wait_for_network must appear in main block")

	imdsIdx := strings.Index(mainBlock, "discover_imds")
	require.Greater(t, imdsIdx, -1, "discover_imds must appear in main block")

	peersIdx := strings.Index(mainBlock, "discover_peers")
	require.Greater(t, peersIdx, -1, "discover_peers must appear in main block")

	assert.Less(t, setupIdx, waitIdx, "setup_os must come before wait_for_network")
	assert.Less(t, waitIdx, imdsIdx, "wait_for_network must come before discover_imds")
	assert.Less(t, imdsIdx, peersIdx, "discover_imds must come before discover_peers")
}

// TestCloudInit_DiscoverPeersIsFunction verifies that discover_peers is defined
// as a shell function, ensuring it encapsulates discover_node_ips rather than
// running it at the top level.
func TestCloudInit_DiscoverPeersIsFunction(t *testing.T) {
	rendered := renderCloudInitScript(t, minimalCloudInitData())

	assert.Contains(t, rendered, "discover_peers() {",
		"discover_peers must be defined as a function")
}

// TestCloudInit_DiscoverNodeIpsNotTopLevel verifies that discover_node_ips is
// NOT called at the top level of the script. It should only appear inside the
// discover_peers function definition and nowhere else as a bare call.
func TestCloudInit_DiscoverNodeIpsNotTopLevel(t *testing.T) {
	rendered := renderCloudInitScript(t, minimalCloudInitData())

	// Extract the main execution block (everything after "# Main execution").
	mainIdx := strings.Index(rendered, "# Main execution")
	require.Greater(t, mainIdx, 0, "rendered script must contain a Main execution section")

	mainBlock := rendered[mainIdx:]

	// discover_node_ips must NOT appear in the main execution block.
	// It should only be called from inside the discover_peers() function body.
	assert.NotContains(t, mainBlock, "discover_node_ips",
		"discover_node_ips must not be called in the main execution block; "+
			"it should only be called from inside discover_peers()")

	// Confirm it IS present somewhere in the script (inside discover_peers).
	assert.Contains(t, rendered, "discover_node_ips",
		"discover_node_ips must exist in the script (inside discover_peers function)")
}

// TestCloudInit_DiscoverImdsUsesOciCli verifies that discover_imds resolves
// the subnet OCID by calling the OCI CLI (`oci network vnic get`) rather than
// trying to read a non-existent `subnetId` field from the IMDS JSON response.
func TestCloudInit_DiscoverImdsUsesOciCli(t *testing.T) {
	rendered := renderCloudInitScript(t, minimalCloudInitData())

	// The discover_imds function must use `oci network vnic get` to resolve
	// the subnet from the VNIC.
	assert.Contains(t, rendered, "oci network vnic get",
		"discover_imds must use OCI CLI to resolve subnet from VNIC")

	// The script must NOT try to parse .subnetId from the IMDS JSON response.
	// That field does not exist in OCI IMDS v2. The old broken approach was:
	//   jq -r '.[0].subnetId'
	// The correct approach reads vnicId from IMDS and resolves via OCI CLI.
	assert.NotContains(t, rendered, ".subnetId",
		"discover_imds must not try to read .subnetId from IMDS JSON (field does not exist)")
}
