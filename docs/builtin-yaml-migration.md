# Built-in YAML Programs + Per-Node Nebula + App Catalog

## Goals

1. Replace the two built-in Go programs with a single embedded YAML program.
2. Per-node Nebula certificates: each OCI instance gets a unique Nebula identity so the
   UI server can independently reach every cluster node (Rancher-like mesh).
3. Introduce an independent **App Catalog** and **App Deployment Plans** — modular,
   reusable, helm-inspired — that replace the monolithic `cloudinit.sh`.
4. App install happens via the agent exec API after the mesh is up; no cloud-init complexity.

---

## Architecture Overview

Three independent layers:

```
Layer 1 — Infrastructure Programs
  YAML programs declare what OCI resources to create.
  Programs know nothing about applications.
  meta.agentAccess: true  → drives NSG/NLB/bootstrap injection
  meta.defaultPlan: <key> → UX hint for plan pre-selection in NewStackDialog

Layer 2 — App Catalog
  Self-contained install units (shell scripts / Nomad job specs).
  Built-ins embedded in catalog/; user-submitted apps in app_definitions DB table.
  Each app: content, configSchema (ConfigField[]), defaultTarget, dependsOn, notes.

Layer 3 — App Deployment Plans
  Named ordered lists of {app, target, config-defaults}.
  Reusable blueprints. Built-ins in code; user plans in DB.
  Attached to a stack by forking: stack gets a private editable copy.
```

### Solution YAML (import/export)

```yaml
kind: solution
name: "Nomad Cluster — Production"

infrastructure:
  program: nomad-cluster   # built-in key, or inline YAML body

deployment:
  plan: nomad-full-stack   # built-in key, or inline plan body
  config:                  # per-stack overrides on top of plan defaults
    consulVersion: "1.17.0"
    traefik.domain: "apps.example.com"
```

---

## Validated Design Decisions

### Apps fully decoupled from programs
`ApplicationProvider` interface is **removed** from programs. Programs declare only
`agentAccess: true` (drives infra injection) and optionally `meta.defaultPlan: <key>`
(UI hint). The engine never inspects app catalogs when building infrastructure.

### App Deployment Plans are forked per-stack
Selecting a plan for a stack creates a copy in `stack_app_plans`. The user edits their
copy freely; the blueprint is never mutated. Same model as program blueprints.

### Backwards compatibility: StackConfig.Applications
`StackConfig` retains `Applications map[string]bool` and `AppConfig map[string]string`
fields. Old stacks (without a `stack_app_plans` row) continue to show their legacy app
selections in the UI. New deploy-apps flow checks for `stack_app_plans` first; falls back
to legacy if absent.

### No Nebula lighthouse; direct peer-to-peer Nebula
`agent_bootstrap.sh` ships with `static_host_map: {}` — correct as-is, no changes.
Each node's public IP is stored in `stack_node_certs.agent_real_ip` after deployment.

### OCI private IPs for intra-cluster communication
Consul/Nomad cluster formation uses OCI VCN private IPs from Pulumi outputs
(`instance-{i}-privateIp`). Deployer reads these and passes as `CONSUL_RETRY_JOIN`.

### Deployer requires mesh manager
The deployer reaches agents via the Nebula mesh (their Nebula IPs are unreachable
without a tunnel). The `Deployer` struct must hold `meshManager *mesh.Manager` and
call `meshManager.GetTunnel(stackName)` to get a mesh-aware HTTP client — same pattern
used by `internal/api/agent_proxy.go`.

### Engine.DeployApps signature simplification
The new deployer loads everything from stores: plan from `StackAppPlanStore`, app content
from catalog/`AppDefinitionStore`, `nodeCount` from `StackStore`, privateIPs from
`GetStackOutputs()`. The handler only needs to call `engine.DeployApps(ctx, stackName, send)`.

### `InjectNetworkingIntoYAML` fix for multi-node public IPs
The "existing NLB" loop (lines 190–204 of `network.go`) adds agent-port backends to ALL
existing NLBs regardless of public IPs. When `allComputesHavePublicIP` → skip agent
backend injection into existing NLBs.

---

## Phase 1 — Per-Node Nebula PKI

### 1a. `internal/db/migrations/013_node_certs.sql`
```sql
CREATE TABLE stack_node_certs (
  stack_name    TEXT    NOT NULL,
  node_index    INTEGER NOT NULL,
  nebula_cert   TEXT    NOT NULL,
  nebula_key    TEXT    NOT NULL,    -- AES-GCM encrypted
  nebula_ip     TEXT    NOT NULL,    -- e.g. "10.42.1.2/24"
  agent_real_ip TEXT,               -- populated post-deploy (nullable)
  PRIMARY KEY (stack_name, node_index)
);
```

### 1b. `internal/db/node_certs.go` — NodeCertStore
```go
type NodeCert struct {
    StackName string; NodeIndex int
    NebulaCert, NebulaKey []byte
    NebulaIP    string
    AgentRealIP *string
}
// CreateAll, ListForStack, UpdateAgentRealIP, DeleteForStack
```

### 1c. `internal/nebula/pki.go` — `GenerateNodeCerts`
```go
func GenerateNodeCerts(caCertPEM, caKeyPEM []byte, subnet string, n int,
    duration time.Duration) ([]CertBundle, error)
```
Uses `SubnetIP(subnet, i+2)` from `internal/nebula/subnet.go` for i=0…n-1.

### 1d. `internal/api/stacks.go` — update `generateNebulaPKI`
After base PKI, call `GenerateNodeCerts(…, 10)` → `NodeCertStore.CreateAll`.
`NebulaAgentCert` in `stack_connections` stays as node-0 cert (backwards compat).

**Handler struct gains:**
```go
NodeCertStore *db.NodeCertStore
```

### 1e. `internal/agentinject/yaml.go`
```go
func InjectIntoYAML(yamlBody string, agentVarsList []AgentVars) (string, error)
```
Resource at position i uses `agentVarsList[min(i, len-1)]`.

### 1f. `internal/agentinject/network.go` — fix multi-node NLB
In the "existing NLB" loop, add early check:
```go
if publicIPInstances {
    continue  // skip — instances reachable directly, don't add agent backends to app NLB
}
```

### 1g. `internal/engine/engine.go` — multi-cert + post-deploy discovery
Engine struct gains:
```go
NodeCertStore *db.NodeCertStore
```
- `agentVarListForStack(stackName) []AgentVars` — loads from `NodeCertStore`, falls back
  to single cert from `StackConnection` for old stacks.
- After `pulumi up`: scan outputs for `^instance-(\d+)-publicIp$` → `NodeCertStore.UpdateAgentRealIP`.
- `agentVarsForStack` (single-cert path) remains for backwards compat.

---

## Phase 2 — App Catalog + Deployment Plans

### 2a. Types — `internal/applications/catalog/types.go`

```go
type AppType   string  // "shell_script" | "nomad_job"
type TargetMode string  // "all" | "first" | "any" | "count:N"

type AppNote struct { Text, Icon string }

type AppDefinition struct {
    Key           string
    Name          string
    Description   string
    Type          AppType
    Content       string                 // script body or job spec
    ConfigSchema  []programs.ConfigField // user-configurable vars
    DefaultTarget TargetMode
    DependsOn     []string
    Notes         []AppNote              // surfaced in UI as informational callouts
    IsBuiltin     bool
}

type PlanEntry struct {
    AppKey         string
    Target         TargetMode
    ConfigDefaults map[string]string
    Enabled        bool
}

type AppDeploymentPlan struct {
    ID, Key, Name, Description string
    Entries   []PlanEntry
    IsBuiltin bool
}

type StackPlanEntry struct {
    AppKey  string
    Target  TargetMode
    Config  map[string]string  // plan defaults + user overrides merged
    Enabled bool
}

type StackAppPlan struct {
    StackName string
    PlanKey   string           // which blueprint this was forked from
    Entries   []StackPlanEntry
}
```

Note: `TargetMode` and related types move here from `internal/programs/applications.go`.
`ApplicationDef` and `ApplicationTier` in that file are removed (replaced by `AppDefinition`
and `AppType`). `AgentAccessProvider` stays in `internal/programs/applications.go`.

### 2b. Built-in apps — `internal/applications/catalog/`
```
docker.sh           OS-aware (Ubuntu apt / Oracle Linux dnf + Docker CE RPM repo)
consul.sh           HashiCorp zip; systemd; Consul DNS for Docker; health-wait loop
nomad.sh            HashiCorp zip; systemd server+client hybrid;
                    if NODE_IS_LEADER=true → run ACL bootstrap, store token in Consul KV
traefik.nomad.hcl   Nomad job; Consul service-discovery; reads NOMAD_TOKEN from env
postgres.nomad.hcl  Nomad job; persistent volume; configurable DB/user/password
catalog.go          //go:embed *.sh *.hcl; builds []AppDefinition with Notes
```

**Shared env vars (every script):**
```
NODE_COUNT          total nodes
NODE_INDEX          0-based index of this node
NODE_IS_LEADER      "true" for node 0 only (ACL bootstrap, etc.)
CONSUL_RETRY_JOIN   space-separated OCI private IPs for cluster formation
CONSUL_VERSION      pinned version string (e.g. "1.17.0")
NOMAD_VERSION       pinned version string
```

**Per-app additional vars** (from PlanEntry.Config + user overrides):
```
traefik.domain       domain for TLS termination (can be empty)
postgres.db          default "app"
postgres.user        default "postgres"
postgres.password    required, no default
```

**Nomad ACL note** (surfaced in UI via `AppDefinition.Notes`):
```
{Text: "ACL bootstrap runs on node 0 after Nomad cluster forms. Token stored in Consul KV at nomad/bootstrap-token.", Icon: "key"}
{Text: "Nomad UI accessible on port 4646 via the NLB public IP.", Icon: "link"}
```

### 2c. Built-in plans — `internal/applications/plans/plans.go`
```go
var BuiltinPlans = []catalog.AppDeploymentPlan{{
    Key: "nomad-full-stack", Name: "Nomad Full Stack",
    Description: "Consul + Nomad cluster with optional Traefik.",
    Entries: []catalog.PlanEntry{
        {AppKey: "consul",  Target: catalog.TargetAll,   ConfigDefaults: map[string]string{"version": "1.17.0"}, Enabled: true},
        {AppKey: "nomad",   Target: catalog.TargetAll,   ConfigDefaults: map[string]string{"version": "1.7.0"},  Enabled: true},
        {AppKey: "traefik", Target: catalog.TargetFirst, ConfigDefaults: map[string]string{"domain": ""},        Enabled: false},
    },
    IsBuiltin: true,
}}
```

### 2d. DB migrations — `internal/db/migrations/014_app_catalog.sql`
```sql
CREATE TABLE app_definitions (
  key            TEXT PRIMARY KEY,
  name           TEXT NOT NULL,
  description    TEXT NOT NULL DEFAULT '',
  type           TEXT NOT NULL DEFAULT 'shell_script',
  content        TEXT NOT NULL,
  config_schema  TEXT NOT NULL DEFAULT '[]',   -- JSON []ConfigField
  default_target TEXT NOT NULL DEFAULT 'all',
  depends_on     TEXT NOT NULL DEFAULT '[]',   -- JSON []string
  notes          TEXT NOT NULL DEFAULT '[]',   -- JSON []AppNote
  created_at     TEXT NOT NULL
);

CREATE TABLE app_deployment_plans (
  id          TEXT PRIMARY KEY,
  key         TEXT UNIQUE NOT NULL,
  name        TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  entries     TEXT NOT NULL DEFAULT '[]',  -- JSON []PlanEntry
  created_at  TEXT NOT NULL
);

CREATE TABLE stack_app_plans (
  stack_name TEXT PRIMARY KEY,
  plan_key   TEXT NOT NULL,
  entries    TEXT NOT NULL,   -- JSON []StackPlanEntry (editable fork)
  updated_at TEXT NOT NULL
);
```

### 2e. DB stores
- `internal/db/app_definitions.go` — CRUD for user-submitted apps
- `internal/db/app_deployment_plans.go` — CRUD for user-created plans
- `internal/db/stack_app_plans.go` — `Get`, `Upsert`, `Delete`

---

## Phase 3 — Built-in YAML Program

### 3a. `internal/programs/yaml_config.go`

Add `ParseDefaultPlan(yamlBody string) string` — returns `meta.defaultPlan` value.

Add `Options []string` to `pulumiMetaField` struct:
```go
type pulumiMetaField struct {
    UIType      string   `yaml:"ui_type"`
    Label       string   `yaml:"label"`
    Description string   `yaml:"description"`
    Options     []string `yaml:"options"`    // ← new: for select-type fields
}
```
Update `ParseConfigFields` to populate `ConfigField.Options` from meta field options.

### 3b. `internal/programs/yaml_program.go`
- Add `defaultPlan string` field; `NewYAMLProgram` calls `ParseDefaultPlan`.
- Add `isBuiltin bool` + `NewBuiltinYAMLProgram(key, name, desc, body)` constructor.
- `Meta()` returns `DefaultPlan: p.defaultPlan` and `IsCustom: !p.isBuiltin`.
- Remove `applications []ApplicationDef` field entirely.

### 3c. `internal/programs/applications.go`
- Remove `ApplicationProvider` interface.
- Remove `ApplicationDef`, `ApplicationTier`, `TargetMode` (moved to `catalog/types.go`).
- Keep `AgentAccessProvider` interface unchanged.

### 3d. `internal/programs/registry.go`
- `ProgramMeta` struct: remove `Applications []ApplicationDef`, add `DefaultPlan string`.
- `List()` no longer type-asserts `ApplicationProvider`.
- `RegisterBuiltins` uses `NewBuiltinYAMLProgram`.

### 3e. `internal/programs/builtins/nomad_cluster.yaml`

```yaml
meta:
  agentAccess: true
  defaultPlan: nomad-full-stack
  fields:
    nodeCount:
      ui_type: select
      options: ["1","2","3","4"]
      label: "Node Count"
      description: "All nodes act as server+client (hybrid mode)"
```

Config fields: `nodeCount` (select), `compartmentId`, `shape`, `imageId`, `ocpus`,
`memoryInGbs`, `sshPublicKey`, `vcnCidr`, `subnetCidr`.

Resources: VCN, IGW, route table, public subnet, SSH NSG + rule, app NSG + rules (80/443/4646),
N instances (`range $i := until (atoi .Config.nodeCount)`, each with `assignPublicIp: true`,
`user_data: ""`), NLB (`nomad-nlb`) with serialized ports 80/443/4646.

Outputs:
```yaml
outputs:
  nlbPublicIp: ${nomad-nlb.ipAddresses[0].ipAddress}
  # per node (Go template range):
  instance-{i}-publicIp:  ${instance-{i}.publicIp}
  instance-{i}-privateIp: ${instance-{i}.primaryPrivateIp}
```

### 3f. Delete `internal/programs/nomad_cluster.go`, `internal/programs/test_vcn.go`

---

## Phase 4 — Deployer executes StackAppPlan

### 4a. Updated `internal/applications/deployer.go`

```go
type Deployer struct {
    connStore     *db.StackConnectionStore
    nodeCertStore *db.NodeCertStore
    planStore     *db.StackAppPlanStore
    appDefStore   *db.AppDefinitionStore
    stackStore    *db.StackStore           // to read nodeCount from config
    meshManager   *mesh.Manager            // to open per-node Nebula tunnels
}

func NewDeployer(
    connStore     *db.StackConnectionStore,
    nodeCertStore *db.NodeCertStore,
    planStore     *db.StackAppPlanStore,
    appDefStore   *db.AppDefinitionStore,
    stackStore    *db.StackStore,
    meshManager   *mesh.Manager,
) *Deployer
```

**`DeployApps(ctx, stackName string, privateIPs []string, send LogFunc) error`:**
1. Load `StackAppPlan` from `planStore.Get(stackName)`.
   - If nil: fall back to legacy `StackConfig.Applications` path for old stacks.
2. Resolve each entry's `AppDefinition`: check built-in catalog first, then `appDefStore`.
3. Topological sort enabled entries by `DependsOn` (Kahn's).
4. Load node certs via `nodeCertStore.ListForStack(stackName)`.
5. **Phase 2**: `waitForAllAgents` — for each node cert, get mesh HTTP client via
   `meshManager.GetTunnel(stackName)` (same pattern as `agent_proxy.go`), poll `/health`.
6. **Phase 3a** (shell_script entries): For each in topo order:
   - Build env map: shared cluster vars + entry.Config values; `NODE_IS_LEADER=true` for node 0.
   - Per target node: `uploadAndExec(ctx, httpClient, token, script, envVars, send)`.
7. **Phase 3b** (nomad_job entries): For each in topo order:
   - Fetch Nomad token: exec `consul kv get nomad/bootstrap-token` on node 0 via agent.
   - Exec `nomad job run -token $TOKEN /tmp/{key}.nomad.hcl` on node 0.

### 4b. `internal/engine/engine.go` — updated DeployApps

Old signature:
```go
func (e *Engine) DeployApps(ctx, stackName, programName string, cfg, selectedApps, creds, send)
```

New signature:
```go
func (e *Engine) DeployApps(ctx context.Context, stackName string, send SSESender) error
```

Internally:
1. Load `StackConfig` from `e.stackStore` → get `nodeCount` from `cfg.Config`.
2. Call `e.GetStackOutputs(stackName)` → extract `instance-{i}-privateIp` keys.
3. Call `e.deployer.DeployApps(ctx, stackName, privateIPs, logFn)`.

Engine struct gains:
```go
StackStore    *db.StackStore
NodeCertStore *db.NodeCertStore
```

### 4c. `internal/api/stacks.go` — `StackDeployApps` handler

Simplifies to:
```go
func (h *Handler) StackDeployApps(w http.ResponseWriter, r *http.Request) {
    name := chi.URLParam(r, "name")
    // SSE setup
    err := h.Engine.DeployApps(opCtx, name, sseWriter)
    // ...
}
```
No longer needs to load `cfg.Applications` or resolve OCI credentials.

---

## Phase 5 — API for App Catalog + Plans

### 5a. `internal/api/app_catalog.go`
```
GET    /api/apps              merged list: built-in + user apps
POST   /api/apps              create user app
GET    /api/apps/:key         get one app (content + config schema + notes)
DELETE /api/apps/:key         delete user app (built-ins: 400 error)

GET    /api/plans             merged list: built-in + user plans
POST   /api/plans             create user plan
GET    /api/plans/:key        get plan with resolved entries
DELETE /api/plans/:key        delete user plan (built-ins: 400 error)
```

### 5b. `internal/api/stack_plans.go`
```
GET    /api/stacks/:name/plan          get stack's forked plan (null if none)
POST   /api/stacks/:name/plan/fork     fork a plan into the stack {planKey: string}
PUT    /api/stacks/:name/plan          update stack's forked plan entries
DELETE /api/stacks/:name/plan          remove plan from stack
```

### 5c. Solution YAML import/export (`internal/api/stacks.go` additions)
```
GET    /api/stacks/:name/solution.yaml   export stack as Solution YAML
POST   /api/solutions/import             import Solution YAML → create new stack
```

### 5d. `StackInfo` API response additions
Add to `StackInfo` struct in `internal/api/stacks.go`:
```go
HasPlan bool `json:"hasPlan"` // true if stack_app_plans row exists
```
Set from `h.StackAppPlanStore.Get(stackName) != nil`.

### 5e. Handler struct additions
```go
NodeCertStore      *db.NodeCertStore
AppDefStore        *db.AppDefinitionStore
AppPlanStore       *db.AppDeploymentPlanStore
StackAppPlanStore  *db.StackAppPlanStore
```

---

## Phase 6 — Frontend

### 6a. `frontend/src/lib/types.ts` changes

**Remove:**
- `ApplicationDef` interface (replaced by `AppDefinition`)
- `applications?: ApplicationDef[]` from `ProgramMeta`

**Add:**
```typescript
export interface AppNote { text: string; icon: string; }

export interface AppDefinition {
  key: string; name: string; description: string;
  type: 'shell_script' | 'nomad_job';
  configSchema: ConfigField[];
  defaultTarget: string;
  dependsOn: string[];
  notes: AppNote[];
  isBuiltin: boolean;
}

export interface PlanEntry {
  appKey: string; target: string;
  configDefaults: Record<string, string>;
  enabled: boolean;
}

export interface AppDeploymentPlan {
  id: string; key: string; name: string; description: string;
  entries: PlanEntry[];
  isBuiltin: boolean;
}

export interface StackPlanEntry {
  appKey: string; target: string;
  config: Record<string, string>;
  enabled: boolean;
}

export interface StackAppPlan {
  stackName: string; planKey: string;
  entries: StackPlanEntry[];
}
```

**Update `ProgramMeta`:** remove `applications?`, add `defaultPlan?: string`

**Update `StackInfo`:** add `hasPlan?: boolean`

### 6b. `frontend/src/lib/api.ts` additions

```typescript
// App catalog
listApps(): Promise<AppDefinition[]>              // GET /api/apps
createApp(data): Promise<AppDefinition>           // POST /api/apps
deleteApp(key: string): Promise<void>             // DELETE /api/apps/:key

// Deployment plans
listPlans(): Promise<AppDeploymentPlan[]>         // GET /api/plans
createPlan(data): Promise<AppDeploymentPlan>      // POST /api/plans
deletePlan(key: string): Promise<void>            // DELETE /api/plans/:key

// Stack plan fork
getStackPlan(name: string): Promise<StackAppPlan | null>     // GET /api/stacks/:name/plan
forkPlan(name: string, planKey: string): Promise<StackAppPlan> // POST /api/stacks/:name/plan/fork
updateStackPlan(name: string, entries: StackPlanEntry[]): Promise<StackAppPlan>  // PUT
deleteStackPlan(name: string): Promise<void>      // DELETE /api/stacks/:name/plan

// Solution YAML
exportSolution(name: string): Promise<string>     // GET /api/stacks/:name/solution.yaml
importSolution(yaml: string): Promise<void>       // POST /api/solutions/import
```

### 6c. `frontend/src/lib/components/NewStackDialog.svelte`

**Step 3 replaces ApplicationSelector with PlanSelector:**

```typescript
// Old:
const hasCatalog = $derived((selectedProgram?.applications?.length ?? 0) > 0)
// New:
const hasDefaultPlan = $derived(!!selectedProgram?.defaultPlan)
```

Step 3 shows `PlanSelector.svelte` — a dropdown of available plans (`listPlans()`),
pre-selecting `selectedProgram.defaultPlan`. User picks a plan to fork.

`doSave` changes: after `putStack(...)` succeeds, if a plan was selected:
```typescript
await forkPlan(stackName, selectedPlanKey)
```

Step button label: "Next: Deployment Plan" instead of "Next: Applications".

### 6d. `frontend/src/lib/components/EditStackDialog.svelte`

Step 2 now loads the stack's existing forked plan via `getStackPlan(stackName)` and
shows `PlanEditor.svelte`. If no plan exists, shows PlanSelector to fork one.
Save updates via `updateStackPlan(stackName, entries)`.

### 6e. `frontend/src/pages/StackDetail.svelte`

**Remove:**
```typescript
// Remove these (rely on program.applications which is gone):
const appCatalog = $derived<ApplicationDef[]>(currentProgram?.applications ?? [])
const hasApps = $derived(appCatalog.length > 0)
const selectedApps = $derived<Record<string, boolean>>(info?.applications ?? {})
const bootstrapApps = $derived(...)
const workloadApps = $derived(...)
import type { ApplicationDef } from '$lib/types'
```

**Add:**
```typescript
let stackPlan = $state<StackAppPlan | null>(null)
const hasApps = $derived(info?.hasPlan === true || !!currentProgram?.defaultPlan)

// Load plan alongside stack info:
async function loadAll() {
  [info, stackPlan] = await Promise.all([getStackInfo(name), getStackPlan(name)])
}
```

**Applications tab** now shows `PlanEditor.svelte` (read-only unless editing):
- Lists plan entries with app name, target badge, enabled/disabled toggle
- Shows `AppNote` callouts (e.g. Nomad ACL info)
- "Edit Plan" button → puts PlanEditor into edit mode
- "Deploy Applications" button unchanged (still calls `streamDeployApps`)
- Mesh connectivity card (unchanged)

### 6f. New component: `PlanSelector.svelte`

Props: `plans: AppDeploymentPlan[], selectedKey: string`

Shows a select/combobox of available plans with name + description.
Highlights built-in plans. Selecting pre-fills the plan key.

### 6g. New component: `PlanEditor.svelte`

Props: `plan: StackAppPlan, apps: AppDefinition[], readOnly: boolean`

Shows each entry as a card row:
- Checkbox (enabled/disabled)
- App name + type badge (`shell_script` → "Bootstrap", `nomad_job` → "Workload")
- Target badge ("All nodes", "Node 0")
- Config fields inline (collapsed by default, expandable)
- Notes rendered as info callouts below the app name

When `readOnly=false`: checkboxes and config fields are editable.
On save: calls `updateStackPlan(stackName, entries)`.

### 6h. Retire `ApplicationSelector.svelte`

The `ApplicationSelector` component (currently used in both `NewStackDialog` and
`EditStackDialog`) is replaced by `PlanSelector` + `PlanEditor`. The file can be
deleted once all references are updated.

### 6i. Nav — no new page for v1

Deployment plans are accessible from stacks only (NewStackDialog / EditStackDialog /
StackDetail). The built-in plan catalog is visible via `GET /api/plans`. A dedicated
Plans management page (`/plans`) is a v2 concern; for v1 custom plans are created via
`POST /api/plans` (API-only or via a future Plans page in Programs section).

---

## `cmd/server/main.go` wiring additions

```go
nodeCertStore    := db.NewNodeCertStore(db, enc)
appDefStore      := db.NewAppDefinitionStore(db)
appPlanStore     := db.NewAppDeploymentPlanStore(db)
stackAppPlanStore := db.NewStackAppPlanStore(db)

deployer := applications.NewDeployer(
    connStore, nodeCertStore, stackAppPlanStore, appDefStore, stackStore, meshMgr)

eng := engine.New(stateDir, registry, deployer, connStore, nodeCertStore, stackStore)

h := api.NewHandler(
    db, creds, ops, stackStore, users, sessions, accounts,
    passphrases, sshKeys, customPrograms, eng, registry, connStore,
    nodeCertStore, appDefStore, appPlanStore, stackAppPlanStore,
)
```

Note: `meshMgr` is created before the deployer (it requires `connStore`), and the
deployer now receives it directly rather than setting it after construction.

---

## Files Summary

| Action | Path |
|--------|------|
| CREATE | `internal/programs/builtins/nomad_cluster.yaml` |
| CREATE | `internal/programs/builtins/embed.go` |
| CREATE | `internal/applications/catalog/docker.sh` |
| CREATE | `internal/applications/catalog/consul.sh` |
| CREATE | `internal/applications/catalog/nomad.sh` |
| CREATE | `internal/applications/catalog/traefik.nomad.hcl` |
| CREATE | `internal/applications/catalog/postgres.nomad.hcl` |
| CREATE | `internal/applications/catalog/catalog.go` |
| CREATE | `internal/applications/catalog/types.go` |
| CREATE | `internal/applications/plans/plans.go` |
| CREATE | `internal/db/node_certs.go` |
| CREATE | `internal/db/app_definitions.go` |
| CREATE | `internal/db/app_deployment_plans.go` |
| CREATE | `internal/db/stack_app_plans.go` |
| CREATE | `internal/db/migrations/013_node_certs.sql` |
| CREATE | `internal/db/migrations/014_app_catalog.sql` |
| CREATE | `internal/api/app_catalog.go` |
| CREATE | `internal/api/stack_plans.go` |
| CREATE | `frontend/src/lib/components/PlanSelector.svelte` |
| CREATE | `frontend/src/lib/components/PlanEditor.svelte` |
| MODIFY | `internal/nebula/pki.go` — GenerateNodeCerts |
| MODIFY | `internal/programs/yaml_config.go` — ParseDefaultPlan, options in pulumiMetaField |
| MODIFY | `internal/programs/yaml_program.go` — defaultPlan, isBuiltin; remove applications |
| MODIFY | `internal/programs/applications.go` — remove ApplicationProvider, ApplicationDef, TargetMode |
| MODIFY | `internal/programs/registry.go` — DefaultPlan replaces Applications in ProgramMeta |
| MODIFY | `internal/agentinject/yaml.go` — []AgentVars slice |
| MODIFY | `internal/agentinject/network.go` — skip NLB backends when publicIPInstances |
| MODIFY | `internal/engine/engine.go` — agentVarListForStack, NodeCertStore, DeployApps simplify |
| MODIFY | `internal/applications/deployer.go` — full rewrite: StackAppPlan, mesh manager, topo-sort |
| MODIFY | `internal/api/router.go` — new routes |
| MODIFY | `internal/api/stacks.go` — NodeCertStore in Handler, HasPlan in StackInfo, StackDeployApps |
| MODIFY | `cmd/server/main.go` — wire all new stores and updated deployer/engine |
| MODIFY | `frontend/src/lib/types.ts` — remove ApplicationDef, add plan types, ProgramMeta.defaultPlan |
| MODIFY | `frontend/src/lib/api.ts` — ~10 new API functions |
| MODIFY | `frontend/src/lib/components/NewStackDialog.svelte` — step 3 → PlanSelector |
| MODIFY | `frontend/src/lib/components/EditStackDialog.svelte` — step 2 → PlanEditor |
| MODIFY | `frontend/src/pages/StackDetail.svelte` — Applications tab uses plan, remove appCatalog |
| DELETE | `internal/programs/nomad_cluster.go` |
| DELETE | `internal/programs/test_vcn.go` |
| DELETE | `frontend/src/lib/components/ApplicationSelector.svelte` (replaced by PlanEditor) |

---

## Validation Checklist

| Step | Expected |
|------|----------|
| `go build ./...` | Passes — no references to deleted programs or removed interfaces |
| `go test ./internal/nebula/...` | GenerateNodeCerts: 10 certs, IPs .2–.11 |
| `go test ./internal/programs/...` | ParseDefaultPlan returns "nomad-full-stack"; isCustom: false for built-in |
| `go test ./internal/agentinject/...` | Inject with []AgentVars: cert 0 → instance-0, cert 1 → instance-1 |
| `make test-frontend` | All existing tests pass; types compile |
| `GET /api/apps` | Returns 5 built-in entries |
| `GET /api/plans` | Returns nomad-full-stack built-in plan |
| `GET /api/programs` | nomad-cluster: isCustom=false, defaultPlan="nomad-full-stack" |
| NewStackDialog step 3 | Shows plan dropdown pre-selecting nomad-full-stack |
| Create stack → forks plan | `GET /api/stacks/:name/plan` returns forked entries |
| Edit plan in StackDetail | PUT updates fork; GET /api/plans unchanged |
| Deploy nodeCount=1 | Agent injected; infra up; Consul+Nomad installed on node 0 |
| Deploy nodeCount=2 | Both instances get different Nebula IPs in user_data |
| Post-deploy n=2 | Both nodes reachable via mesh; plan deployed on both |
| Consul cluster n=2 | consul members shows 2 on each node |
| Nomad ACL | Token in /etc/nomad.d/nomad-bootstrap-token on node 0 |
| Traefik enabled | Nomad job running; accessible via NLB |
| `GET .../solution.yaml` | Valid YAML with infra + plan + config |
| Import solution YAML | New stack created matching source |
| POST /api/apps (user) | Custom app in GET /api/apps; deployable via plan |
| Old stack (no plan row) | Applications tab shows legacy apps; deploy-apps falls back gracefully |

---

## Testing Plan

### Go unit tests (new files)

**`internal/nebula/pki_test.go`**
- `TestGenerateNodeCerts_Count` — 10 certs returned for n=10
- `TestGenerateNodeCerts_IPs` — IPs are `.2`–`.11` of the subnet, no duplicates
- `TestGenerateNodeCerts_SignedByCA` — each cert verifiable against the CA

**`internal/programs/yaml_config_test.go`** (extend existing)
- `TestParseDefaultPlan` — returns `"nomad-full-stack"` for nomad YAML; `""` when absent
- `TestParseConfigFieldOptions` — `options: [1,2,3,4]` parsed into `ConfigField.Options`

**`internal/agentinject/yaml_test.go`** (extend existing)
- `TestInjectIntoYAML_MultiNode` — cert 0 → instance-0 user_data, cert 1 → instance-1; different content
- `TestInjectIntoYAML_FallbackWhenFewer` — 1 cert, 2 instances → both get cert 0

**`internal/agentinject/network_test.go`** (extend existing)
- `TestInjectNetworking_PublicIPSkipsNLBBackend` — existing NLB with public-IP instances does NOT get agent-port backends added

**`internal/applications/catalog/catalog_test.go`**
- `TestBuiltinAppsLoaded` — all 5 expected keys present (docker, consul, nomad, traefik, postgres)
- `TestBuiltinAppsContent` — each has non-empty Content (script not empty)
- `TestBuiltinAppsNotes` — nomad has ≥1 note with Icon="key"

**`internal/applications/catalog/topo_test.go`**
- `TestTopoSort_LinearChain` — docker → consul → nomad → [traefik, postgres] correctly ordered
- `TestTopoSort_IndependentApps` — apps with no dependsOn can be in any order (valid topologies accepted)
- `TestTopoSort_CycleDetected` — cycle returns error

**`internal/applications/plans/plans_test.go`**
- `TestBuiltinPlansExist` — `nomad-full-stack` plan present
- `TestBuiltinPlanEntries` — all entry appKeys reference known built-in apps

**`internal/db/node_certs_test.go`**
- `TestNodeCertStore_CreateAll` — 10 rows inserted for a stack
- `TestNodeCertStore_ListForStack` — returns rows sorted by node_index
- `TestNodeCertStore_UpdateAgentRealIP` — IP updated for correct node
- `TestNodeCertStore_DeleteForStack` — all rows removed
- `TestNodeCertStore_Encryption` — raw DB value differs from plaintext cert

**`internal/db/stack_app_plans_test.go`**
- `TestStackAppPlanStore_Upsert` — creates row; second upsert replaces entries
- `TestStackAppPlanStore_Get` — returns nil for unknown stack
- `TestStackAppPlanStore_Delete` — row removed after delete

**`internal/db/app_definitions_test.go`**
- `TestAppDefinitionStore_CRUD` — create, get, list, delete cycle

**`internal/db/app_deployment_plans_test.go`**
- `TestAppDeploymentPlanStore_CRUD` — create, get, list, delete cycle

### Go tests to extend

**`internal/api/stacks_test.go`** (already has TestGenerateNebulaPKI*)
- `TestGenerateNebulaPKI_CreatesNodeCerts` — after generateNebulaPKI, NodeCertStore has 10 rows
- `TestStackInfo_HasPlan` — `hasPlan` field true/false based on stack_app_plans row

**`internal/programs/yaml_program_test.go`**
- `TestBuiltinYAMLProgram_IsCustomFalse` — `NewBuiltinYAMLProgram` returns `isCustom: false`
- `TestYAMLProgram_IsCustomTrue` — `NewYAMLProgram` returns `isCustom: true`

### Frontend Vitest tests (new)

**`src/lib/components/PlanSelector.test.ts`**
- Renders available plans from props
- Pre-selects `defaultPlan` from program meta
- Emits `select` event with plan key on selection

**`src/lib/components/PlanEditor.test.ts`**
- Renders all plan entries
- Toggle enabled updates local state
- Config field edit updates entry config
- Read-only mode disables all inputs
- Notes rendered as callouts

**`src/lib/api.test.ts`** (extend existing)
- `getStackPlan` — returns null on 404, StackAppPlan on 200
- `forkPlan` — POST with correct body, returns StackAppPlan
- `updateStackPlan` — PUT with entries array

### End-to-end manual verification sequence

1. `go build ./...` — zero errors
2. `go test ./...` — all pass
3. `make test-frontend` (`svelte-check` + Vitest) — all pass
4. Start server: `go run ./cmd/server`
5. Create stack with nomad-cluster program
   - NewStackDialog step 3 shows plan dropdown with `nomad-full-stack`
   - Stack created → `GET /api/stacks/:name/plan` returns forked entries
6. Verify `GET /api/apps` returns 5 apps; `GET /api/plans` returns `nomad-full-stack`
7. Open StackDetail → Applications tab shows PlanEditor (read-only)
8. Click "Edit Plan" → toggle traefik enabled, set domain → save
9. `GET /api/stacks/:name/plan` reflects change; original plan unchanged
10. `GET /api/plans/nomad-full-stack` traefik still `enabled: false`
11. Deploy infra (nodeCount=1) → agent injected → up succeeds
12. `GET /api/stacks/:name/info` → `hasPlan: true`, `deployed: true`
13. Deploy Apps → mesh connects → consul+nomad install on node 0 → ACL bootstrap
14. Repeat with nodeCount=2 → both instances get different Nebula IPs in user_data
15. Both nodes appear in `consul members` (2 members)
16. Traefik Nomad job runs on node 0
17. `GET /api/stacks/:name/solution.yaml` — valid YAML, import creates matching stack
18. POST user app (`shell_script`) → appears in `GET /api/apps` with `isBuiltin: false`
19. Old stack (pre-migration, has `StackConfig.Applications`) — deploy-apps uses legacy path
20. `go test ./internal/api/...` — all pass with real SQLite DB

---

## Documentation Updates

### Files to rewrite / heavily update

**`docs/application-catalog-architecture.md`** — complete rewrite
Current content is obsolete (describes monolithic cloudinit.sh). New content:
- Three-layer architecture (programs / catalog / plans)
- App Catalog: AppDefinition structure, built-in vs user apps, script env vars
- App Deployment Plans: blueprint model, forking, StackAppPlan
- Solution YAML format
- Deploy-apps pipeline: mesh → bootstrap apps (topo-ordered) → workload apps
- Backwards compatibility story (legacy StackConfig.Applications)

**`docs/api.md`** — add new endpoint groups
- `App Catalog` section: `GET/POST/DELETE /api/apps`, `GET /api/apps/:key`
- `Deployment Plans` section: `GET/POST/DELETE /api/plans`, `GET /api/plans/:key`
- `Stack Plans` section: `GET/POST/PUT/DELETE /api/stacks/:name/plan`, fork endpoint
- `Solution YAML` section: export + import endpoints
- Update `GET /api/stacks/:name/info` response: add `hasPlan` field, note `defaultPlan` in program meta

**`docs/programs.md`** — update YAML program section
- Remove `ApplicationProvider` interface description
- Add `meta.defaultPlan: <key>` — links program to a built-in or user plan
- Add `meta.fields.*.options` for select fields with predefined values
- Update `isCustom` — built-in YAML programs return `false`
- Add `ParseDefaultPlan()` function description

**`docs/yaml-programs.md`** — add to meta section
- Document `meta.defaultPlan: <key>` field
- Document `meta.fields.<key>.options: [...]` for select dropdowns
- Note: `meta.applications` is removed; app catalog is independent

**`docs/database.md`** — add new tables
- `stack_node_certs` — per-node Nebula cert storage (migration 013)
- `app_definitions` — user-submitted app catalog entries (migration 014)
- `app_deployment_plans` — user-created deployment plan blueprints (migration 014)
- `stack_app_plans` — per-stack forked plan (migration 014)

**`docs/architecture.md`** — update layer diagram
- Remove `ApplicationProvider` from programs layer
- Add `App Catalog` and `App Deployment Plans` as new layers
- Update deploy-apps flow description

### Files with minor updates

**`CLAUDE.md`**
- Repo map: add `internal/applications/catalog/`, `internal/applications/plans/`, new DB stores, new API files
- Update `internal/programs/` description (no `ApplicationProvider`)
- Update architecture layers section
- Roadmap: mark agent phases complete; add App Catalog as new completed item
- Remove mention of `nomad_cluster.go`, `test_vcn.go`, `cloudinit.sh` from built-in programs description

**`docs/frontend.md`**
- Add `PlanSelector.svelte`, `PlanEditor.svelte` to component list
- Update `NewStackDialog` description (step 3 is now plan selection)
- Update `StackDetail` Applications tab description
- Note `ApplicationSelector.svelte` is removed

---

## Key Invariants Preserved

- **OCI NLB serialized ports**: `nomad_cluster.yaml` uses `dependsOn` chain for NLB mutations.
- **Cloud-init gzip**: `InjectIntoYAML` → `ComposeAndEncode` / `GzipBase64` unchanged.
- **Go template in Pulumi interpolation**: `{{ printf "${%s}" $name }}` pattern.
- **`agent_bootstrap.sh` unchanged**: no lighthouse; peer-to-peer Nebula model.
- **Backwards compat**: old stacks without node_certs/stack_app_plans rows use fallback paths.
- **`agentAccess: true` drives injection**: no interface assertion needed; YAML meta flag sufficient.
- **`generateNebulaPKI` trigger**: only `AgentAccessProvider` now (YAML programs with `agentAccess: true`).
