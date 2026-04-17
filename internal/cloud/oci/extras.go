package oci

import "github.com/trustos/pulumi-ui/internal/cloud"

// OCIExtras is the OCI-specific payload attached to cloud.ComputeType
// via ComputeType.WithExtras. Consumers unwrap it with ExtrasFor.
type OCIExtras struct {
	ProcessorDescription    string      `json:"processorDescription,omitempty"`
	NetworkingBandwidthGbps float64     `json:"networkingBandwidthGbps,omitempty"`
	MaxVnicAttachments      int         `json:"maxVnicAttachments,omitempty"`
	MemPerOCPUBounds        cloud.Range `json:"memPerOCPUBounds,omitempty"`
}

// GetProcessorDescription satisfies the interface cloud.ComputeType's
// MarshalJSON looks for to emit the legacy `processorDescription` JSON
// field for back-compat with pre-refactor frontend code.
func (x OCIExtras) GetProcessorDescription() string { return x.ProcessorDescription }

// ExtrasFor returns the OCI extras attached to ct, if any.
func ExtrasFor(ct cloud.ComputeType) (OCIExtras, bool) {
	x, ok := ct.ExtrasRaw().(OCIExtras)
	return x, ok
}
