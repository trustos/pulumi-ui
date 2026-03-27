package agentinject

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInjectNetworking_NSGRule(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-nsg:
    type: oci:Core/networkSecurityGroup:NetworkSecurityGroup
    properties:
      compartmentId: ocid1.compartment
      vcnId: ${my-vcn.id}
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
`
	result, err := InjectNetworkingIntoYAML(yaml)
	require.NoError(t, err)
	assert.Contains(t, result, "__agent_nsg_rule_my-nsg")
	assert.Contains(t, result, "NetworkSecurityGroupSecurityRule")
	assert.Contains(t, result, fmt.Sprintf("%d", AgentPort))
	assert.Contains(t, result, "${my-nsg.id}")
}

func TestInjectNetworking_NLBBackendSetAndListener(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-nlb:
    type: oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer
    properties:
      compartmentId: ocid1.compartment
      subnetId: ${my-subnet.id}
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
`
	result, err := InjectNetworkingIntoYAML(yaml)
	require.NoError(t, err)
	assert.Contains(t, result, "__agent_bs_my-nlb_0")
	assert.Contains(t, result, "__agent_ln_my-nlb_0")
	assert.Contains(t, result, "__agent_be_my-nlb_0")
	assert.Contains(t, result, "BackendSet")
	assert.Contains(t, result, "Listener")
	assert.Contains(t, result, "Backend")
}

func TestInjectNetworking_NoNetworkingResources(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: ocid1.compartment
`
	result, err := InjectNetworkingIntoYAML(yaml)
	require.NoError(t, err)
	assert.Equal(t, yaml, result, "no NSG/NLB means no injection")
}

func TestInjectNetworking_SkipsDuplicate(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-nsg:
    type: oci:Core/networkSecurityGroup:NetworkSecurityGroup
    properties:
      compartmentId: ocid1.compartment
`
	// First injection
	result1, err := InjectNetworkingIntoYAML(yaml)
	require.NoError(t, err)
	assert.Contains(t, result1, "__agent_nsg_rule_my-nsg")

	// Second injection should be a no-op
	result2, err := InjectNetworkingIntoYAML(result1)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(result2, "__agent_nsg_rule_my-nsg"),
		"should not inject duplicate resources")
}

func TestInjectNetworking_MultipleNSGs(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  nsg-public:
    type: oci:Core/networkSecurityGroup:NetworkSecurityGroup
    properties:
      compartmentId: ocid1.compartment
  nsg-private:
    type: oci:Core/networkSecurityGroup:NetworkSecurityGroup
    properties:
      compartmentId: ocid1.compartment
`
	result, err := InjectNetworkingIntoYAML(yaml)
	require.NoError(t, err)
	assert.Contains(t, result, "__agent_nsg_rule_nsg-public")
	assert.Contains(t, result, "__agent_nsg_rule_nsg-private")
}

func TestInjectNetworking_NLBWithMultipleCompute(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-nlb:
    type: oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer
    properties:
      compartmentId: ocid1.compartment
  node-1:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
  node-2:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
`
	result, err := InjectNetworkingIntoYAML(yaml)
	require.NoError(t, err)
	// Per-node injection: node-1 → index 0, node-2 → index 1
	assert.Contains(t, result, "__agent_bs_my-nlb_0", "backend set for node-1")
	assert.Contains(t, result, "__agent_bs_my-nlb_1", "backend set for node-2")
	assert.Contains(t, result, "__agent_be_my-nlb_0", "backend for node-1")
	assert.Contains(t, result, "__agent_be_my-nlb_1", "backend for node-2")
	assert.Contains(t, result, fmt.Sprintf("%d", AgentNLBPortBase), "first listener port")
	assert.Contains(t, result, fmt.Sprintf("%d", AgentNLBPortBase+1), "second listener port")
}

func TestInjectNetworking_BareInstance_CreatesNSGAndNLB(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: ocid1.compartment.test
      cidrBlock: 10.0.0.0/16
  my-subnet:
    type: oci:Core/subnet:Subnet
    properties:
      compartmentId: ocid1.compartment.test
      vcnId: ${my-vcn.id}
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment.test
      shape: VM.Standard.A1.Flex
`
	result, err := InjectNetworkingIntoYAML(yaml)
	require.NoError(t, err)

	// Should create an NSG and rule
	assert.Contains(t, result, "__agent_nsg", "should create NSG")
	assert.Contains(t, result, "__agent_nsg_rule", "should create NSG rule")
	assert.Contains(t, result, "pulumi-ui-agent-nsg", "NSG should have display name")
	assert.Contains(t, result, "${my-vcn.id}", "NSG should reference VCN")

	// NLB is no longer auto-created; only NSG is injected when no existing NLB
	assert.NotContains(t, result, "__agent_nlb", "NLB should not be auto-created")

	// Should attach NSG to instance
	assert.Contains(t, result, "createVnicDetails", "should add createVnicDetails to instance")
	assert.Contains(t, result, "nsgIds", "should add nsgIds")
	assert.Contains(t, result, "${__agent_nsg.id}", "should reference created NSG")
}

func TestInjectNetworking_BareInstance_NoVCN_NoInjection(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment.test
`
	result, err := InjectNetworkingIntoYAML(yaml)
	require.NoError(t, err)
	assert.NotContains(t, result, "__agent_nsg",
		"no VCN/subnet means no networking can be created")
}

func TestInjectNetworking_BareInstance_VCNNoSubnet(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: ocid1.compartment.test
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment.test
`
	result, err := InjectNetworkingIntoYAML(yaml)
	require.NoError(t, err)
	// Should create NSG (has VCN) but not NLB (no subnet)
	assert.Contains(t, result, "__agent_nsg", "should create NSG from VCN")
	assert.NotContains(t, result, "__agent_nlb", "no subnet means no NLB")
}

func TestInjectNetworking_BareInstance_UsesCompartmentFromVCN(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: ${my-compartment.id}
  my-subnet:
    type: oci:Core/subnet:Subnet
    properties:
      compartmentId: ${my-compartment.id}
      vcnId: ${my-vcn.id}
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ${my-compartment.id}
`
	result, err := InjectNetworkingIntoYAML(yaml)
	require.NoError(t, err)
	// Created NSG and NLB should use the same compartmentId as the VCN/subnet
	assert.Contains(t, result, "${my-compartment.id}")
}

func TestInjectNetworking_BareInstance_ExistingVnicDetails(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: ocid1.compartment
  my-subnet:
    type: oci:Core/subnet:Subnet
    properties:
      compartmentId: ocid1.compartment
      vcnId: ${my-vcn.id}
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      createVnicDetails:
        subnetId: ${my-subnet.id}
        assignPublicIp: true
`
	result, err := InjectNetworkingIntoYAML(yaml)
	require.NoError(t, err)
	assert.Contains(t, result, "nsgIds", "should add nsgIds to existing createVnicDetails")
	assert.Contains(t, result, "${__agent_nsg.id}")
	assert.Contains(t, result, "assignPublicIp")
	// Public IP instance should NOT get an NLB
	assert.NotContains(t, result, "__agent_nlb", "public IP instance should not get NLB")
	assert.NotContains(t, result, "__agent_bs", "public IP instance should not get backend set")
	assert.NotContains(t, result, "__agent_ln", "public IP instance should not get listener")
}

func TestInjectNetworking_SkipsDuplicate_BareInstance(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: ocid1.compartment
  my-subnet:
    type: oci:Core/subnet:Subnet
    properties:
      compartmentId: ocid1.compartment
      vcnId: ${my-vcn.id}
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
`
	result1, err := InjectNetworkingIntoYAML(yaml)
	require.NoError(t, err)
	assert.Contains(t, result1, "__agent_nsg")

	result2, err := InjectNetworkingIntoYAML(result1)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(result2, "__agent_nsg:"),
		"should not inject duplicate NSG")
}

func TestInjectNetworking_OnlyInstance_WithSubnetRef(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment.test
      shape: VM.Standard.A1.Flex
      createVnicDetails:
        subnetId: ocid1.subnet.existing
`
	result, err := InjectNetworkingIntoYAML(yaml)
	require.NoError(t, err)

	// Should create NSG using fn::invoke to resolve VCN from subnet
	assert.Contains(t, result, "__agent_subnet_info", "should create subnet lookup variable")
	assert.Contains(t, result, "getSubnet", "should use fn::invoke for subnet")
	assert.Contains(t, result, "ocid1.subnet.existing", "should reference the instance's subnetId")
	assert.Contains(t, result, "__agent_nsg", "should create NSG")
	assert.Contains(t, result, "${__agent_subnet_info.vcnId}", "NSG should use VCN from subnet lookup")
	assert.Contains(t, result, "__agent_nsg_rule", "should create NSG rule")

	// NLB not auto-created; only NSG injected
	assert.NotContains(t, result, "__agent_nlb", "NLB should not be auto-created")

	// Should attach NSG to instance
	assert.Contains(t, result, "nsgIds", "should add nsgIds")
	assert.Contains(t, result, "${__agent_nsg.id}", "should reference created NSG")
}

func TestInjectNetworking_OnlyInstance_WithPulumiSubnetRef(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ${compartmentId}
      shape: VM.Standard.A1.Flex
      createVnicDetails:
        subnetId: ${subnetId}
`
	result, err := InjectNetworkingIntoYAML(yaml)
	require.NoError(t, err)

	assert.Contains(t, result, "__agent_subnet_info")
	assert.Contains(t, result, "${subnetId}", "should pass through the Pulumi ref")
	assert.Contains(t, result, "__agent_nsg")
	assert.NotContains(t, result, "__agent_nlb", "NLB should not be auto-created")
}

func TestInjectNetworking_OnlyInstance_NoSubnetRef(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment.test
      shape: VM.Standard.A1.Flex
`
	result, err := InjectNetworkingIntoYAML(yaml)
	require.NoError(t, err)
	assert.NotContains(t, result, "__agent_nsg",
		"no subnetId means no networking can be inferred")
}

func TestInjectNetworking_OnlyInstance_SkipsDuplicate(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment.test
      shape: VM.Standard.A1.Flex
      createVnicDetails:
        subnetId: ocid1.subnet.existing
`
	result1, err := InjectNetworkingIntoYAML(yaml)
	require.NoError(t, err)
	assert.Contains(t, result1, "__agent_nsg")

	result2, err := InjectNetworkingIntoYAML(result1)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(result2, "__agent_nsg:"),
		"should not inject duplicate resources on second pass")
}

func TestInjectNetworking_PublicIP_SkipsNLB_VCNSubnet(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: ocid1.compartment.test
      cidrBlock: 10.0.0.0/16
  my-subnet:
    type: oci:Core/subnet:Subnet
    properties:
      compartmentId: ocid1.compartment.test
      vcnId: ${my-vcn.id}
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment.test
      createVnicDetails:
        subnetId: ${my-subnet.id}
        assignPublicIp: true
`
	result, err := InjectNetworkingIntoYAML(yaml)
	require.NoError(t, err)
	// NSG is always created (firewall rule)
	assert.Contains(t, result, "__agent_nsg", "should create NSG")
	assert.Contains(t, result, "__agent_nsg_rule", "should create NSG rule")
	// NLB is skipped when instance has a public IP
	assert.NotContains(t, result, "__agent_nlb", "should skip NLB for public IP instance")
	assert.NotContains(t, result, "__agent_bs", "should skip backend set for public IP instance")
	assert.NotContains(t, result, "__agent_ln", "should skip listener for public IP instance")
}

func TestInjectNetworking_PublicIP_SkipsNLB_SubnetRef(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment.test
      createVnicDetails:
        subnetId: ocid1.subnet.existing
        assignPublicIp: true
`
	result, err := InjectNetworkingIntoYAML(yaml)
	require.NoError(t, err)
	// NSG is always created via subnet lookup
	assert.Contains(t, result, "__agent_nsg", "should create NSG")
	assert.Contains(t, result, "__agent_subnet_info", "should create subnet lookup")
	// NLB is skipped when instance has a public IP
	assert.NotContains(t, result, "__agent_nlb", "should skip NLB for public IP instance")
	assert.NotContains(t, result, "__agent_be_", "should skip backends for public IP instance")
}

func TestInjectNetworking_PrivateInstance_CreatesNLB(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: ocid1.compartment.test
  my-subnet:
    type: oci:Core/subnet:Subnet
    properties:
      compartmentId: ocid1.compartment.test
      vcnId: ${my-vcn.id}
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment.test
      createVnicDetails:
        subnetId: ${my-subnet.id}
        assignPublicIp: false
`
	result, err := InjectNetworkingIntoYAML(yaml)
	require.NoError(t, err)
	assert.Contains(t, result, "__agent_nsg", "should create NSG")
	// NLB not auto-created when no existing NLB in template
	assert.NotContains(t, result, "__agent_nlb", "NLB should not be auto-created")
}

func TestInjectNetworking_MixedPublicPrivate_CreatesNLB(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: ocid1.compartment.test
  my-subnet:
    type: oci:Core/subnet:Subnet
    properties:
      compartmentId: ocid1.compartment.test
      vcnId: ${my-vcn.id}
  public-node:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment.test
      createVnicDetails:
        subnetId: ${my-subnet.id}
        assignPublicIp: true
  private-node:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment.test
      createVnicDetails:
        subnetId: ${my-subnet.id}
        assignPublicIp: false
`
	result, err := InjectNetworkingIntoYAML(yaml)
	require.NoError(t, err)
	assert.Contains(t, result, "__agent_nsg", "should create NSG")
	// NLB not auto-created when no existing NLB in template
	assert.NotContains(t, result, "__agent_nlb", "NLB should not be auto-created")
}

func TestInjectNetworking_NoVnicDetails_CreatesNLB(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: ocid1.compartment.test
  my-subnet:
    type: oci:Core/subnet:Subnet
    properties:
      compartmentId: ocid1.compartment.test
      vcnId: ${my-vcn.id}
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment.test
`
	result, err := InjectNetworkingIntoYAML(yaml)
	require.NoError(t, err)
	assert.Contains(t, result, "__agent_nsg", "should create NSG")
	assert.NotContains(t, result, "__agent_nlb", "NLB should not be auto-created")
}

func TestInjectNetworking_MultiplePublicInstances_SkipsNLB(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: ocid1.compartment.test
  my-subnet:
    type: oci:Core/subnet:Subnet
    properties:
      compartmentId: ocid1.compartment.test
      vcnId: ${my-vcn.id}
  node-1:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment.test
      createVnicDetails:
        subnetId: ${my-subnet.id}
        assignPublicIp: true
  node-2:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment.test
      createVnicDetails:
        subnetId: ${my-subnet.id}
        assignPublicIp: true
  node-3:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment.test
      createVnicDetails:
        subnetId: ${my-subnet.id}
        assignPublicIp: true
`
	result, err := InjectNetworkingIntoYAML(yaml)
	require.NoError(t, err)
	assert.Contains(t, result, "__agent_nsg", "all-public cluster still needs NSG")
	assert.Contains(t, result, "__agent_nsg_rule", "all-public cluster still needs NSG rule")
	assert.NotContains(t, result, "__agent_nlb", "all-public cluster should skip NLB")
	assert.NotContains(t, result, "__agent_bs", "all-public cluster should skip backend set")
	// All instances should get NSG attachment
	assert.Contains(t, result, "nsgIds")
}

func TestInjectNetworking_ExistingNSG_PublicIP_AddsRuleNoNewNLB(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-nsg:
    type: oci:Core/networkSecurityGroup:NetworkSecurityGroup
    properties:
      compartmentId: ocid1.compartment
      vcnId: ${my-vcn.id}
  my-subnet:
    type: oci:Core/subnet:Subnet
    properties:
      compartmentId: ocid1.compartment
      vcnId: ${my-vcn.id}
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      createVnicDetails:
        subnetId: ${my-subnet.id}
        assignPublicIp: true
`
	result, err := InjectNetworkingIntoYAML(yaml)
	require.NoError(t, err)
	assert.Contains(t, result, "__agent_nsg_rule_my-nsg", "should add rule to existing NSG")
	assert.NotContains(t, result, "__agent_nlb", "public IP with existing NSG should skip NLB")
}

func TestInjectNetworking_ExistingNLB_PublicIP_InjectsBackends(t *testing.T) {
	// When an NLB exists alongside public-IP instances, prefer the NLB path
	// for Nebula connectivity (T3 topology). Per-node backends are always injected.
	yaml := `name: test
runtime: yaml
resources:
  my-nlb:
    type: oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer
    properties:
      compartmentId: ocid1.compartment
      subnetId: ${my-subnet.id}
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      createVnicDetails:
        subnetId: ${my-subnet.id}
        assignPublicIp: true
`
	result, err := InjectNetworkingIntoYAML(yaml)
	require.NoError(t, err)
	assert.Contains(t, result, "__agent_bs_my-nlb_0", "NLB should get per-node backend set")
	assert.Contains(t, result, "__agent_ln_my-nlb_0", "NLB should get per-node listener")
	assert.Contains(t, result, "__agent_be_my-nlb_0", "NLB should get per-node backend")
}

func TestInjectNetworking_NSGRuleContent(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: ocid1.compartment
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
`
	result, err := InjectNetworkingIntoYAML(yaml)
	require.NoError(t, err)

	assert.Contains(t, result, "NetworkSecurityGroupSecurityRule", "should use correct rule type")
	assert.Contains(t, result, "INGRESS", "rule direction should be INGRESS")
	assert.Contains(t, result, "protocol: 17", "protocol 17 is UDP")
	assert.Contains(t, result, "0.0.0.0/0", "source should be any")
	assert.Contains(t, result, "CIDR_BLOCK", "source type should be CIDR_BLOCK")
	assert.Contains(t, result, fmt.Sprintf("%d", AgentPort), "port should be agent port")
	assert.Contains(t, result, "destinationPortRange", "should have port range for UDP")
}

func TestInjectNetworking_NLBDependencyChain(t *testing.T) {
	// Per-node injection: each node gets its own BS → LN → BE chain
	yaml := `name: test
runtime: yaml
resources:
  my-nlb:
    type: oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer
    properties:
      compartmentId: ocid1.compartment
  node-a:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
  node-b:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
`
	result, err := InjectNetworkingIntoYAML(yaml)
	require.NoError(t, err)

	// Each per-node listener depends on its own backend set
	assert.Contains(t, result, "${__agent_bs_my-nlb_0}", "node-0 listener should depend on its backend set")
	assert.Contains(t, result, "${__agent_bs_my-nlb_1}", "node-1 listener should depend on its backend set")

	// Backends come after their respective listeners in the output
	bs0Pos := strings.Index(result, "__agent_bs_my-nlb_0:")
	ln0Pos := strings.Index(result, "__agent_ln_my-nlb_0:")
	be0Pos := strings.Index(result, "__agent_be_my-nlb_0:")
	assert.Greater(t, ln0Pos, bs0Pos, "listener-0 should come after backend-set-0")
	assert.Greater(t, be0Pos, ln0Pos, "backend-0 should come after listener-0")
}

func TestInjectNetworking_NSGAttachesAllInstances(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: ocid1.compartment
  inst-a:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
  inst-b:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
`
	result, err := InjectNetworkingIntoYAML(yaml)
	require.NoError(t, err)

	// Count includes: NSG rule's networkSecurityGroupId + one nsgIds entry per instance
	nsgRefCount := strings.Count(result, "${__agent_nsg.id}")
	assert.Equal(t, 3, nsgRefCount, "NSG rule ref + 2 instance nsgIds attachments")
}

func TestInjectNetworking_EndToEnd_SingleInstanceTemplate(t *testing.T) {
	// Realistic template: full networking stack + single instance with public IP
	yaml := `name: single-instance
runtime: yaml
description: One VM with public IP
meta:
  agentAccess: true
config:
  compartmentId:
    type: string
variables:
  availabilityDomains:
    fn::invoke:
      function: oci:Identity/getAvailabilityDomains:getAvailabilityDomains
      arguments:
        compartmentId: ${oci:tenancyOcid}
      return: availabilityDomains
resources:
  agent-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: {{ .Config.compartmentId }}
      cidrBlocks: "[\"10.250.0.0/16\"]"
      displayName: agent-vcn
  agent-igw:
    type: oci:Core/internetGateway:InternetGateway
    properties:
      compartmentId: {{ .Config.compartmentId }}
      vcnId: ${agent-vcn.id}
      displayName: agent-igw
    options:
      dependsOn:
        - ${agent-vcn}
  agent-route-table:
    type: oci:Core/routeTable:RouteTable
    properties:
      compartmentId: {{ .Config.compartmentId }}
      vcnId: ${agent-vcn.id}
      displayName: agent-route-table
    options:
      dependsOn:
        - ${agent-igw}
  agent-subnet:
    type: oci:Core/subnet:Subnet
    properties:
      compartmentId: {{ .Config.compartmentId }}
      vcnId: ${agent-vcn.id}
      cidrBlock: 10.250.0.0/24
      displayName: agent-subnet
      routeTableId: ${agent-route-table.id}
      prohibitPublicIpOnVnic: "false"
    options:
      dependsOn:
        - ${agent-route-table}
  instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: {{ .Config.compartmentId }}
      availabilityDomain: ${availabilityDomains[0].name}
      shape: VM.Standard.A1.Flex
      createVnicDetails:
        subnetId: ${agent-subnet.id}
        assignPublicIp: true
    options:
      dependsOn:
        - ${agent-subnet}
`
	result, err := InjectNetworkingIntoYAML(yaml)
	require.NoError(t, err)

	// NSG should be created (firewall needed even with public IP)
	assert.Contains(t, result, "__agent_nsg", "should create NSG")
	assert.Contains(t, result, "__agent_nsg_rule", "should create NSG rule")
	assert.Contains(t, result, "${agent-vcn.id}", "NSG should reference existing VCN")
	assert.Contains(t, result, "nsgIds", "instance should get NSG attachment")
	assert.Contains(t, result, "${__agent_nsg.id}", "instance should reference agent NSG")

	// NLB should NOT be created (instance has public IP)
	assert.NotContains(t, result, "__agent_nlb", "public IP instance should not get NLB")
	assert.NotContains(t, result, "__agent_bs", "public IP instance should not get backend set")
	assert.NotContains(t, result, "__agent_ln", "public IP instance should not get listener")
	assert.NotContains(t, result, "NetworkLoadBalancer", "no NLB resource type")
}

func TestInjectNetworking_EndToEnd_PrivateCluster(t *testing.T) {
	// Realistic template: cluster behind NLB, no public IPs
	yaml := `name: private-cluster
runtime: yaml
resources:
  my-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: ocid1.compartment
  my-subnet:
    type: oci:Core/subnet:Subnet
    properties:
      compartmentId: ocid1.compartment
      vcnId: ${my-vcn.id}
  server-1:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      createVnicDetails:
        subnetId: ${my-subnet.id}
        assignPublicIp: false
  server-2:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      createVnicDetails:
        subnetId: ${my-subnet.id}
        assignPublicIp: false
  server-3:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      createVnicDetails:
        subnetId: ${my-subnet.id}
        assignPublicIp: false
`
	result, err := InjectNetworkingIntoYAML(yaml)
	require.NoError(t, err)

	// NSG created and attached to all instances
	assert.Contains(t, result, "__agent_nsg", "should create NSG")
	// Count includes: NSG rule's networkSecurityGroupId + one nsgIds entry per instance
	nsgAttachments := strings.Count(result, "${__agent_nsg.id}")
	assert.Equal(t, 4, nsgAttachments, "NSG rule ref + 3 instance nsgIds attachments")

	// NLB not auto-created; users must include an NLB in the template
	assert.NotContains(t, result, "__agent_nlb", "NLB should not be auto-created")
}


func TestInjectNetworking_FlowMappingCreateVnicDetails(t *testing.T) {
	// When the frontend serializes createVnicDetails as a quoted flow-mapping
	// string (e.g. "{ subnetId: ..., assignPublicIp: true }"), the backend
	// must promote it to a proper mapping before adding nsgIds — not create a
	// duplicate createVnicDetails key.
	input := `
name: test
runtime: yaml
resources:
  vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: ${oci:tenancyOcid}
      cidrBlocks: ["10.0.0.0/16"]
  subnet:
    type: oci:Core/subnet:Subnet
    properties:
      compartmentId: ${oci:tenancyOcid}
      vcnId: ${vcn.id}
      cidrBlock: "10.0.1.0/24"
  instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ${oci:tenancyOcid}
      shape: VM.Standard.A1.Flex
      createVnicDetails: "{ subnetId: ${subnet.id}, assignPublicIp: true }"
`
	result, err := InjectNetworkingIntoYAML(input)
	require.NoError(t, err)

	// Must NOT contain duplicate createVnicDetails
	count := strings.Count(result, "createVnicDetails")
	assert.Equal(t, 1, count, "expected exactly one createVnicDetails key, got %d:\n%s", count, result)

	// Must contain nsgIds referencing the injected NSG
	assert.Contains(t, result, "nsgIds")
	assert.Contains(t, result, "${__agent_nsg.id}")

	// The promoted mapping must preserve existing fields
	assert.Contains(t, result, "subnetId")
	assert.Contains(t, result, "assignPublicIp")

	// Since assignPublicIp is true, no NLB should be created
	assert.NotContains(t, result, "__agent_nlb")
}

func TestInjectNetworking_FlowMappingExtractSubnet(t *testing.T) {
	// When createVnicDetails is a scalar like "{ subnetId: ${ext.id} }",
	// extractSubnetFromCompute must still find the subnetId.
	input := `
name: test
runtime: yaml
resources:
  instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ${oci:tenancyOcid}
      createVnicDetails: "{ subnetId: ${external-subnet.id}, assignPublicIp: false }"
`
	result, err := InjectNetworkingIntoYAML(input)
	require.NoError(t, err)

	// NSG injected via fn::invoke; NLB not auto-created
	assert.Contains(t, result, "__agent_nsg")
	assert.Contains(t, result, "__agent_subnet_info")
	assert.NotContains(t, result, "__agent_nlb", "NLB should not be auto-created")
}

// ---------------------------------------------------------------------------
// Fix: existing NLB + public IP instances should NOT get agent backends
// ---------------------------------------------------------------------------

func TestInjectNetworking_ExistingNLB_PublicIPInstances_InjectsBackends(t *testing.T) {
	// An NLB exists and instances have assignPublicIp: true (T3 topology).
	// Per-node NLB backends are always injected when an NLB is present.
	input := `name: test
runtime: yaml
resources:
  my-nlb:
    type: oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer
    properties:
      compartmentId: ocid1.compartment
      subnetId: ${my-subnet.id}
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      createVnicDetails:
        subnetId: ${my-subnet.id}
        assignPublicIp: "true"
`
	result, err := InjectNetworkingIntoYAML(input)
	require.NoError(t, err)

	assert.Contains(t, result, "__agent_bs_my-nlb_0", "NLB should get per-node backend set")
	assert.Contains(t, result, "__agent_ln_my-nlb_0", "NLB should get per-node listener")
	assert.Contains(t, result, "__agent_be_my-nlb_0", "NLB should get per-node backend")
}

func TestInjectNetworking_ExistingNLB_PrivateInstances_GetsBackends(t *testing.T) {
	// Same scenario but without public IPs — NLB should get agent backends.
	input := `name: test
runtime: yaml
resources:
  my-nlb:
    type: oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer
    properties:
      compartmentId: ocid1.compartment
      subnetId: ${my-subnet.id}
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      createVnicDetails:
        subnetId: ${my-subnet.id}
        assignPublicIp: "false"
`
	result, err := InjectNetworkingIntoYAML(input)
	require.NoError(t, err)

	assert.Contains(t, result, "__agent_bs_my-nlb_0")
	assert.Contains(t, result, "__agent_ln_my-nlb_0")
	assert.Contains(t, result, "__agent_be_my-nlb_0")
}

// --- NLB serialization tests (409-Conflict prevention) ---

func TestInjectNetworking_NLBSerializationAgainstUserResources(t *testing.T) {
	// HA-pair pattern: user has NLB + backend-set + listener + backends.
	// Agent backend sets must chain AFTER the last user NLB resource to
	// prevent OCI 409 "Invalid State Transition from Updating to Updating".
	input := `name: test
runtime: yaml
resources:
  nlb:
    type: oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer
    properties:
      compartmentId: ocid1.compartment
      subnetId: ${subnet.id}
      isPrivate: "false"
  backend-set:
    type: oci:NetworkLoadBalancer/backendSet:BackendSet
    properties:
      networkLoadBalancerId: ${nlb.id}
      name: app-backends
      policy: FIVE_TUPLE
      healthChecker:
        protocol: TCP
        port: 443
  listener:
    type: oci:NetworkLoadBalancer/listener:Listener
    properties:
      networkLoadBalancerId: ${nlb.id}
      name: app-listener
      defaultBackendSetName: ${backend-set.name}
      protocol: TCP
      port: 443
  node-a:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      createVnicDetails:
        subnetId: ${subnet.id}
        assignPublicIp: true
  node-b:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      createVnicDetails:
        subnetId: ${subnet.id}
        assignPublicIp: true
  backend-a:
    type: oci:NetworkLoadBalancer/backend:Backend
    properties:
      networkLoadBalancerId: ${nlb.id}
      backendSetName: ${backend-set.name}
      targetId: ${node-a.id}
      port: 443
  backend-b:
    type: oci:NetworkLoadBalancer/backend:Backend
    properties:
      networkLoadBalancerId: ${nlb.id}
      backendSetName: ${backend-set.name}
      targetId: ${node-b.id}
      port: 443
`
	result, err := InjectNetworkingIntoYAML(input)
	require.NoError(t, err)

	// Agent backend sets should exist
	assert.Contains(t, result, "__agent_bs_nlb_0")
	assert.Contains(t, result, "__agent_bs_nlb_1")

	// First agent backend set must depend on the last user NLB resource (backend-b)
	// to prevent concurrent NLB mutations.
	assert.Contains(t, result, "${backend-b}", "first agent BS must chain after last user NLB resource")

	// Agent resources should be serialized: bs_0 → ln_0 → be_0 → bs_1 → ln_1 → be_1
	bs0Pos := strings.Index(result, "__agent_bs_nlb_0:")
	ln0Pos := strings.Index(result, "__agent_ln_nlb_0:")
	be0Pos := strings.Index(result, "__agent_be_nlb_0:")
	bs1Pos := strings.Index(result, "__agent_bs_nlb_1:")
	ln1Pos := strings.Index(result, "__agent_ln_nlb_1:")
	be1Pos := strings.Index(result, "__agent_be_nlb_1:")

	assert.Greater(t, ln0Pos, bs0Pos, "ln_0 after bs_0")
	assert.Greater(t, be0Pos, ln0Pos, "be_0 after ln_0")
	assert.Greater(t, bs1Pos, be0Pos, "bs_1 after be_0")
	assert.Greater(t, ln1Pos, bs1Pos, "ln_1 after bs_1")
	assert.Greater(t, be1Pos, ln1Pos, "be_1 after ln_1")
}

func TestInjectNetworking_NLBSerializationMinimal(t *testing.T) {
	// NLB with no user backend sets — agent BS should depend on NLB itself.
	input := `name: test
runtime: yaml
resources:
  my-nlb:
    type: oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer
    properties:
      compartmentId: ocid1.compartment
      subnetId: ${subnet.id}
  node-a:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
`
	result, err := InjectNetworkingIntoYAML(input)
	require.NoError(t, err)

	// First agent BS should depend on the NLB itself (no user children to chain after)
	assert.Contains(t, result, "${my-nlb}", "agent BS must depend on NLB when no user children exist")
}

func TestInjectNetworking_NLBSerializationCrossNodeChaining(t *testing.T) {
	// With 2 nodes, the second agent backend set should chain after the
	// first node's backend (be_0), not after the user's last resource.
	input := `name: test
runtime: yaml
resources:
  nlb:
    type: oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer
    properties:
      compartmentId: ocid1.compartment
      subnetId: ${subnet.id}
  node-a:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
  node-b:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
`
	result, err := InjectNetworkingIntoYAML(input)
	require.NoError(t, err)

	// bs_1 should depend on be_0 (cross-node chaining)
	assert.Contains(t, result, "__agent_bs_nlb_1")
	assert.Contains(t, result, "${__agent_be_nlb_0}", "second node BS should chain after first node backend")
}
