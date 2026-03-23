package programs

import (
	"fmt"
	"strconv"

	"github.com/pulumi/pulumi-oci/sdk/v2/go/oci/core"
	"github.com/pulumi/pulumi-oci/sdk/v2/go/oci/identity"
	"github.com/pulumi/pulumi-oci/sdk/v2/go/oci/networkloadbalancer"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/trustos/pulumi-ui/internal/agentinject"
)

// ─────────────────────────────────────────────────────────────────────────────
// Registration
// ─────────────────────────────────────────────────────────────────────────────

type NomadClusterProgram struct{}

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

		// ── Compute ────────────────────────────────────────────────────────
		{Key: "ocpusPerNode", Label: "OCPUs per Node", Type: "number",
			Required: false, Default: "1",
			Description: "OCPUs allocated to each node (Always Free limit: 4 total)",
			Group: gComp, GroupLabel: lComp},
		{Key: "memoryGbPerNode", Label: "Memory per Node (GB)", Type: "number",
			Required: false, Default: "6",
			Description: "Memory GiB per node (Always Free limit: 24 GB total)",
			Group: gComp, GroupLabel: lComp},
		{Key: "bootVolSizeGb", Label: "Boot Volume (GB)", Type: "number",
			Required: false, Default: "50",
			Group: gComp, GroupLabel: lComp},
		{Key: "sshPublicKey", Label: "SSH Public Key", Type: "ssh-public-key",
			Required: true,
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

// Applications implements ApplicationProvider, exposing the catalog of
// selectable applications for the Nomad cluster program.
func (p *NomadClusterProgram) Applications() []ApplicationDef {
	return []ApplicationDef{
		{
			Key:         "docker",
			Name:        "Docker",
			Description: "Container runtime (required for Nomad workloads)",
			Tier:        TierBootstrap,
			Target:      TargetAll,
			Required:    true,
			DefaultOn:   true,
		},
		{
			Key:         "consul",
			Name:        "Consul",
			Description: "Service mesh and service discovery",
			Tier:        TierBootstrap,
			Target:      TargetAll,
			Required:    true,
			DefaultOn:   true,
		},
		{
			Key:         "nomad",
			Name:        "Nomad",
			Description: "Workload orchestrator",
			Tier:        TierBootstrap,
			Target:      TargetAll,
			Required:    true,
			DefaultOn:   true,
			DependsOn:   []string{"docker", "consul"},
		},
		{
			Key:         "traefik",
			Name:        "Traefik Reverse Proxy",
			Description: "Ingress controller and automatic TLS",
			Tier:        TierWorkload,
			Target:      TargetFirst,
			Required:    false,
			DefaultOn:   true,
			DependsOn:   []string{"nomad"},
		},
		{
			Key:         "postgres",
			Name:        "PostgreSQL",
			Description: "Managed PostgreSQL database on Nomad",
			Tier:        TierWorkload,
			Target:      TargetFirst,
			Required:    false,
			DefaultOn:   false,
			DependsOn:   []string{"nomad"},
		},
		{
			Key:         "nomad-ops",
			Name:        "nomad-ops",
			Description: "Nomad operations dashboard and management UI",
			Tier:        TierWorkload,
			Target:      TargetFirst,
			Required:    false,
			DefaultOn:   false,
			DependsOn:   []string{"nomad"},
		},
	}
}

// Compile-time check that NomadClusterProgram implements ApplicationProvider.
var _ ApplicationProvider = (*NomadClusterProgram)(nil)

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
	nebulaNsgID  pulumi.IDOutput
}

type poolsResult struct {
	instanceIDs pulumi.StringArrayOutput // compute instance OCIDs
	adName      pulumi.StringOutput      // first availability domain name
}

// ─────────────────────────────────────────────────────────────────────────────
// Run entry point
// ─────────────────────────────────────────────────────────────────────────────

func (p *NomadClusterProgram) ForkYAML() string {
	return `name: nomad-cluster-custom
runtime: yaml
description: "Forked from Nomad Cluster"

meta:
  groups:
    - key: iam
      label: "IAM & Permissions"
      fields: [skipDynamicGroup, adminGroupName, identityDomain]
    - key: infrastructure
      label: "Infrastructure"
      fields: [nodeCount, compartmentName, compartmentDescription, vcnCidr, publicSubnetCidr, privateSubnetCidr, sshSourceCidr, shape, imageId]
    - key: compute
      label: "Compute & Storage"
      fields: [ocpusPerNode, memoryGbPerNode, bootVolSizeGb, sshPublicKey]
    - key: software
      label: "Software Versions"
      fields: [nomadVersion, consulVersion]
  fields:
    imageId:
      ui_type: oci-image
    shape:
      ui_type: oci-shape
    sshPublicKey:
      ui_type: ssh-public-key

config:
  skipDynamicGroup:
    type: string
    default: "false"
  adminGroupName:
    type: string
    default: ""
  identityDomain:
    type: string
    default: ""
  nodeCount:
    type: integer
    default: 3
  compartmentName:
    type: string
    default: "nomad-compartment"
  compartmentDescription:
    type: string
    default: "Compartment for Nomad cluster"
  vcnCidr:
    type: string
    default: "10.0.0.0/16"
  publicSubnetCidr:
    type: string
    default: "10.0.1.0/24"
  privateSubnetCidr:
    type: string
    default: "10.0.2.0/24"
  sshSourceCidr:
    type: string
    default: "0.0.0.0/0"
  shape:
    type: string
    default: "VM.Standard.A1.Flex"
  imageId:
    type: string
  ocpusPerNode:
    type: integer
    default: 1
  memoryGbPerNode:
    type: integer
    default: 6
  bootVolSizeGb:
    type: integer
    default: 50
  nomadVersion:
    type: string
    default: "1.10.3"
  consulVersion:
    type: string
    default: "1.21.3"
  sshPublicKey:
    type: string

variables:
  availabilityDomains:
    fn::invoke:
      function: oci:Identity/getAvailabilityDomains:getAvailabilityDomains
      arguments:
        compartmentId: ${oci:tenancyOcid}
      return: availabilityDomains

resources:
# --- section: identity ---
  nomad-compartment:
    type: oci:Identity/compartment:Compartment
    properties:
      compartmentId: ${oci:tenancyOcid}
      name: {{ .Config.compartmentName }}
      description: {{ .Config.compartmentDescription | quote }}
      enableDelete: false

# --- section: iam ---
{{- if ne .Config.skipDynamicGroup "true" }}
{{- if .Config.adminGroupName }}
  nomad-iam-prereq-policy:
    type: oci:Identity/policy:Policy
    properties:
      compartmentId: ${oci:tenancyOcid}
      name: nomad-iam-prereq
      description: "Grants admin group permission to manage dynamic groups and policies"
      statements:
        - {{ groupRef .Config.adminGroupName .Config.identityDomain "manage dynamic-groups in tenancy" | quote }}
        - {{ groupRef .Config.adminGroupName .Config.identityDomain "manage policies in tenancy" | quote }}
{{- end }}
  nomad-cluster-dg:
    type: oci:Identity/dynamicGroup:DynamicGroup
    properties:
      compartmentId: ${oci:tenancyOcid}
      name: nomad-cluster-dg
      description: "Dynamic group for Nomad cluster instances"
      matchingRule: "ALL {instance.compartment.id = '${nomad-compartment.id}'}"
{{- if .Config.adminGroupName }}
    options:
      dependsOn:
        - ${nomad-iam-prereq-policy}
{{- end }}
  nomad-cluster-policy:
    type: oci:Identity/policy:Policy
    properties:
      compartmentId: ${nomad-compartment.id}
      name: nomad-cluster-policy
      description: "Allow Nomad cluster nodes to manage cluster resources"
      statements:
        - "Allow dynamic-group nomad-cluster-dg to inspect instances in compartment id ${nomad-compartment.id}"
        - "Allow dynamic-group nomad-cluster-dg to inspect vnics in compartment id ${nomad-compartment.id}"
        - "Allow dynamic-group nomad-cluster-dg to inspect compartments in tenancy"
        - "Allow dynamic-group nomad-cluster-dg to inspect tenancies in tenancy"
        - "Allow dynamic-group nomad-cluster-dg to read instance-family in compartment id ${nomad-compartment.id}"
        - "Allow dynamic-group nomad-cluster-dg to read compute-management-family in compartment id ${nomad-compartment.id}"
        - "Allow dynamic-group nomad-cluster-dg to read load-balancers in compartment id ${nomad-compartment.id}"
        - "Allow dynamic-group nomad-cluster-dg to inspect private-ips in compartment id ${nomad-compartment.id}"
        - "Allow dynamic-group nomad-cluster-dg to manage buckets in compartment id ${nomad-compartment.id}"
        - "Allow dynamic-group nomad-cluster-dg to manage objects in compartment id ${nomad-compartment.id}"
    options:
      dependsOn:
        - ${nomad-cluster-dg}
{{- end }}

# --- section: networking ---
  nomad-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: ${nomad-compartment.id}
      cidrBlock: {{ .Config.vcnCidr | quote }}
      displayName: "nomad-vcn"
      dnsLabel: "nomadvcn"

  nomad-igw:
    type: oci:Core/internetGateway:InternetGateway
    properties:
      compartmentId: ${nomad-compartment.id}
      vcnId: ${nomad-vcn.id}
      enabled: true
      displayName: "nomad-igw"

  nomad-natgw:
    type: oci:Core/natGateway:NatGateway
    properties:
      compartmentId: ${nomad-compartment.id}
      vcnId: ${nomad-vcn.id}
      displayName: "nomad-natgw"
      blockTraffic: false

  public-security-list:
    type: oci:Core/securityList:SecurityList
    properties:
      compartmentId: ${nomad-compartment.id}
      vcnId: ${nomad-vcn.id}
      displayName: "Public Security List"
      ingressSecurityRules:
        - protocol: "6"
          source: "0.0.0.0/0"
          sourceType: CIDR_BLOCK
          tcpOptions:
            max: 80
            min: 80
        - protocol: "6"
          source: "0.0.0.0/0"
          sourceType: CIDR_BLOCK
          tcpOptions:
            max: 443
            min: 443
        - protocol: "6"
          source: {{ .Config.sshSourceCidr | quote }}
          sourceType: CIDR_BLOCK
          tcpOptions:
            max: 22
            min: 22
        - protocol: "all"
          source: {{ .Config.vcnCidr | quote }}
          sourceType: CIDR_BLOCK
      egressSecurityRules:
        - protocol: "all"
          destination: "0.0.0.0/0"
          destinationType: CIDR_BLOCK

  private-security-list:
    type: oci:Core/securityList:SecurityList
    properties:
      compartmentId: ${nomad-compartment.id}
      vcnId: ${nomad-vcn.id}
      displayName: "Private Security List"
      ingressSecurityRules:
        - protocol: "all"
          source: {{ .Config.vcnCidr | quote }}
          sourceType: CIDR_BLOCK
      egressSecurityRules:
        - protocol: "all"
          destination: "0.0.0.0/0"
          destinationType: CIDR_BLOCK

  nomad-public-rt:
    type: oci:Core/routeTable:RouteTable
    properties:
      compartmentId: ${nomad-compartment.id}
      vcnId: ${nomad-vcn.id}
      displayName: "nomad-public-rt"
      routeRules:
        - networkEntityId: ${nomad-igw.id}
          destination: "0.0.0.0/0"
          destinationType: CIDR_BLOCK

  nomad-private-rt:
    type: oci:Core/routeTable:RouteTable
    properties:
      compartmentId: ${nomad-compartment.id}
      vcnId: ${nomad-vcn.id}
      displayName: "nomad-private-rt"
      routeRules:
        - networkEntityId: ${nomad-natgw.id}
          destination: "0.0.0.0/0"
          destinationType: CIDR_BLOCK

  public-subnet:
    type: oci:Core/subnet:Subnet
    properties:
      compartmentId: ${nomad-compartment.id}
      vcnId: ${nomad-vcn.id}
      cidrBlock: {{ .Config.publicSubnetCidr | quote }}
      displayName: "public-subnet"
      dnsLabel: "nomadvcnpub"
      prohibitPublicIpOnVnic: false
      routeTableId: ${nomad-public-rt.id}
      securityListIds:
        - ${public-security-list.id}
      dhcpOptionsId: ${nomad-vcn.defaultDhcpOptionsId}

  private-subnet:
    type: oci:Core/subnet:Subnet
    properties:
      compartmentId: ${nomad-compartment.id}
      vcnId: ${nomad-vcn.id}
      cidrBlock: {{ .Config.privateSubnetCidr | quote }}
      displayName: "private-subnet"
      dnsLabel: "nomadvcnpriv"
      prohibitPublicIpOnVnic: true
      routeTableId: ${nomad-private-rt.id}
      securityListIds:
        - ${private-security-list.id}
      dhcpOptionsId: ${nomad-vcn.defaultDhcpOptionsId}

  ssh-nsg:
    type: oci:Core/networkSecurityGroup:NetworkSecurityGroup
    properties:
      compartmentId: ${nomad-compartment.id}
      vcnId: ${nomad-vcn.id}
      displayName: "ssh-nsg"

  ssh-nsg-rule:
    type: oci:Core/networkSecurityGroupSecurityRule:NetworkSecurityGroupSecurityRule
    properties:
      networkSecurityGroupId: ${ssh-nsg.id}
      direction: INGRESS
      protocol: "6"
      source: {{ .Config.sshSourceCidr | quote }}
      sourceType: CIDR_BLOCK
      tcpOptions:
        destinationPortRange:
          min: 22
          max: 22
      description: "Allow SSH"

  nomad-nsg:
    type: oci:Core/networkSecurityGroup:NetworkSecurityGroup
    properties:
      compartmentId: ${nomad-compartment.id}
      vcnId: ${nomad-vcn.id}
      displayName: "nomad-nsg"

  nomad-nsg-rule-4646:
    type: oci:Core/networkSecurityGroupSecurityRule:NetworkSecurityGroupSecurityRule
    properties:
      networkSecurityGroupId: ${nomad-nsg.id}
      direction: INGRESS
      protocol: "6"
      source: {{ .Config.publicSubnetCidr | quote }}
      sourceType: CIDR_BLOCK
      tcpOptions:
        destinationPortRange:
          min: 4646
          max: 4646
      description: "Allow Nomad port 4646 from public subnet"

  nomad-nsg-rule-4647:
    type: oci:Core/networkSecurityGroupSecurityRule:NetworkSecurityGroupSecurityRule
    properties:
      networkSecurityGroupId: ${nomad-nsg.id}
      direction: INGRESS
      protocol: "6"
      source: {{ .Config.publicSubnetCidr | quote }}
      sourceType: CIDR_BLOCK
      tcpOptions:
        destinationPortRange:
          min: 4647
          max: 4647
      description: "Allow Nomad port 4647 from public subnet"

  nomad-nsg-rule-4648:
    type: oci:Core/networkSecurityGroupSecurityRule:NetworkSecurityGroupSecurityRule
    properties:
      networkSecurityGroupId: ${nomad-nsg.id}
      direction: INGRESS
      protocol: "6"
      source: {{ .Config.publicSubnetCidr | quote }}
      sourceType: CIDR_BLOCK
      tcpOptions:
        destinationPortRange:
          min: 4648
          max: 4648
      description: "Allow Nomad port 4648 from public subnet"

  traefik-nsg:
    type: oci:Core/networkSecurityGroup:NetworkSecurityGroup
    properties:
      compartmentId: ${nomad-compartment.id}
      vcnId: ${nomad-vcn.id}
      displayName: "traefik-nsg"

  traefik-nsg-rule-80:
    type: oci:Core/networkSecurityGroupSecurityRule:NetworkSecurityGroupSecurityRule
    properties:
      networkSecurityGroupId: ${traefik-nsg.id}
      direction: INGRESS
      protocol: "6"
      source: "0.0.0.0/0"
      sourceType: CIDR_BLOCK
      tcpOptions:
        destinationPortRange:
          min: 80
          max: 80
      description: "Allow HTTP"

  traefik-nsg-rule-443:
    type: oci:Core/networkSecurityGroupSecurityRule:NetworkSecurityGroupSecurityRule
    properties:
      networkSecurityGroupId: ${traefik-nsg.id}
      direction: INGRESS
      protocol: "6"
      source: "0.0.0.0/0"
      sourceType: CIDR_BLOCK
      tcpOptions:
        destinationPortRange:
          min: 443
          max: 443
      description: "Allow HTTPS"

# --- section: compute ---
  nomad-ic:
    type: oci:Core/instanceConfiguration:InstanceConfiguration
    properties:
      compartmentId: ${nomad-compartment.id}
      displayName: "nomad-ic"
      instanceDetails:
        instanceType: compute
        launchDetails:
          compartmentId: ${nomad-compartment.id}
          availabilityDomain: ${availabilityDomains[0].name}
          shape: {{ .Config.shape }}
          shapeConfig:
            ocpus: {{ .Config.ocpusPerNode }}
            memoryInGbs: {{ .Config.memoryGbPerNode }}
          sourceDetails:
            sourceType: image
            imageId: {{ .Config.imageId }}
            bootVolumeSizeInGbs: {{ .Config.bootVolSizeGb | quote }}
          createVnicDetails:
            subnetId: ${private-subnet.id}
            assignPublicIp: false
            nsgIds:
              - ${ssh-nsg.id}
              - ${nomad-nsg.id}
              - ${traefik-nsg.id}
          metadata:
            ssh_authorized_keys: {{ .Config.sshPublicKey }}
            user_data: {{ cloudInit 0 .Config }}

  nomad-pool:
    type: oci:Core/instancePool:InstancePool
    properties:
      compartmentId: ${nomad-compartment.id}
      instanceConfigurationId: ${nomad-ic.id}
      size: {{ .Config.nodeCount }}
      displayName: "nomad-pool"
      placementConfigurations:
        - availabilityDomain: ${availabilityDomains[0].name}
          primarySubnetId: ${private-subnet.id}
    options:
      dependsOn:
        - ${nomad-ic}

# --- section: loadbalancer ---
  traefik-nlb:
    type: oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer
    properties:
      compartmentId: ${nomad-compartment.id}
      subnetId: ${public-subnet.id}
      displayName: "traefik-nlb"
      isPrivate: false
      networkSecurityGroupIds:
        - ${traefik-nsg.id}

  traefik-nlb-bs-80:
    type: oci:NetworkLoadBalancer/backendSet:BackendSet
    properties:
      networkLoadBalancerId: ${traefik-nlb.id}
      name: bs-80
      policy: FIVE_TUPLE
      isPreserveSource: false
      healthChecker:
        protocol: TCP
        port: 80
    options:
      dependsOn:
        - ${traefik-nlb}

  traefik-nlb-listener-80:
    type: oci:NetworkLoadBalancer/listener:Listener
    properties:
      networkLoadBalancerId: ${traefik-nlb.id}
      name: listener-80
      defaultBackendSetName: bs-80
      protocol: TCP
      port: 80
    options:
      dependsOn:
        - ${traefik-nlb}
        - ${traefik-nlb-bs-80}

  traefik-nlb-bs-443:
    type: oci:NetworkLoadBalancer/backendSet:BackendSet
    properties:
      networkLoadBalancerId: ${traefik-nlb.id}
      name: bs-443
      policy: FIVE_TUPLE
      isPreserveSource: false
      healthChecker:
        protocol: TCP
        port: 443
    options:
      dependsOn:
        - ${traefik-nlb}
        - ${traefik-nlb-listener-80}

  traefik-nlb-listener-443:
    type: oci:NetworkLoadBalancer/listener:Listener
    properties:
      networkLoadBalancerId: ${traefik-nlb.id}
      name: listener-443
      defaultBackendSetName: bs-443
      protocol: TCP
      port: 443
    options:
      dependsOn:
        - ${traefik-nlb}
        - ${traefik-nlb-bs-443}

  traefik-nlb-bs-4646:
    type: oci:NetworkLoadBalancer/backendSet:BackendSet
    properties:
      networkLoadBalancerId: ${traefik-nlb.id}
      name: bs-4646
      policy: FIVE_TUPLE
      isPreserveSource: false
      healthChecker:
        protocol: TCP
        port: 4646
    options:
      dependsOn:
        - ${traefik-nlb}
        - ${traefik-nlb-listener-443}

  traefik-nlb-listener-4646:
    type: oci:NetworkLoadBalancer/listener:Listener
    properties:
      networkLoadBalancerId: ${traefik-nlb.id}
      name: listener-4646
      defaultBackendSetName: bs-4646
      protocol: TCP
      port: 4646
    options:
      dependsOn:
        - ${traefik-nlb}
        - ${traefik-nlb-bs-4646}

outputs:
  traefikNlbIps: ${traefik-nlb.ipAddresses}
  privateSubnetId: ${private-subnet.id}
`
}

func (p *NomadClusterProgram) Run(cfg map[string]string) pulumi.RunFunc {
	return func(ctx *pulumi.Context) error {
		tenancyOCID := cfgOr(cfg, "OCI_TENANCY_OCID", "")
		sshPublicKey := cfgOr(cfg, "OCI_USER_SSH_PUBLIC_KEY", "")

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
			cfgOr(cfg, "sshSourceCidr", "0.0.0.0/0"),
			cfgOr(cfg, "publicSubnetCidr", "10.0.1.0/24"))
		if err != nil {
			return err
		}

		// 5. Instance pool (homogeneous)
		pools, err := createInstancePools(ctx, tenancyOCID, comp.ID(), net, nsgs, cfg, nodeCount, sshPublicKey)
		if err != nil {
			return err
		}

		// 6. Network Load Balancer
		nlb, err := createNLB(ctx, comp.ID(), net.publicSubnetID, nsgs.traefikNsgID, nsgs.nebulaNsgID, pools.instanceIDs, nodeCount)
		if err != nil {
			return err
		}

		ctx.Export("traefikNlbIps", nlb.IpAddresses)
		ctx.Export("privateSubnetId", net.privateSubnetID)

		// Nebula lighthouse address: NLB public IP + UDP port
		nebulaAddr := nlb.IpAddresses.ApplyT(func(addrs []networkloadbalancer.NetworkLoadBalancerIpAddress) string {
			for _, a := range addrs {
				if a.IpAddress != nil && (a.IsPublic == nil || *a.IsPublic) {
					return *a.IpAddress + ":41820"
				}
			}
			return ""
		}).(pulumi.StringOutput)
		ctx.Export("nebulaLighthouseAddr", nebulaAddr)

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

func createNSGs(ctx *pulumi.Context, compartmentID, vcnID pulumi.IDOutput, sshSourceCidr, publicSubnetCidr string) (nsgResult, error) {
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
			Source: pulumi.String(publicSubnetCidr), SourceType: pulumi.String("CIDR_BLOCK"),
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
			Source: pulumi.String("0.0.0.0/0"), SourceType: pulumi.String("CIDR_BLOCK"),
			TcpOptions: core.NetworkSecurityGroupSecurityRuleTcpOptionsArgs{
				DestinationPortRange: core.NetworkSecurityGroupSecurityRuleTcpOptionsDestinationPortRangeArgs{
					Min: pulumi.Int(port), Max: pulumi.Int(port),
				},
			},
			Description: pulumi.Sprintf("Allow Traefik port %d", port),
		}); err != nil {
			return nsgResult{}, err
		}
	}

	// Nebula NSG — UDP 41820 for Nebula mesh overlay
	nebulaNsg, err := core.NewNetworkSecurityGroup(ctx, "nebula-nsg", &core.NetworkSecurityGroupArgs{
		CompartmentId: compartmentID, VcnId: vcnID, DisplayName: pulumi.String("nebula-nsg"),
	})
	if err != nil {
		return nsgResult{}, err
	}
	if _, err := core.NewNetworkSecurityGroupSecurityRule(ctx, "nebula-nsg-rule", &core.NetworkSecurityGroupSecurityRuleArgs{
		NetworkSecurityGroupId: nebulaNsg.ID(), Direction: pulumi.String("INGRESS"),
		Protocol: pulumi.String("17"), // UDP
		Source:   pulumi.String("0.0.0.0/0"), SourceType: pulumi.String("CIDR_BLOCK"),
		UdpOptions: core.NetworkSecurityGroupSecurityRuleUdpOptionsArgs{
			DestinationPortRange: core.NetworkSecurityGroupSecurityRuleUdpOptionsDestinationPortRangeArgs{
				Min: pulumi.Int(41820), Max: pulumi.Int(41820),
			},
		},
		Description: pulumi.String("Allow Nebula mesh UDP"),
	}); err != nil {
		return nsgResult{}, err
	}

	return nsgResult{
		sshNsgID:     sshNsg.ID(),
		nomadNsgID:   nomadNsg.ID(),
		traefikNsgID: traefikNsg.ID(),
		nebulaNsgID:  nebulaNsg.ID(),
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
	ocpusPerNode, _ := strconv.Atoi(cfgOr(cfg, "ocpusPerNode", "1"))
	if ocpusPerNode < 1 {
		ocpusPerNode = 1
	}
	memoryGbPerNode, _ := strconv.Atoi(cfgOr(cfg, "memoryGbPerNode", "6"))
	if memoryGbPerNode < 1 {
		memoryGbPerNode = 6
	}
	nomadVersion := cfgOr(cfg, "nomadVersion", "1.10.3")
	consulVersion := cfgOr(cfg, "consulVersion", "1.21.3")

	// Get first availability domain
	adsResult := identity.GetAvailabilityDomainsOutput(ctx, identity.GetAvailabilityDomainsOutputArgs{
		CompartmentId: pulumi.String(tenancyOCID),
	})
	adName := adsResult.AvailabilityDomains().Index(pulumi.Int(0)).Name()

	var agentBootstrap []byte
	if v, ok := cfg[agentinject.CfgKeyAgentBootstrap]; ok && v != "" {
		agentBootstrap = []byte(v)
	}
	cloudInitB64 := buildCloudInit(ocpusPerNode, memoryGbPerNode, nodeCount, nomadVersion, consulVersion, nil, nil, agentBootstrap)

	nsgIDs := pulumi.StringArray{
		nsgs.sshNsgID.ToStringOutput(),
		nsgs.nomadNsgID.ToStringOutput(),
		nsgs.traefikNsgID.ToStringOutput(),
		nsgs.nebulaNsgID.ToStringOutput(),
	}

	instanceConfig, err := core.NewInstanceConfiguration(ctx, "nomad-ic", &core.InstanceConfigurationArgs{
		CompartmentId: compartmentID,
		DisplayName:   pulumi.String("nomad-ic"),
		InstanceDetails: core.InstanceConfigurationInstanceDetailsArgs{
			InstanceType: pulumi.String("compute"),
			LaunchDetails: core.InstanceConfigurationInstanceDetailsLaunchDetailsArgs{
				CompartmentId:      compartmentID.ToStringOutput().ToStringPtrOutput(),
				AvailabilityDomain: adName.ToStringPtrOutput(),
				Shape:              pulumi.StringPtr(shape),
				ShapeConfig: core.InstanceConfigurationInstanceDetailsLaunchDetailsShapeConfigArgs{
					Ocpus:       pulumi.Float64Ptr(float64(ocpusPerNode)),
					MemoryInGbs: pulumi.Float64Ptr(float64(memoryGbPerNode)),
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
					"user_data":           pulumi.String(cloudInitB64),
				},
				DisplayName: pulumi.StringPtr("nomad-node"),
			},
		},
	})
	if err != nil {
		return poolsResult{}, err
	}

	pool, err := core.NewInstancePool(ctx, "nomad-pool", &core.InstancePoolArgs{
		CompartmentId:           compartmentID,
		InstanceConfigurationId: instanceConfig.ID(),
		Size:                    pulumi.Int(nodeCount),
		DisplayName:             pulumi.String("nomad-pool"),
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

	poolInstancesResult := core.GetInstancePoolInstancesOutput(ctx, core.GetInstancePoolInstancesOutputArgs{
		CompartmentId:  compartmentID.ToStringOutput(),
		InstancePoolId: pool.ID().ToStringOutput(),
	}, pulumi.DependsOn([]pulumi.Resource{pool}))

	allIDs := poolInstancesResult.Instances().ApplyT(func(instances []core.GetInstancePoolInstancesInstance) []string {
		ids := make([]string, len(instances))
		for i, inst := range instances {
			ids[i] = inst.InstanceId
		}
		return ids
	}).(pulumi.StringArrayOutput)

	return poolsResult{instanceIDs: allIDs, adName: adName}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// 5. Network Load Balancer
// ─────────────────────────────────────────────────────────────────────────────

func createNLB(
	ctx *pulumi.Context,
	compartmentID pulumi.IDOutput,
	publicSubnetID pulumi.IDOutput,
	traefikNsgID pulumi.IDOutput,
	nebulaNsgID pulumi.IDOutput,
	instanceIDs pulumi.StringArrayOutput,
	nodeCount int,
) (*networkloadbalancer.NetworkLoadBalancer, error) {
	nlb, err := networkloadbalancer.NewNetworkLoadBalancer(ctx, "traefik-nlb", &networkloadbalancer.NetworkLoadBalancerArgs{
		CompartmentId: compartmentID,
		SubnetId:      publicSubnetID,
		DisplayName:   pulumi.String("traefik-nlb"),
		IsPrivate:     pulumi.Bool(false),
		NetworkSecurityGroupIds: pulumi.StringArray{
			traefikNsgID.ToStringOutput(),
			nebulaNsgID.ToStringOutput(),
		},
	})
	if err != nil {
		return nil, err
	}

	// TCP backend sets/listeners/backends for Traefik and Nomad API.
	// OCI NLB rejects concurrent mutations (409 Conflict), so all port
	// resources must be serialized via dependsOn.
	var prevResource pulumi.Resource = nlb
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
		}, pulumi.DependsOn([]pulumi.Resource{prevResource}))
		if err != nil {
			return nil, err
		}
		prevResource = bs

		listener, err := networkloadbalancer.NewListener(ctx, fmt.Sprintf("traefik-nlb-listener-%d", port), &networkloadbalancer.ListenerArgs{
			NetworkLoadBalancerId: nlb.ID(),
			Name:                  pulumi.String(fmt.Sprintf("listener-%d", port)),
			DefaultBackendSetName: bs.Name,
			Protocol:              pulumi.String("TCP"),
			Port:                  pulumi.Int(port),
		}, pulumi.DependsOn([]pulumi.Resource{prevResource}))
		if err != nil {
			return nil, err
		}
		prevResource = listener

		for i := 0; i < nodeCount; i++ {
			i := i
			targetID := instanceIDs.Index(pulumi.Int(i))
			backend, err := networkloadbalancer.NewBackend(ctx, fmt.Sprintf("traefik-nlb-backend-%d-%d", port, i), &networkloadbalancer.BackendArgs{
				NetworkLoadBalancerId: nlb.ID(),
				BackendSetName:        bs.Name,
				TargetId:              targetID.ToStringPtrOutput(),
				Port:                  pulumi.Int(port),
			}, pulumi.DependsOn([]pulumi.Resource{prevResource}))
			if err != nil {
				return nil, err
			}
			prevResource = backend
		}
	}

	// Nebula UDP 41820 backend set + listener
	nebulaBS, err := networkloadbalancer.NewBackendSet(ctx, "nebula-nlb-bs", &networkloadbalancer.BackendSetArgs{
		NetworkLoadBalancerId: nlb.ID(),
		Name:                  pulumi.String("bs-nebula"),
		Policy:                pulumi.String("FIVE_TUPLE"),
		IsPreserveSource:      pulumi.Bool(false),
		HealthChecker: networkloadbalancer.BackendSetHealthCheckerArgs{
			Protocol: pulumi.String("TCP"),
			Port:     pulumi.Int(22), // health check on SSH since UDP has no native health check
		},
	}, pulumi.DependsOn([]pulumi.Resource{prevResource}))
	if err != nil {
		return nil, err
	}
	prevResource = nebulaBS

	nebulaListener, err := networkloadbalancer.NewListener(ctx, "nebula-nlb-listener", &networkloadbalancer.ListenerArgs{
		NetworkLoadBalancerId: nlb.ID(),
		Name:                  pulumi.String("listener-nebula"),
		DefaultBackendSetName: nebulaBS.Name,
		Protocol:              pulumi.String("UDP"),
		Port:                  pulumi.Int(41820),
	}, pulumi.DependsOn([]pulumi.Resource{prevResource}))
	if err != nil {
		return nil, err
	}
	prevResource = nebulaListener

	for i := 0; i < nodeCount; i++ {
		i := i
		targetID := instanceIDs.Index(pulumi.Int(i))
		backend, err := networkloadbalancer.NewBackend(ctx, fmt.Sprintf("nebula-nlb-backend-%d", i), &networkloadbalancer.BackendArgs{
			NetworkLoadBalancerId: nlb.ID(),
			BackendSetName:        nebulaBS.Name,
			TargetId:              targetID.ToStringPtrOutput(),
			Port:                  pulumi.Int(41820),
		}, pulumi.DependsOn([]pulumi.Resource{prevResource}))
		if err != nil {
			return nil, err
		}
		prevResource = backend
	}

	return nlb, nil
}
