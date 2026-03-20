package programs

import (
	"fmt"
	"os"

	"github.com/pulumi/pulumi-oci/sdk/v2/go/oci/core"
	"github.com/pulumi/pulumi-oci/sdk/v2/go/oci/identity"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type TestVcnProgram struct{}

func init() { Register(&TestVcnProgram{}) }

func (p *TestVcnProgram) Name() string       { return "test-vcn" }
func (p *TestVcnProgram) DisplayName() string { return "Test VCN" }
func (p *TestVcnProgram) Description() string {
	return "Creates a compartment and VCN only — safe smoke test for OCI credentials"
}

func (p *TestVcnProgram) ConfigFields() []ConfigField {
	return []ConfigField{
		{Key: "compartmentName", Label: "Compartment Name", Type: "text",
			Required: false, Default: "test-compartment"},
		{Key: "vcnCidr", Label: "VCN CIDR", Type: "text",
			Required: false, Default: "10.0.0.0/16", Description: "CIDR block for the test VCN"},
	}
}

func (p *TestVcnProgram) Run(cfg map[string]string) pulumi.RunFunc {
	compartmentName := cfgOr(cfg, "compartmentName", "test-compartment")
	vcnCidr := cfgOr(cfg, "vcnCidr", "10.0.0.0/16")

	return func(ctx *pulumi.Context) error {
		tenancyOCID := os.Getenv("OCI_TENANCY_OCID")
		if tenancyOCID == "" {
			return fmt.Errorf("OCI_TENANCY_OCID must be set")
		}

		comp, err := identity.NewCompartment(ctx, "test-compartment", &identity.CompartmentArgs{
			CompartmentId: pulumi.String(tenancyOCID),
			Name:          pulumi.String(compartmentName),
			Description:   pulumi.String("Test compartment — safe to destroy"),
			EnableDelete:  pulumi.Bool(true),
		})
		if err != nil {
			return err
		}

		vcn, err := core.NewVcn(ctx, "test-vcn", &core.VcnArgs{
			CompartmentId: comp.ID(),
			CidrBlock:     pulumi.String(vcnCidr),
			DisplayName:   pulumi.String("test-vcn"),
			DnsLabel:      pulumi.String("testvcn"),
		})
		if err != nil {
			return err
		}

		ctx.Export("compartmentId", comp.ID())
		ctx.Export("vcnId", vcn.ID())
		return nil
	}
}

// cfgOr returns cfg[key] if set and non-empty, otherwise returns def.
func cfgOr(cfg map[string]string, key, def string) string {
	if v, ok := cfg[key]; ok && v != "" {
		return v
	}
	return def
}
