package agentinject

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
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
	assert.Contains(t, result, "__agent_bs_my-nlb")
	assert.Contains(t, result, "__agent_ln_my-nlb")
	assert.Contains(t, result, "__agent_be_my-nlb_my-instance")
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
	assert.Contains(t, result, "__agent_be_my-nlb_node-1")
	assert.Contains(t, result, "__agent_be_my-nlb_node-2")
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

	// Should create an NLB, backend set, listener, and backend
	assert.Contains(t, result, "__agent_nlb", "should create NLB")
	assert.Contains(t, result, "pulumi-ui-agent-nlb", "NLB should have display name")
	assert.Contains(t, result, "${my-subnet.id}", "NLB should reference subnet")
	assert.Contains(t, result, "__agent_bs", "should create backend set")
	assert.Contains(t, result, "__agent_ln", "should create listener")
	assert.Contains(t, result, "__agent_be_my-instance", "should create backend for instance")
	assert.Contains(t, result, fmt.Sprintf("%d", AgentPort))

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

	// Should create NLB using the same subnetId
	assert.Contains(t, result, "__agent_nlb", "should create NLB")
	assert.Contains(t, result, "__agent_bs", "should create backend set")
	assert.Contains(t, result, "__agent_ln", "should create listener")
	assert.Contains(t, result, "__agent_be_my-instance", "should create backend for instance")

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
	assert.Contains(t, result, "__agent_nlb")
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
	assert.Contains(t, result, "__agent_nlb", "private instance needs NLB")
	assert.Contains(t, result, "__agent_bs", "private instance needs backend set")
	assert.Contains(t, result, "__agent_ln", "private instance needs listener")
	assert.Contains(t, result, "__agent_be_my-instance", "private instance needs backend")
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
	// Mixed public/private means NLB is still needed
	assert.Contains(t, result, "__agent_nlb", "mixed cluster needs NLB")
	assert.Contains(t, result, "__agent_be_public-node", "should create backend for public node")
	assert.Contains(t, result, "__agent_be_private-node", "should create backend for private node")
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
	assert.Contains(t, result, "__agent_nlb", "no createVnicDetails means NLB is needed")
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

func TestInjectNetworking_ExistingNLB_PublicIP_SkipsBackends(t *testing.T) {
	// When instances have public IPs, Nebula reaches them directly — no NLB
	// backends needed for the agent port, even if an NLB already exists.
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
	assert.NotContains(t, result, "__agent_bs_my-nlb", "public IP instances: NLB should not get agent backend set")
	assert.NotContains(t, result, "__agent_ln_my-nlb", "public IP instances: NLB should not get agent listener")
	assert.NotContains(t, result, "__agent_be_my-nlb_my-instance", "public IP instances: NLB should not get agent backend")
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

	// Listener depends on backend set
	assert.Contains(t, result, "${__agent_bs}", "listener should depend on backend set")
	// First backend depends on listener, second depends on first
	lnPos := strings.Index(result, "__agent_ln:")
	beAPos := strings.Index(result, "__agent_be_node-a:")
	beBPos := strings.Index(result, "__agent_be_node-b:")
	assert.Greater(t, beAPos, lnPos, "first backend should come after listener")
	assert.Greater(t, beBPos, beAPos, "second backend should come after first")
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

	// NLB created with backends for all instances
	assert.Contains(t, result, "__agent_nlb", "private cluster needs NLB")
	assert.Contains(t, result, "__agent_be_server-1", "backend for server-1")
	assert.Contains(t, result, "__agent_be_server-2", "backend for server-2")
	assert.Contains(t, result, "__agent_be_server-3", "backend for server-3")
}

func TestAllComputesHavePublicIP(t *testing.T) {
	tests := []struct {
		name     string
		yaml     string
		computes []discoveredResource
		want     bool
	}{
		{
			name:     "empty computes returns false",
			yaml:     `resources: {}`,
			computes: nil,
			want:     false,
		},
		{
			name: "single public instance",
			yaml: `resources:
  inst:
    type: oci:Core/instance:Instance
    properties:
      createVnicDetails:
        assignPublicIp: true`,
			computes: []discoveredResource{{name: "inst", category: "compute"}},
			want:     true,
		},
		{
			name: "single private instance",
			yaml: `resources:
  inst:
    type: oci:Core/instance:Instance
    properties:
      createVnicDetails:
        assignPublicIp: false`,
			computes: []discoveredResource{{name: "inst", category: "compute"}},
			want:     false,
		},
		{
			name: "all public",
			yaml: `resources:
  a:
    type: oci:Core/instance:Instance
    properties:
      createVnicDetails:
        assignPublicIp: true
  b:
    type: oci:Core/instance:Instance
    properties:
      createVnicDetails:
        assignPublicIp: true`,
			computes: []discoveredResource{
				{name: "a", category: "compute"},
				{name: "b", category: "compute"},
			},
			want: true,
		},
		{
			name: "one public one private",
			yaml: `resources:
  a:
    type: oci:Core/instance:Instance
    properties:
      createVnicDetails:
        assignPublicIp: true
  b:
    type: oci:Core/instance:Instance
    properties:
      createVnicDetails:
        assignPublicIp: false`,
			computes: []discoveredResource{
				{name: "a", category: "compute"},
				{name: "b", category: "compute"},
			},
			want: false,
		},
		{
			name: "instance without createVnicDetails",
			yaml: `resources:
  inst:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.test`,
			computes: []discoveredResource{{name: "inst", category: "compute"}},
			want:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var doc yaml.Node
			err := yaml.Unmarshal([]byte(tt.yaml), &doc)
			require.NoError(t, err)
			root := doc.Content[0]
			resources := findMapValue(root, "resources")
			if resources == nil || resources.Kind != yaml.MappingNode {
				got := allComputesHavePublicIP(nil, tt.computes)
				assert.Equal(t, tt.want, got)
				return
			}
			got := allComputesHavePublicIP(resources, tt.computes)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestComputeHasPublicIP(t *testing.T) {
	tests := []struct {
		name     string
		yaml     string
		instance string
		want     bool
	}{
		{
			name: "assignPublicIp true",
			yaml: `resources:
  inst:
    type: oci:Core/instance:Instance
    properties:
      createVnicDetails:
        assignPublicIp: true`,
			instance: "inst",
			want:     true,
		},
		{
			name: "assignPublicIp false",
			yaml: `resources:
  inst:
    type: oci:Core/instance:Instance
    properties:
      createVnicDetails:
        assignPublicIp: false`,
			instance: "inst",
			want:     false,
		},
		{
			name: "no createVnicDetails",
			yaml: `resources:
  inst:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.test`,
			instance: "inst",
			want:     false,
		},
		{
			name: "no assignPublicIp key",
			yaml: `resources:
  inst:
    type: oci:Core/instance:Instance
    properties:
      createVnicDetails:
        subnetId: ocid1.subnet.test`,
			instance: "inst",
			want:     false,
		},
		{
			name:     "instance not found",
			yaml:     `resources: {}`,
			instance: "missing",
			want:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var doc yaml.Node
			err := yaml.Unmarshal([]byte(tt.yaml), &doc)
			require.NoError(t, err)
			root := doc.Content[0]
			resources := findMapValue(root, "resources")
			if resources == nil || resources.Kind != yaml.MappingNode {
				if tt.want {
					t.Fatal("expected resources mapping")
				}
				return
			}
			got := computeHasPublicIP(resources, tt.instance)
			assert.Equal(t, tt.want, got)
		})
	}
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

	// Should have created NSG + NLB since there are no VCN/subnet resources
	// and the instance is private.
	assert.Contains(t, result, "__agent_nsg")
	assert.Contains(t, result, "__agent_nlb")
	assert.Contains(t, result, "__agent_subnet_info")
}

// ---------------------------------------------------------------------------
// Fix: existing NLB + public IP instances should NOT get agent backends
// ---------------------------------------------------------------------------

func TestInjectNetworking_ExistingNLB_PublicIPInstances_NoBackends(t *testing.T) {
	// An NLB exists (e.g. for app traffic) and all instances have assignPublicIp: true.
	// The NLB should NOT receive agent port backends — Nebula dials direct.
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

	// NSG rule should be added (required for UDP 41820 ingress).
	assert.NotContains(t, result, "__agent_bs_my-nlb", "NLB should not get agent backend set when instances have public IPs")
	assert.NotContains(t, result, "__agent_ln_my-nlb", "NLB should not get agent listener when instances have public IPs")
	assert.NotContains(t, result, "__agent_be_my-nlb_my-instance", "NLB should not get agent backend when instances have public IPs")
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

	assert.Contains(t, result, "__agent_bs_my-nlb")
	assert.Contains(t, result, "__agent_ln_my-nlb")
	assert.Contains(t, result, "__agent_be_my-nlb_my-instance")
}
