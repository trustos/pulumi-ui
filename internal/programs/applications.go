package programs

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

// ApplicationDef describes one selectable application within a program's catalog.
type ApplicationDef struct {
	Key          string          `json:"key"`                    // stable identifier: "traefik", "postgres"
	Name         string          `json:"name"`                   // display name: "Traefik Reverse Proxy"
	Description  string          `json:"description,omitempty"`
	Tier         ApplicationTier `json:"tier"`
	Target       TargetMode      `json:"target"`
	Required     bool            `json:"required"`               // always deployed, cannot be deselected
	DefaultOn    bool            `json:"defaultOn"`              // pre-selected in UI when not required
	DependsOn    []string        `json:"dependsOn,omitempty"`    // keys of other ApplicationDefs
	ConfigFields []ConfigField   `json:"configFields,omitempty"` // app-specific config fields
}

// ApplicationProvider is an optional interface a Program can implement to expose
// an application catalog. Discovered via type assertion:
//
//	if provider, ok := prog.(programs.ApplicationProvider); ok {
//	    catalog := provider.Applications()
//	}
//
// Programs that do not implement this interface behave as today — no catalog,
// no Phase 2/3 deployment. The base Program interface is unchanged.
//
// When a program implements ApplicationProvider, the engine automatically
// injects the Nebula mesh + pulumi-ui agent bootstrap into every compute
// resource's user_data via multipart MIME composition (see internal/agentinject).
// Programs do NOT need to include Nebula or agent in their application catalog;
// they are infrastructure plumbing managed by the engine.
type ApplicationProvider interface {
	Applications() []ApplicationDef
}

// AgentAccessProvider is an optional interface for YAML programs that opt into
// automatic agent connectivity injection. When a program returns true from
// AgentAccess(), the engine will:
//  1. Inject the agent bootstrap into user_data of all compute resources
//  2. Add NSG security rules for the Nebula UDP port on detected NSG resources
//  3. Add NLB backend set + listener for the agent port on detected NLB resources
//
// Built-in Go programs that implement ApplicationProvider handle their own
// networking and should NOT also implement AgentAccessProvider.
type AgentAccessProvider interface {
	AgentAccess() bool
}
