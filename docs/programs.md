# Programs

A **program** is a reusable infrastructure blueprint. A **stack** is a named instance of a program with specific config values, linked OCI credentials, and Pulumi backend state. One program (e.g. `nomad-cluster`) can be instantiated as many stacks (`prod`, `staging`, `dev`), each with different values. This mirrors Pulumi's own `Pulumi.yaml` / `Pulumi.<stack>.yaml` distinction.

---

## Program Interface

### `internal/programs/registry.go`

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
| `ssh-public-key` | Picker loaded from `/api/ssh-keys`; allows selecting a named SSH key pair from the SSH Keys store |

```go
// ProgramMeta is the safe, serializable view of a Program (sent to the UI)
type ProgramMeta struct {
    Name         string        `json:"name"`
    DisplayName  string        `json:"displayName"`
    Description  string        `json:"description"`
    ConfigFields []ConfigField `json:"configFields"`
    IsCustom     bool          `json:"isCustom"` // true for user-defined YAML programs
}

// Program is the internal interface all Pulumi programs implement
type Program interface {
    Name() string
    DisplayName() string
    Description() string
    ConfigFields() []ConfigField
    // Run returns a PulumiFn for the given config map.
    Run(config map[string]string) pulumi.RunFunc
}

var registry []Program

func Register(p Program)
func Get(name string) (Program, bool)
func List() []ProgramMeta
func Deregister(name string) // removes a program by name (used when a custom program is updated or deleted)
```

Built-in programs register explicitly from `main.go` via `RegisterBuiltins(r)`. There are no `init()` self-registrations.

---

## Config Groups

Config fields carry `Group` (stable machine key) and `GroupLabel` (human-readable heading). `ConfigForm.svelte` groups fields by `Group` and renders each group as a labeled section. Fields without a group render flat. This is purely a presentation concern — storage and validation are unaffected.

Example groups for `nomad-cluster`:

| Group key | Group label | Fields |
|---|---|---|
| `iam` | IAM & Permissions | `skipDynamicGroup`, `adminGroupName`, `identityDomain` |
| `infrastructure` | Infrastructure | `nodeCount`, `compartmentName`, `vcnCidr`, `publicSubnetCidr`, `privateSubnetCidr`, `sshSourceCidr`, `shape`, `imageId` |
| `compute` | Compute & Storage | `bootVolSizeGb`, `glusterVolSizeGb`, `sshPublicKey` |
| `software` | Software Versions | `nomadVersion`, `consulVersion` |

---

## Built-in Programs

### test-vcn — `internal/programs/test_vcn.go`

Creates a compartment and VCN — a safe smoke test for OCI credentials that creates minimal resources.

**Config fields:**

| Key | Type | Default | Description |
|---|---|---|---|
| `compartmentName` | `text` | `test-compartment` | OCI compartment name |
| `vcnCidr` | `text` | `10.0.0.0/16` | CIDR block for the test VCN |

**Outputs:** `compartmentId`, `vcnId`

---

### nomad-cluster — `internal/programs/nomad_cluster.go`

Full Nomad + Consul cluster on OCI VM.Standard.A1.Flex (Always Free eligible). Seven sub-functions, one `Run` entry point.

**Config fields (17 total):**

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
| `ocpusPerNode` | `number` | _(empty)_ | Override OCPUs per node (homogeneous pools) | Compute & Storage |
| `memoryGbPerNode` | `number` | _(empty)_ | Override memory per node | Compute & Storage |
| `bootVolSizeGb` | `number` | `50` | Boot volume size in GB | Compute & Storage |
| `glusterVolSizeGb` | `number` | `100` | GlusterFS block volume size in GB | Compute & Storage |
| `sshPublicKey` | `ssh-public-key` | _(empty)_ | SSH public key injected into instance metadata | Compute & Storage |
| `nomadVersion` | `text` | `1.10.3` | | Software Versions |
| `consulVersion` | `text` | `1.21.3` | | Software Versions |
| `skipDynamicGroup` | `select` | `false` | Skip Dynamic Group creation if OCI user lacks tenancy-level IAM permissions | IAM & Permissions |
| `adminGroupName` | `text` | _(empty)_ | IAM group name of the deploying user — needed to grant permission to create Dynamic Groups | IAM & Permissions |
| `identityDomain` | `text` | _(empty)_ | Leave empty for old-style IDCS tenancies; set to e.g. `Default` for new Identity Domain tenancies | IAM & Permissions |

**Outputs:** `traefikNlbIps`, `privateSubnetId`

### SSH key injection

The SSH public key is passed to the program via the `cfg` map under `OCI_USER_SSH_PUBLIC_KEY`, injected by `engine.buildEnvVars()`. Sources (resolved by `api.resolveCredentials`):

1. The linked SSH key (`ssh_key_id` on the stack) — takes priority
2. The OCI account's `ssh_public_key` field — fallback

The program reads it from the config map:
```go
sshPublicKey := cfgOr(cfg, "OCI_USER_SSH_PUBLIC_KEY", "")
```

This value is passed as `metadata.ssh_authorized_keys` in the instance's cloud-init `LaunchDetails`.

### Program structure

```
Run()
 ├─ 1. createCompartment()   — identity.NewCompartment
 ├─ 2. createIAM()           — dynamic groups, policies for instance principals
 │      (skipped if skipDynamicGroup = true)
 ├─ 3. createNetwork()       — VCN, IGW, NAT GW, route tables, subnets
 ├─ 4. createNSGs()          — SSH, Nomad, Traefik, GlusterFS NSGs
 ├─ 5. createInstancePools() — instances with cloud-init (per node-count spec)
 ├─ 6. attachGlusterVolumes() — block volumes attached to each instance
 └─ 7. createNLB()           — Network Load Balancer for Traefik
```

### IAM sub-function: `createIAM`

When `skipDynamicGroup = false` (the default):
1. If `adminGroupName` is set, a prerequisite policy is created granting that group permission to manage dynamic groups and policies at the tenancy level.
2. A DynamicGroup is created matching all instances in the new compartment.
3. A Policy is created granting the DynamicGroup instance-principals permissions.

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
    glusterNsgID pulumi.IDOutput
}
```

Creates 4 NSGs with security rules:
- **SSH NSG**: port 22 from `sshSourceCidr`
- **Nomad NSG**: ports 4646, 4647, 4648 from public subnet CIDR
- **Traefik NSG**: ports 80, 443 from public subnet CIDR
- **GlusterFS NSG**: ports 24007, 24008, 49152–49251 from VCN CIDR

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
// internal/programs/cloudinit.go
//go:embed cloudinit.sh
var cloudInitScript string

func buildCloudInit(ocpus, memoryGb, nodeCount int, nomadVersion, consulVersion string) string {
    // applies @@PLACEHOLDER@@ substitutions, gzip-compresses, and returns base64(gzip(script))
}
```

The `cloudinit.sh` file lives at `internal/programs/cloudinit.sh`. Using `//go:embed` avoids escaping issues entirely.

The result is gzip-compressed before base64 encoding. OCI instance metadata has a 32 KB total limit; the uncompressed script is ~29 KB (~39 KB base64) which would exceed it. Gzipped it becomes ~8.5 KB (~11 KB base64). `cloud-init` detects gzip via magic bytes and decompresses transparently.

---

## YAML Programs

User-defined programs are stored as Go-templated Pulumi YAML in the `custom_programs` database table.

### `internal/programs/yaml_program.go`

```go
// YAMLProgram implements Program. Run() returns nil — the engine detects this
// via type assertion to YAMLProgramProvider and uses UpsertStackLocalSource.
type YAMLProgram struct {
    name        string
    displayName string
    description string
    yamlBody    string
    fields      []ConfigField  // parsed from the YAML config: section
}

// YAMLProgramProvider is checked via type assertion by the engine.
type YAMLProgramProvider interface {
    YAMLBody() string
}
```

`NewYAMLProgram(name, displayName, description, yamlBody)` parses the config fields from the YAML body at construction time. `RegisterYAML(...)` is the convenience function called at startup (for rows loaded from the DB) and at runtime (when a user creates/updates a program via the API).

### `internal/programs/yaml_config.go`

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

The optional `meta:` top-level section (stripped before execution) allows declaring field groups and explicit `ui_type` overrides:
```yaml
meta:
  groups:
    - key: network
      label: "Network"
      fields: [vcnCidr, compartmentName]
  fields:
    imageId:
      ui_type: oci-image
```

### `internal/programs/template.go`

`RenderTemplate(templateBody, config)` renders a Go-templated YAML body using:
- **Sprig** (`github.com/Masterminds/sprig/v3`) — same 100+ function library as Helm
- **Custom OCI helpers**: `instanceOcpus`, `instanceMemoryGb`, `cloudInit`, `groupRef`

`SanitizeYAML(yamlBody)` strips `fn::readFile` directives to prevent user programs from reading server filesystem files.

### Credential injection for YAML programs

YAML programs cannot read environment variables directly (the Pulumi OCI provider reads its config from the Pulumi config system). The engine calls `stack.SetConfig()` after stack creation:

```go
stack.SetConfig(ctx, "oci:tenancyOcid", auto.ConfigValue{Value: oci.TenancyOCID})
stack.SetConfig(ctx, "oci:userOcid",    auto.ConfigValue{Value: oci.UserOCID})
stack.SetConfig(ctx, "oci:fingerprint", auto.ConfigValue{Value: oci.Fingerprint})
stack.SetConfig(ctx, "oci:privateKey",  auto.ConfigValue{Value: oci.PrivateKey, Secret: true})
stack.SetConfig(ctx, "oci:region",      auto.ConfigValue{Value: oci.Region})
```

The private key is always passed as inline PEM content (`oci:privateKey`, `Secret: true`), never as a file path. A temp file path would be deleted after `Up` and cause 401 errors on subsequent `Refresh` operations.

See `docs/yaml-programs.md` for the full YAML program authoring reference.

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

## Adding a New Program

### Built-in Go program
1. Create `internal/programs/<name>.go` implementing the `Program` interface
2. Add `func New<Name>Program() *<Name>Program` constructor
3. Register in `cmd/server/main.go` via `RegisterBuiltins(r)` — no other changes needed
4. The registry, API, and UI form update automatically

### User-defined YAML program (via UI)
1. Navigate to the Programs page in the UI
2. Click "New Program" and write or paste a Go-templated Pulumi YAML body
3. The program is saved to `custom_programs` and registered immediately — no restart needed
4. Use `{{ .Config.key }}` for template-time substitution and `${resource.property}` for Pulumi cross-resource references

For fields backed by live OCI data, use `Type: "oci-shape"` or `Type: "oci-image"` — the UI will automatically fetch from the selected account's endpoint and render a searchable Combobox.
