package programs

import (
	"encoding/base64"
	_ "embed"
	"strconv"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

//go:embed cloudinit.sh
var cloudInitScript string

// buildCloudInit substitutes runtime values into the cloud-init shell script
// and returns a base64-encoded string suitable for OCI instance metadata.
func buildCloudInit(
	ocpus, memoryGb, nodeCount int,
	compartmentID pulumi.IDOutput,
	subnetID pulumi.IDOutput,
	nomadVersion, consulVersion string,
) pulumi.StringOutput {
	return pulumi.All(compartmentID, subnetID).ApplyT(func(args []interface{}) (string, error) {
		compID := string(args[0].(pulumi.ID))
		subID := string(args[1].(pulumi.ID))

		replacements := map[string]string{
			"NOMAD_CLIENT_CPU":       strconv.Itoa(ocpus * 3000),
			"NOMAD_CLIENT_MEMORY":    strconv.Itoa(memoryGb*1024 - 512),
			"NOMAD_BOOTSTRAP_EXPECT": strconv.Itoa(nodeCount),
			"COMPARTMENT_OCID":       compID,
			"SUBNET_OCID":            subID,
			"NOMAD_VERSION":          nomadVersion,
			"CONSUL_VERSION":         consulVersion,
		}

		result := cloudInitScript
		for k, v := range replacements {
			result = strings.ReplaceAll(result, "@@"+k+"@@", v)
		}
		return base64.StdEncoding.EncodeToString([]byte(result)), nil
	}).(pulumi.StringOutput)
}
