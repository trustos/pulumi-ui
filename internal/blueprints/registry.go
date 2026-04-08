package blueprints

import (
	"sync"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	builtins "github.com/trustos/pulumi-ui/blueprints"
)

// ConfigField describes one config field for the UI form.
type ConfigField struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Type        string   `json:"type"`        // text | number | textarea | select | oci-shape | oci-image | oci-compartment | oci-ad
	Required    bool     `json:"required"`
	Default     string   `json:"default,omitempty"`
	Description string   `json:"description,omitempty"`
	Options     []string `json:"options,omitempty"`     // for select type
	Group       string   `json:"group,omitempty"`      // stable group key, e.g. "iam"
	GroupLabel  string   `json:"groupLabel,omitempty"` // display heading, e.g. "IAM & Permissions"
	Secret      bool     `json:"secret,omitempty"`     // true = Consul KV auto-managed credential
	Hidden      bool     `json:"hidden,omitempty"`     // true = hidden from regular config form (auto-wired by orchestrator)
}

// MultiAccountRole defines a role in a multi-account deployment group.
type MultiAccountRole struct {
	Key   string `json:"key" yaml:"key"`
	Label string `json:"label" yaml:"label"`
	Min   int    `json:"min" yaml:"min"`
	Max   int    `json:"max" yaml:"max"`
}

// MultiAccountWiringMapping maps an output from one role to a config field on another.
type MultiAccountWiringMapping struct {
	Output string `json:"output" yaml:"output"`
	Config string `json:"config" yaml:"config"`
}

// MultiAccountAccountMapping maps an account field to a config field.
type MultiAccountAccountMapping struct {
	AccountField string `json:"accountField" yaml:"accountField"`
	Config       string `json:"config" yaml:"config"`
}

// MultiAccountCollectMapping collects a field from all stacks of a role.
type MultiAccountCollectMapping struct {
	AccountField string `json:"accountField" yaml:"accountField"`
	Config       string `json:"config" yaml:"config"`
	Separator    string `json:"separator" yaml:"separator"`
}

// MultiAccountWiring defines how outputs flow between roles.
type MultiAccountWiring struct {
	FromRole        string                       `json:"fromRole" yaml:"fromRole"`
	ToRole          string                       `json:"toRole" yaml:"toRole"`
	Mappings        []MultiAccountWiringMapping  `json:"mappings,omitempty" yaml:"mappings"`
	AccountMappings []MultiAccountAccountMapping `json:"accountMappings,omitempty" yaml:"accountMappings"`
	CollectMappings []MultiAccountCollectMapping `json:"collectMappings,omitempty" yaml:"collectMappings"`
}

// MultiAccountPerRoleConfig defines config fields with per-role-instance defaults.
type MultiAccountPerRoleConfig struct {
	Key     string `json:"key" yaml:"key"`
	Pattern string `json:"pattern" yaml:"pattern"` // e.g. "10.{index}.0.0/16"
}

// MultiAccountMeta declares that a blueprint supports multi-account deployment.
type MultiAccountMeta struct {
	Roles         []MultiAccountRole          `json:"roles" yaml:"roles"`
	DeployOrder   []string                    `json:"deployOrder" yaml:"deployOrder"`
	Wiring        []MultiAccountWiring        `json:"wiring" yaml:"wiring"`
	PerRoleConfig []MultiAccountPerRoleConfig `json:"perRoleConfig,omitempty" yaml:"perRoleConfig"`
}

// BlueprintMeta is the safe, serialisable view of a Blueprint (sent to the UI).
type BlueprintMeta struct {
	Name         string             `json:"name"`
	DisplayName  string             `json:"displayName"`
	Description  string             `json:"description"`
	ConfigFields []ConfigField      `json:"configFields"`
	IsCustom     bool               `json:"isCustom"`                // true for user-defined YAML blueprints
	IsBuiltin    bool               `json:"isBuiltin,omitempty"`     // true for blueprints registered via RegisterBuiltins (not editable/deletable)
	Applications []ApplicationDef   `json:"applications,omitempty"`  // nil for blueprints without a catalog
	AgentAccess  bool               `json:"agentAccess,omitempty"`   // true if agent networking is auto-injected
	MultiAccount *MultiAccountMeta  `json:"multiAccount,omitempty"`  // non-nil if blueprint supports multi-account deployment
}

// Blueprint is the internal interface all Pulumi blueprints implement.
type Blueprint interface {
	Name() string
	DisplayName() string
	Description() string
	ConfigFields() []ConfigField
	// Run returns a PulumiFn for the given config map.
	Run(config map[string]string) pulumi.RunFunc
}

// BlueprintRegistry is a thread-safe registry of Blueprints.
// It must be constructed via NewBlueprintRegistry and passed as a dependency
// to the Engine and HTTP handlers. No package-level state is used.
type BlueprintRegistry struct {
	mu         sync.RWMutex
	blueprints []Blueprint
	builtins   map[string]bool // blueprint names registered via RegisterBuiltins
}

// NewBlueprintRegistry creates an empty BlueprintRegistry.
func NewBlueprintRegistry() *BlueprintRegistry {
	return &BlueprintRegistry{builtins: make(map[string]bool)}
}

// RegisterBuiltins registers the built-in blueprints into r.
// Called explicitly from main.go — no init() self-registration.
//
// Built-in blueprint YAML files live in the top-level blueprints/ directory of
// the repository (easy to find, easy to edit). They are embedded at compile
// time via the builtins package.
func RegisterBuiltins(r *BlueprintRegistry) {
	RegisterYAML(r, "nomad-cluster", "Nomad Cluster",
		"Docker + Consul + Nomad infrastructure on OCI ARM instances. Add applications later.",
		builtins.ReadFile("nomad-cluster.yaml"))
	r.builtins["nomad-cluster"] = true

	RegisterYAML(r, "nomad-full-stack", "Nomad Full Stack",
		"Nomad cluster with application catalog: Traefik, PostgreSQL, pgAdmin, NocoBase, and more",
		builtins.ReadFile("nomad-full-stack.yaml"))
	r.builtins["nomad-full-stack"] = true

	RegisterYAML(r, "nomad-multi-account", "Nomad Multi-Account",
		"Multi-account Nomad + Consul cluster over OCI DRG. Pool resources across Always Free accounts.",
		builtins.ReadFile("nomad-multi-account.yaml"))
	r.builtins["nomad-multi-account"] = true
}

// Register adds p to the registry.
func (r *BlueprintRegistry) Register(p Blueprint) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.blueprints = append(r.blueprints, p)
}

// Deregister removes the blueprint with the given name from the registry.
// Used when a custom blueprint is updated or deleted at runtime.
func (r *BlueprintRegistry) Deregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	updated := r.blueprints[:0]
	for _, p := range r.blueprints {
		if p.Name() != name {
			updated = append(updated, p)
		}
	}
	r.blueprints = updated
}

// Get returns the blueprint with the given name, or (nil, false) if not found.
func (r *BlueprintRegistry) Get(name string) (Blueprint, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.blueprints {
		if p.Name() == name {
			return p, true
		}
	}
	return nil, false
}

// IsBuiltin returns true if the named blueprint was registered via RegisterBuiltins.
func (r *BlueprintRegistry) IsBuiltin(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.builtins[name]
}

// List returns a serialisable snapshot of all registered blueprints.
func (r *BlueprintRegistry) List() []BlueprintMeta {
	r.mu.RLock()
	defer r.mu.RUnlock()
	metas := make([]BlueprintMeta, 0, len(r.blueprints))
	for _, p := range r.blueprints {
		isBuiltin := r.builtins[p.Name()]
		_, isYAML := p.(YAMLBlueprintProvider)
		meta := BlueprintMeta{
			Name:         p.Name(),
			DisplayName:  p.DisplayName(),
			Description:  p.Description(),
			ConfigFields: p.ConfigFields(),
			IsCustom:     isYAML && !isBuiltin,
			IsBuiltin:    isBuiltin,
		}
		if ap, ok := p.(ApplicationProvider); ok {
			meta.Applications = ap.Applications()
		}
		if aap, ok := p.(AgentAccessProvider); ok {
			meta.AgentAccess = aap.AgentAccess()
		}
		if map_, ok := p.(MultiAccountProvider); ok {
			meta.MultiAccount = map_.MultiAccount()
		}
		metas = append(metas, meta)
	}
	return metas
}
