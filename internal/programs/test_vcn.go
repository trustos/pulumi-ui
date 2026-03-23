package programs

import (
	"fmt"

	"github.com/pulumi/pulumi-oci/sdk/v2/go/oci/core"
	"github.com/pulumi/pulumi-oci/sdk/v2/go/oci/identity"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type TestVcnProgram struct{}

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

func (p *TestVcnProgram) ForkYAML() string {
	return `name: test-vcn-custom
runtime: yaml
description: "Forked from Test VCN"

config:
  compartmentName:
    type: string
    default: "test-compartment"
  vcnCidr:
    type: string
    default: "10.0.0.0/16"

resources:
  test-compartment:
    type: oci:Identity/compartment:Compartment
    properties:
      compartmentId: ${oci:tenancyOcid}
      name: {{ .Config.compartmentName }}
      description: "Test compartment — safe to destroy"
      enableDelete: true

  test-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: ${test-compartment.id}
      cidrBlock: {{ .Config.vcnCidr | quote }}
      displayName: "test-vcn"
      dnsLabel: "testvcn"

outputs:
  compartmentId: ${test-compartment.id}
  vcnId: ${test-vcn.id}
`
}

func (p *TestVcnProgram) Run(cfg map[string]string) pulumi.RunFunc {
	compartmentName := cfgOr(cfg, "compartmentName", "test-compartment")
	vcnCidr := cfgOr(cfg, "vcnCidr", "10.0.0.0/16")
	tenancyOCID := cfgOr(cfg, "OCI_TENANCY_OCID", "")

	return func(ctx *pulumi.Context) error {
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
