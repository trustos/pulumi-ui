# Pulumi Programs

Programs implement the `Program` interface. There are two kinds:

- **Built-in Go programs**: Run inline via `auto.UpsertStackInlineSource`. The OCI resources are created directly by the Go OCI SDK. No subprocess, no TypeScript runtime, no `pulumi` CLI call.
- **User-defined YAML programs**: Stored in the `custom_programs` database table as Go-templated Pulumi YAML. Rendered at runtime by the template engine and executed via `auto.UpsertStackLocalSource`.

---

## Program Interface

### `internal/programs/registry.go`

```go
package programs

import (
    "github.com/pulumi/pulumi/sdk/v3/go/pulumi"
    "github.com/pulumi/pulumi/sdk/v3/go/auto"
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
```

`Type` values:

| Type | UI control |
|---|---|
| `text` | Plain text input |
| `number` | Number input |
| `textarea` | Multi-line textarea |
| `select` | Dropdown from `Options` |
| `oci-shape` | Combobox loaded from `/api/accounts/{id}/shapes`; "Always Free" badge on eligible shapes |
| `oci-image` | Combobox loaded from `/api/accounts/{id}/images`; auto-selects latest Ubuntu Minimal by default |

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
    // Called by engine.go with the validated config from the stack YAML.
    Run(config map[string]string) pulumi.RunFunc
}

// registry holds all known programs
var registry []Program

func Register(p Program) {
    registry = append(registry, p)
}

func Get(name string) (Program, bool) { ... }
func List() []ProgramMeta { ... }
func Deregister(name string) // removes a program by name (used when a custom program is updated or deleted)
```

Programs self-register in their `init()` function:

```go
func init() {
    programs.Register(&NomadClusterProgram{})
}
```

---

## test-vcn Program

### `internal/programs/test_vcn.go`

Creates a compartment and VCN — a safe smoke test for OCI credentials that creates minimal resources.

**Config fields:**

| Key | Type | Default | Description |
|---|---|---|---|
| `compartmentName` | `text` | `test-compartment` | OCI compartment name |
| `vcnCidr` | `text` | `10.0.0.0/16` | CIDR block for the test VCN |

**Outputs:** `compartmentId`, `vcnId`

---

## nomad-cluster Program

### `internal/programs/nomad_cluster.go`

Full Nomad + Consul cluster on OCI VM.Standard.A1.Flex (Always Free eligible). Seven sub-functions, one `Run` entry point.

**Config fields (16 total):**

| Key | Type | Default | Description | Group |
|---|---|---|---|---|
| `nodeCount` | `select` | `3` | Number of nodes (1–4; Always Free limit: 4 OCPUs / 24 GB total) | Infrastructure |
| `compartmentName` | `text` | `nomad-compartment` | OCI compartment name | Infrastructure |
| `compartmentDescription` | `text` | `Compartment for Nomad cluster` | | Infrastructure |
| `vcnCidr` | `text` | `10.0.0.0/16` | | Infrastructure |
| `publicSubnetCidr` | `text` | `10.0.1.0/24` | | Infrastructure |
| `privateSubnetCidr` | `text` | `10.0.2.0/24` | | Infrastructure |
| `sshSourceCidr` | `text` | `0.0.0.0/0` | Restrict to your IP for production security | Infrastructure |
| `shape` | `oci-shape` | `VM.Standard.A1.Flex` | OCI compute shape (fetched dynamically from account) | Infrastructure |
| `imageId` | `oci-image` | _(required, no default)_ | OCI image OCID (fetched dynamically from account) | Infrastructure |
| `bootVolSizeGb` | `number` | `50` | Boot volume size in GB | Compute & Storage |
| `glusterVolSizeGb` | `number` | `100` | GlusterFS block volume size in GB | Compute & Storage |
| `nomadVersion` | `text` | `1.10.3` | | Software Versions |
| `consulVersion` | `text` | `1.21.3` | | Software Versions |
| `adminGroupName` | `text` | _(empty)_ | IAM group name of the deploying user — needed to grant permission to create Dynamic Groups and Policies (not required when `skipDynamicGroup = true`) | IAM & Permissions |
| `identityDomain` | `text` | _(empty)_ | Leave empty for old-style IDCS tenancies. Set to e.g. `Default` for new Identity Domain tenancies | IAM & Permissions |
| `skipDynamicGroup` | `select` | `false` | Set to `true` to skip Dynamic Group creation if your OCI user lacks tenancy-level IAM permissions | IAM & Permissions |

**Outputs:** `traefikNlbIps`, `privateSubnetId`

### SSH key injection

The SSH public key for instance access is injected at **runtime** via the `OCI_USER_SSH_PUBLIC_KEY` environment variable set by `engine.buildEnvVars()`. It comes from one of two sources (resolved by `api.resolveCredentials`):

1. The linked SSH key (`ssh_key_id` on the stack) — takes priority
2. The OCI account's `ssh_public_key` field — fallback

The program reads it directly from the environment:
```go
sshPublicKey := os.Getenv("OCI_USER_SSH_PUBLIC_KEY")
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

The IAM setup is conditional on the `skipDynamicGroup` config value. When `skipDynamicGroup = false` (the default):

1. If `adminGroupName` is set, a prerequisite policy is created granting that group permission to manage dynamic groups and policies at the tenancy level. This is required before a DynamicGroup can be created when the deploying user is not a tenancy admin.
2. A DynamicGroup is created matching all instances in the new compartment.
3. A Policy is created granting the DynamicGroup instance-principals permissions (inspect instances, VNICs, compartments, manage buckets and objects, etc.).

The `identityDomain` value controls how the admin group is referenced in policy statements:
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

### cloud-init handling

```go
// internal/programs/cloudinit.go
//go:embed cloudinit.sh
var cloudInitScript string

func buildCloudInit(ocpus, memoryGb, nodeCount int, compartmentID, subnetID pulumi.IDOutput, nomadVersion, consulVersion string) pulumi.StringOutput {
    // applies @@PLACEHOLDER@@ substitutions and returns base64(script) as a Pulumi output
}
```

The `cloudinit.sh` file lives at `internal/programs/cloudinit.sh`. Using `//go:embed` avoids escaping issues entirely.

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
2. Add `func init() { Register(&MyProgram{}) }` — no other changes needed
3. The registry, API, and UI form update automatically

### User-defined YAML program (via UI)
1. Navigate to the Programs page in the UI
2. Click "New Program" and write or paste a Go-templated Pulumi YAML body
3. The program is saved to `custom_programs` and registered immediately — no restart needed
4. Use `{{ .Config.key }}` for template-time substitution and `${resource.property}` for Pulumi cross-resource references

For fields backed by live OCI data, use `Type: "oci-shape"` or `Type: "oci-image"` — the UI will automatically fetch from the selected account's endpoint and render a searchable Combobox.

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
- `cleanYAML` — the same body with the `meta:` section stripped (Pulumi ignores unknown top-level keys, but we strip it for cleanliness)

Type mapping:
| YAML type | ConfigField type |
|---|---|
| `String` | `text` |
| `Integer` / `Number` | `number` |
| `Boolean` | `select` |
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
- **Sprig** (`github.com/Masterminds/sprig/v3`) — same 100+ function library as Helm (`until`, `atoi`, `b64enc`, `quote`, `default`, `printf`, etc.)
- **Custom OCI helpers**:
  - `instanceOcpus(nodeIndex, nodeCount int) int` — OCPU allocation per node
  - `instanceMemoryGb(nodeIndex, nodeCount int) int` — memory allocation per node
  - `cloudInit(nodeIndex int, config map[string]string) string` — renders and base64-encodes the cloud-init script
  - `groupRef(groupName, domain, statement string) string` — formats IAM policy statements for old IDCS or new Identity Domain tenancies

`SanitizeYAML(yamlBody)` strips `fn::readFile` directives to prevent user programs from reading server filesystem files.

### Template syntax

```yaml
# Go template — resolved at render time (before Pulumi runs)
{{ range $i := until (atoi .Config.nodeCount) }}
instance-{{ $i }}:
  type: oci:core:Instance
  properties:
    ocpus: {{ instanceOcpus $i (atoi $.Config.nodeCount) }}
{{ end }}

# Pulumi reference — resolved at apply time (by the YAML runtime)
    subnetId: ${nomad-subnet.id}
```

### Credential injection for YAML programs

YAML programs cannot read environment variables directly (the Pulumi OCI provider reads its config from the Pulumi config system). The engine calls `stack.SetConfig()` after stack creation:

```go
stack.SetConfig(ctx, "oci:tenancyOcid",    auto.ConfigValue{Value: oci.TenancyOCID})
stack.SetConfig(ctx, "oci:userOcid",       auto.ConfigValue{Value: oci.UserOCID})
stack.SetConfig(ctx, "oci:fingerprint",    auto.ConfigValue{Value: oci.Fingerprint})
stack.SetConfig(ctx, "oci:privateKeyPath", auto.ConfigValue{Value: keyPath})
stack.SetConfig(ctx, "oci:region",         auto.ConfigValue{Value: oci.Region})
```

---

## IaC Best Practices Applied

- **Inline programs**: No subprocess, no external runtime. `auto.UpsertStackInlineSource` runs the Go function in-process.
- **Config injection**: All OCI env vars (`OCI_TENANCY_OCID`, etc.) are set on the Pulumi workspace by the engine via `buildEnvVars(creds Credentials)`. The API resolves credentials from the `oci_accounts` table (by `oci_account_id` on the stack) and from the `passphrases` table (by `passphrase_id` on the stack).
- **Secrets as `ConfigValue{Secret: true}`**: Pulumi passphrase injected via workspace `EnvVars`, never stored in plaintext state.
- **Stack outputs**: `ctx.Export()` produces typed outputs readable by the engine via `stack.Outputs(ctx)` for cross-stack config injection.
