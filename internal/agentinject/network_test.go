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
	// Should still have original properties
	assert.Contains(t, result, "assignPublicIp")
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
