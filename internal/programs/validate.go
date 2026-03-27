package programs

import (
	"regexp"
	"strconv"
	"strings"
	"text/template"

	"github.com/trustos/pulumi-ui/internal/agentinject"
	"github.com/trustos/pulumi-ui/internal/oci"
	"gopkg.in/yaml.v3"
)

// ValidationLevel identifies which validation stage produced an error.
type ValidationLevel int

const (
	LevelTemplateParse     ValidationLevel = 1
	LevelTemplateRender    ValidationLevel = 2
	LevelRenderedYAML      ValidationLevel = 3
	LevelConfigSection     ValidationLevel = 4
	LevelResourceStructure ValidationLevel = 5
)

// ValidationError is one structured error produced by ValidateProgram.
// Field is the YAML key or template variable involved, or "" for
// document-level errors. Line is 1-based; 0 means the line is unknown.
type ValidationError struct {
	Level   ValidationLevel `json:"level"`
	Field   string          `json:"field,omitempty"`
	Message string          `json:"message"`
	Line    int             `json:"line,omitempty"`
}

var templateLineRe = regexp.MustCompile(`program:(\d+)`)
var missingKeyRe = regexp.MustCompile(`map has no entry for key "([^"]+)"`)

// ValidateProgram runs all six validation levels and returns every error
// found. Levels run sequentially; rendering-dependent levels are skipped if
// the template cannot be parsed or rendered.
func ValidateProgram(yamlBody string) []ValidationError {
	var errs []ValidationError

	// Level 4 is independent — run first so config errors are always reported.
	errs = append(errs, validateConfigSection(yamlBody)...)

	// Level 1 — template syntax.
	l1 := validateTemplateParse(yamlBody)
	errs = append(errs, l1...)
	if len(l1) > 0 {
		return errs // cannot render if parse failed
	}

	// Level 2 — render with synthesised defaults.
	rendered, l2 := validateTemplateRender(yamlBody)
	errs = append(errs, l2...)
	if len(l2) > 0 {
		return errs // cannot validate structure without a rendered body
	}

	// Level 3 — rendered YAML structure.
	errs = append(errs, validateRenderedYAML(rendered)...)

	// Level 5 — resource type structure (needs rendered body).
	errs = append(errs, validateResourceStructure(rendered)...)

	// Level 6 — variable reference integrity (needs rendered body).
	errs = append(errs, validateVariableReferences(rendered)...)

	// Level 7 — agent access networking context + required outputs (needs rendered body + meta).
	if ParseAgentAccess(yamlBody) {
		errs = append(errs, validateAgentAccessContext(rendered)...)
		errs = append(errs, validateAgentAccessOutputs(rendered)...)
	}

	return errs
}

// --- Level 1: template parse ------------------------------------------------

func validateTemplateParse(yamlBody string) []ValidationError {
	_, err := template.New("program").
		Delims("{{", "}}").
		Funcs(buildFuncMap()).
		Option("missingkey=error").
		Parse(yamlBody)
	if err == nil {
		return nil
	}
	line := extractLine(err.Error())
	msg := cleanTemplateError(err.Error())
	return []ValidationError{{Level: LevelTemplateParse, Message: msg, Line: line}}
}

// --- Level 2: render with synthesised config --------------------------------

func validateTemplateRender(yamlBody string) (string, []ValidationError) {
	fields, _, _ := ParseConfigFields(yamlBody)
	cfg := buildValidationConfig(fields)

	rendered, err := RenderTemplate(yamlBody, cfg)
	if err == nil {
		return rendered, nil
	}

	field := ""
	if m := missingKeyRe.FindStringSubmatch(err.Error()); len(m) > 1 {
		field = m[1]
	}
	line := extractLine(err.Error())
	msg := cleanTemplateError(err.Error())
	return "", []ValidationError{{Level: LevelTemplateRender, Field: field, Message: msg, Line: line}}
}

// buildValidationConfig returns a config map populated with declared defaults,
// filling placeholder values for fields that declare no default so that
// rendered YAML properties are non-null for structure validation.
func buildValidationConfig(fields []ConfigField) map[string]string {
	cfg := make(map[string]string, len(fields))
	for _, f := range fields {
		if f.Default != "" {
			cfg[f.Key] = f.Default
		} else {
			switch f.Type {
			case "number":
				cfg[f.Key] = "0"
			case "select":
				cfg[f.Key] = "false"
			default:
				cfg[f.Key] = "placeholder"
			}
		}
	}
	return cfg
}

// --- Level 3: rendered YAML structure ---------------------------------------

func validateRenderedYAML(rendered string) []ValidationError {
	var doc struct {
		Name      string                 `yaml:"name"`
		Runtime   string                 `yaml:"runtime"`
		Resources map[string]interface{} `yaml:"resources"`
	}
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		return []ValidationError{{
			Level:   LevelRenderedYAML,
			Message: "rendered output is not valid YAML: " + err.Error(),
		}}
	}

	var errs []ValidationError
	if strings.TrimSpace(doc.Name) == "" {
		errs = append(errs, ValidationError{Level: LevelRenderedYAML, Field: "name", Message: "top-level 'name' key is missing or empty"})
	}
	if doc.Runtime != "yaml" {
		errs = append(errs, ValidationError{
			Level:   LevelRenderedYAML,
			Field:   "runtime",
			Message: "runtime must be 'yaml'" + func() string {
				if doc.Runtime != "" {
					return ", got '" + doc.Runtime + "'"
				}
				return " (key missing)"
			}(),
		})
	}
	if doc.Resources == nil {
		errs = append(errs, ValidationError{Level: LevelRenderedYAML, Field: "resources", Message: "'resources' key is missing"})
	} else if len(doc.Resources) == 0 {
		errs = append(errs, ValidationError{Level: LevelRenderedYAML, Field: "resources", Message: "'resources' map has no entries"})
	}
	return errs
}

// --- Level 4: config section structure (raw YAML, template-stripped) --------

var validFieldKeyRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9]*$`)

// Pulumi YAML accepts lowercase type names (current spec) and the older
// capitalized forms for backward compatibility.
var validTypes = map[string]bool{
	"string": true, "integer": true, "number": true, "boolean": true,
	"String": true, "Integer": true, "Number": true, "Boolean": true,
}

func validateConfigSection(yamlBody string) []ValidationError {
	stripped := truncateAtResources(yamlBody)

	var doc pulumiYAMLConfig
	if err := yaml.Unmarshal([]byte(stripped), &doc); err != nil {
		return []ValidationError{{
			Level:   LevelConfigSection,
			Field:   "config",
			Message: "could not parse YAML structure: " + err.Error(),
		}}
	}

	var errs []ValidationError

	for key, cf := range doc.Config {
		if !validFieldKeyRe.MatchString(key) {
			errs = append(errs, ValidationError{
				Level:   LevelConfigSection,
				Field:   key,
				Message: "config field key '" + key + "' must start with a letter and contain only letters and digits (no hyphens or underscores)",
			})
		}
		if cf.Type != "" && !validTypes[cf.Type] {
			errs = append(errs, ValidationError{
				Level:   LevelConfigSection,
				Field:   key,
				Message: "config field '" + key + "' has unknown type '" + cf.Type + "' — use String, Integer, Number, or Boolean",
			})
		}
	}

	if doc.Meta != nil {
		seen := map[string]string{} // fieldKey → groupKey (first occurrence)
		for _, g := range doc.Meta.Groups {
			for _, fk := range g.Fields {
				if _, exists := doc.Config[fk]; !exists && len(doc.Config) > 0 {
					errs = append(errs, ValidationError{
						Level:   LevelConfigSection,
						Field:   "meta.groups",
						Message: "meta group '" + g.Key + "' references field '" + fk + "' which is not declared in config:",
					})
				}
				if prev, dup := seen[fk]; dup {
					errs = append(errs, ValidationError{
						Level:   LevelConfigSection,
						Field:   "meta.groups",
						Message: "field '" + fk + "' appears in both group '" + prev + "' and group '" + g.Key + "'",
					})
				} else {
					seen[fk] = g.Key
				}
			}
		}
	}

	return errs
}

// --- Level 5: resource structure (rendered YAML) ----------------------------

// Pulumi OCI type format: provider:Module/submodule:Resource (e.g. oci:Core/vcn:Vcn)
// Also accepts the simpler provider:module:Resource form (e.g. oci:core:Instance)
var resourceTypeRe = regexp.MustCompile(`^[a-z][a-z0-9]*:[a-zA-Z][a-zA-Z0-9/]*:[A-Za-z][A-Za-z0-9]*$`)

func validateResourceStructure(rendered string) []ValidationError {
	var doc struct {
		Resources map[string]struct {
			Type       string                 `yaml:"type"`
			Get        string                 `yaml:"get"`
			Properties map[string]interface{} `yaml:"properties"`
		} `yaml:"resources"`
	}
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		return nil // Level 3 already catches YAML parse errors
	}

	schema := oci.GetSchema()
	var errs []ValidationError
	for name, res := range doc.Resources {
		if res.Get != "" {
			continue // data-source resource, no type: required
		}
		if res.Type == "" {
			errs = append(errs, ValidationError{
				Level:   LevelResourceStructure,
				Field:   name,
				Message: "resource '" + name + "' is missing a 'type:' field",
			})
			continue
		}
		if !resourceTypeRe.MatchString(res.Type) {
			errs = append(errs, ValidationError{
				Level:   LevelResourceStructure,
				Field:   name,
				Message: "resource '" + name + "' has invalid type '" + res.Type + "' — expected pattern provider:Module/submodule:Resource, e.g. oci:Core/instance:Instance",
			})
			continue
		}
		if resSchema, ok := schema[res.Type]; ok {
			for prop, pSchema := range resSchema.Inputs {
				if pSchema.Required {
					if _, exists := res.Properties[prop]; !exists {
						errs = append(errs, ValidationError{
							Level:   LevelResourceStructure,
							Field:   name,
							Message: "resource '" + name + "' (" + res.Type + ") is missing required property '" + prop + "'",
						})
					}
				}
			}
		}
	}
	return errs
}

// --- Level 6: variable reference integrity (rendered YAML) ------------------

const LevelVariableReference ValidationLevel = 6

var pulumiVarRefRe = regexp.MustCompile(`\$\{([^.[}]+)`)

func validateVariableReferences(rendered string) []ValidationError {
	var doc struct {
		Variables map[string]interface{} `yaml:"variables"`
		Resources map[string]struct {
			Type       string                 `yaml:"type"`
			Properties map[string]interface{} `yaml:"properties"`
			Options    struct {
				DependsOn []string `yaml:"dependsOn"`
			} `yaml:"options"`
		} `yaml:"resources"`
		Outputs map[string]interface{} `yaml:"outputs"`
	}
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		return nil
	}

	defined := make(map[string]bool, len(doc.Variables)+len(doc.Resources))
	for k := range doc.Variables {
		defined[k] = true
	}
	for k := range doc.Resources {
		defined[k] = true
	}

	var errs []ValidationError
	for resName, res := range doc.Resources {
		checkRefValues(res.Properties, resName, defined, &errs)
		// Check dependsOn references.
		for _, dep := range res.Options.DependsOn {
			for _, m := range pulumiVarRefRe.FindAllStringSubmatch(dep, -1) {
				ref := m[1]
				if strings.Contains(ref, ":") {
					continue
				}
				if !defined[ref] {
					errs = append(errs, ValidationError{
						Level:   LevelVariableReference,
						Field:   resName,
						Message: "resource '" + resName + "' dependsOn references '${" + ref + "}' which is not defined in resources:",
					})
				}
			}
		}
	}
	// Check outputs for undefined resource/variable references.
	for outputKey, outputVal := range doc.Outputs {
		if s, ok := outputVal.(string); ok {
			for _, m := range pulumiVarRefRe.FindAllStringSubmatch(s, -1) {
				ref := m[1]
				if strings.Contains(ref, ":") {
					continue // provider config ref like oci:tenancyOcid
				}
				if !defined[ref] {
					errs = append(errs, ValidationError{
						Level:   LevelVariableReference,
						Field:   "outputs." + outputKey,
						Message: "output '" + outputKey + "' references '${" + ref + "}' which is not defined in variables: or resources:",
					})
				}
			}
		}
	}
	return errs
}

func checkRefValues(props map[string]interface{}, resName string, defined map[string]bool, errs *[]ValidationError) {
	for propKey, propVal := range props {
		switch v := propVal.(type) {
		case string:
			for _, m := range pulumiVarRefRe.FindAllStringSubmatch(v, -1) {
				ref := m[1]
				if strings.Contains(ref, ":") {
					continue // provider config ref like oci:tenancyOcid
				}
				if !defined[ref] {
					*errs = append(*errs, ValidationError{
						Level:   LevelVariableReference,
						Field:   resName,
						Message: "resource '" + resName + "' property '" + propKey + "' references '${" + ref + "}' which is not defined in variables: or resources:",
					})
				}
			}
		case map[string]interface{}:
			checkRefValues(v, resName, defined, errs)
		}
	}
}

// --- Level 7: agent access networking context --------------------------------

const LevelAgentAccess ValidationLevel = 7

// validateAgentAccessContext checks both the raw template and rendered YAML.
// The raw template is needed because config-driven subnetId values may render
// as empty strings during validation (synthetic config has no real OCIDs).
func validateAgentAccessContext(rendered string) []ValidationError {
	var doc struct {
		Resources map[string]struct {
			Type       string                 `yaml:"type"`
			Properties map[string]interface{} `yaml:"properties"`
		} `yaml:"resources"`
	}
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		return nil
	}

	hasCompute := false
	hasVCN := false
	hasSubnet := false
	hasNSG := false
	hasPublicNLB := false
	hasPrivateNLB := false
	hasNAT := false
	hasLayerSevenLB := false
	hasInstancePool := false
	hasPublicIP := false
	hasSubnetRef := false

	for _, res := range doc.Resources {
		switch res.Type {
		case "oci:Core/vcn:Vcn":
			hasVCN = true
		case "oci:Core/subnet:Subnet":
			hasSubnet = true
		case "oci:Core/networkSecurityGroup:NetworkSecurityGroup":
			hasNSG = true
		case "oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer":
			isPriv := false
			if v, ok := res.Properties["isPrivate"]; ok {
				switch val := v.(type) {
				case bool:
					isPriv = val
				case string:
					isPriv = val == "true"
				}
			}
			if isPriv {
				hasPrivateNLB = true
			} else {
				hasPublicNLB = true
			}
		case "oci:Core/natGateway:NatGateway":
			hasNAT = true
		case "oci:LoadBalancer/loadBalancer:LoadBalancer":
			hasLayerSevenLB = true
		case "oci:Core/instancePool:InstancePool":
			hasInstancePool = true
		}
		if agentinject.IsComputeResource(res.Type) {
			hasCompute = true
			if vnic, ok := res.Properties["createVnicDetails"].(map[string]interface{}); ok {
				if _, hasKey := vnic["subnetId"]; hasKey {
					hasSubnetRef = true
				}
				if v, ok := vnic["assignPublicIp"]; ok {
					switch val := v.(type) {
					case bool:
						if val {
							hasPublicIP = true
						}
					case string:
						if val == "true" {
							hasPublicIP = true
						}
					}
				}
			}
		}
	}

	hasAnyCompute := hasCompute || hasInstancePool

	if !hasAnyCompute {
		return []ValidationError{{
			Level:   LevelAgentAccess,
			Field:   "meta.agentAccess",
			Message: "agentAccess is enabled but the program has no compute resources — the agent bootstrap has nothing to inject into",
		}}
	}

	// T8b: Instance pool with no public NLB and no public IPs
	if hasInstancePool && !hasPublicNLB && !hasPublicIP {
		return []ValidationError{{
			Level:   LevelAgentAccess,
			Field:   "meta.agentAccess",
			Message: "Instance pool has no inbound path; add a public Network Load Balancer (isPrivate: false) for agent connectivity",
		}}
	}

	// T7: Layer 7 LB (no UDP support) and no public NLB, no public IPs
	if hasLayerSevenLB && !hasPublicNLB && !hasPublicIP {
		return []ValidationError{{
			Level:   LevelAgentAccess,
			Field:   "meta.agentAccess",
			Message: "OCI Load Balancer (Layer 7) cannot forward UDP; Nebula requires UDP. Add a Network Load Balancer (oci:NetworkLoadBalancer) for agent connectivity",
		}}
	}

	// T4: private NLB only — not externally reachable
	if hasPrivateNLB && !hasPublicNLB && !hasPublicIP {
		return []ValidationError{{
			Level:   LevelAgentAccess,
			Field:   "meta.agentAccess",
			Message: "NLB is private (isPrivate: true) — not externally reachable; make the NLB public or assign public IPs to instances",
		}}
	}

	// T5: NAT-only (outbound-only internet)
	if hasNAT && !hasPublicNLB && !hasPublicIP {
		return []ValidationError{{
			Level:   LevelAgentAccess,
			Field:   "meta.agentAccess",
			Message: "Instances have outbound-only internet (NAT gateway); add a public Network Load Balancer so the engine can reach each agent",
		}}
	}

	// T6: no internet path at all
	if !hasVCN && !hasSubnet && !hasNSG && !hasPublicNLB && !hasPublicIP && !hasSubnetRef {
		return []ValidationError{{
			Level:   LevelAgentAccess,
			Field:   "meta.agentAccess",
			Message: "agentAccess is enabled but no networking context found — add a VCN + subnet, or set createVnicDetails.subnetId on the compute instance so the engine can create an NSG for agent connectivity",
		}}
	}

	return nil
}

// validateAgentAccessOutputs checks that at least one IP output is present so
// the engine can populate stack_node_certs.agent_real_ip after deploy.
//
// Accepted output key formats:
//   - instance-{i}-publicIp   (per-node; engine scans sequentially)
//   - instancePublicIp / instancePublicIP
//   - nlbPublicIp / nlbPublicIP   (required when a public NLB is present)
//   - publicIp / publicIP
//   - serverPublicIp / serverPublicIP
//
// Returns a Level 7 warning when agentAccess is enabled, compute resources
// exist, and no recognised IP output key is defined.
func validateAgentAccessOutputs(rendered string) []ValidationError {
	var doc struct {
		Resources map[string]struct {
			Type       string                 `yaml:"type"`
			Properties map[string]interface{} `yaml:"properties"`
		} `yaml:"resources"`
		Outputs map[string]interface{} `yaml:"outputs"`
	}
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		return nil
	}

	hasCompute := false
	hasPublicNLB := false
	hasInstancePool := false
	for _, res := range doc.Resources {
		if agentinject.IsComputeResource(res.Type) {
			hasCompute = true
		}
		if res.Type == "oci:Core/instancePool:InstancePool" {
			hasInstancePool = true
		}
		if res.Type == "oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer" {
			isPriv := false
			if v, ok := res.Properties["isPrivate"]; ok {
				switch val := v.(type) {
				case bool:
					isPriv = val
				case string:
					isPriv = val == "true"
				}
			}
			if !isPriv {
				hasPublicNLB = true
			}
		}
	}
	if !hasCompute && !hasInstancePool {
		return nil
	}

	// Level 7b: NLB topology requires nlbPublicIp output.
	if hasPublicNLB {
		if _, ok := doc.Outputs["nlbPublicIp"]; ok {
			return nil
		}
		if _, ok := doc.Outputs["nlbPublicIP"]; ok {
			return nil
		}
		return []ValidationError{{
			Level:   LevelAgentAccess,
			Field:   "outputs",
			Message: "NLB topology requires an nlbPublicIp output; add: nlbPublicIp: ${<nlb-name>.ipAddresses[0].ipAddress}",
		}}
	}

	// All output key names the engine accepts for agent IP discovery (non-NLB paths).
	knownIpKeys := []string{
		"instancePublicIp", "instancePublicIP",
		"nlbPublicIp", "nlbPublicIP",
		"publicIp", "publicIP",
		"serverPublicIp", "serverPublicIP",
	}
	instanceOutputRe := regexp.MustCompile(`^instance-\d+-publicIp$`)

	for _, key := range knownIpKeys {
		if _, ok := doc.Outputs[key]; ok {
			return nil
		}
	}
	for key := range doc.Outputs {
		if instanceOutputRe.MatchString(key) {
			return nil
		}
	}

	return []ValidationError{{
		Level:   LevelAgentAccess,
		Field:   "outputs",
		Message: "agentAccess is enabled but no instance IP outputs are defined — add instance-0-publicIp (and instance-N-publicIp for each additional node) to outputs so the engine can discover agent addresses after deploy",
	}}
}

// --- helpers ----------------------------------------------------------------

func extractLine(errMsg string) int {
	if m := templateLineRe.FindStringSubmatch(errMsg); len(m) > 1 {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

// cleanTemplateError strips the repetitive "template render: template: program:N:M: "
// prefix from Go template error messages, leaving just the actionable part.
func cleanTemplateError(msg string) string {
	// Strip outermost wrappers added by RenderTemplate.
	msg = strings.TrimPrefix(msg, "template render: ")
	msg = strings.TrimPrefix(msg, "template parse: ")
	// Strip "template: program:N:M: " prefix.
	if idx := strings.Index(msg, ": executing"); idx >= 0 {
		// Keep from "executing" onwards.
		msg = msg[idx+2:]
	} else if idx := strings.Index(msg, "error calling"); idx >= 0 {
		msg = msg[idx:]
	} else if colon := strings.LastIndex(msg, ": "); colon >= 0 && strings.Contains(msg[:colon], "program:") {
		msg = msg[colon+2:]
	}
	return msg
}
