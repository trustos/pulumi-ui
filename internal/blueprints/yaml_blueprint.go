package blueprints

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// YAMLBlueprint is a user-defined blueprint backed by a Go-templated Pulumi YAML
// body stored in the database. It implements the Blueprint interface; Run()
// returns nil as a signal to the engine to use the YAML execution path
// (UpsertStackLocalSource) instead of the inline Go path.
type YAMLBlueprint struct {
	name         string
	displayName  string
	description  string
	yamlBody     string           // raw template body as stored in DB
	fields       []ConfigField    // derived from the YAML config: section
	agentAccess  bool             // parsed from meta.agentAccess
	applications []ApplicationDef // parsed from meta.applications
}

// NewYAMLBlueprint parses a raw Pulumi YAML template body and returns a
// registered YAMLBlueprint. Config fields are derived from the config: section.
func NewYAMLBlueprint(name, displayName, description, yamlBody string) (*YAMLBlueprint, error) {
	fields, _, err := ParseConfigFields(yamlBody)
	if err != nil || fields == nil {
		// Non-fatal: we still register the blueprint; the UI will show no fields.
		fields = []ConfigField{}
	}
	return &YAMLBlueprint{
		name:         name,
		displayName:  displayName,
		description:  description,
		yamlBody:     yamlBody,
		fields:       fields,
		agentAccess:  ParseAgentAccess(yamlBody),
		applications: ParseApplications(yamlBody),
	}, nil
}

func (p *YAMLBlueprint) Name() string              { return p.name }
func (p *YAMLBlueprint) DisplayName() string        { return p.displayName }
func (p *YAMLBlueprint) Description() string        { return p.description }
func (p *YAMLBlueprint) ConfigFields() []ConfigField { return p.fields }

// YAMLBody returns the raw Go-template / Pulumi YAML body.
func (p *YAMLBlueprint) YAMLBody() string { return p.yamlBody }

// AgentAccess returns true if the blueprint opted into automatic agent
// connectivity injection via meta.agentAccess: true.
func (p *YAMLBlueprint) AgentAccess() bool { return p.agentAccess }

// Applications returns the application catalog declared in meta.applications.
func (p *YAMLBlueprint) Applications() []ApplicationDef { return p.applications }

// Compile-time checks.
var _ AgentAccessProvider = (*YAMLBlueprint)(nil)
var _ ApplicationProvider = (*YAMLBlueprint)(nil)

// Run returns nil — this signals the engine to use the YAML execution path.
func (p *YAMLBlueprint) Run(_ map[string]string) pulumi.RunFunc { return nil }

// RegisterYAML creates a YAMLBlueprint and adds it to r. Errors in YAML parsing
// are silently ignored so a single bad blueprint doesn't block the others.
func RegisterYAML(r *BlueprintRegistry, name, displayName, description, yamlBody string) {
	p, _ := NewYAMLBlueprint(name, displayName, description, yamlBody)
	r.Register(p)
}

// YAMLBlueprintProvider is a capability interface the engine checks via type
// assertion to determine whether a blueprint should use the YAML execution path.
type YAMLBlueprintProvider interface {
	YAMLBody() string
}

// ForkableBlueprint is an optional capability interface. Built-in blueprints that
// implement it return a full, deployable YAML template when forked, instead of
// the minimal config-only stub that buildForkYAML generates as a fallback.
type ForkableBlueprint interface {
	ForkYAML() string
}
