# Cloud-Init Redesign Plan

This document describes the current cloud-init implementation, identifies the problems
with it, and specifies a clean replacement design. It is self-contained: an agent with
no prior context can read this file and implement everything described.

---

## 1. Current implementation — how it works today

### 1a. Built-in Go programs (nomad_cluster.go)

The nomad cluster program uses cloud-init to install Nomad, Consul, GlusterFS, the OCI
CLI, and supporting tooling at instance boot time.

**Embedding (programs/cloudinit.go:12)**

```go
//go:embed cloudinit.sh
var cloudInitScript string
```

`cloudinit.sh` (~29 KB uncompressed) is embedded into the server binary at compile time.
It contains `@@PLACEHOLDER@@` strings for values that are only known at deploy time.

**Substitution at runtime (programs/cloudinit.go:17-43)**

`buildCloudInit()` is called inside a `pulumi.All(...).ApplyT(...)` block after the
compartment and subnet OCIDs are resolved. It does string-replacement of the
placeholders, then base64-encodes the result:

```go
func buildCloudInit(ocpus, memoryGb, nodeCount int,
    compartmentID pulumi.IDOutput, subnetID pulumi.IDOutput,
    nomadVersion, consulVersion string) pulumi.StringOutput {
    return pulumi.All(compartmentID, subnetID).ApplyT(func(args []interface{}) (string, error) {
        // ... string replacement + base64 ...
        return base64.StdEncoding.EncodeToString([]byte(result)), nil
    }).(pulumi.StringOutput)
}
```

Note: this version does **NOT** gzip. The base64-encoded ~29 KB script comfortably fits
within the OCI 32 KB metadata limit.

**Instance metadata (nomad_cluster.go)**

```go
Metadata: pulumi.StringMap{
    "ssh_authorized_keys": pulumi.String(sshPublicKey),
    "user_data":           cloudInit,  // base64-encoded script
},
```

### 1b. YAML programs — the `{{ cloudInit }}` template function

**Registration (programs/template.go:25)**

```go
fm["cloudInit"] = templateCloudInit
```

**Signature (programs/template.go:96)**

```go
func templateCloudInit(nodeIndex int, config map[string]string) string
```

This function:
1. Reads `nodeCount`, `nomadVersion`, `consulVersion` from the config map
2. Computes per-node CPU/RAM from `templateInstanceOcpus()` / `templateInstanceMemoryGb()`
3. Does the same `@@PLACEHOLDER@@` substitution as `buildCloudInit()` but leaves
   `COMPARTMENT_OCID` and `SUBNET_OCID` **empty** (these are Pulumi runtime outputs
   not available at template render time)
4. Returns a base64-encoded string (not gzipped)

Usage in a YAML program:
```yaml
metadata: { user_data: "{{ cloudInit 0 $.Config }}" }
```

**Critical limitation:** Template render happens before Pulumi executes, so runtime
values (subnet OCID, compartment OCID) cannot be injected via the template function.
For Go programs these are filled in via `pulumi.All().ApplyT()`. For YAML programs,
they remain empty.

### 1c. What config fields control cloud-init behaviour

For built-in programs, these `ConfigField` entries in `nomad_cluster.go` flow into
cloud-init:

| Config key | What it controls |
|---|---|
| `nomadVersion` | Nomad version installed |
| `consulVersion` | Consul version installed |
| `nodeCount` | `NOMAD_BOOTSTRAP_EXPECT` and per-node CPU/RAM distribution |
| `sshPublicKey` | Placed in `metadata.ssh_authorized_keys` |

For YAML programs: no convention exists. Users must manually declare matching config
fields and reference them in the script substitution map — which requires writing Go
code (forking the binary).

### 1d. Frontend visibility today

**Zero.** Cloud-init is completely invisible in the frontend:

- `ConfigForm.svelte` renders fields of type `textarea` generically, but no cloud-init
  field of that type is ever declared for any program
- `ConfigFieldPanel.svelte` (visual editor) lets users create fields with types
  `string | integer | boolean | number` — there is no way to declare a `textarea` field
  or a dedicated cloud-init script field
- The visual program editor has no concept of cloud-init at all
- Users writing YAML programs must know that `{{ cloudInit 0 $.Config }}` exists and
  what config keys it expects — there is no in-app documentation

---

## 2. Problems with the current design

### SOLID violations

**Single Responsibility (SRP)**

`nomad_cluster.go` mixes two concerns:
- OCI infrastructure orchestration (compartment, VCN, instances, NLB, etc.)
- Cloud-init script rendering (calling `buildCloudInit()` with outputs)

`template.go` also mixes: it is the function map builder AND hosts the cloud-init
rendering logic (`templateCloudInit`). Cloud-init rendering should live in its own
file with its own narrow interface.

**Open/Closed (OCP)**

The cloud-init script is hardcoded to install Nomad + Consul + GlusterFS. Adding
support for a different workload (e.g. Docker + Kubernetes, or a plain apt-get) requires
modifying `cloudinit.sh` and rebuilding the server binary. Users cannot extend it.

**Dependency Inversion (DIP)**

`nomad_cluster.go` directly depends on the concrete package-level `cloudInitScript`
variable. It should depend on an interface so the cloud-init source can be swapped
(e.g. from embedded file to DB-stored script to user-provided script).

### Functional gaps

1. **No custom cloud-init for YAML programs.** A user writing a custom YAML program
   that deploys regular Ubuntu instances has no way to provide a boot script through
   the UI. They'd need to hardcode a base64 blob.

2. **The `{{ cloudInit }}` function is tightly coupled to Nomad.** Its substitution map
   contains `NOMAD_CLIENT_CPU`, `CONSUL_VERSION`, etc. Any non-Nomad program calling
   it gets Nomad installed — undesirable.

3. **Runtime OCIDs missing from YAML programs.** The subnet and compartment OCIDs,
   needed by the Nomad auto-join mechanism, are left blank in YAML programs. This
   only works for Consul's mDNS gossip — it silently breaks for any other use case
   that needs those IDs in the script.

4. **No validation of user-provided scripts.** The backend has no Level 6 validator
   for cloud-init scripts. A script with a bad shebang or missing `set -e` will only
   fail at instance boot time — hours after the deploy completes.

---

## 3. Proposed design

The goal is a clean, separated cloud-init system that:

1. Works for both YAML programs and Go programs without duplication
2. Is visible and configurable in the visual editor frontend
3. Respects the project's coding principles (thin handlers, service layer, interfaces)
4. Does not require binary rebuilds to change the cloud-init script of a YAML program

### 3a. Backend — new `internal/cloudinit/` package

Create `internal/cloudinit/` with the following files.

#### `internal/cloudinit/renderer.go`

Defines the interface and the two implementations.

```go
package cloudinit

import "compress/gzip"
import "encoding/base64"
import "bytes"

// Renderer turns a cloud-init script string into the base64 value suitable
// for OCI instance metadata's "user_data" key.
type Renderer interface {
    Render(script string, vars map[string]string) (base64gzip string, err error)
}

// DefaultRenderer implements Renderer. It substitutes @@KEY@@ placeholders,
// gzip-compresses the result, and base64-encodes it.
type DefaultRenderer struct{}

func (DefaultRenderer) Render(script string, vars map[string]string) (string, error) {
    for k, v := range vars {
        script = strings.ReplaceAll(script, "@@"+k+"@@", v)
    }
    // Fail fast on unsubstituted placeholders
    if idx := strings.Index(script, "@@"); idx != -1 {
        end := strings.Index(script[idx:], "@@")
        if end != -1 {
            return "", fmt.Errorf("unsubstituted placeholder: %s", script[idx:idx+end+2])
        }
    }
    var buf bytes.Buffer
    gz := gzip.NewWriter(&buf)
    if _, err := gz.Write([]byte(script)); err != nil {
        return "", err
    }
    gz.Close()
    return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}
```

Why gzip? The OCI instance metadata limit is 32 KB total. An uncompressed custom
script can easily exceed this. Gzip + base64 consistently stays well under the limit.
OCI cloud-init detects gzip via magic bytes and decompresses transparently.

#### `internal/cloudinit/nomad.go`

Keeps the Nomad-specific script logic separate:

```go
package cloudinit

import _ "embed"

//go:embed nomad.sh
var nomadScript string

// NomadVars builds the substitution map for the embedded Nomad script.
// compartmentID and subnetID are Pulumi runtime values; pass empty strings
// when calling from a YAML template context.
func NomadVars(ocpus, memGb, nodeCount int, compartmentID, subnetID,
    nomadVersion, consulVersion string) map[string]string {
    return map[string]string{
        "NOMAD_CLIENT_CPU":       strconv.Itoa(ocpus * 3000),
        "NOMAD_CLIENT_MEMORY":    strconv.Itoa(memGb*1024 - 512),
        "NOMAD_BOOTSTRAP_EXPECT": strconv.Itoa(nodeCount),
        "COMPARTMENT_OCID":       compartmentID,
        "SUBNET_OCID":            subnetID,
        "NOMAD_VERSION":          nomadVersion,
        "CONSUL_VERSION":         consulVersion,
    }
}

// NomadScript returns the raw (unrendered) Nomad cloud-init script.
func NomadScript() string { return nomadScript }
```

Rename `programs/cloudinit.sh` → `cloudinit/nomad.sh` and update the embed path.

#### `internal/cloudinit/user.go`

Handles user-provided scripts (the new capability for YAML programs):

```go
package cloudinit

// UserScript validates and prepares a user-provided cloud-init script.
// script is the raw shell/cloud-config content as the user typed it.
// Returns an error if the script is missing a shebang or is empty.
func ValidateUserScript(script string) error {
    trimmed := strings.TrimSpace(script)
    if trimmed == "" {
        return fmt.Errorf("cloud-init script is empty")
    }
    if !strings.HasPrefix(trimmed, "#!") && !strings.HasPrefix(trimmed, "#cloud-config") {
        return fmt.Errorf("cloud-init script must start with a shebang (#!/bin/bash) or #cloud-config")
    }
    return nil
}
```

### 3b. Backend — update existing callers

**Update `programs/cloudinit.go`**

Replace the entire file content with a thin adapter that delegates to the new package:

```go
package programs

import (
    "strconv"
    "github.com/you/pulumi-ui/internal/cloudinit"
    "github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

var ciRenderer = cloudinit.DefaultRenderer{}

// buildCloudInit is called from nomad_cluster.go with Pulumi output values.
func buildCloudInit(ocpus, memGb, nodeCount int,
    compartmentID pulumi.IDOutput, subnetID pulumi.IDOutput,
    nomadVersion, consulVersion string) pulumi.StringOutput {

    return pulumi.All(compartmentID, subnetID).ApplyT(func(args []interface{}) (string, error) {
        vars := cloudinit.NomadVars(
            ocpus, memGb, nodeCount,
            string(args[0].(pulumi.ID)),
            string(args[1].(pulumi.ID)),
            nomadVersion, consulVersion,
        )
        return ciRenderer.Render(cloudinit.NomadScript(), vars)
    }).(pulumi.StringOutput)
}
```

**Update `programs/template.go`**

Replace `templateCloudInit` with a version that delegates to the new package:

```go
// templateCloudInit is the {{ cloudInit nodeIndex $.Config }} template function.
// It renders the Nomad cloud-init script for a single node. Runtime OCIDs
// (COMPARTMENT_OCID, SUBNET_OCID) are left blank — Consul mDNS handles join.
func templateCloudInit(nodeIndex int, config map[string]string) string {
    nodeCount, _ := strconv.Atoi(config["nodeCount"])
    ocpus := templateInstanceOcpus(nodeIndex, nodeCount)
    memGb := templateInstanceMemoryGb(nodeIndex, nodeCount)
    vars := cloudinit.NomadVars(ocpus, memGb, nodeCount, "", "",
        config["nomadVersion"], config["consulVersion"])
    result, err := ciRenderer.Render(cloudinit.NomadScript(), vars)
    if err != nil {
        return "" // validation step would have caught this earlier
    }
    return result
}
```

**Add a new template function `{{ userInit $.Config.cloudInitScript }}`**

Register in `buildFuncMap()`:

```go
fm["userInit"] = templateUserInit
```

Implementation:

```go
// templateUserInit encodes a user-provided cloud-init script from a config field.
// The field value is the raw script text. Returns base64-gzip or empty string if blank.
func templateUserInit(script string) string {
    if strings.TrimSpace(script) == "" {
        return ""
    }
    result, err := ciRenderer.Render(script, nil)
    if err != nil {
        return ""
    }
    return result
}
```

Usage in a YAML program that has a `cloudInitScript` config field:

```yaml
config:
  cloudInitScript:
    type: string
    default: "#!/bin/bash\nset -e\napt-get update"

resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      ...
      metadata:
        ssh_authorized_keys: "{{ .Config.sshPublicKey }}"
        user_data: "{{ userInit .Config.cloudInitScript }}"
```

### 3c. Backend — validation extension

Extend the Level 5 validator in `internal/programs/validate.go` to detect `userInit`
calls and validate the referenced config field's default value:

This is optional for MVP. The minimum is that `templateUserInit` returns `""` for empty
scripts and `ValidateUserScript` is called in the API handler when a program is saved.

Add to `POST /api/programs/validate` handler in `internal/api/programs.go`:

After the existing 5-level `ValidateProgram()` call, scan the YAML body for any
`{{ userInit .Config.X }}` pattern and validate the default value of field `X` using
`cloudinit.ValidateUserScript()`. Append any resulting error to the response.

### 3d. Frontend — cloud-init in ConfigFieldPanel

**New field type in program-graph types**

In `frontend/src/lib/types/program-graph.ts`, extend `ConfigFieldDef`:

```typescript
export interface ConfigFieldDef {
  key: string;
  type: 'string' | 'integer' | 'boolean' | 'number' | 'cloudinit';  // ← add 'cloudinit'
  default?: string;
  description?: string;
}
```

The `'cloudinit'` type is a special string-type field that the frontend renders as a
code editor instead of a plain text input.

**Update ConfigFieldPanel.svelte**

Add `'cloudinit'` to the type selector dropdown:

```svelte
{#each ['string', 'integer', 'boolean', 'number', 'cloudinit'] as t}
  <Select.Item value={t}>{t === 'cloudinit' ? 'Cloud-Init Script' : t}</Select.Item>
{/each}
```

When editing a `cloudinit`-type field, show a `<textarea>` instead of a plain `<Input>`
for the default value, with a monospace font and a help note:

```svelte
{#if draft.type === 'cloudinit'}
  <div class="space-y-1">
    <p class="text-xs text-muted-foreground">
      Script will be gzip+base64 encoded and passed as OCI instance <code>user_data</code>.
      Must start with <code>#!/bin/bash</code> or <code>#cloud-config</code>.
    </p>
    <textarea
      bind:value={draft.default}
      class="w-full h-32 text-xs font-mono border rounded px-2 py-1 resize-y"
      placeholder="#!/bin/bash&#10;set -euo pipefail&#10;apt-get update -y"
    ></textarea>
  </div>
{:else}
  <Input
    placeholder="default value (optional)"
    value={draft.default ?? ''}
    oninput={(e) => draft = { ...draft, default: (e.currentTarget as HTMLInputElement).value }}
    class="text-sm h-7"
  />
{/if}
```

**Update serializer.ts**

The `cloudinit` type must serialize as `string` in the YAML config section (Pulumi YAML
does not know about `cloudinit` — it's our UI convention only). Also the `default:`
value must be a multi-line string escaped correctly:

In `serializer.ts`, update the config field emission:

```typescript
for (const f of graph.configFields) {
  lines.push(`  ${f.key}:`);
  // Serialize 'cloudinit' as 'string' in YAML — it is our UI-only convention
  lines.push(`    type: ${f.type === 'cloudinit' ? 'string' : f.type}`);
  if (f.default !== undefined && f.default !== '') {
    if (f.type === 'cloudinit') {
      // Use YAML literal block scalar for multi-line scripts
      lines.push(`    default: |`);
      for (const scriptLine of f.default.split('\n')) {
        lines.push(`      ${scriptLine}`);
      }
    } else {
      lines.push(`    default: ${JSON.stringify(f.default)}`);
    }
  }
  if (f.description) lines.push(`    # ${f.description}`);
}
```

**Update parser.ts**

When parsing a config field, detect that it is a `cloudinit` type by convention:
a `string`-type config field whose key is `cloudInitScript` (or matches
`/[Cc]loud[Ii]nit|[Uu]ser[Dd]ata/`) is typed as `'cloudinit'` in the visual model:

```typescript
// In parseConfigFields(), after determining type:
let resolvedType = ... as ConfigFieldDef['type'];
if (resolvedType === 'string' &&
    /cloudInit|CloudInit|userData|UserData/.test(key)) {
  resolvedType = 'cloudinit';
}
```

**Update serializer.ts for resources**

Add a convenience function that auto-suggests adding a `user_data` property to instance
resources when a `cloudinit`-type config field is declared:

This is optional. The minimum is that if `cloudInitScript` is declared, the Property
Editor shows an autocomplete suggestion `{{ userInit .Config.cloudInitScript }}` when
editing the `metadata.user_data` property of an instance resource.

### 3e. Frontend — cloud-init preview in ConfigFieldPanel

When a `cloudinit`-type field is displayed in the list, show a collapsed preview of
the script with a character count:

```svelte
{#if field.type === 'cloudinit'}
  <p class="text-xs text-muted-foreground font-mono truncate">
    cloud-init · {(field.default ?? '').split('\n').length} lines
  </p>
{/if}
```

---

## 4. Migration — existing Nomad program

The existing `nomad_cluster.go` built-in program does not need to change its external
interface. Internal changes:

1. Remove `programs/cloudinit.go` (replaced by `cloudinit/` package)
2. Update `nomad_cluster.go` import to `internal/cloudinit`
3. Call `cloudinit.NomadVars()` and `ciRenderer.Render()` instead of `buildCloudInit()`

The `{{ cloudInit }}` template function continues to work identically for any existing
YAML programs already using it.

---

## 5. Implementation order

```
Step 1  Create internal/cloudinit/ package
        - renderer.go  (DefaultRenderer interface + implementation)
        - nomad.go     (NomadVars, NomadScript, embed nomad.sh)
        - user.go      (ValidateUserScript)
        Move programs/cloudinit.sh → cloudinit/nomad.sh
        Update embed path

Step 2  Update programs/cloudinit.go (thin adapter)
        Update programs/template.go  (templateCloudInit delegates to package)
        Add templateUserInit to template.go + register in buildFuncMap()

Step 3  Add ValidateUserScript check to programs.go validate handler

Step 4  Add 'cloudinit' to ConfigFieldDef type union (program-graph.ts)
        Update ConfigFieldPanel.svelte (textarea for cloudinit default, type option)
        Update serializer.ts (literal block scalar for cloudinit, type as string)
        Update parser.ts (convention-based cloudinit detection)

Step 5  Test end-to-end:
        - Create a YAML program in visual editor with a cloudInitScript field
        - Enter a bash script as the default value
        - Save, inspect YAML → should contain YAML literal block scalar
        - Validate → should pass
        - Deploy → instance should boot with the custom script
```

---

## 6. What this design does NOT solve

### Runtime OCI resource IDs in YAML program cloud-init scripts

The fundamental limitation that `COMPARTMENT_OCID` and `SUBNET_OCID` are blank in YAML
programs remains. This is a Pulumi YAML architecture constraint: template render happens
before Pulumi executes, so Pulumi output values cannot be injected at render time.

For use cases that need runtime OCIDs in the cloud-init script (e.g. OCI CLI commands
that reference the compartment), users must use a built-in Go program. This limitation
should be documented in `docs/09-templated-yaml-programs.md` with an explicit note:

> **Limitation:** `{{ cloudInit }}` and `{{ userInit }}` run at template render time,
> before Pulumi provisions any resources. They cannot reference `${resource.id}` outputs
> or other runtime values. If your boot script needs a compartment or subnet OCID,
> you must use a built-in Go program where `pulumi.All(...).ApplyT(...)` is available.

### Per-node cloud-init variation

The `{{ cloudInit nodeIndex $.Config }}` function supports per-node CPU/RAM variation
(for Always Free A1 allocation). The new `{{ userInit }}` function does not; all nodes
in a loop receive the same script. This is intentional — user scripts can test the
node index themselves via `$HOSTNAME` or OCI instance metadata if they need to vary
per-node behaviour.

### Script versioning or multi-part cloud-init

MIME multipart cloud-init (combining a shell script with a cloud-config YAML) is not
supported. Only single-part scripts (starting with `#!/bin/bash` or `#cloud-config`)
are accepted. This covers the vast majority of real-world use cases.

---

## 7. File change summary

| File | Action |
|---|---|
| `internal/cloudinit/renderer.go` | **NEW** — Renderer interface + DefaultRenderer |
| `internal/cloudinit/nomad.go` | **NEW** — NomadVars(), NomadScript() |
| `internal/cloudinit/nomad.sh` | **MOVED** from `internal/programs/cloudinit.sh` |
| `internal/cloudinit/user.go` | **NEW** — ValidateUserScript() |
| `internal/programs/cloudinit.go` | **REPLACED** — thin adapter to cloudinit package |
| `internal/programs/template.go` | **MODIFIED** — templateCloudInit delegates; add templateUserInit |
| `internal/api/programs.go` | **MODIFIED** — call ValidateUserScript in validate handler |
| `frontend/src/lib/types/program-graph.ts` | **MODIFIED** — add `'cloudinit'` to ConfigFieldDef.type |
| `frontend/src/lib/program-graph/serializer.ts` | **MODIFIED** — literal block scalar for cloudinit |
| `frontend/src/lib/program-graph/parser.ts` | **MODIFIED** — convention-based cloudinit type detection |
| `frontend/src/lib/components/ConfigFieldPanel.svelte` | **MODIFIED** — textarea for cloudinit type, new type option |
