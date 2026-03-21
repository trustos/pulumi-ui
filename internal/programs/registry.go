package programs

import (
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

// registry holds all known programs.
var registry []Program

func Register(p Program) {
	registry = append(registry, p)
}

// Deregister removes the program with the given name from the registry.
// Used when a custom program is updated or deleted at runtime.
func Deregister(name string) {
	updated := registry[:0]
	for _, p := range registry {
		if p.Name() != name {
			updated = append(updated, p)
		}
	}
	registry = updated
}

func Get(name string) (Program, bool) {
	for _, p := range registry {
		if p.Name() == name {
			return p, true
		}
	}
	return nil, false
}

func List() []ProgramMeta {
	metas := make([]ProgramMeta, 0, len(registry))
	for _, p := range registry {
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
