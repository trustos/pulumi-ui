package programs

import (
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// YAMLProgram is a user-defined program backed by a Go-templated Pulumi YAML
// body stored in the database. It implements the Program interface; Run()
// returns nil as a signal to the engine to use the YAML execution path
// (UpsertStackLocalSource) instead of the inline Go path.
type YAMLProgram struct {
	name        string
	displayName string
	description string
	yamlBody    string        // raw template body as stored in DB
	fields      []ConfigField // derived from the YAML config: section
	agentAccess bool          // parsed from meta.agentAccess
}

// NewYAMLProgram parses a raw Pulumi YAML template body and returns a
// registered YAMLProgram. Config fields are derived from the config: section.
func NewYAMLProgram(name, displayName, description, yamlBody string) (*YAMLProgram, error) {
	fields, _, err := ParseConfigFields(yamlBody)
	if err != nil || fields == nil {
		// Non-fatal: we still register the program; the UI will show no fields.
		fields = []ConfigField{}
	}
	return &YAMLProgram{
		name:        name,
		displayName: displayName,
		description: description,
		yamlBody:    yamlBody,
		fields:      fields,
		agentAccess: ParseAgentAccess(yamlBody),
	}, nil
}

func (p *YAMLProgram) Name() string              { return p.name }
func (p *YAMLProgram) DisplayName() string        { return p.displayName }
func (p *YAMLProgram) Description() string        { return p.description }
func (p *YAMLProgram) ConfigFields() []ConfigField { return p.fields }

// YAMLBody returns the raw Go-template / Pulumi YAML body.
func (p *YAMLProgram) YAMLBody() string { return p.yamlBody }

// AgentAccess returns true if the program opted into automatic agent
// connectivity injection via meta.agentAccess: true.
func (p *YAMLProgram) AgentAccess() bool { return p.agentAccess }

// Compile-time check that YAMLProgram implements AgentAccessProvider.
var _ AgentAccessProvider = (*YAMLProgram)(nil)

// Run returns nil — this signals the engine to use the YAML execution path.
func (p *YAMLProgram) Run(_ map[string]string) pulumi.RunFunc { return nil }

// RegisterYAML creates a YAMLProgram and adds it to r. Errors in YAML parsing
// are silently ignored so a single bad program doesn't block the others.
func RegisterYAML(r *ProgramRegistry, name, displayName, description, yamlBody string) {
	p, _ := NewYAMLProgram(name, displayName, description, yamlBody)
	r.Register(p)
}

// YAMLProgramProvider is a capability interface the engine checks via type
// assertion to determine whether a program should use the YAML execution path.
type YAMLProgramProvider interface {
	YAMLBody() string
}

// ForkableProgram is an optional capability interface. Built-in programs that
// implement it return a full, deployable YAML template when forked, instead of
// the minimal config-only stub that buildForkYAML generates as a fallback.
type ForkableProgram interface {
	ForkYAML() string
}
