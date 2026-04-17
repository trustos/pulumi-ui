package oci

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/trustos/pulumi-ui/internal/cloud"
)

func TestCheckAvailabilityDomain_ShapeInAllowedAD(t *testing.T) {
	ct := cloud.ComputeType{
		Name:                "VM.Standard.E2.1.Micro",
		AvailabilityDomains: []string{"PpGL:EU-FRANKFURT-1-AD-3"},
	}
	inst := cloud.ResourceNode{
		Name: "instance",
		Properties: map[string]any{
			"availabilityDomain": "PpGL:EU-FRANKFURT-1-AD-3",
		},
	}
	errs := checkAvailabilityDomain(inst, ct)
	assert.Empty(t, errs)
}

func TestCheckAvailabilityDomain_ShapeNotInChosenAD(t *testing.T) {
	ct := cloud.ComputeType{
		Name:                "VM.Standard.E2.1.Micro",
		AvailabilityDomains: []string{"PpGL:EU-FRANKFURT-1-AD-3"},
	}
	inst := cloud.ResourceNode{
		Name: "instance",
		Properties: map[string]any{
			"availabilityDomain": "PpGL:EU-FRANKFURT-1-AD-1",
		},
	}
	errs := checkAvailabilityDomain(inst, ct)
	if assert.Len(t, errs, 1) {
		assert.Equal(t, cloud.LevelRuntimeCompat, errs[0].Level)
		assert.Equal(t, "resources.instance.properties.availabilityDomain", errs[0].Field)
		assert.Contains(t, errs[0].Message, "VM.Standard.E2.1.Micro")
		assert.Contains(t, errs[0].Message, "AD-1")
		assert.Contains(t, errs[0].Message, "try")
		assert.Contains(t, errs[0].Message, "AD-3")
	}
}

func TestCheckAvailabilityDomain_SkipsUnknownMetadata(t *testing.T) {
	// When AvailabilityDomains is empty, don't block — metadata may be stale.
	ct := cloud.ComputeType{Name: "VM.Standard.A1.Flex"}
	inst := cloud.ResourceNode{
		Name:       "instance",
		Properties: map[string]any{"availabilityDomain": "AD-1"},
	}
	errs := checkAvailabilityDomain(inst, ct)
	assert.Empty(t, errs)
}

func TestCheckAvailabilityDomain_SkipsUnresolvedReferences(t *testing.T) {
	ct := cloud.ComputeType{
		Name:                "VM.Standard.E2.1.Micro",
		AvailabilityDomains: []string{"AD-3"},
	}
	inst := cloud.ResourceNode{
		Name:       "instance",
		Properties: map[string]any{"availabilityDomain": "${availabilityDomains[0].name}"},
	}
	errs := checkAvailabilityDomain(inst, ct)
	assert.Empty(t, errs, "unresolved Pulumi expressions are skipped")
}

func TestCheckAvailabilityDomain_SkipsEmptyAD(t *testing.T) {
	ct := cloud.ComputeType{
		Name:                "VM.Standard.E2.1.Micro",
		AvailabilityDomains: []string{"AD-3"},
	}
	inst := cloud.ResourceNode{
		Name:       "instance",
		Properties: map[string]any{"availabilityDomain": ""},
	}
	errs := checkAvailabilityDomain(inst, ct)
	assert.Empty(t, errs)
}
