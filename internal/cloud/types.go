package cloud

import "encoding/json"

type Architecture string

const (
	ArchArm64 Architecture = "arm64"
	ArchX8664 Architecture = "x86_64"
)

type Range struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// Sizing is a tagged union: either FixedSizing or RangeSizing.
// UI code switches on the concrete type to decide whether cpu/memory
// inputs should be editable.
type Sizing interface {
	isSizing()
}

type FixedSizing struct {
	VCPU   float64 `json:"vcpu"`
	MemGiB float64 `json:"memGiB"`
}

func (FixedSizing) isSizing() {}

type RangeSizing struct {
	VCPURange         Range   `json:"vcpuRange"`
	MemGiBRange       Range   `json:"memGiBRange"`
	MemPerVCPUDefault float64 `json:"memPerVCPUDefault,omitempty"`
}

func (RangeSizing) isSizing() {}

// ComputeType is the provider-neutral representation of a VM shape /
// instance type. Provider-specific fields are carried in the private
// extras field and accessed via a typed helper in the provider package
// (e.g. oci.ExtrasFor).
//
// AvailabilityDomains lists the AD/AZ names (scoped to the account's
// region) where this shape is offered. Empty means "unknown" — callers
// should treat it as "available everywhere" and rely on Level-8
// runtime validation to catch mismatches after deploy time.
type ComputeType struct {
	Name                string       `json:"name"`
	DisplayName         string       `json:"displayName,omitempty"`
	Architecture        Architecture `json:"architecture,omitempty"`
	Sizing              Sizing       `json:"sizing,omitempty"`
	AvailabilityDomains []string     `json:"availabilityDomains,omitempty"`

	extras any
}

// WithExtras returns a copy of ct with extras attached. Provider
// implementations call this after parsing their wire format.
func (ct ComputeType) WithExtras(x any) ComputeType {
	ct.extras = x
	return ct
}

// ExtrasRaw returns the attached extras value. Consumers should use a
// provider-scoped typed accessor (e.g. oci.ExtrasFor(ct)) rather than
// inspecting the raw any.
func (ct ComputeType) ExtrasRaw() any { return ct.extras }

// MarshalJSON emits Sizing as a discriminated union so the frontend can
// switch on kind without reflecting over the Go type. Also emits
// legacy fields (`shape`, `processorDescription`) as back-compat aliases
// so pre-refactor frontend code keeps working without changes.
func (ct ComputeType) MarshalJSON() ([]byte, error) {
	out := struct {
		Shape                string       `json:"shape"`
		ProcessorDescription string       `json:"processorDescription,omitempty"`
		Name                 string       `json:"name"`
		DisplayName          string       `json:"displayName,omitempty"`
		Architecture         Architecture `json:"architecture,omitempty"`
		Sizing               any          `json:"sizing,omitempty"`
		AvailabilityDomains  []string     `json:"availabilityDomains,omitempty"`
		Extras               any          `json:"extras,omitempty"`
	}{
		Shape:               ct.Name,
		Name:                ct.Name,
		DisplayName:         ct.DisplayName,
		Architecture:        ct.Architecture,
		AvailabilityDomains: ct.AvailabilityDomains,
		Extras:              ct.extras,
	}
	if extras, ok := ct.extras.(interface{ GetProcessorDescription() string }); ok {
		out.ProcessorDescription = extras.GetProcessorDescription()
	}
	switch s := ct.Sizing.(type) {
	case FixedSizing:
		out.Sizing = map[string]any{"kind": "fixed", "vcpu": s.VCPU, "memGiB": s.MemGiB}
	case RangeSizing:
		out.Sizing = map[string]any{
			"kind":              "range",
			"vcpuRange":         s.VCPURange,
			"memGiBRange":       s.MemGiBRange,
			"memPerVCPUDefault": s.MemPerVCPUDefault,
		}
	}
	return json.Marshal(out)
}

type Image struct {
	ID                     string       `json:"id"`
	DisplayName            string       `json:"displayName"`
	OperatingSystem        string       `json:"operatingSystem,omitempty"`
	OperatingSystemVersion string       `json:"operatingSystemVersion,omitempty"` // back-compat alias for frontend
	OSVersion              string       `json:"osVersion,omitempty"`
	Architecture           Architecture `json:"architecture,omitempty"`
}

type Namespace struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	ParentID    string `json:"parentId,omitempty"`
}

type Zone struct {
	Name string `json:"name"`
	ID   string `json:"id,omitempty"`
}

type Region struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// Credentials is the provider-agnostic credential envelope. Providers
// interpret the Fields map in their own way (OCI expects tenancyOCID,
// userOCID, fingerprint, privateKey; AWS would expect accessKey,
// secretKey; etc.).
type Credentials struct {
	Region string
	Fields map[string]string
}

// AccountRef is the compact identifier threaded through the provider
// layer. Always carries TenantID (may be empty in single-tenant mode)
// so the Registry's cache key is tenant-aware from day one.
type AccountRef struct {
	TenantID   string
	AccountID  string
	ProviderID string
	Region     string
}
