package programs

import (
	"sync"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// ConfigField describes one config field for the UI form.
type ConfigField struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Type        string   `json:"type"`        // text | number | textarea | select | oci-shape | oci-image
	Required    bool     `json:"required"`
	Default     string   `json:"default,omitempty"`
	Description string   `json:"description,omitempty"`
	Options     []string `json:"options,omitempty"` // for select type
	Group       string   `json:"group,omitempty"`      // stable group key, e.g. "iam"
	GroupLabel  string   `json:"groupLabel,omitempty"` // display heading, e.g. "IAM & Permissions"
}

// ProgramMeta is the safe, serialisable view of a Program (sent to the UI).
type ProgramMeta struct {
	Name         string        `json:"name"`
	DisplayName  string        `json:"displayName"`
	Description  string        `json:"description"`
	ConfigFields []ConfigField `json:"configFields"`
	IsCustom     bool          `json:"isCustom"` // true for user-defined YAML programs
}

// Program is the internal interface all Pulumi programs implement.
type Program interface {
	Name() string
	DisplayName() string
	Description() string
	ConfigFields() []ConfigField
	// Run returns a PulumiFn for the given config map.
	Run(config map[string]string) pulumi.RunFunc
}

// ProgramRegistry is a thread-safe registry of Programs.
// It must be constructed via NewProgramRegistry and passed as a dependency
// to the Engine and HTTP handlers. No package-level state is used.
type ProgramRegistry struct {
	mu       sync.RWMutex
	programs []Program
}

// NewProgramRegistry creates an empty ProgramRegistry.
func NewProgramRegistry() *ProgramRegistry {
	return &ProgramRegistry{}
}

// RegisterBuiltins registers the built-in Go programs into r.
// Called explicitly from main.go — no init() self-registration.
func RegisterBuiltins(r *ProgramRegistry) {
	r.Register(&NomadClusterProgram{})
	r.Register(&TestVcnProgram{})
}

// Register adds p to the registry.
func (r *ProgramRegistry) Register(p Program) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.programs = append(r.programs, p)
}

// Deregister removes the program with the given name from the registry.
// Used when a custom program is updated or deleted at runtime.
func (r *ProgramRegistry) Deregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	updated := r.programs[:0]
	for _, p := range r.programs {
		if p.Name() != name {
			updated = append(updated, p)
		}
	}
	r.programs = updated
}

// Get returns the program with the given name, or (nil, false) if not found.
func (r *ProgramRegistry) Get(name string) (Program, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.programs {
		if p.Name() == name {
			return p, true
		}
	}
	return nil, false
}

// List returns a serialisable snapshot of all registered programs.
func (r *ProgramRegistry) List() []ProgramMeta {
	r.mu.RLock()
	defer r.mu.RUnlock()
	metas := make([]ProgramMeta, 0, len(r.programs))
	for _, p := range r.programs {
		_, isCustom := p.(YAMLProgramProvider)
		metas = append(metas, ProgramMeta{
			Name:         p.Name(),
			DisplayName:  p.DisplayName(),
			Description:  p.Description(),
			ConfigFields: p.ConfigFields(),
			IsCustom:     isCustom,
		})
	}
	return metas
}
