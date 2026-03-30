package blueprints

// ApplicationTier distinguishes when an application runs in the lifecycle.
type ApplicationTier string

const (
	TierBootstrap ApplicationTier = "bootstrap" // installed by cloud-init at first boot
	TierWorkload  ApplicationTier = "workload"  // deployed via agent after infrastructure is ready
)

// TargetMode describes which instances an application should execute on.
type TargetMode string

const (
	TargetAll   TargetMode = "all"   // run on every instance in the cluster
	TargetFirst TargetMode = "first" // run on the first/leader node only
	TargetAny   TargetMode = "any"   // run on any single instance
)

// ApplicationHook declares a lifecycle hook that an application registers when deployed.
type ApplicationHook struct {
	Trigger         string `json:"trigger" yaml:"trigger"`                           // "pre-destroy", "post-up", etc.
	Type            string `json:"type" yaml:"type"`                                 // "agent-exec", "webhook"
	Command         string `json:"command,omitempty" yaml:"command,omitempty"`        // shell command for agent-exec
	ContinueOnError bool   `json:"continueOnError" yaml:"continueOnError"`           // don't block operation on failure
	Priority        int    `json:"priority" yaml:"priority"`                         // execution order (lower first)
	Description     string `json:"description" yaml:"description"`
}

// ApplicationDef describes one selectable application within a blueprint's catalog.
type ApplicationDef struct {
	Key          string             `json:"key"`                    // stable identifier: "traefik", "postgres"
	Name         string             `json:"name"`                   // display name: "Traefik Reverse Proxy"
	Description  string             `json:"description,omitempty"`
	Tier         ApplicationTier    `json:"tier"`
	Target       TargetMode         `json:"target"`
	Required     bool               `json:"required"`               // always deployed, cannot be deselected
	DefaultOn    bool               `json:"defaultOn"`              // pre-selected in UI when not required
	DependsOn    []string           `json:"dependsOn,omitempty"`    // keys of other ApplicationDefs
	ConfigFields []ConfigField      `json:"configFields,omitempty"` // app-specific config fields
	ConsulEnv    map[string]string  `json:"consulEnv,omitempty"`    // env var name → Consul KV path (read before job run)
	Port         int                `json:"port,omitempty"`         // default port for port forwarding (e.g., 80 for traefik)
	Hooks        []ApplicationHook  `json:"hooks,omitempty"`        // lifecycle hooks registered on deploy
}

// ApplicationProvider is an optional interface a Blueprint can implement to expose
// an application catalog. Discovered via type assertion:
//
//	if provider, ok := prog.(blueprints.ApplicationProvider); ok {
//	    catalog := provider.Applications()
//	}
//
// Blueprints that do not implement this interface behave as today — no catalog,
// no Phase 2/3 deployment. The base Blueprint interface is unchanged.
//
// When a blueprint implements ApplicationProvider, the engine automatically
// injects the Nebula mesh + pulumi-ui agent bootstrap into every compute
// resource's user_data via multipart MIME composition (see internal/agentinject).
// Blueprints do NOT need to include Nebula or agent in their application catalog;
// they are infrastructure plumbing managed by the engine.
type ApplicationProvider interface {
	Applications() []ApplicationDef
}

// AgentAccessProvider is an optional interface for YAML blueprints that opt into
// automatic agent connectivity injection. When a blueprint returns true from
// AgentAccess(), the engine will:
//  1. Inject the agent bootstrap into user_data of all compute resources
//  2. Add NSG security rules for the Nebula UDP port on detected NSG resources
//  3. Add NLB backend set + listener for the agent port on detected NLB resources
//
// Built-in Go blueprints that implement ApplicationProvider handle their own
// networking and should NOT also implement AgentAccessProvider.
type AgentAccessProvider interface {
	AgentAccess() bool
}
