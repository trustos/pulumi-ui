# Blueprints

A **blueprint** is a reusable infrastructure definition. A **stack** is a named instance of a blueprint with specific config values, linked OCI credentials, and Pulumi backend state. One blueprint (e.g. `nomad-cluster`) can be instantiated as many stacks (`prod`, `staging`, `dev`), each with different values. This mirrors Pulumi's own `Pulumi.yaml` / `Pulumi.<stack>.yaml` distinction.

---

## Blueprint Interface

### `internal/blueprints/registry.go`

```go
// ConfigField describes one config field for the UI form.
type ConfigField struct {
    Key         string   `json:"key"`
    Label       string   `json:"label"`
    Type        string   `json:"type"`        // see types table below
    Required    bool     `json:"required"`
    Default     string   `json:"default,omitempty"`
    Description string   `json:"description,omitempty"`
    Options     []string `json:"options,omitempty"` // for select type
    Group       string   `json:"group,omitempty"`      // stable group key, e.g. "iam"
    GroupLabel  string   `json:"groupLabel,omitempty"` // display heading, e.g. "IAM & Permissions"
}
```

**`Type` values:**

| Type | UI control |
|---|---|
| `text` | Plain text input |
| `number` | Number input |
| `textarea` | Multi-line textarea |
| `select` | Dropdown from `Options` |
| `oci-shape` | Combobox loaded from `/api/accounts/{id}/shapes`; "Always Free" badge on eligible shapes |
| `oci-image` | Combobox loaded from `/api/accounts/{id}/images`; auto-selects latest Ubuntu Minimal by default |
| `oci-compartment` | Combobox loaded from `/api/accounts/{id}/compartments`; includes tenancy root as first entry |
| `oci-ad` | Combobox loaded from `/api/accounts/{id}/availability-domains`; typically 1-3 entries per region |
| `ssh-public-key` | Picker loaded from `/api/ssh-keys`; allows selecting a named SSH key pair from the SSH Keys store |

```go
// BlueprintMeta is the safe, serializable view of a Blueprint (sent to the UI)
type BlueprintMeta struct {
    Name         string           `json:"name"`
    DisplayName  string           `json:"displayName"`
    Description  string           `json:"description"`
    ConfigFields []ConfigField    `json:"configFields"`
    IsCustom     bool             `json:"isCustom"`               // true for user-defined YAML blueprints
    Applications []ApplicationDef `json:"applications,omitempty"` // present when ApplicationProvider
    AgentAccess  bool             `json:"agentAccess,omitempty"`  // true when agent networking auto-injected
}

// Blueprint is the internal interface all Pulumi blueprints implement
type Blueprint interface {
    Name() string
    DisplayName() string
    Description() string
    ConfigFields() []ConfigField
    // Run returns a PulumiFn for the given config map.
    Run(config map[string]string) pulumi.RunFunc
}

// BlueprintRegistry is a thread-safe registry of Blueprints.
// Constructed in main.go, passed as a dependency to Engine and Handler.
type BlueprintRegistry struct { /* sync.RWMutex + []Blueprint */ }

func NewBlueprintRegistry() *BlueprintRegistry
func (r *BlueprintRegistry) Register(p Blueprint)
func (r *BlueprintRegistry) Deregister(name string)
func (r *BlueprintRegistry) Get(name string) (Blueprint, bool)
func (r *BlueprintRegistry) List() []BlueprintMeta

// RegisterBuiltins adds all built-in blueprints to r. Called from main.go.
func RegisterBuiltins(r *BlueprintRegistry)

// RegisterYAML creates a YAMLBlueprint and adds it to r.
func RegisterYAML(r *BlueprintRegistry, name, displayName, description, yamlBody string)
```

Built-in blueprints register explicitly from `main.go` via `RegisterBuiltins(r)`. There are no `init()` self-registrations. The registry is passed as a dependency to both the engine and the HTTP handler (`h.Registry`).

---

## Config Groups

Config fields carry `Group` (stable machine key) and `GroupLabel` (human-readable heading). `ConfigForm.svelte` groups fields by `Group` and renders each group as a labeled section. Fields without a group render flat. This is purely a presentation concern — storage and validation are unaffected.

Example groups for `nomad-cluster`:

| Group key | Group label | Fields |
|---|---|---|
| `iam` | IAM & Permissions | _(dynamic group + policy, always created)_ |
| `infrastructure` | Infrastructure | `nodeCount`, `compartmentName`, `vcnCidr`, `publicSubnetCidr`, `privateSubnetCidr`, `sshSourceCidr`, `shape`, `imageId` |
| `compute` | Compute & Storage | `bootVolSizeGb`, `sshPublicKey` |
| `software` | Software Versions | `nomadVersion`, `consulVersion` |

---

## Built-in Blueprints

### test-vcn — `internal/blueprints/test_vcn.go`

Creates a compartment and VCN — a safe smoke test for OCI credentials that creates minimal resources.

**Config fields:**

| Key | Type | Default | Description |
|---|---|---|---|
| `compartmentName` | `text` | `test-compartment` | OCI compartment name |
| `vcnCidr` | `text` | `10.0.0.0/16` | CIDR block for the test VCN |

**Outputs:** `compartmentId`, `vcnId`

---

### nomad-cluster — `internal/blueprints/nomad_cluster.go`

Full Nomad + Consul cluster on OCI VM.Standard.A1.Flex (Always Free eligible). Seven sub-functions, one `Run` entry point.

**Config fields (15 total):**

| Key | Type | Default | Description | Group |
|---|---|---|---|---|
| `nodeCount` | `select` | `3` | Number of nodes (1–4; Always Free limit: 4 OCPUs / 24 GB total) | Infrastructure |
| `compartmentName` | `text` | `nomad-compartment` | OCI compartment name | Infrastructure |
| `compartmentDescription` | `text` | `Compartment for Nomad cluster` | | Infrastructure |
| `vcnCidr` | `text` | `10.0.0.0/16` | | Infrastructure |
| `publicSubnetCidr` | `text` | `10.0.1.0/24` | | Infrastructure |
| `privateSubnetCidr` | `text` | `10.0.2.0/24` | | Infrastructure |
| `sshSourceCidr` | `text` | `0.0.0.0/0` | Restrict to your IP for production security | Infrastructure |
| `shape` | `oci-shape` | `VM.Standard.A1.Flex` | OCI compute shape | Infrastructure |
| `imageId` | `oci-image` | _(required)_ | OCI image OCID | Infrastructure |
| `ocpusPerNode` | `number` | `1` | OCPUs allocated to each node (Always Free limit: 4 total) | Compute & Storage |
| `memoryGbPerNode` | `number` | `6` | Memory GiB per node (Always Free limit: 24 GB total) | Compute & Storage |
| `bootVolSizeGb` | `number` | `50` | Boot volume size in GB | Compute & Storage |
| `sshPublicKey` | `ssh-public-key` | _(required)_ | SSH public key injected into instance metadata | Compute & Storage |
| `nomadVersion` | `text` | `1.10.3` | | Software Versions |
| `consulVersion` | `text` | `1.21.3` | | Software Versions |

**Outputs:** `traefikNlbIps`, `privateSubnetId`

### SSH key injection

The SSH public key is passed to the blueprint via the `cfg` map under `OCI_USER_SSH_PUBLIC_KEY`, injected by `engine.buildEnvVars()`. Sources (resolved by `api.resolveCredentials`):

1. The linked SSH key (`ssh_key_id` on the stack) — takes priority
2. The OCI account's `ssh_public_key` field — fallback

The blueprint reads it from the config map:
```go
sshPublicKey := cfgOr(cfg, "OCI_USER_SSH_PUBLIC_KEY", "")
```

This value is passed as `metadata.ssh_authorized_keys` in the instance's cloud-init `LaunchDetails`.

### Blueprint structure

```
Run()
 ├─ 1. createCompartment()   — identity.NewCompartment
 ├─ 2. createIAM()           — dynamic groups, policies for instance principals
 ├─ 3. createNetwork()       — VCN, IGW, NAT GW, route tables, subnets
 ├─ 4. createNSGs()          — SSH, Nomad, Traefik NSGs
 ├─ 5. createInstancePools() — instances with cloud-init (per node-count spec)
 └─ 6. createNLB()           — Network Load Balancer for Traefik
```

### IAM sub-function: `createIAM`

The dynamic group and IAM policy are always created (required for peer discovery):
1. A DynamicGroup is created matching all instances in the new compartment.
2. A Policy is created granting the DynamicGroup `read virtual-network-family` (for subnet resolution via IMDS + OCI CLI), `read instance-family`, and other required permissions.

The deploying user must have tenancy-level permission to create dynamic groups and policies. If they don't, a tenancy admin must grant this manually before the first deploy.

`identityDomain` controls the group reference format in policy statements:
- Empty string → bare group name (old-style IDCS tenancies)
- Non-empty → `'DomainName'/GroupName` syntax (new Identity Domain tenancies)

### Sub-function: `createNetwork`

```go
type networkResult struct {
    vcnID           pulumi.IDOutput
    publicSubnetID  pulumi.IDOutput
    privateSubnetID pulumi.IDOutput
}
```

Creates VCN → IGW → NAT GW → public/private route tables → security lists → two subnets.

### Sub-function: `createNSGs`

```go
type nsgResult struct {
    sshNsgID     pulumi.IDOutput
    nomadNsgID   pulumi.IDOutput
    traefikNsgID pulumi.IDOutput
}
```

Creates 3 NSGs with security rules:
- **SSH NSG**: port 22 from `sshSourceCidr`
- **Nomad NSG**: ports 4646, 4647, 4648 from public subnet CIDR
- **Traefik NSG**: ports 80, 443 from public subnet CIDR

### Node sizing

```go
func getInstanceSpecs(nodeCount int) []instanceSpec {
    switch nodeCount {
    case 1: return []instanceSpec{{"single-node", 4, 24, 1}}
    case 2: return []instanceSpec{{"two-nodes", 2, 12, 2}}
    case 3: return []instanceSpec{
        {"small-nodes", 1, 8, 2},
        {"large-node", 2, 8, 1},
    }
    case 4: return []instanceSpec{{"four-nodes", 1, 6, 4}}
    }
}
```

All specs fit within the OCI Always Free quota of 4 OCPUs / 24 GB RAM total.

### Cloud-init handling

```go
// internal/blueprints/cloudinit.go
//go:embed cloudinit.sh
var cloudInitScript string

func buildCloudInit(
    ocpus, memoryGb, nodeCount int,
    nomadVersion, consulVersion string,
    apps map[string]bool,
    extraVars map[string]string,
    agentBootstrap []byte,
) string
```

The `cloudinit.sh` file lives at `internal/blueprints/cloudinit.sh` and is embedded via `//go:embed`. It uses Go `text/template` with conditional blocks (`{{ if .Apps.KEY }}`) for each application (Docker, Consul, Nomad). Runtime variables are passed through a `CloudInitData` struct with `Vars` (string map) and `Apps` (boolean map).

When `agentBootstrap` is non-empty (provided by the engine for blueprints implementing `ApplicationProvider`), the result is a **multipart MIME message** composing the blueprint's cloud-init script with the agent bootstrap script (Nebula + pulumi-ui agent). The agent bootstrap is rendered and injected automatically by the engine — blueprints do not manage it directly. See `internal/agentinject` for the composition logic.

When `agentBootstrap` is nil, the result is a simple gzip+base64 encoded script. OCI instance metadata has a 32 KB total limit; the uncompressed script is ~29 KB (~39 KB base64) which would exceed it. Gzipped it becomes ~8.5 KB (~11 KB base64). `cloud-init` detects gzip via magic bytes and decompresses transparently.

### Application catalog

Blueprints can optionally implement the `ApplicationProvider` interface to expose an application catalog — a list of selectable applications deployed after infrastructure provisioning via the pulumi-ui agent. When a blueprint implements this interface, the engine automatically injects the Nebula mesh + agent bootstrap into every compute resource's `user_data` via multipart MIME composition (see `internal/agentinject`). Nebula and the agent are **not** part of the blueprint's application catalog; they are infrastructure plumbing managed by the engine.

The nomad-cluster catalog includes: Traefik, PostgreSQL, pgAdmin (separate apps — pgAdmin depends on postgres), NocoBase (with `init-secrets` and `init-db` tasks for Consul KV credential setup and dedicated DB creation; image: `nocobase/nocobase:latest`, health check: `HTTP GET /`), and GitHub Actions Runner. Config fields can be marked `secret: true` for Consul KV auto-managed credentials with a per-app `_autoCredentials` toggle.

The deployer uses `nomad job run -detach` + polling (`checkDeploymentStatus()` every 10s with a fresh tunnel per poll) rather than blocking exec streams, avoiding tunnel-death hangs.

Separately, YAML blueprints can declare `meta.agentAccess: true` to opt into automatic agent connectivity. This causes the engine to:
1. **Generate Nebula PKI** at stack creation — per-stack CA, UI cert (`.1`, group "server"), agent cert (`.2`, group "agent"), and a `crypto/rand` 32-byte hex auth token. Stored in `stack_connections`.
2. Inject the agent bootstrap into compute resource `user_data` (same as `ApplicationProvider`). Missing intermediate property nodes (e.g. `metadata`) are created automatically.
3. Auto-add NSG security rules for the Nebula UDP port on existing NSG resources, or create a new NSG from the VCN if none exist
4. Auto-add NLB backend set + listener for the agent port on existing NLB resources, or create a new NLB from the subnet if none exist
5. **Post-deploy IP discovery** — after successful `Up`, the engine scans Pulumi outputs for IP patterns and stores the agent's real IP in `stack_connections` for Nebula tunnel establishment. The engine accepts: `instance-{i}-publicIp` (per-node, sequential), `instancePublicIp/IP`, `nlbPublicIp/IP`, `publicIp/IP`, `serverPublicIp/IP`. Blueprints must expose at least one of these; the visual editor warns and blocks save when they are absent.

The agent bootstrap script installs both the Nebula binary (from GitHub releases, configured as a systemd service on port 41820) and the pulumi-ui agent binary (from the server at `GET /api/agent/binary/{os}/{arch}`). All subsequent agent communication routes through the Nebula mesh via `internal/mesh/mesh.go`.

See `docs/application-catalog-architecture.md` for the full architecture.

---

## YAML Blueprints

User-defined blueprints are stored as Go-templated Pulumi YAML in the `custom_blueprints` database table.

### `internal/blueprints/yaml_blueprint.go`

```go
// YAMLBlueprint implements Blueprint. Run() returns nil — the engine detects this
// via type assertion to YAMLBlueprintProvider and uses UpsertStackLocalSource.
type YAMLBlueprint struct {
    name        string
    displayName string
    description string
    yamlBody    string
    fields      []ConfigField  // parsed from the YAML config: section
    agentAccess bool           // parsed from meta.agentAccess
}

// YAMLBlueprintProvider is checked via type assertion by the engine.
type YAMLBlueprintProvider interface {
    YAMLBody() string
}

// AgentAccessProvider — YAMLBlueprint implements this when meta.agentAccess: true.
// The engine injects user_data + networking resources automatically.
type AgentAccessProvider interface {
    AgentAccess() bool
}
```

`NewYAMLBlueprint(name, displayName, description, yamlBody)` parses the config fields from the YAML body at construction time. `RegisterYAML(...)` is the convenience function called at startup (for rows loaded from the DB) and at runtime (when a user creates/updates a blueprint via the API).

### `internal/blueprints/yaml_config.go`

`ParseConfigFields(yamlBody string) ([]ConfigField, string, error)` parses the `config:` section of a Pulumi YAML body and returns:
- `[]ConfigField` — derived from the YAML field names and types, with `meta:` groups and `ui_type` overrides applied
- `cleanYAML` — the same body with the `meta:` section stripped

Type mapping:
| YAML type | ConfigField type |
|---|---|
| `string` | `text` |
| `integer` / `number` | `number` |
| `boolean` | `select` |
| key == `imageId` | `oci-image` (convention) |
| key == `shape` | `oci-shape` (convention) |
| key == `compartmentId` | `oci-compartment` (convention) |
| key == `availabilityDomain` | `oci-ad` (convention) |

The optional `meta:` top-level section (stripped before execution) allows declaring field groups, explicit `ui_type` overrides, and agent connectivity. The `meta:` section is parsed from YAML blueprints:
```yaml
meta:
  agentAccess: true  # opt-in: auto-inject agent + networking resources
  groups:
    - key: network
      label: "Network"
      fields: [vcnCidr, compartmentName]
  fields:
    imageId:
      ui_type: oci-image
```

When `agentAccess: true` is set, the YAML blueprint implements `AgentAccessProvider` and the engine automatically injects agent bootstrap into compute `user_data` (creating intermediate nodes like `metadata` if missing) and adds or creates NSG/NLB resources for agent connectivity (see `internal/agentinject/network.go`).

### `internal/blueprints/template.go`

`RenderTemplate(templateBody, config)` renders a Go-templated YAML body using:
- **Sprig** (`github.com/Masterminds/sprig/v3`) — same 100+ function library as Helm
- **Custom OCI helpers**: `instanceOcpus`, `instanceMemoryGb`, `cloudInit`, `groupRef`, `gzipBase64`

`SanitizeYAML(yamlBody)` strips `fn::readFile` directives to prevent user blueprints from reading server filesystem files.

### Credential injection for YAML programs

YAML blueprints cannot read environment variables directly (the Pulumi OCI provider reads its config from the Pulumi config system). The engine calls `stack.SetConfig()` after stack creation:

```go
stack.SetConfig(ctx, "oci:tenancyOcid", auto.ConfigValue{Value: oci.TenancyOCID})
stack.SetConfig(ctx, "oci:userOcid",    auto.ConfigValue{Value: oci.UserOCID})
stack.SetConfig(ctx, "oci:fingerprint", auto.ConfigValue{Value: oci.Fingerprint})
stack.SetConfig(ctx, "oci:privateKey",  auto.ConfigValue{Value: oci.PrivateKey, Secret: true})
stack.SetConfig(ctx, "oci:region",      auto.ConfigValue{Value: oci.Region})
```

The private key is always passed as inline PEM content (`oci:privateKey`, `Secret: true`), never as a file path. A temp file path would be deleted after `Up` and cause 401 errors on subsequent `Refresh` operations.

---

## YAML Blueprint Authoring Reference

A YAML blueprint lets you define infrastructure as a Go-templated Pulumi YAML file stored in the database. Once created, it appears in the **New Stack** dialog alongside built-in blueprints and can be edited or deleted at any time — no server restart required.

### How It Works

```
DB (blueprint_yaml column)         Runtime rendering              Pulumi execution
────────────────────────         ──────────────────────         ─────────────────
Go-templated YAML  ─────────→   text/template.Execute()  ────→ UpsertStackLocalSource
{{ range }}, {{ if }},           with config map + Sprig         on rendered plain YAML
custom OCI functions             + custom OCI helpers
```

Two layers of resolution — keep them straight:

| Syntax | Resolved by | When | Example |
|---|---|---|---|
| `{{ ... }}` | Go template | Before Pulumi runs | `{{ .Config.nodeCount }}` |
| `${ ... }` | Pulumi | At apply time | `${my-compartment.id}` |

Go template decides **structure** (loops, conditionals, how many resources). Pulumi resolves **runtime values** (actual OCIDs, outputs from other resources).

> **Important:** Go template processes the entire file — including YAML comments. Do **not** write `{{ }}` inside comments. Use plain text instead.

### Minimal Blueprint

```yaml
name: my-vcn
runtime: yaml
description: "Creates a compartment and VCN"

config:
  compartmentName:
    type: string
    default: my-compartment
  vcnCidr:
    type: string
    default: "10.0.0.0/16"

resources:
  my-compartment:
    type: oci:Identity/compartment:Compartment
    properties:
      compartmentId: ${oci:tenancyOcid}
      name: {{ .Config.compartmentName }}
      description: "Created by Pulumi"
      enableDelete: true

  my-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: ${my-compartment.id}
      cidrBlock: {{ .Config.vcnCidr | quote }}
      displayName: my-vcn
      dnsLabel: myvcn

outputs:
  compartmentId: ${my-compartment.id}
  vcnId: ${my-vcn.id}
```

**Required top-level keys:** `name`, `runtime: yaml`, `resources`.

### Config Section

The `config:` block defines the fields shown in the UI form when creating a stack.

```yaml
config:
  myField:
    type: string        # string | integer | number | boolean
    default: "hello"    # optional
```

#### Field Types → Form Controls

| YAML type | Form field | Notes |
|---|---|---|
| `string` | Text input | Default for most values |
| `integer` | Number input | Use for counts, sizes |
| `number` | Number input | Use for floats |
| `boolean` | Select (true/false) | |
| key = `imageId` | OCI image picker | Convention — type must be `string` |
| key = `shape` | OCI shape picker | Convention — type must be `string` |
| key = `sshPublicKey` | SSH key picker | Convention — type must be `string` |
| key = `compartmentId` | OCI compartment picker | Convention — type must be `string` |
| key = `availabilityDomain` | OCI availability domain picker | Convention — type must be `string` |

#### Accessing Config Values

```yaml
# Simple value
name: {{ .Config.compartmentName }}

# Quoted (required for YAML string values containing special chars)
cidrBlock: {{ .Config.vcnCidr | quote }}

# As integer for arithmetic
{{ until (atoi .Config.nodeCount) }}
```

> **Defaults are applied automatically.** When a stack runs, declared `default:` values are merged into the config before the template renders. A field with a `default:` is always safe to reference as `{{ .Config.key }}` even if the user never edited it.

#### Meta Section (UI Groups)

Add a `meta:` block at the top of your file to group config fields in the UI form and override field types. It is stripped before Pulumi executes the blueprint.

```yaml
meta:
  groups:
    - key: network
      label: "Network"
      fields: [compartmentName, vcnCidr, subnetCidr]
    - key: compute
      label: "Compute"
      fields: [shape, imageId, nodeCount, bootVolSizeGb, sshPublicKey]
  fields:
    imageId:
      ui_type: oci-image
      description: "Ubuntu Minimal image for your region"
    shape:
      ui_type: oci-shape
      label: "Instance Shape"
      description: "VM.Standard.A1.Flex is Always Free eligible"
    sshPublicKey:
      ui_type: ssh-public-key
      description: "Used to SSH into instances after deploy"
```

`meta:` supports these per-field properties:

| Property | Description |
|---|---|
| `ui_type` | Override the form control. Options: `oci-image`, `oci-shape`, `oci-compartment`, `oci-ad`, `ssh-public-key`, `text`, `number`, `select`, `textarea` |
| `label` | Override the auto-generated label (default: camelCase → Title Case) |
| `description` | Help text shown below the field in the stack form |

Fields not listed in any group appear at the bottom ungrouped.

#### Display Name (meta.displayName)

Set `displayName` in the `meta:` block to give the blueprint a human-readable title separate from its machine-friendly `name` identifier:

```yaml
meta:
  displayName: My Production Cluster
```

#### Agent Access (meta.agentAccess)

Set `agentAccess: true` in the `meta:` block to opt into automatic agent connectivity injection. You can also toggle this via the **Agent Connect** button in the blueprint editor header:

```yaml
meta:
  agentAccess: true
```

When enabled, at deploy time the engine automatically:
1. **Bootstrap injection** — injects the Nebula mesh + pulumi-ui agent bootstrap into every compute resource's `user_data` (via multipart MIME).
2. **NSG injection** — if an NSG exists: adds UDP ingress rule on port 41820. If no NSG but a VCN exists: creates `__agent_nsg` and attaches it to each compute instance.
3. **NLB per-node injection** — if a **public** NLB exists: creates a dedicated backend set + listener per compute node at ports **41821, 41822, …**. No NLB is auto-created — blueprints without an NLB use per-instance public IPs.

See `docs/oci-networking-rules.md` for the full topology coverage table (T1–T8) and `docs/application-catalog-architecture.md` for the agent architecture.

### Loops

Use Sprig's `until` to iterate over a range of integers.

```yaml
config:
  nodeCount:
    type: integer
    default: 3

resources:
  {{- range $i := until (atoi $.Config.nodeCount) }}
  instance-{{ $i }}:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ${my-compartment.id}
      displayName: {{ printf "node-%d" $i }}
      shape: {{ $.Config.shape }}
      shapeConfig:
        ocpus: {{ instanceOcpus $i (atoi $.Config.nodeCount) }}
        memoryInGbs: {{ instanceMemoryGb $i (atoi $.Config.nodeCount) }}
  {{- end }}
```

> **Note:** Use `$.Config` (global `$` prefix) inside `range` blocks to access config. The loop variable `$i` shadows `.` inside the loop body.

#### Iterating a Fixed List of Values

```yaml
{{- range $port := list 4646 4647 4648 }}
  nsg-rule-{{ $port }}:
    type: oci:Core/networkSecurityGroupSecurityRule:NetworkSecurityGroupSecurityRule
    properties:
      networkSecurityGroupId: ${my-nsg.id}
      direction: INGRESS
      protocol: "6"
      tcpOptions:
        destinationPortRange:
          min: {{ $port }}
          max: {{ $port }}
{{- end }}
```

#### Serialized Loops (NLB Pattern)

OCI Network Load Balancer rejects concurrent mutations with `409 Conflict`. Use a `dependsOn` chain:

```yaml
{{- $prevNlbResource := "my-nlb" }}
{{- range $port := list 80 443 4646 }}
  backend-set-{{ $port }}:
    type: oci:NetworkLoadBalancer/backendSet:BackendSet
    properties:
      networkLoadBalancerId: ${my-nlb.id}
      name: backend-set-{{ $port }}
      policy: FIVE_TUPLE
      healthChecker:
        protocol: TCP
        port: {{ $port }}
    options:
      dependsOn:
        - {{ printf "${%s}" $prevNlbResource }}
  {{- $prevNlbResource = printf "backend-set-%d" $port }}
{{- end }}
```

> **Note:** `{{ printf "${%s}" $prevResource }}` is the correct way to build a Pulumi interpolation string inside a Go template. Do NOT write `${{{ $prevResource }}}`.

### Conditionals

```yaml
{{- if ne .Config.skipOptionalResource "true" }}
  optional-resource:
    type: oci:Identity/dynamicGroup:DynamicGroup
    properties:
      compartmentId: ${oci:tenancyOcid}
      name: my-dg
      matchingRule: "ALL {instance.compartment.id = '${my-compartment.id}'}"
{{- end }}
```

Common conditions:

| Template | Meaning |
|---|---|
| `{{- if eq .Config.x "value" }}` | string equals |
| `{{- if ne .Config.x "true" }}` | string not equals |
| `{{- if .Config.x }}` | non-empty string |
| `{{- if not .Config.x }}` | empty string |
| `{{- if gt (atoi .Config.n) 2 }}` | integer greater than |

### Sprig Functions

All [Sprig](https://masterminds.github.io/sprig/) functions are available (same library as Helm).

| Category | Function | Example | Result |
|---|---|---|---|
| Strings | `quote` | `{{ "hello" \| quote }}` | `"hello"` |
| | `upper` / `lower` | `{{ .Config.name \| lower }}` | `my-name` |
| | `printf` | `{{ printf "node-%d" $i }}` | `node-0` |
| | `b64enc` | `{{ .Config.script \| b64enc }}` | base64 encoded |
| Numbers | `atoi` | `{{ atoi .Config.nodeCount }}` | string → int |
| | `add` / `sub` / `mul` / `div` | `{{ add $i 1 }}` | `$i + 1` |
| Lists | `list` | `{{ list 80 443 8080 }}` | `[80, 443, 8080]` |
| | `until` | `{{ until 3 }}` | `[0, 1, 2]` |
| Logic | `default` | `{{ .Config.shape \| default "VM.Standard.A1.Flex" }}` | fallback |
| | `ternary` | `{{ ternary "yes" "no" (eq .Config.x "1") }}` | conditional |
| | `empty` | `{{ if empty .Config.name }}` | empty check |

### Custom OCI Functions

Four helper functions are built into the template engine specifically for OCI.

#### `instanceOcpus` / `instanceMemoryGb`

Distributes OCI Always Free quota (4 OCPUs / 24 GB) across nodes:

| nodeCount | node 0 | node 1 | node 2 | node 3 | Total OCPUs |
|---|---|---|---|---|---|
| 1 | 4 | — | — | — | 4 |
| 2 | 2 | 2 | — | — | 4 |
| 3 | 1 | 1 | 2 | — | 4 |
| 4 | 1 | 1 | 1 | 1 | 4 |

```yaml
ocpus: {{ instanceOcpus $i (atoi $.Config.nodeCount) }}
memoryInGbs: {{ instanceMemoryGb $i (atoi $.Config.nodeCount) }}
```

#### `cloudInit`

```
cloudInit(nodeIndex int, config map[string]string) string
```

Renders the Nomad/Consul cluster cloud-init script, gzip-compresses it, and returns it base64-encoded. Uses config values such as `nodeCount`, `nomadVersion`, `consulVersion`.

```yaml
metadata:
  user_data: {{ cloudInit $i $.Config }}
```

> **Limitation:** `cloudInit` runs at template render time. It cannot reference `${resource.id}` outputs. If the boot script needs a runtime OCID, use a built-in Go blueprint where `pulumi.All(...).ApplyT(...)` is available.

> **Agent injection:** For blueprints with `meta.agentAccess: true`, the engine automatically injects the Nebula mesh + agent bootstrap into every compute resource's `user_data` **after** template rendering, via multipart MIME composition. Blueprint authors do not need to include agent setup in their `cloudInit` calls.

#### `groupRef`

Generates an IAM policy statement for old IDCS tenancies (no domain) and new Identity Domain tenancies:

```yaml
statements:
  - {{ groupRef .Config.adminGroupName .Config.identityDomain "manage dynamic-groups in tenancy" | quote }}
```

#### `gzipBase64`

Compresses a shell script with gzip and returns the base64-encoded result, suitable for OCI instance `metadata.user_data`. Use this for custom cloud-init scripts without the full `cloudInit` function:

```yaml
metadata:
  user_data: {{ gzipBase64 "#!/bin/bash\napt-get update && apt-get install -y nginx" }}
```

### Multi-Account Deployment (`meta.multiAccount`)

Blueprints can support coordinated multi-account deployments via deployment groups. Declare roles, deployment order, and output-to-config wiring in `meta.multiAccount`:

```yaml
meta:
  multiAccount:
    roles:
      - key: primary
        label: "Primary (Server)"
        min: 1
        max: 1
      - key: worker
        label: "Worker (Client)"
        min: 1
        max: 3
    deployOrder: [primary, worker]
    wiring:
      - fromRole: primary
        toRole: worker
        mappings:
          - output: drgOcid
            config: drgOcid
          - output: instancePrivateIp
            config: primaryPrivateIp
        accountMappings:
          - accountField: tenancyOcid
            config: primaryTenancyOcid
      - fromRole: worker
        toRole: primary
        collectMappings:
          - accountField: tenancyOcid
            config: workerTenancyOcids
            separator: ","
    perRoleConfig:
      - key: vcnCidr
        pattern: "10.{index}.0.0/16"
```

The deployment group wizard reads this metadata to present role selection, auto-generate CIDRs, and wire outputs between stacks. Fields referenced in wiring should be marked `hidden: true` in `meta.fields`.

### Config Field Options and Hidden Fields

`meta.fields` supports `options` (for select dropdowns) and `hidden` (for orchestrator-managed fields):

```yaml
meta:
  fields:
    role:
      ui_type: select
      options: ["primary", "worker"]
    drgOcid:
      hidden: true
    primaryPrivateIp:
      hidden: true
```

- `options: [...]` renders the field as a `<select>` dropdown with the given values
- `hidden: true` excludes the field from all config forms (regular stacks and groups). The field is still in the config and set by the orchestrator at deploy time.

### OCI Resource Types

Resource type tokens follow the canonical pattern `oci:[Module]/[subpath]:[Resource]`. Short-form aliases (`oci:core:Vcn`) work at runtime but will not receive schema assistance in the visual editor.

| Short-form (accepted) | Canonical (preferred) |
|---|---|
| `oci:core:Vcn` | `oci:Core/vcn:Vcn` |
| `oci:core:NatGateway` | `oci:Core/natGateway:NatGateway` |
| `oci:identity:DynamicGroup` | `oci:Identity/dynamicGroup:DynamicGroup` |
| `oci:networkloadbalancer:Backend` | `oci:NetworkLoadBalancer/backend:Backend` |

**Use canonical form in new blueprints.** The visual editor's property autocomplete, required-field validation (Level 5), and the Resource Catalog all key off the canonical form.

### Availability Domain (variables block)

`availabilityDomain` is always resolved at apply time via a `variables:` block — never as a config field:

```yaml
variables:
  availabilityDomains:
    fn::invoke:
      function: oci:Identity/getAvailabilityDomains:getAvailabilityDomains
      arguments:
        compartmentId: ${oci:tenancyOcid}
      return: availabilityDomains
```

Use `${availabilityDomains[0].name}` for the first domain. For clusters, use `mod` to round-robin:

```yaml
availabilityDomain: ${availabilityDomains[{{ mod $i (atoi $.Config.adCount) }}].name}
```

### Outputs

```yaml
outputs:
  compartmentId: ${my-compartment.id}
  vcnId: ${my-vcn.id}
  instancePublicIp: ${my-instance-0.publicIp}
  nlbIp: ${my-nlb.ipAddresses[0].ipAddress}
```

### Special Variables

```yaml
# Tenancy OCID — always available, no config needed
compartmentId: ${oci:tenancyOcid}

# Config values injected by the engine (always available)
# oci:tenancyOcid, oci:userOcid, oci:fingerprint, oci:privateKey, oci:region
```

### Validation

Blueprints are validated on every save. Validation runs seven levels sequentially:

| Level | Name | What it checks |
|---|---|---|
| 1 | Template syntax | Can the Go template be parsed? |
| 2 | Template render | Can it render with all defaults applied? |
| 3 | YAML structure | Does the rendered output have `name`, `runtime: yaml`, and `resources`? |
| 4 | Config section | Are field types valid? Do `meta:` group references exist in `config:`? |
| 5 | Resource structure | Does each resource have a valid type token? Are all required properties present? |
| 6 | Variable references | Does every `${varName}` reference a name defined in `variables:` or `resources:`? |
| 7a | Agent networking | If `agentAccess: true`, are there compute resources with networking context? |
| 7b | Agent IP outputs | If `agentAccess: true`, is at least one IP output key defined? |

Levels 1–6 are blocking. Level 7 produces non-blocking warnings.

### Security

- `fn::readFile` — any line containing this is stripped before execution via `SanitizeYAML()`. Blueprints cannot read server files.
- Blueprints can only call OCI APIs through the Pulumi OCI provider using credentials you have configured.
- OCI credentials are injected via Pulumi config, not environment variables.

### Limitations vs Built-in Blueprints

| Capability | Built-in Go | YAML blueprint |
|---|---|---|
| Loops / Conditionals | Yes | Yes (via Go template) |
| `pulumi.All(...).ApplyT(...)` | Yes | No |
| Runtime OCIDs in cloud-init | Yes | No — use IMDS at boot |
| Agent bootstrap injection | Yes | Yes |
| Agent networking injection | Manual | Automatic (`meta.agentAccess`) |
| Arbitrary Go logic | Yes | No |
| No recompile needed | No | Yes |
| Stored in database / editable via UI | No | Yes |

### Troubleshooting

| Error | Cause | Fix |
|---|---|---|
| `map has no entry for key "X"` | `{{ .Config.X }}` has no `default:` and was never set | Add a `default:` |
| `{{ }}` in comments causes parse error | Go template processes comments | Use plain text in comments |
| `yaml: unmarshal errors` | Invalid rendered YAML | Check quoting (`\| quote`) and indentation in `range` blocks |
| `fn::readFile` lines disappear | Security sanitization | Use config fields for file content |
| `atoi: parsing ""` | Config field used with `atoi` has no default | Add a numeric `default:` |
| No property autocomplete | Short-form type used | Use canonical form (`oci:Core/natGateway:NatGateway`) |
| `Metadata size > 32000 bytes` | Missing gzip before base64 | Use `cloudInit` or `gzipBase64` which handle gzip automatically |

### Reference Blueprints

| File | Description |
|---|---|
| `frontend/src/lib/blueprint-graph/templates/*.yaml` | 11 built-in templates |
| `docs/nomad-cluster-program.yaml` | v1 — short-form type aliases |
| `docs/nomad-cluster-v2-program.yaml` | v2 — canonical types, full IAM, configurable backup |

---

## OCI API Client

### `internal/oci/`

Standalone HTTP client for OCI REST APIs using HTTP Signature authentication (RSA-SHA256, signing string: `(request-target) date host`).

| File | Purpose |
|---|---|
| `client.go` | `Client` struct, `get()` helper, `VerifyCredentials()`, `ListShapes()`, `ListImages()` |
| `endpoints.go` | URL builder functions: `UserURL`, `ShapesURL`, `ImagesURL` |

`ListImages()` fetches both "Oracle Linux" and "Canonical Ubuntu" in a loop and combines the results. All image queries include `lifecycleState=AVAILABLE&sortBy=TIMECREATED&sortOrder=DESC`.

`VerifyCredentials()` calls `GET /users/{userOCID}` (Identity API) — accessible to any user for their own profile, unlike `GET /tenancies/{id}` which requires `inspect tenancy` IAM policy.

### `internal/oci/configparser/`

Parses OCI SDK config files (standard `~/.oci/config` INI format) into `Profile` structs. Used by the account import feature to read config files uploaded by the user or extracted from a ZIP archive.

---

## Adding a New Blueprint

### Built-in Go blueprint
1. Create `internal/blueprints/<name>.go` implementing the `Blueprint` interface
2. Add `func New<Name>Blueprint() *<Name>Blueprint` constructor
3. Register in `cmd/server/main.go` via `RegisterBuiltins(r)` — no other changes needed
4. The registry, API, and UI form update automatically

### User-defined YAML blueprint (via UI)
1. Navigate to the Blueprints page in the UI
2. Click "New Blueprint" and write or paste a Go-templated Pulumi YAML body
3. The blueprint is saved to `custom_blueprints` and registered immediately — no restart needed
4. Use `{{ .Config.key }}` for template-time substitution and `${resource.property}` for Pulumi cross-resource references

For fields backed by live OCI data, use `Type: "oci-shape"` or `Type: "oci-image"` — the UI will automatically fetch from the selected account's endpoint and render a searchable Combobox.
