# Pulumi Programs

Programs are Go functions implementing the `Program` interface. They run **inline** via `auto.UpsertStackInlineSource` — no subprocess, no TypeScript runtime, no `pulumi` CLI call. The OCI resources are created directly by the Go OCI SDK.

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

**Config fields:**

| Key | Type | Default | Description |
|---|---|---|---|
| `nodeCount` | `select` | `3` | Number of nodes (1–4; Always Free limit: 4 OCPUs / 24 GB total) |
| `compartmentName` | `text` | `nomad-compartment` | OCI compartment name |
| `compartmentDescription` | `text` | `Compartment for Nomad cluster` | |
| `vcnCidr` | `text` | `10.0.0.0/16` | |
| `publicSubnetCidr` | `text` | `10.0.1.0/24` | |
| `privateSubnetCidr` | `text` | `10.0.2.0/24` | |
| `sshSourceCidr` | `text` | `0.0.0.0/0` | Restrict to your IP for production security |
| `shape` | `oci-shape` | `VM.Standard.A1.Flex` | OCI compute shape (fetched dynamically from account) |
| `imageId` | `oci-image` | _(auto: latest Ubuntu Minimal)_ | Base OS image OCID (fetched dynamically from account) |
| `bootVolSizeGb` | `number` | `50` | Boot volume size in GB |
| `glusterVolSizeGb` | `number` | `100` | GlusterFS block volume size in GB |
| `nomadVersion` | `text` | `1.10.3` | |
| `consulVersion` | `text` | `1.21.3` | |

**Outputs:** `traefikNlbIps`, `privateSubnetId`

### Program structure

```
Run()
 ├─ 1. createCompartment()   — identity.NewCompartment
 ├─ 2. createIAM()           — dynamic groups, policies for instance principals
 ├─ 3. createNetwork()       — VCN, IGW, NAT GW, route tables, subnets
 ├─ 4. createNSGs()          — SSH, Nomad, Traefik, GlusterFS NSGs
 ├─ 5. createInstancePools() — instances with cloud-init (per node-count spec)
 ├─ 6. attachGlusterVolumes() — block volumes attached to each instance
 └─ 7. createNLB()           — Network Load Balancer for Traefik
```

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
- **Nomad NSG**: ports 4646, 4647, 4648 from VCN CIDR
- **Traefik NSG**: ports 80, 443 from `0.0.0.0/0`
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

func buildCloudInit(ocpus, memoryGb, nodeCount int, compartmentID, subnetID string) string {
    replacements := map[string]string{
        "NOMAD_CLIENT_CPU":       strconv.Itoa(ocpus * 3000),
        "NOMAD_CLIENT_MEMORY":    strconv.Itoa(memoryGb*1024 - 512),
        "NOMAD_BOOTSTRAP_EXPECT": strconv.Itoa(nodeCount),
        "COMPARTMENT_OCID":       compartmentID,
        "SUBNET_OCID":            subnetID,
    }
    result := cloudInitScript
    for k, v := range replacements {
        result = strings.ReplaceAll(result, "@@"+k+"@@", v)
    }
    return base64.StdEncoding.EncodeToString([]byte(result))
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

---

## Adding a New Program

1. Create `internal/programs/<name>.go` implementing the `Program` interface
2. Add `func init() { Register(&MyProgram{}) }` — no other changes needed
3. The registry, API, and UI form update automatically

For fields backed by live OCI data, use `Type: "oci-shape"` or `Type: "oci-image"` — the UI will automatically fetch from the selected account's endpoint and render a searchable Combobox.

---

## IaC Best Practices Applied

- **Inline programs**: No subprocess, no external runtime. `auto.UpsertStackInlineSource` runs the Go function in-process.
- **Config injection**: All OCI env vars (`OCI_TENANCY_OCID`, etc.) are set on the Pulumi workspace by the engine via `buildEnvVars(creds Credentials)`. The API resolves credentials from the `oci_accounts` table (by `oci_account_id` on the stack) and from the `passphrases` table (by `passphrase_id` on the stack).
- **Secrets as `ConfigValue{Secret: true}`**: Pulumi passphrase injected via workspace `EnvVars`, never stored in plaintext state.
- **Stack outputs**: `ctx.Export()` produces typed outputs readable by the engine via `stack.Outputs(ctx)` for cross-stack config injection.
