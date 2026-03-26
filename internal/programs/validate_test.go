package programs

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateProgram_ValidMinimal(t *testing.T) {
	yaml := `name: test-prog
runtime: yaml
resources:
  my-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: ocid1.compartment
      cidrBlock: 10.0.0.0/16
`
	errs := ValidateProgram(yaml)
	for _, e := range errs {
		// Allow Level 5 errors from schema validation (missing required props)
		// but no structural or template errors
		assert.True(t, e.Level >= LevelResourceStructure,
			"unexpected error at level %d: %s", e.Level, e.Message)
	}
}

func TestValidateProgram_Level1_TemplateSyntaxError(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  r:
    type: oci:Core/vcn:Vcn
    properties:
      name: {{ .Config.name`

	errs := ValidateProgram(yaml)
	require.NotEmpty(t, errs)
	assert.Equal(t, LevelTemplateParse, errs[0].Level)
}

func TestValidateProgram_Level2_MissingConfigKey(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  r:
    type: oci:Core/vcn:Vcn
    properties:
      name: {{ .Config.undeclaredField }}
`
	errs := ValidateProgram(yaml)
	var l2 []ValidationError
	for _, e := range errs {
		if e.Level == LevelTemplateRender {
			l2 = append(l2, e)
		}
	}
	require.NotEmpty(t, l2)
	assert.Contains(t, l2[0].Message, "undeclaredField")
}

func TestValidateProgram_Level3_MissingName(t *testing.T) {
	yaml := `runtime: yaml
resources:
  r:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: test
`
	errs := ValidateProgram(yaml)
	var l3 []ValidationError
	for _, e := range errs {
		if e.Level == LevelRenderedYAML {
			l3 = append(l3, e)
		}
	}
	require.NotEmpty(t, l3)
	assert.Contains(t, l3[0].Message, "name")
}

func TestValidateProgram_Level4_InvalidConfigType(t *testing.T) {
	yaml := `name: test
runtime: yaml
config:
  myField:
    type: invalid-type
resources:
  r:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: test
`
	errs := ValidateProgram(yaml)
	var l4 []ValidationError
	for _, e := range errs {
		if e.Level == LevelConfigSection {
			l4 = append(l4, e)
		}
	}
	require.NotEmpty(t, l4)
	assert.Contains(t, l4[0].Message, "unknown type")
}

func TestValidateProgram_Level5_MissingResourceType(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  r:
    properties:
      name: test
`
	errs := ValidateProgram(yaml)
	var l5 []ValidationError
	for _, e := range errs {
		if e.Level == LevelResourceStructure {
			l5 = append(l5, e)
		}
	}
	require.NotEmpty(t, l5)
	assert.Contains(t, l5[0].Message, "missing a 'type:'")
}

func TestValidateProgram_Level6_UndefinedVariableRef(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      availabilityDomain: ${undefinedVar}
`
	errs := ValidateProgram(yaml)
	var l6 []ValidationError
	for _, e := range errs {
		if e.Level == LevelVariableReference {
			l6 = append(l6, e)
		}
	}
	require.NotEmpty(t, l6, "should flag undefined variable reference")
	assert.Contains(t, l6[0].Message, "undefinedVar")
}

func TestValidateProgram_Level6_DefinedVariableRef_NoError(t *testing.T) {
	yaml := `name: test
runtime: yaml
variables:
  availabilityDomains:
    fn::invoke:
      function: oci:Identity/getAvailabilityDomains:getAvailabilityDomains
      arguments:
        compartmentId: test
      return: availabilityDomains
resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      availabilityDomain: ${availabilityDomains[0].name}
`
	errs := ValidateProgram(yaml)
	for _, e := range errs {
		if e.Level == LevelVariableReference {
			t.Errorf("unexpected Level 6 error: %s", e.Message)
		}
	}
}

func TestValidateProgram_Level6_ResourceRef_NoError(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: ocid1.compartment
      cidrBlock: 10.0.0.0/16
  my-subnet:
    type: oci:Core/subnet:Subnet
    properties:
      compartmentId: ocid1.compartment
      vcnId: ${my-vcn.id}
`
	errs := ValidateProgram(yaml)
	for _, e := range errs {
		if e.Level == LevelVariableReference {
			t.Errorf("unexpected Level 6 error: %s", e.Message)
		}
	}
}

func TestValidateProgram_Level6_ProviderConfigRef_NoError(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ${oci:tenancyOcid}
`
	errs := ValidateProgram(yaml)
	for _, e := range errs {
		if e.Level == LevelVariableReference {
			t.Errorf("unexpected Level 6 error for provider config ref: %s", e.Message)
		}
	}
}

func TestValidateProgram_Level6_UndefinedOutputRef_Error(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  my-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: ocid1.compartment
      cidrBlock: 10.0.0.0/16
outputs:
  instanceIp: ${instance.publicIp}
`
	errs := ValidateProgram(yaml)
	var l6 []ValidationError
	for _, e := range errs {
		if e.Level == LevelVariableReference {
			l6 = append(l6, e)
		}
	}
	require.NotEmpty(t, l6, "should flag undefined output reference")
	assert.Contains(t, l6[0].Message, "instance")
	assert.Contains(t, l6[0].Field, "outputs.")
}

func TestValidateProgram_Level6_DefinedOutputRef_NoError(t *testing.T) {
	yaml := `name: test
runtime: yaml
resources:
  instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
outputs:
  instanceIp: ${instance.publicIp}
`
	errs := ValidateProgram(yaml)
	for _, e := range errs {
		if e.Level == LevelVariableReference {
			t.Errorf("unexpected Level 6 error for valid output ref: %s", e.Message)
		}
	}
}

func TestValidateProgram_Level7_AgentAccess_NoCompute(t *testing.T) {
	yaml := `name: test
runtime: yaml

meta:
  agentAccess: true

resources:
  my-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: ocid1.compartment
      cidrBlock: 10.0.0.0/16
`
	errs := ValidateProgram(yaml)
	var l7 []ValidationError
	for _, e := range errs {
		if e.Level == LevelAgentAccess {
			l7 = append(l7, e)
		}
	}
	require.NotEmpty(t, l7, "should warn when agentAccess is ON but no compute exists")
	assert.Contains(t, l7[0].Message, "no compute resources")
}

func TestValidateProgram_Level7_AgentAccess_NoNetworkingContext(t *testing.T) {
	yaml := `name: test
runtime: yaml

meta:
  agentAccess: true

resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      shape: VM.Standard.A1.Flex
`
	errs := ValidateProgram(yaml)
	var l7 []ValidationError
	for _, e := range errs {
		if e.Level == LevelAgentAccess {
			l7 = append(l7, e)
		}
	}
	require.NotEmpty(t, l7, "should warn when agentAccess is ON but no networking context")
	assert.Contains(t, l7[0].Message, "no networking context")
}

func TestValidateProgram_Level7_AgentAccess_WithVCN_NoWarning(t *testing.T) {
	yaml := `name: test
runtime: yaml

meta:
  agentAccess: true

resources:
  my-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: ocid1.compartment
      cidrBlock: 10.0.0.0/16
  my-subnet:
    type: oci:Core/subnet:Subnet
    properties:
      compartmentId: ocid1.compartment
      vcnId: ${my-vcn.id}
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      shape: VM.Standard.A1.Flex

outputs:
  instance-0-publicIp: ${my-instance.publicIp}
`
	errs := ValidateProgram(yaml)
	for _, e := range errs {
		if e.Level == LevelAgentAccess {
			t.Errorf("unexpected Level 7 error when VCN+subnet+instance exist: %s", e.Message)
		}
	}
}

func TestValidateProgram_Level7_AgentAccess_WithSubnetRef_NoWarning(t *testing.T) {
	yaml := `name: test
runtime: yaml

meta:
  agentAccess: true

resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      shape: VM.Standard.A1.Flex
      createVnicDetails:
        subnetId: ocid1.subnet.existing

outputs:
  instance-0-publicIp: ${my-instance.publicIp}
`
	errs := ValidateProgram(yaml)
	for _, e := range errs {
		if e.Level == LevelAgentAccess {
			t.Errorf("unexpected Level 7 error when instance has createVnicDetails.subnetId: %s", e.Message)
		}
	}
}

func TestValidateProgram_Level7_NoAgentAccess_NoWarning(t *testing.T) {
	yaml := `name: test
runtime: yaml

resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      shape: VM.Standard.A1.Flex
`
	errs := ValidateProgram(yaml)
	for _, e := range errs {
		if e.Level == LevelAgentAccess {
			t.Errorf("Level 7 should not run when agentAccess is OFF: %s", e.Message)
		}
	}
}

func TestValidateProgram_Level7_OnlyWarnings_NonBlocking(t *testing.T) {
	yaml := `name: test
runtime: yaml

meta:
  agentAccess: true

resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      availabilityDomain: test-AD-1
      shape: VM.Standard.A1.Flex
`
	errs := ValidateProgram(yaml)
	var blocking, warnings int
	for _, e := range errs {
		if e.Level < LevelAgentAccess {
			blocking++
		} else {
			warnings++
		}
	}
	assert.Equal(t, 0, blocking, "should have no blocking errors")
	assert.Greater(t, warnings, 0, "should have at least one Level 7 warning")
	hasNetworkingWarning := false
	for _, e := range errs {
		if e.Level == LevelAgentAccess && strings.Contains(e.Message, "no networking context") {
			hasNetworkingWarning = true
		}
	}
	assert.True(t, hasNetworkingWarning, "should include 'no networking context' warning")
}

// ── validateAgentAccessOutputs ───────────────────────────────────────────────

func TestValidateAgentAccessOutputs_MissingOutputs_NoCompute(t *testing.T) {
	yaml := `name: test
runtime: yaml

meta:
  agentAccess: true

resources:
  my-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: ocid1.compartment
`
	// No compute resources → no outputs warning
	for _, e := range ValidateProgram(yaml) {
		if e.Level == LevelAgentAccess && strings.Contains(e.Message, "no instance IP outputs") {
			t.Errorf("should not warn about outputs when no compute resources present: %s", e.Message)
		}
	}
}

func TestValidateAgentAccessOutputs_MissingOutputs_Instance(t *testing.T) {
	yaml := `name: test
runtime: yaml

meta:
  agentAccess: true

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
      shape: VM.Standard.A1.Flex
`
	var outputErr *ValidationError
	for _, e := range ValidateProgram(yaml) {
		e := e
		if e.Level == LevelAgentAccess && strings.Contains(e.Message, "no instance IP outputs") {
			outputErr = &e
		}
	}
	require.NotNil(t, outputErr, "should warn when instance present but no IP outputs defined")
}

func TestValidateAgentAccessOutputs_MissingOutputs_InstanceConfiguration(t *testing.T) {
	yaml := `name: test
runtime: yaml

meta:
  agentAccess: true

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
  my-template:
    type: oci:Core/instanceConfiguration:InstanceConfiguration
    properties:
      compartmentId: ocid1.compartment
`
	var outputErr *ValidationError
	for _, e := range ValidateProgram(yaml) {
		e := e
		if e.Level == LevelAgentAccess && strings.Contains(e.Message, "no instance IP outputs") {
			outputErr = &e
		}
	}
	require.NotNil(t, outputErr, "instanceConfiguration should also require IP outputs")
}

func TestValidateAgentAccessOutputs_AcceptedKeys(t *testing.T) {
	// All of these output keys should silence the warning for a single-instance program.
	acceptedKeys := []string{
		"instancePublicIp", "instancePublicIP",
		"nlbPublicIp", "nlbPublicIP",
		"publicIp", "publicIP",
		"serverPublicIp", "serverPublicIP",
		"instance-0-publicIp",
	}

	for _, key := range acceptedKeys {
		t.Run(key, func(t *testing.T) {
			yaml := `name: test
runtime: yaml

meta:
  agentAccess: true

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
      shape: VM.Standard.A1.Flex

outputs:
  ` + key + `: ${my-instance.publicIp}
`
			for _, e := range ValidateProgram(yaml) {
				if e.Level == LevelAgentAccess && strings.Contains(e.Message, "no instance IP outputs") {
					t.Errorf("key %q should satisfy the outputs check but got warning: %s", key, e.Message)
				}
			}
		})
	}
}

func TestValidateAgentAccessOutputs_MultiNode_AllCovered(t *testing.T) {
	yaml := `name: test
runtime: yaml

meta:
  agentAccess: true

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
  node-0:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      shape: VM.Standard.A1.Flex
  node-1:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      shape: VM.Standard.A1.Flex

outputs:
  instance-0-publicIp: ${node-0.publicIp}
  instance-1-publicIp: ${node-1.publicIp}
`
	for _, e := range ValidateProgram(yaml) {
		if e.Level == LevelAgentAccess && strings.Contains(e.Message, "no instance IP outputs") {
			t.Errorf("should not warn when all per-node outputs are defined: %s", e.Message)
		}
	}
}

func TestValidateAgentAccessOutputs_NoAgentAccess_NoCheck(t *testing.T) {
	// agentAccess is off — outputs check must not run at all
	yaml := `name: test
runtime: yaml

resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ocid1.compartment
      shape: VM.Standard.A1.Flex
`
	for _, e := range ValidateProgram(yaml) {
		if strings.Contains(e.Message, "no instance IP outputs") {
			t.Errorf("outputs check should not run when agentAccess is off: %s", e.Message)
		}
	}
}
