package oci

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/trustos/pulumi-ui/internal/cloud"
)

func TestMergeShapesAcrossADs(t *testing.T) {
	// Flex shape available in all 3 ADs; fixed micro shape in AD-3 only.
	flex := Shape{Shape: "VM.Standard.A1.Flex", IsFlexible: true}
	micro := Shape{Shape: "VM.Standard.E2.1.Micro", Ocpus: 1, MemoryInGBs: 1}

	perAD := map[string][]Shape{
		"PpGL:EU-FRANKFURT-1-AD-1": {flex},
		"PpGL:EU-FRANKFURT-1-AD-2": {flex},
		"PpGL:EU-FRANKFURT-1-AD-3": {flex, micro},
	}

	out := mergeShapesAcrossADs(perAD)

	byName := map[string]cloud.ComputeType{}
	for _, ct := range out {
		byName[ct.Name] = ct
	}

	if assert.Contains(t, byName, "VM.Standard.A1.Flex") {
		ads := byName["VM.Standard.A1.Flex"].AvailabilityDomains
		assert.Len(t, ads, 3)
		assert.Equal(t, []string{
			"PpGL:EU-FRANKFURT-1-AD-1",
			"PpGL:EU-FRANKFURT-1-AD-2",
			"PpGL:EU-FRANKFURT-1-AD-3",
		}, ads)
	}
	if assert.Contains(t, byName, "VM.Standard.E2.1.Micro") {
		ads := byName["VM.Standard.E2.1.Micro"].AvailabilityDomains
		assert.Equal(t, []string{"PpGL:EU-FRANKFURT-1-AD-3"}, ads)
	}
}

func TestMergeShapesAcrossADs_Deduplicates(t *testing.T) {
	// Same shape returned in two ADs should appear once with both ADs.
	s := Shape{Shape: "VM.Standard.A1.Flex"}
	perAD := map[string][]Shape{
		"AD-1": {s},
		"AD-2": {s},
	}
	out := mergeShapesAcrossADs(perAD)
	assert.Len(t, out, 1)
	assert.Equal(t, []string{"AD-1", "AD-2"}, out[0].AvailabilityDomains)
}

func TestMergeShapesAcrossADs_EmptyInput(t *testing.T) {
	out := mergeShapesAcrossADs(map[string][]Shape{})
	assert.Empty(t, out)
}
