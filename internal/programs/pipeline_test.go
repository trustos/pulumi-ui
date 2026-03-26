package programs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/trustos/pulumi-ui/internal/agentinject"
)

// TestPipeline_TemplateRender_InjectUserData_InjectNetworking tests the full
// engine pipeline: Go template render → agent user_data injection → networking
// injection. This mirrors what the engine does in getOrCreateYAMLStack.
func TestPipeline_TemplateRender_InjectUserData_InjectNetworking(t *testing.T) {
	yamlTemplate := `name: test-cluster
runtime: yaml

config:
  compartmentId:
    type: string

meta:
  agentAccess: true

variables:
  availabilityDomains:
    fn::invoke:
      function: oci:Identity/getAvailabilityDomains:getAvailabilityDomains
      arguments:
        compartmentId: {{ .Config.compartmentId }}
      return: availabilityDomains

resources:
  my-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: {{ .Config.compartmentId }}
      cidrBlock: 10.0.0.0/16
  my-subnet:
    type: oci:Core/subnet:Subnet
    properties:
      compartmentId: {{ .Config.compartmentId }}
      vcnId: ${my-vcn.id}
  my-nsg:
    type: oci:Core/networkSecurityGroup:NetworkSecurityGroup
    properties:
      compartmentId: {{ .Config.compartmentId }}
      vcnId: ${my-vcn.id}
  my-nlb:
    type: oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer
    properties:
      compartmentId: {{ .Config.compartmentId }}
      subnetId: ${my-subnet.id}
      displayName: test-nlb
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: {{ .Config.compartmentId }}
      availabilityDomain: ${availabilityDomains[0].name}
      shape: VM.Standard.A1.Flex
      metadata:
        ssh_authorized_keys: test-key
`
	cfg := map[string]string{
		"compartmentId": "ocid1.compartment.test",
	}

	// Step 1: Apply config defaults
	cfg = ApplyConfigDefaults(yamlTemplate, cfg)

	// Step 2: Render Go template
	rendered, err := RenderTemplate(yamlTemplate, cfg)
	require.NoError(t, err)
	assert.Contains(t, rendered, "ocid1.compartment.test")
	assert.Contains(t, rendered, "${my-vcn.id}", "Pulumi interpolations survive template render")
	assert.Contains(t, rendered, "${availabilityDomains[0].name}")

	// Step 3: Validate
	errs := ValidateProgram(yamlTemplate)
	for _, e := range errs {
		if e.Level < LevelResourceStructure {
			t.Errorf("unexpected validation error at level %d: %s", e.Level, e.Message)
		}
	}

	// Step 4: Sanitize (we'd call SanitizeYAML here, just ensure it exists)
	sanitized := SanitizeYAML(rendered)
	assert.NotEmpty(t, sanitized)

	// Step 5: Agent user_data injection
	agentVars := agentinject.AgentVars{
		NebulaCACert:   "test-ca",
		NebulaHostCert: "test-cert",
		NebulaHostKey:  "test-key",
		NebulaVersion:  "v1.10.3",
		AgentVersion:   "latest",
		AgentToken:     "test-token",
	}
	injected, err := agentinject.InjectIntoYAML(sanitized, []agentinject.AgentVars{agentVars})
	require.NoError(t, err)
	assert.Contains(t, injected, "user_data", "agent bootstrap should be injected into instance")

	// Step 6: Networking injection
	netInjected, err := agentinject.InjectNetworkingIntoYAML(injected)
	require.NoError(t, err)
	assert.Contains(t, netInjected, "__agent_nsg_rule_my-nsg", "NSG rule should be injected")
	assert.Contains(t, netInjected, "__agent_bs_my-nlb", "NLB backend set should be injected")
	assert.Contains(t, netInjected, "__agent_ln_my-nlb", "NLB listener should be injected")
	assert.Contains(t, netInjected, "__agent_be_my-nlb_my-instance", "NLB backend should be injected")
}

// TestPipeline_SimpleProgram_NoAgentAccess verifies that a program
// without agentAccess in meta doesn't get called through the engine path.
func TestPipeline_SimpleProgram_NoAgentAccess(t *testing.T) {
	yamlTemplate := `name: simple
runtime: yaml

config:
  compartmentId:
    type: string

resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: {{ .Config.compartmentId }}
      shape: VM.Standard.A1.Flex
`
	cfg := ApplyConfigDefaults(yamlTemplate, map[string]string{
		"compartmentId": "ocid1.test",
	})

	rendered, err := RenderTemplate(yamlTemplate, cfg)
	require.NoError(t, err)

	assert.False(t, ParseAgentAccess(yamlTemplate))

	// No VCN/subnet/NSG/NLB and just a bare instance → nothing to inject
	netResult, err := agentinject.InjectNetworkingIntoYAML(rendered)
	require.NoError(t, err)
	assert.Equal(t, rendered, netResult, "bare instance without VCN gets no injection")
}

// TestPipeline_BareInstance_WithAgentAccess verifies the full pipeline for a
// single instance with VCN/subnet but no NSG/NLB — the engine should create
// NSG, NLB, and all agent networking resources.
func TestPipeline_BareInstance_WithAgentAccess(t *testing.T) {
	yamlTemplate := `name: simple-agent
runtime: yaml

config:
  compartmentId:
    type: string

meta:
  agentAccess: true

resources:
  my-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: {{ .Config.compartmentId }}
      cidrBlock: 10.0.0.0/16
  my-subnet:
    type: oci:Core/subnet:Subnet
    properties:
      compartmentId: {{ .Config.compartmentId }}
      vcnId: ${my-vcn.id}
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: {{ .Config.compartmentId }}
      shape: VM.Standard.A1.Flex
      createVnicDetails:
        subnetId: ${my-subnet.id}
`
	cfg := ApplyConfigDefaults(yamlTemplate, map[string]string{
		"compartmentId": "ocid1.compartment.test",
	})

	rendered, err := RenderTemplate(yamlTemplate, cfg)
	require.NoError(t, err)

	assert.True(t, ParseAgentAccess(yamlTemplate), "meta.agentAccess should be true")

	sanitized := SanitizeYAML(rendered)

	// Agent user_data injection
	agentVars := agentinject.AgentVars{
		NebulaCACert:   "test-ca",
		NebulaHostCert: "test-cert",
		NebulaHostKey:  "test-key",
		NebulaVersion:  "v1.10.3",
		AgentVersion:   "latest",
		AgentToken:     "test-token",
	}
	injected, err := agentinject.InjectIntoYAML(sanitized, []agentinject.AgentVars{agentVars})
	require.NoError(t, err)
	assert.Contains(t, injected, "user_data", "agent bootstrap injected into instance")

	// Networking injection — should CREATE NSG and NLB
	netInjected, err := agentinject.InjectNetworkingIntoYAML(injected)
	require.NoError(t, err)

	assert.Contains(t, netInjected, "__agent_nsg", "should create NSG")
	assert.Contains(t, netInjected, "__agent_nsg_rule", "should create NSG rule")
	assert.Contains(t, netInjected, "__agent_nlb", "should create NLB")
	assert.Contains(t, netInjected, "__agent_bs", "should create backend set")
	assert.Contains(t, netInjected, "__agent_ln", "should create listener")
	assert.Contains(t, netInjected, "__agent_be_my-instance", "should create backend for instance")
	assert.Contains(t, netInjected, "nsgIds", "should attach NSG to instance")
	assert.Contains(t, netInjected, "${__agent_nsg.id}", "NSG reference in instance")
}

// TestPipeline_BareInstance_NoSubnet_AgentAccess verifies that a single
// instance with agentAccess but no VCN/subnet/createVnicDetails.subnetId
// triggers a Level 7 validation warning and produces NO networking injection.
func TestPipeline_BareInstance_NoSubnet_AgentAccess(t *testing.T) {
	yamlTemplate := `name: bare
runtime: yaml

config:
  compartmentId:
    type: string

meta:
  agentAccess: true

resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: {{ .Config.compartmentId }}
      shape: VM.Standard.A1.Flex
`
	assert.True(t, ParseAgentAccess(yamlTemplate))

	// Validation should flag missing networking context
	errs := ValidateProgram(yamlTemplate)
	var l7 []ValidationError
	for _, e := range errs {
		if e.Level == LevelAgentAccess {
			l7 = append(l7, e)
		}
	}
	require.NotEmpty(t, l7, "should warn about missing networking context")
	assert.Contains(t, l7[0].Message, "no networking context")

	// Render and check injection does NOT add networking (no context)
	cfg := ApplyConfigDefaults(yamlTemplate, map[string]string{
		"compartmentId": "ocid1.compartment.test",
	})
	rendered, err := RenderTemplate(yamlTemplate, cfg)
	require.NoError(t, err)

	netResult, err := agentinject.InjectNetworkingIntoYAML(rendered)
	require.NoError(t, err)
	assert.NotContains(t, netResult, "__agent_nsg",
		"no VCN/subnet/subnetId → no networking injected")
}

// TestPipeline_BareInstance_WithSubnetRef_AgentAccess verifies that a single
// instance with createVnicDetails.subnetId and agentAccess triggers full
// networking injection via fn::invoke.
func TestPipeline_BareInstance_WithSubnetRef_AgentAccess(t *testing.T) {
	yamlTemplate := `name: bare-subnet
runtime: yaml

config:
  compartmentId:
    type: string
  subnetId:
    type: string

meta:
  agentAccess: true

resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: {{ .Config.compartmentId }}
      shape: VM.Standard.A1.Flex
      createVnicDetails:
        subnetId: {{ .Config.subnetId }}
`
	assert.True(t, ParseAgentAccess(yamlTemplate))

	// Validation should pass (has createVnicDetails.subnetId)
	errs := ValidateProgram(yamlTemplate)
	for _, e := range errs {
		if e.Level == LevelAgentAccess {
			t.Errorf("unexpected Level 7 error: %s", e.Message)
		}
	}

	cfg := ApplyConfigDefaults(yamlTemplate, map[string]string{
		"compartmentId": "ocid1.compartment.test",
		"subnetId":      "ocid1.subnet.existing",
	})
	rendered, err := RenderTemplate(yamlTemplate, cfg)
	require.NoError(t, err)

	sanitized := SanitizeYAML(rendered)

	// Networking injection should create NSG+NLB from subnetId
	netInjected, err := agentinject.InjectNetworkingIntoYAML(sanitized)
	require.NoError(t, err)

	assert.Contains(t, netInjected, "__agent_subnet_info", "should create fn::invoke variable")
	assert.Contains(t, netInjected, "__agent_nsg", "should create NSG")
	assert.Contains(t, netInjected, "__agent_nlb", "should create NLB")
	assert.Contains(t, netInjected, "__agent_be_my-instance", "should create backend")
	assert.Contains(t, netInjected, "ocid1.subnet.existing", "should reference the subnet OCID")
}

// TestPipeline_VariableRef_Validation tests that undefined variable references
// are caught by the validation pipeline.
func TestPipeline_VariableRef_Validation(t *testing.T) {
	yaml := `name: test
runtime: yaml
config:
  compartmentId:
    type: string
    default: "ocid1.test"
resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: {{ .Config.compartmentId }}
      availabilityDomain: ${undefinedVariable}
`
	errs := ValidateProgram(yaml)
	var l6Errors []ValidationError
	for _, e := range errs {
		if e.Level == LevelVariableReference {
			l6Errors = append(l6Errors, e)
		}
	}
	require.NotEmpty(t, l6Errors, "should catch undefined variable reference")
	assert.Contains(t, l6Errors[0].Message, "undefinedVariable")
}
