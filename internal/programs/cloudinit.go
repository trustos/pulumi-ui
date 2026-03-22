package programs

import (
	"encoding/base64"
	_ "embed"
	"strconv"
	"strings"
)

//go:embed cloudinit.sh
var cloudInitScript string

// buildCloudInit substitutes static values into the cloud-init shell script
// and returns a base64-encoded string suitable for OCI instance metadata.
// COMPARTMENT_OCID and SUBNET_OCID are resolved at boot via IMDS inside
// cloudinit.sh, so they no longer need to be injected here.
func buildCloudInit(
	ocpus, memoryGb, nodeCount int,
	nomadVersion, consulVersion string,
) string {
	replacements := map[string]string{
		"NOMAD_CLIENT_CPU":       strconv.Itoa(ocpus * 3000),
		"NOMAD_CLIENT_MEMORY":    strconv.Itoa(memoryGb*1024 - 512),
		"NOMAD_BOOTSTRAP_EXPECT": strconv.Itoa(nodeCount),
		"NOMAD_VERSION":          nomadVersion,
		"CONSUL_VERSION":         consulVersion,
	}

	result := cloudInitScript
	for k, v := range replacements {
		result = strings.ReplaceAll(result, "@@"+k+"@@", v)
	}
	return base64.StdEncoding.EncodeToString([]byte(result))
}
