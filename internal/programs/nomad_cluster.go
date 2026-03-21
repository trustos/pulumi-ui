package programs

import (
	"fmt"
	"os"
	"strconv"

	"github.com/pulumi/pulumi-oci/sdk/v2/go/oci/core"
	"github.com/pulumi/pulumi-oci/sdk/v2/go/oci/identity"
	"github.com/pulumi/pulumi-oci/sdk/v2/go/oci/networkloadbalancer"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// ─────────────────────────────────────────────────────────────────────────────
// Registration
// ─────────────────────────────────────────────────────────────────────────────

type NomadClusterProgram struct{}

func init() { Register(&NomadClusterProgram{}) }

func (p *NomadClusterProgram) Name() string       { return "nomad-cluster" }
func (p *NomadClusterProgram) DisplayName() string { return "Nomad Cluster" }
func (p *NomadClusterProgram) Description() string {
	return "Full Nomad + Consul cluster on OCI VM.Standard.A1.Flex (Always Free eligible)"
}

func (p *NomadClusterProgram) ConfigFields() []ConfigField {
	const (
		gIAM   = "iam"
		lIAM   = "IAM & Permissions"
		gInfra = "infrastructure"
		lInfra = "Infrastructure"
		gComp  = "compute"
		lComp  = "Compute & Storage"
		gSW    = "software"
		lSW    = "Software Versions"
	)
	return []ConfigField{
		// ── IAM & Permissions ──────────────────────────────────────────────
		{Key: "skipDynamicGroup", Label: "Skip Dynamic Group", Type: "select",
			Required: false, Default: "false", Options: []string{"false", "true"},
			Description: "Set to true to skip Dynamic Group creation if your OCI user lacks tenancy-level IAM permissions",
			Group: gIAM, GroupLabel: lIAM},
		{Key: "adminGroupName", Label: "Admin IAM Group Name", Type: "text",
			Required: false,
			Description: "IAM group name of the deploying user — needed to grant permission to create Dynamic Groups and Policies (not required when Skip Dynamic Group is true)",
			Group: gIAM, GroupLabel: lIAM},
		{Key: "identityDomain", Label: "Identity Domain Name", Type: "text",
			Required: false, Default: "",
			Description: "Leave empty for old-style tenancies (OracleIdentityCloudService). Set to 'Default' for new Identity Domain tenancies",
			Group: gIAM, GroupLabel: lIAM},

		// ── Infrastructure ─────────────────────────────────────────────────
		{Key: "nodeCount", Label: "Node Count", Type: "select", Required: false,
			Default: "3", Options: []string{"1", "2", "3", "4"},
			Description: "Number of nodes (Always Free limit: 4 OCPUs / 24 GB total)",
			Group: gInfra, GroupLabel: lInfra},
		{Key: "compartmentName", Label: "Compartment Name", Type: "text",
			Required: false, Default: "nomad-compartment",
			Group: gInfra, GroupLabel: lInfra},
		{Key: "compartmentDescription", Label: "Compartment Description", Type: "text",
			Required: false, Default: "Compartment for Nomad cluster",
			Group: gInfra, GroupLabel: lInfra},
		{Key: "vcnCidr", Label: "VCN CIDR", Type: "text",
			Required: false, Default: "10.0.0.0/16",
			Group: gInfra, GroupLabel: lInfra},
		{Key: "publicSubnetCidr", Label: "Public Subnet CIDR", Type: "text",
			Required: false, Default: "10.0.1.0/24",
			Group: gInfra, GroupLabel: lInfra},
		{Key: "privateSubnetCidr", Label: "Private Subnet CIDR", Type: "text",
			Required: false, Default: "10.0.2.0/24",
			Group: gInfra, GroupLabel: lInfra},
		{Key: "sshSourceCidr", Label: "SSH Source CIDR", Type: "text",
			Required: false, Default: "0.0.0.0/0",
			Description: "Restrict to your IP for production security",
			Group: gInfra, GroupLabel: lInfra},
		{Key: "shape", Label: "Instance Shape", Type: "oci-shape",
			Required: false, Default: "VM.Standard.A1.Flex",
			Group: gInfra, GroupLabel: lInfra},
		{Key: "imageId", Label: "OCI Image", Type: "oci-image",
			Required: true, Description: "Oracle Linux image for your region",
			Group: gInfra, GroupLabel: lInfra},

		// ── Compute & Storage ──────────────────────────────────────────────
		{Key: "bootVolSizeGb", Label: "Boot Volume (GB)", Type: "number",
			Required: false, Default: "50",
			Group: gComp, GroupLabel: lComp},
		{Key: "glusterVolSizeGb", Label: "GlusterFS Volume (GB)", Type: "number",
			Required: false, Default: "100",
			Group: gComp, GroupLabel: lComp},

		// ── Software Versions ──────────────────────────────────────────────
		{Key: "nomadVersion", Label: "Nomad Version", Type: "text",
			Required: false, Default: "1.10.3",
			Group: gSW, GroupLabel: lSW},
		{Key: "consulVersion", Label: "Consul Version", Type: "text",
			Required: false, Default: "1.21.3",
			Group: gSW, GroupLabel: lSW},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helper types
// ─────────────────────────────────────────────────────────────────────────────

type networkResult struct {
	vcnID           pulumi.IDOutput
	publicSubnetID  pulumi.IDOutput
	privateSubnetID pulumi.IDOutput
}

type nsgResult struct {
	sshNsgID     pulumi.IDOutput
	nomadNsgID   pulumi.IDOutput
	traefikNsgID pulumi.IDOutput
	glusterNsgID pulumi.IDOutput
}

type instanceSpec struct {
	name        string
	ocpus       int
	memoryInGBs int
	count       int
}

type poolsResult struct {
	instanceIDs pulumi.StringArrayOutput // compute instance OCIDs
	adName      pulumi.StringOutput      // first availability domain name
}

// ─────────────────────────────────────────────────────────────────────────────
// Node sizing (mirrors v1 TypeScript exactly)
// ─────────────────────────────────────────────────────────────────────────────

func getInstanceSpecs(nodeCount int) []instanceSpec {
	switch nodeCount {
	case 1:
		return []instanceSpec{{"single-node", 4, 24, 1}}
	case 2:
		return []instanceSpec{{"two-nodes", 2, 12, 2}}
	case 3:
		return []instanceSpec{
			{"small-nodes", 1, 8, 2},
			{"large-node", 2, 8, 1},
		}
	case 4:
		return []instanceSpec{{"four-nodes", 1, 6, 4}}
	default:
		return []instanceSpec{{"nodes", 1, 6, nodeCount}}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Run entry point
// ─────────────────────────────────────────────────────────────────────────────

func (p *NomadClusterProgram) Run(cfg map[string]string) pulumi.RunFunc {
	return func(ctx *pulumi.Context) error {
		tenancyOCID := os.Getenv("OCI_TENANCY_OCID")
		sshPublicKey := os.Getenv("OCI_USER_SSH_PUBLIC_KEY")

		nodeCount, _ := strconv.Atoi(cfgOr(cfg, "nodeCount", "3"))
		if nodeCount < 1 || nodeCount > 4 {
			nodeCount = 3
		}

		// 1. Compartment
		comp, err := identity.NewCompartment(ctx, "nomad-compartment", &identity.CompartmentArgs{
			CompartmentId: pulumi.String(tenancyOCID),
			Name:          pulumi.String(cfgOr(cfg, "compartmentName", "nomad-compartment")),
			Description:   pulumi.String(cfgOr(cfg, "compartmentDescription", "Nomad cluster")),
			EnableDelete:  pulumi.Bool(false),
		})
		if err != nil {
			return err
		}

		// 2. IAM (dynamic group + policy)
		adminGroupName := cfgOr(cfg, "adminGroupName", "")
		identityDomain := cfgOr(cfg, "identityDomain", "")
		skipDynamicGroup := cfgOr(cfg, "skipDynamicGroup", "false") == "true"
		if !skipDynamicGroup {
			if err := createIAM(ctx, tenancyOCID, comp.ID(), adminGroupName, identityDomain); err != nil {
				return err
			}
		}

		// 3. Network
		net, err := createNetwork(ctx, comp.ID(), cfg)
		if err != nil {
			return err
		}

		// 4. NSGs
		nsgs, err := createNSGs(ctx, comp.ID(), net.vcnID,
			cfgOr(cfg, "vcnCidr", "10.0.0.0/16"),
			cfgOr(cfg, "sshSourceCidr", "0.0.0.0/0"))
		if err != nil {
			return err
		}

		// 5. Instance pools
		pools, err := createInstancePools(ctx, tenancyOCID, comp.ID(), net, nsgs, cfg, nodeCount, sshPublicKey)
		if err != nil {
			return err
		}

		// 6. Block volumes (GlusterFS)
		if err := attachGlusterVolumes(ctx, comp.ID(), pools.adName, pools.instanceIDs, cfg, nodeCount); err != nil {
			return err
		}

		// 7. Network Load Balancer
		nlb, err := createNLB(ctx, comp.ID(), net.publicSubnetID, nsgs.traefikNsgID, pools.instanceIDs, nodeCount)
		if err != nil {
			return err
		}

		ctx.Export("traefikNlbIps", nlb.IpAddresses)
		ctx.Export("privateSubnetId", net.privateSubnetID)
		return nil
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 1. IAM
// ─────────────────────────────────────────────────────────────────────────────

// groupRef formats an IAM group reference for policy statements.
// Old-domain tenancies (OracleIdentityCloudService) use bare group names.
// New Identity Domain tenancies require 'DomainName'/GroupName syntax.
func groupRef(groupName, identityDomain string) string {
	if identityDomain != "" {
		return fmt.Sprintf("'%s'/%s", identityDomain, groupName)
	}
	return groupName
}

func createIAM(ctx *pulumi.Context, tenancyOCID string, compartmentID pulumi.IDOutput, adminGroupName, identityDomain string) error {
	// If an admin group is provided, create a prerequisite policy that grants it
	// permission to manage dynamic-groups and policies at the tenancy level.
	// This is required before a DynamicGroup can be created.
	var prereqDeps []pulumi.Resource
	if adminGroupName != "" {
		ref := groupRef(adminGroupName, identityDomain)
		prereqPolicy, err := identity.NewPolicy(ctx, "nomad-iam-prereq-policy", &identity.PolicyArgs{
			CompartmentId: pulumi.String(tenancyOCID),
			Name:          pulumi.String("nomad-iam-prereq"),
			Description:   pulumi.String("Grants admin group permission to manage dynamic groups and policies for Nomad cluster"),
			Statements: pulumi.StringArray{
				pulumi.Sprintf("Allow group %s to manage dynamic-groups in tenancy", ref),
				pulumi.Sprintf("Allow group %s to manage policies in tenancy", ref),
			},
		})
		if err != nil {
			return err
		}
		prereqDeps = []pulumi.Resource{prereqPolicy}
	}

	matchingRule := compartmentID.ApplyT(func(id string) (string, error) {
		return fmt.Sprintf("ALL {instance.compartment.id = '%s'}", id), nil
	}).(pulumi.StringOutput)

	dg, err := identity.NewDynamicGroup(ctx, "nomad-cluster-dg", &identity.DynamicGroupArgs{
		CompartmentId: pulumi.String(tenancyOCID),
		Name:          pulumi.String("nomad-cluster-dg"),
		Description:   pulumi.String("Dynamic group for Nomad cluster instances"),
		MatchingRule:  matchingRule,
	}, pulumi.DependsOn(prereqDeps))
	if err != nil {
		return err
	}

	statements := pulumi.All(dg.Name, compartmentID).ApplyT(func(args []interface{}) ([]string, error) {
		dgName := args[0].(string)
		compID := args[1].(string)
		return []string{
			fmt.Sprintf("Allow dynamic-group %s to inspect instances in compartment id %s", dgName, compID),
			fmt.Sprintf("Allow dynamic-group %s to inspect vnics in compartment id %s", dgName, compID),
			fmt.Sprintf("Allow dynamic-group %s to inspect compartments in tenancy", dgName),
			fmt.Sprintf("Allow dynamic-group %s to inspect tenancies in tenancy", dgName),
			fmt.Sprintf("Allow dynamic-group %s to read instance-family in compartment id %s", dgName, compID),
			fmt.Sprintf("Allow dynamic-group %s to read compute-management-family in compartment id %s", dgName, compID),
			fmt.Sprintf("Allow dynamic-group %s to read load-balancers in compartment id %s", dgName, compID),
			fmt.Sprintf("Allow dynamic-group %s to inspect private-ips in compartment id %s", dgName, compID),
			fmt.Sprintf("Allow dynamic-group %s to manage buckets in compartment id %s", dgName, compID),
			fmt.Sprintf("Allow dynamic-group %s to manage objects in compartment id %s", dgName, compID),
		}, nil
	}).(pulumi.StringArrayOutput)

	_, err = identity.NewPolicy(ctx, "nomad-cluster-policy", &identity.PolicyArgs{
		CompartmentId: pulumi.String(tenancyOCID),
		Name:          pulumi.String("nomad-cluster-policy"),
		Description:   pulumi.String("Allow Nomad cluster nodes to manage cluster resources"),
		Statements:    statements,
	})
	return err
}

// ─────────────────────────────────────────────────────────────────────────────
// 2. Network
// ─────────────────────────────────────────────────────────────────────────────

func createNetwork(ctx *pulumi.Context, compartmentID pulumi.IDOutput, cfg map[string]string) (networkResult, error) {
	vcnCidr := cfgOr(cfg, "vcnCidr", "10.0.0.0/16")
	publicSubnetCidr := cfgOr(cfg, "publicSubnetCidr", "10.0.1.0/24")
	privateSubnetCidr := cfgOr(cfg, "privateSubnetCidr", "10.0.2.0/24")

	vcn, err := core.NewVcn(ctx, "nomad-vcn", &core.VcnArgs{
		CompartmentId: compartmentID,
		CidrBlock:     pulumi.String(vcnCidr),
		DisplayName:   pulumi.String("nomad-vcn"),
		DnsLabel:      pulumi.String("nomadvcn"),
	})
	if err != nil {
		return networkResult{}, err
	}

	igw, err := core.NewInternetGateway(ctx, "nomad-igw", &core.InternetGatewayArgs{
		CompartmentId: compartmentID,
		VcnId:         vcn.ID(),
		Enabled:       pulumi.Bool(true),
		DisplayName:   pulumi.String("nomad-igw"),
	})
	if err != nil {
		return networkResult{}, err
	}

	natgw, err := core.NewNatGateway(ctx, "nomad-natgw", &core.NatGatewayArgs{
		CompartmentId: compartmentID,
		VcnId:         vcn.ID(),
		DisplayName:   pulumi.String("nomad-natgw"),
		BlockTraffic:  pulumi.Bool(false),
	})
	if err != nil {
		return networkResult{}, err
	}

	publicSL, err := core.NewSecurityList(ctx, "public-security-list", &core.SecurityListArgs{
		CompartmentId: compartmentID,
		VcnId:         vcn.ID(),
		DisplayName:   pulumi.String("Public Security List"),
		IngressSecurityRules: core.SecurityListIngressSecurityRuleArray{
			core.SecurityListIngressSecurityRuleArgs{
				Protocol: pulumi.String("6"), Source: pulumi.String("0.0.0.0/0"), SourceType: pulumi.String("CIDR_BLOCK"),
				TcpOptions: core.SecurityListIngressSecurityRuleTcpOptionsArgs{Max: pulumi.Int(80), Min: pulumi.Int(80)},
			},
			core.SecurityListIngressSecurityRuleArgs{
				Protocol: pulumi.String("6"), Source: pulumi.String("0.0.0.0/0"), SourceType: pulumi.String("CIDR_BLOCK"),
				TcpOptions: core.SecurityListIngressSecurityRuleTcpOptionsArgs{Max: pulumi.Int(443), Min: pulumi.Int(443)},
			},
			core.SecurityListIngressSecurityRuleArgs{
				Protocol: pulumi.String("6"), Source: pulumi.String("0.0.0.0/0"), SourceType: pulumi.String("CIDR_BLOCK"),
				TcpOptions: core.SecurityListIngressSecurityRuleTcpOptionsArgs{Max: pulumi.Int(22), Min: pulumi.Int(22)},
			},
			core.SecurityListIngressSecurityRuleArgs{
				Protocol: pulumi.String("all"), Source: pulumi.String(vcnCidr), SourceType: pulumi.String("CIDR_BLOCK"),
			},
		},
		EgressSecurityRules: core.SecurityListEgressSecurityRuleArray{
			core.SecurityListEgressSecurityRuleArgs{
				Protocol: pulumi.String("all"), Destination: pulumi.String("0.0.0.0/0"), DestinationType: pulumi.String("CIDR_BLOCK"),
			},
		},
	})
	if err != nil {
		return networkResult{}, err
	}

	privateSL, err := core.NewSecurityList(ctx, "private-security-list", &core.SecurityListArgs{
		CompartmentId: compartmentID,
		VcnId:         vcn.ID(),
		DisplayName:   pulumi.String("Private Security List"),
		IngressSecurityRules: core.SecurityListIngressSecurityRuleArray{
			core.SecurityListIngressSecurityRuleArgs{
				Protocol: pulumi.String("all"), Source: pulumi.String(vcnCidr), SourceType: pulumi.String("CIDR_BLOCK"),
			},
		},
		EgressSecurityRules: core.SecurityListEgressSecurityRuleArray{
			core.SecurityListEgressSecurityRuleArgs{
				Protocol: pulumi.String("all"), Destination: pulumi.String("0.0.0.0/0"), DestinationType: pulumi.String("CIDR_BLOCK"),
			},
		},
	})
	if err != nil {
		return networkResult{}, err
	}

	publicRT, err := core.NewRouteTable(ctx, "nomad-public-rt", &core.RouteTableArgs{
		CompartmentId: compartmentID,
		VcnId:         vcn.ID(),
		DisplayName:   pulumi.String("nomad-public-rt"),
		RouteRules: core.RouteTableRouteRuleArray{
			core.RouteTableRouteRuleArgs{
				NetworkEntityId: igw.ID(),
				Destination:     pulumi.String("0.0.0.0/0"),
				DestinationType: pulumi.String("CIDR_BLOCK"),
			},
		},
	})
	if err != nil {
		return networkResult{}, err
	}

	privateRT, err := core.NewRouteTable(ctx, "nomad-private-rt", &core.RouteTableArgs{
		CompartmentId: compartmentID,
		VcnId:         vcn.ID(),
		DisplayName:   pulumi.String("nomad-private-rt"),
		RouteRules: core.RouteTableRouteRuleArray{
			core.RouteTableRouteRuleArgs{
				NetworkEntityId: natgw.ID(),
				Destination:     pulumi.String("0.0.0.0/0"),
				DestinationType: pulumi.String("CIDR_BLOCK"),
			},
		},
	})
	if err != nil {
		return networkResult{}, err
	}

	publicSubnet, err := core.NewSubnet(ctx, "public-subnet", &core.SubnetArgs{
		CompartmentId:          compartmentID,
		VcnId:                  vcn.ID(),
		CidrBlock:              pulumi.String(publicSubnetCidr),
		DisplayName:            pulumi.String("public-subnet"),
		DnsLabel:               pulumi.String("nomadvcnpub"),
		ProhibitPublicIpOnVnic: pulumi.Bool(false),
		RouteTableId:           publicRT.ID(),
		SecurityListIds:        pulumi.StringArray{publicSL.ID().ToStringOutput()},
		DhcpOptionsId:          vcn.DefaultDhcpOptionsId,
	})
	if err != nil {
		return networkResult{}, err
	}

	privateSubnet, err := core.NewSubnet(ctx, "private-subnet", &core.SubnetArgs{
		CompartmentId:          compartmentID,
		VcnId:                  vcn.ID(),
		CidrBlock:              pulumi.String(privateSubnetCidr),
		DisplayName:            pulumi.String("private-subnet"),
		DnsLabel:               pulumi.String("nomadvcnpriv"),
		ProhibitPublicIpOnVnic: pulumi.Bool(true),
		RouteTableId:           privateRT.ID(),
		SecurityListIds:        pulumi.StringArray{privateSL.ID().ToStringOutput()},
		DhcpOptionsId:          vcn.DefaultDhcpOptionsId,
	})
	if err != nil {
		return networkResult{}, err
	}

	return networkResult{
		vcnID:           vcn.ID(),
		publicSubnetID:  publicSubnet.ID(),
		privateSubnetID: privateSubnet.ID(),
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// 3. NSGs
// ─────────────────────────────────────────────────────────────────────────────

func createNSGs(ctx *pulumi.Context, compartmentID, vcnID pulumi.IDOutput, vcnCidr, sshSourceCidr string) (nsgResult, error) {
	// SSH NSG
	sshNsg, err := core.NewNetworkSecurityGroup(ctx, "ssh-nsg", &core.NetworkSecurityGroupArgs{
		CompartmentId: compartmentID, VcnId: vcnID, DisplayName: pulumi.String("ssh-nsg"),
	})
	if err != nil {
		return nsgResult{}, err
	}
	if _, err := core.NewNetworkSecurityGroupSecurityRule(ctx, "ssh-nsg-rule", &core.NetworkSecurityGroupSecurityRuleArgs{
		NetworkSecurityGroupId: sshNsg.ID(), Direction: pulumi.String("INGRESS"), Protocol: pulumi.String("6"),
		Source: pulumi.String(sshSourceCidr), SourceType: pulumi.String("CIDR_BLOCK"),
		TcpOptions: core.NetworkSecurityGroupSecurityRuleTcpOptionsArgs{
			DestinationPortRange: core.NetworkSecurityGroupSecurityRuleTcpOptionsDestinationPortRangeArgs{
				Min: pulumi.Int(22), Max: pulumi.Int(22),
			},
		},
		Description: pulumi.String("Allow SSH"),
	}); err != nil {
		return nsgResult{}, err
	}

	// Nomad NSG — ports 4646/4647/4648
	nomadNsg, err := core.NewNetworkSecurityGroup(ctx, "nomad-nsg", &core.NetworkSecurityGroupArgs{
		CompartmentId: compartmentID, VcnId: vcnID, DisplayName: pulumi.String("nomad-nsg"),
	})
	if err != nil {
		return nsgResult{}, err
	}
	for _, port := range []int{4646, 4647, 4648} {
		port := port
		if _, err := core.NewNetworkSecurityGroupSecurityRule(ctx, fmt.Sprintf("nomad-nsg-rule-%d", port), &core.NetworkSecurityGroupSecurityRuleArgs{
			NetworkSecurityGroupId: nomadNsg.ID(), Direction: pulumi.String("INGRESS"), Protocol: pulumi.String("6"),
			Source: pulumi.String("10.0.1.0/24"), SourceType: pulumi.String("CIDR_BLOCK"),
			TcpOptions: core.NetworkSecurityGroupSecurityRuleTcpOptionsArgs{
				DestinationPortRange: core.NetworkSecurityGroupSecurityRuleTcpOptionsDestinationPortRangeArgs{
					Min: pulumi.Int(port), Max: pulumi.Int(port),
				},
			},
			Description: pulumi.Sprintf("Allow Nomad port %d from public subnet", port),
		}); err != nil {
			return nsgResult{}, err
		}
	}

	// Traefik NSG — ports 80/443
	traefikNsg, err := core.NewNetworkSecurityGroup(ctx, "traefik-nsg", &core.NetworkSecurityGroupArgs{
		CompartmentId: compartmentID, VcnId: vcnID, DisplayName: pulumi.String("traefik-nsg"),
	})
	if err != nil {
		return nsgResult{}, err
	}
	for _, port := range []int{80, 443} {
		port := port
		if _, err := core.NewNetworkSecurityGroupSecurityRule(ctx, fmt.Sprintf("traefik-nsg-rule-%d", port), &core.NetworkSecurityGroupSecurityRuleArgs{
			NetworkSecurityGroupId: traefikNsg.ID(), Direction: pulumi.String("INGRESS"), Protocol: pulumi.String("6"),
			Source: pulumi.String("10.0.1.0/24"), SourceType: pulumi.String("CIDR_BLOCK"),
			TcpOptions: core.NetworkSecurityGroupSecurityRuleTcpOptionsArgs{
				DestinationPortRange: core.NetworkSecurityGroupSecurityRuleTcpOptionsDestinationPortRangeArgs{
					Min: pulumi.Int(port), Max: pulumi.Int(port),
				},
			},
			Description: pulumi.Sprintf("Allow Traefik port %d from public subnet", port),
		}); err != nil {
			return nsgResult{}, err
		}
	}

	// GlusterFS NSG
	glusterNsg, err := core.NewNetworkSecurityGroup(ctx, "gluster-nsg", &core.NetworkSecurityGroupArgs{
		CompartmentId: compartmentID, VcnId: vcnID, DisplayName: pulumi.String("gluster-nsg"),
	})
	if err != nil {
		return nsgResult{}, err
	}
	for _, port := range []int{24007, 24008} {
		port := port
		if _, err := core.NewNetworkSecurityGroupSecurityRule(ctx, fmt.Sprintf("gluster-nsg-rule-%d", port), &core.NetworkSecurityGroupSecurityRuleArgs{
			NetworkSecurityGroupId: glusterNsg.ID(), Direction: pulumi.String("INGRESS"), Protocol: pulumi.String("6"),
			Source: pulumi.String(vcnCidr), SourceType: pulumi.String("CIDR_BLOCK"),
			TcpOptions: core.NetworkSecurityGroupSecurityRuleTcpOptionsArgs{
				DestinationPortRange: core.NetworkSecurityGroupSecurityRuleTcpOptionsDestinationPortRangeArgs{
					Min: pulumi.Int(port), Max: pulumi.Int(port),
				},
			},
			Description: pulumi.Sprintf("Allow GlusterFS port %d from VCN", port),
		}); err != nil {
			return nsgResult{}, err
		}
	}
	if _, err := core.NewNetworkSecurityGroupSecurityRule(ctx, "gluster-nsg-rule-dynamic", &core.NetworkSecurityGroupSecurityRuleArgs{
		NetworkSecurityGroupId: glusterNsg.ID(), Direction: pulumi.String("INGRESS"), Protocol: pulumi.String("6"),
		Source: pulumi.String(vcnCidr), SourceType: pulumi.String("CIDR_BLOCK"),
		TcpOptions: core.NetworkSecurityGroupSecurityRuleTcpOptionsArgs{
			DestinationPortRange: core.NetworkSecurityGroupSecurityRuleTcpOptionsDestinationPortRangeArgs{
				Min: pulumi.Int(49152), Max: pulumi.Int(49251),
			},
		},
		Description: pulumi.String("Allow GlusterFS dynamic ports from VCN"),
	}); err != nil {
		return nsgResult{}, err
	}

	return nsgResult{
		sshNsgID:     sshNsg.ID(),
		nomadNsgID:   nomadNsg.ID(),
		traefikNsgID: traefikNsg.ID(),
		glusterNsgID: glusterNsg.ID(),
	}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// 4. Instance pools
// ─────────────────────────────────────────────────────────────────────────────

func createInstancePools(
	ctx *pulumi.Context,
	tenancyOCID string,
	compartmentID pulumi.IDOutput,
	net networkResult,
	nsgs nsgResult,
	cfg map[string]string,
	nodeCount int,
	sshPublicKey string,
) (poolsResult, error) {
	shape := cfgOr(cfg, "shape", "VM.Standard.A1.Flex")
	imageID := cfgOr(cfg, "imageId", "")
	bootVolSizeGb, _ := strconv.Atoi(cfgOr(cfg, "bootVolSizeGb", "50"))
	nomadVersion := cfgOr(cfg, "nomadVersion", "1.10.3")
	consulVersion := cfgOr(cfg, "consulVersion", "1.21.3")

	// Get first availability domain
	adsResult := identity.GetAvailabilityDomainsOutput(ctx, identity.GetAvailabilityDomainsOutputArgs{
		CompartmentId: pulumi.String(tenancyOCID),
	})
	adName := adsResult.AvailabilityDomains().Index(pulumi.Int(0)).Name()

	specs := getInstanceSpecs(nodeCount)

	// Collect per-pool instance ID outputs so we can combine them
	var perPoolIDs []interface{}

	for _, spec := range specs {
		spec := spec

		cloudInit := buildCloudInit(
			spec.ocpus, spec.memoryInGBs, nodeCount,
			compartmentID, net.privateSubnetID,
			nomadVersion, consulVersion,
		)

		nsgIDs := pulumi.StringArray{
			nsgs.sshNsgID.ToStringOutput(),
			nsgs.nomadNsgID.ToStringOutput(),
			nsgs.traefikNsgID.ToStringOutput(),
			nsgs.glusterNsgID.ToStringOutput(),
		}

		instanceConfig, err := core.NewInstanceConfiguration(ctx, fmt.Sprintf("nomad-ic-%s", spec.name), &core.InstanceConfigurationArgs{
			CompartmentId: compartmentID,
			DisplayName:   pulumi.String(fmt.Sprintf("nomad-ic-%s", spec.name)),
			InstanceDetails: core.InstanceConfigurationInstanceDetailsArgs{
				InstanceType: pulumi.String("compute"),
				LaunchDetails: core.InstanceConfigurationInstanceDetailsLaunchDetailsArgs{
					CompartmentId:      compartmentID.ToStringOutput().ToStringPtrOutput(),
					AvailabilityDomain: adName.ToStringPtrOutput(),
					Shape:              pulumi.StringPtr(shape),
					ShapeConfig: core.InstanceConfigurationInstanceDetailsLaunchDetailsShapeConfigArgs{
						Ocpus:       pulumi.Float64Ptr(float64(spec.ocpus)),
						MemoryInGbs: pulumi.Float64Ptr(float64(spec.memoryInGBs)),
					},
					SourceDetails: core.InstanceConfigurationInstanceDetailsLaunchDetailsSourceDetailsArgs{
						SourceType:          pulumi.String("image"),
						ImageId:             pulumi.StringPtr(imageID),
						BootVolumeSizeInGbs: pulumi.StringPtr(strconv.Itoa(bootVolSizeGb)),
					},
					CreateVnicDetails: core.InstanceConfigurationInstanceDetailsLaunchDetailsCreateVnicDetailsArgs{
						SubnetId:       net.privateSubnetID.ToStringOutput().ToStringPtrOutput(),
						AssignPublicIp: pulumi.BoolPtr(false),
						NsgIds:         nsgIDs,
					},
					Metadata: pulumi.StringMap{
						"ssh_authorized_keys": pulumi.String(sshPublicKey),
						"user_data":           cloudInit,
					},
					DisplayName: pulumi.StringPtr(fmt.Sprintf("nomad-%s", spec.name)),
				},
			},
		})
		if err != nil {
			return poolsResult{}, err
		}

		pool, err := core.NewInstancePool(ctx, fmt.Sprintf("nomad-pool-%s", spec.name), &core.InstancePoolArgs{
			CompartmentId:           compartmentID,
			InstanceConfigurationId: instanceConfig.ID(),
			Size:                    pulumi.Int(spec.count),
			DisplayName:             pulumi.String(fmt.Sprintf("nomad-pool-%s", spec.name)),
			PlacementConfigurations: core.InstancePoolPlacementConfigurationArray{
				core.InstancePoolPlacementConfigurationArgs{
					AvailabilityDomain: adName,
					PrimarySubnetId:    net.privateSubnetID,
				},
			},
		})
		if err != nil {
			return poolsResult{}, err
		}

		// Collect instance IDs from this pool
		poolInstancesResult := core.GetInstancePoolInstancesOutput(ctx, core.GetInstancePoolInstancesOutputArgs{
			CompartmentId:  compartmentID.ToStringOutput(),
			InstancePoolId: pool.ID().ToStringOutput(),
		}, pulumi.DependsOn([]pulumi.Resource{pool}))

		poolIDs := poolInstancesResult.Instances().ApplyT(func(instances []core.GetInstancePoolInstancesInstance) []string {
			ids := make([]string, len(instances))
			for i, inst := range instances {
				ids[i] = inst.InstanceId
			}
			return ids
		}).(pulumi.StringArrayOutput)

		perPoolIDs = append(perPoolIDs, poolIDs)
	}

	// Combine all instance IDs from all pools into a single array
	allIDs := pulumi.All(perPoolIDs...).ApplyT(func(args []interface{}) ([]string, error) {
		var combined []string
		for _, arg := range args {
			ids := arg.([]string)
			combined = append(combined, ids...)
		}
		return combined, nil
	}).(pulumi.StringArrayOutput)

	return poolsResult{instanceIDs: allIDs, adName: adName}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// 5. GlusterFS volumes
// ─────────────────────────────────────────────────────────────────────────────

func attachGlusterVolumes(
	ctx *pulumi.Context,
	compartmentID pulumi.IDOutput,
	adName pulumi.StringOutput,
	instanceIDs pulumi.StringArrayOutput,
	cfg map[string]string,
	nodeCount int,
) error {
	glusterVolSizeGb, _ := strconv.Atoi(cfgOr(cfg, "glusterVolSizeGb", "100"))

	// Create one GlusterFS volume per node and attach it to the corresponding instance
	for i := 0; i < nodeCount; i++ {
		i := i
		instanceID := instanceIDs.Index(pulumi.Int(i))

		vol, err := core.NewVolume(ctx, fmt.Sprintf("glusterfs-volume-%d", i+1), &core.VolumeArgs{
			CompartmentId:      compartmentID,
			AvailabilityDomain: adName,
			SizeInGbs:          pulumi.String(strconv.Itoa(glusterVolSizeGb)),
			DisplayName:        pulumi.String(fmt.Sprintf("glusterfs-data-%d", i+1)),
		})
		if err != nil {
			return err
		}

		if _, err := core.NewVolumeAttachment(ctx, fmt.Sprintf("glusterfs-attachment-%d", i+1), &core.VolumeAttachmentArgs{
			InstanceId:     instanceID,
			VolumeId:       vol.ID(),
			AttachmentType: pulumi.String("paravirtualized"),
			DisplayName:    pulumi.String(fmt.Sprintf("glusterfs-attachment-%d", i+1)),
		}); err != nil {
			return err
		}

		// Incremental daily backup policy on the first volume only
		if i == 0 {
			backupPolicy, err := core.NewVolumeBackupPolicy(ctx, "glusterfs-backup-policy", &core.VolumeBackupPolicyArgs{
				CompartmentId: compartmentID,
				DisplayName:   pulumi.String("glusterfs-backup-policy"),
				Schedules: core.VolumeBackupPolicyScheduleArray{
					core.VolumeBackupPolicyScheduleArgs{
						BackupType:       pulumi.String("INCREMENTAL"),
						Period:           pulumi.String("ONE_DAY"),
						RetentionSeconds: pulumi.Int(5 * 24 * 60 * 60),
						TimeZone:         pulumi.String("UTC"),
					},
				},
			})
			if err != nil {
				return err
			}
			if _, err := core.NewVolumeBackupPolicyAssignment(ctx, "glusterfs-backup-policy-assignment", &core.VolumeBackupPolicyAssignmentArgs{
				AssetId:  vol.ID(),
				PolicyId: backupPolicy.ID(),
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// 6. Network Load Balancer
// ─────────────────────────────────────────────────────────────────────────────

func createNLB(
	ctx *pulumi.Context,
	compartmentID pulumi.IDOutput,
	publicSubnetID pulumi.IDOutput,
	traefikNsgID pulumi.IDOutput,
	instanceIDs pulumi.StringArrayOutput,
	nodeCount int,
) (*networkloadbalancer.NetworkLoadBalancer, error) {
	nlb, err := networkloadbalancer.NewNetworkLoadBalancer(ctx, "traefik-nlb", &networkloadbalancer.NetworkLoadBalancerArgs{
		CompartmentId:           compartmentID,
		SubnetId:                publicSubnetID,
		DisplayName:             pulumi.String("traefik-nlb"),
		IsPrivate:               pulumi.Bool(false),
		NetworkSecurityGroupIds: pulumi.StringArray{traefikNsgID.ToStringOutput()},
	})
	if err != nil {
		return nil, err
	}

	for _, port := range []int{80, 443, 4646} {
		port := port
		bsName := fmt.Sprintf("bs-%d", port)

		bs, err := networkloadbalancer.NewBackendSet(ctx, fmt.Sprintf("traefik-nlb-bs-%d", port), &networkloadbalancer.BackendSetArgs{
			NetworkLoadBalancerId: nlb.ID(),
			Name:                  pulumi.String(bsName),
			Policy:                pulumi.String("FIVE_TUPLE"),
			IsPreserveSource:      pulumi.Bool(false),
			HealthChecker: networkloadbalancer.BackendSetHealthCheckerArgs{
				Protocol: pulumi.String("TCP"),
				Port:     pulumi.Int(port),
			},
		})
		if err != nil {
			return nil, err
		}

		if _, err := networkloadbalancer.NewListener(ctx, fmt.Sprintf("traefik-nlb-listener-%d", port), &networkloadbalancer.ListenerArgs{
			NetworkLoadBalancerId: nlb.ID(),
			Name:                  pulumi.String(fmt.Sprintf("listener-%d", port)),
			DefaultBackendSetName: bs.Name,
			Protocol:              pulumi.String("TCP"),
			Port:                  pulumi.Int(port),
		}, pulumi.DependsOn([]pulumi.Resource{nlb, bs})); err != nil {
			return nil, err
		}

		// Add each instance as a backend using TargetId (instance OCID)
		for i := 0; i < nodeCount; i++ {
			i := i
			targetID := instanceIDs.Index(pulumi.Int(i))
			if _, err := networkloadbalancer.NewBackend(ctx, fmt.Sprintf("traefik-nlb-backend-%d-%d", port, i), &networkloadbalancer.BackendArgs{
				NetworkLoadBalancerId: nlb.ID(),
				BackendSetName:        bs.Name,
				TargetId:              targetID.ToStringPtrOutput(),
				Port:                  pulumi.Int(port),
			}, pulumi.DependsOn([]pulumi.Resource{nlb, bs})); err != nil {
				return nil, err
			}
		}
	}

	return nlb, nil
}
