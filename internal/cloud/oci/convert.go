package oci

import (
	"strings"

	"github.com/trustos/pulumi-ui/internal/cloud"
)

// shapeToComputeType maps an OCI wire-format Shape into the
// provider-neutral ComputeType. OCI-specific fields land in OCIExtras.
func shapeToComputeType(s Shape) cloud.ComputeType {
	ct := cloud.ComputeType{
		Name:         s.Shape,
		DisplayName:  s.Shape,
		Architecture: inferArchitecture(s.Shape),
		Sizing:       inferSizing(s),
	}
	extras := OCIExtras{
		ProcessorDescription:    s.ProcessorDescription,
		NetworkingBandwidthGbps: s.NetworkingBandwidth,
		MaxVnicAttachments:      s.MaxVnicAttachments,
	}
	if s.MemoryOptions != nil {
		extras.MemPerOCPUBounds = cloud.Range{
			Min: s.MemoryOptions.MinPerOcpuInGBs,
			Max: s.MemoryOptions.MaxPerOcpuInGBs,
		}
	}
	return ct.WithExtras(extras)
}

// imageToCloudImage maps an OCI wire-format Image into the
// provider-neutral Image. Architecture is inferred from OS vendor name
// when the image record doesn't carry it explicitly.
func imageToCloudImage(im Image) cloud.Image {
	return cloud.Image{
		ID:                     im.ID,
		DisplayName:            im.DisplayName,
		OperatingSystem:        im.OperatingSystem,
		OperatingSystemVersion: im.OperatingSystemVersion,
		OSVersion:              im.OperatingSystemVersion,
	}
}

// compartmentToNamespace maps an OCI compartment to Namespace.
func compartmentToNamespace(c Compartment) cloud.Namespace {
	return cloud.Namespace{
		ID:          c.ID,
		Name:        c.Name,
		Description: c.Description,
		ParentID:    c.CompartmentID,
	}
}

// adToZone maps an OCI availability domain to Zone.
func adToZone(a AvailabilityDomain) cloud.Zone {
	return cloud.Zone{Name: a.Name, ID: a.ID}
}

// inferArchitecture classifies an OCI shape by name. A1 family is ARM;
// everything else (E-series, Standard1, Optimized3, etc.) is x86_64.
func inferArchitecture(shape string) cloud.Architecture {
	if strings.Contains(shape, ".A1.") || strings.HasSuffix(shape, ".A1.Flex") ||
		strings.Contains(shape, ".A2.") {
		return cloud.ArchArm64
	}
	return cloud.ArchX8664
}

// inferSizing builds the tagged Sizing union. Flex shapes produce
// RangeSizing; fixed shapes produce FixedSizing. Falls back to a name
// suffix check for records that omit isFlexible.
func inferSizing(s Shape) cloud.Sizing {
	isFlex := s.IsFlexible || strings.HasSuffix(s.Shape, ".Flex")
	if isFlex && s.OcpuOptions != nil && s.MemoryOptions != nil {
		return cloud.RangeSizing{
			VCPURange: cloud.Range{
				Min: s.OcpuOptions.Min,
				Max: s.OcpuOptions.Max,
			},
			MemGiBRange: cloud.Range{
				Min: s.MemoryOptions.MinInGBs,
				Max: s.MemoryOptions.MaxInGBs,
			},
			MemPerVCPUDefault: s.MemoryOptions.DefaultPerOcpuInGBs,
		}
	}
	return cloud.FixedSizing{
		VCPU:   s.Ocpus,
		MemGiB: s.MemoryInGBs,
	}
}

// isFlexShape is the pure name-suffix heuristic used by the template
// helper (synchronous, no metadata lookup). OCI's .Flex family is the
// only shape family that accepts shapeConfig.
func isFlexShape(name string) bool {
	return strings.HasSuffix(name, ".Flex")
}

// ShapesToComputeTypes is the exported bulk converter used by handlers
// that call the OCI client directly and want to return provider-neutral
// JSON to the UI.
func ShapesToComputeTypes(shapes []Shape) []cloud.ComputeType {
	out := make([]cloud.ComputeType, 0, len(shapes))
	seen := map[string]struct{}{}
	for _, s := range shapes {
		if _, dup := seen[s.Shape]; dup {
			continue
		}
		seen[s.Shape] = struct{}{}
		out = append(out, shapeToComputeType(s))
	}
	return out
}

// ImagesToCloudImages is the bulk converter for OCI Image records.
func ImagesToCloudImages(imgs []Image) []cloud.Image {
	out := make([]cloud.Image, 0, len(imgs))
	for _, im := range imgs {
		out = append(out, imageToCloudImage(im))
	}
	return out
}
