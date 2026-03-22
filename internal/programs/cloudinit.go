package programs

import (
	"bytes"
	"compress/gzip"
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
	// Gzip before base64: OCI metadata limit is 32 KB total; the uncompressed
	// script is ~29 KB (~39 KB base64). Gzipped it is ~8.5 KB (~11 KB base64).
	// cloud-init detects gzip via magic bytes and decompresses transparently.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.Write([]byte(result))
	gz.Close()
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}
