# Program Manifests & Config Organization

This document describes the config organization work done for this project. **Phase 1 (config groups) is implemented.** The TOML manifest format originally proposed in Phases 2–3 was superseded by the Go-templated Pulumi YAML approach, which is documented in `docs/09-yaml-programs.md`.

---

## The Problem: Config Mixing

The `nomad-cluster` program currently has 16 config fields presented as a single undifferentiated list. Those fields actually address four distinct concerns:

| Concern | Fields |
|---|---|
| **Stack-level** (already separate as DB fields — not in config) | OCI account, passphrase, SSH key |
| **IAM & authorization** | `skipDynamicGroup`, `adminGroupName`, `identityDomain` |
| **Infrastructure topology** | `nodeCount`, `compartmentName`, `compartmentDescription`, `vcnCidr`, `publicSubnetCidr`, `privateSubnetCidr`, `sshSourceCidr`, `shape`, `imageId` |
| **Compute & storage sizing** | `bootVolSizeGb`, `glusterVolSizeGb` |
| **Software versions** | `nomadVersion`, `consulVersion` |

Mixing these in a single scrolling form creates cognitive overhead: the user must mentally distinguish "I'm filling in IAM configuration" from "I'm choosing infrastructure shape" from "I'm pinning software versions." Groups make the intent of each field immediately clear.

A secondary problem is that there is no portable description of a program's config schema. Currently the schema is hardcoded in Go source. This makes it impossible for operators to inspect or share a program's configuration requirements without reading source code.

---

## Entity Model: Program vs Stack

Before diving into manifests, it is worth being precise about the entity model, because "is the program the stack?" is a question that comes up.

### Pulumi's own model (for reference)

| Pulumi concept | Description |
|---|---|
| `Pulumi.yaml` | Project file — defines the program name, runtime, and plugin requirements |
| `Pulumi.<stack>.yaml` | Stack config file — config values specific to one instance |
| `pulumi stack` | A named instance of a project with its own state and config |

### Our model

| Our concept | Maps to | Description |
|---|---|---|
| **Program** | Pulumi project | A reusable blueprint: Go code implementing `Program` + a config schema (`ConfigFields`) |
| **Stack** | Pulumi stack | A named instance of a program with specific config values, linked OCI credentials, and Pulumi backend state |
| **Program Manifest** | (new) `Pulumi.yaml`-equivalent | A TOML file describing a program's metadata, config schema, and default values — portable, importable, no Go code |

The key clarification: **a program is NOT a stack**. A program defines _what_ can be deployed and _what parameters_ it accepts. A stack is a specific deployment of that program with concrete parameter values. One program (e.g. `nomad-cluster`) can be instantiated as many stacks (`prod`, `staging`, `dev`), each with different config values.

This mirrors Pulumi's own model exactly. The Go implementation is our equivalent of `Pulumi.yaml` + the program code. Each stack's config map stored in SQLite is our equivalent of `Pulumi.<stack>.yaml`.

---

## Config Groups ✅ Implemented

### Goal

The UI should render the config form in labeled sections rather than a flat list. Each field should carry metadata indicating which group it belongs to, so the frontend can split the form automatically.

### Change to `ConfigField`

Two fields were added to `ConfigField` in `internal/programs/registry.go`:

```go
type ConfigField struct {
    Key         string   `json:"key"`
    Label       string   `json:"label"`
    Type        string   `json:"type"`
    Required    bool     `json:"required"`
    Default     string   `json:"default,omitempty"`
    Description string   `json:"description,omitempty"`
    Options     []string `json:"options,omitempty"`
    Group       string   `json:"group,omitempty"`       // NEW: group key, e.g. "iam"
    GroupLabel  string   `json:"groupLabel,omitempty"`  // NEW: display label, e.g. "IAM & Permissions"
}
```

`Group` is a stable machine identifier used for ordering and programmatic access. `GroupLabel` is the human-readable section heading shown in the UI. Both are optional for backward compatibility — fields without a group are rendered ungrouped (or under a default "General" section).

### Proposed groups for `nomad-cluster`

| Group key | Group label | Fields |
|---|---|---|
| `iam` | IAM & Permissions | `skipDynamicGroup`, `adminGroupName`, `identityDomain` |
| `infrastructure` | Infrastructure | `nodeCount`, `compartmentName`, `compartmentDescription`, `vcnCidr`, `publicSubnetCidr`, `privateSubnetCidr`, `sshSourceCidr`, `shape`, `imageId` |
| `compute` | Compute & Storage | `bootVolSizeGb`, `glusterVolSizeGb` |
| `software` | Software Versions | `nomadVersion`, `consulVersion` |

### UI rendering

`ConfigForm.svelte` groups fields by `Group`, renders each group as a labeled section with the `GroupLabel` as a heading, and falls back to flat rendering for fields without a group. The form structure and field types remain unchanged — groups are purely a presentation concern.

---

## TOML Manifest Format ⛔ Superseded

**Note:** This approach was superseded by Go-templated Pulumi YAML programs. See `docs/09-yaml-programs.md` for the implemented solution.

A **Program Manifest** is a TOML file that captures everything the UI needs to render the config form for a program. It does NOT contain Go code — it's a description of inputs and metadata.

### Purpose

- **Portability**: operators can inspect a program's requirements without reading source code
- **Shareability**: manifests can be checked into version control, posted in documentation, or distributed as a gist
- **Import**: a manifest can be loaded into the UI to register a custom program entry pointing at a built-in implementation
- **Export**: the UI can generate a manifest from any registered program, capturing its current config schema

### Full example: `nomad-cluster`

```toml
[program]
name        = "nomad-cluster"
version     = "1.0.0"
display_name = "Nomad Cluster"
description  = "Full Nomad + Consul cluster on OCI VM.Standard.A1.Flex (Always Free eligible)"
runtime     = "nomad-cluster"   # references the built-in Go implementation name

[[groups]]
key         = "iam"
label       = "IAM & Permissions"
description = "OCI IAM setup required for instance principals. Only needed once per tenancy."

  [[groups.fields]]
  key         = "skipDynamicGroup"
  label       = "Skip Dynamic Group"
  type        = "select"
  options     = ["false", "true"]
  default     = "false"
  required    = false
  description = "Set to true to skip Dynamic Group creation if your OCI user lacks tenancy-level IAM permissions"

  [[groups.fields]]
  key         = "adminGroupName"
  label       = "Admin IAM Group Name"
  type        = "text"
  required    = false
  description = "IAM group of the deploying user — needed to grant permission to create Dynamic Groups and Policies (not required when skipDynamicGroup = true)"

  [[groups.fields]]
  key         = "identityDomain"
  label       = "Identity Domain Name"
  type        = "text"
  required    = false
  default     = ""
  description = "Leave empty for old-style IDCS tenancies. Set to 'Default' for new Identity Domain tenancies"

[[groups]]
key   = "infrastructure"
label = "Infrastructure"

  [[groups.fields]]
  key         = "nodeCount"
  label       = "Node Count"
  type        = "select"
  options     = ["1", "2", "3", "4"]
  default     = "3"
  required    = false
  description = "Number of nodes (Always Free limit: 4 OCPUs / 24 GB total)"

  [[groups.fields]]
  key      = "compartmentName"
  label    = "Compartment Name"
  type     = "text"
  default  = "nomad-compartment"
  required = false

  [[groups.fields]]
  key      = "compartmentDescription"
  label    = "Compartment Description"
  type     = "text"
  default  = "Compartment for Nomad cluster"
  required = false

  [[groups.fields]]
  key      = "vcnCidr"
  label    = "VCN CIDR"
  type     = "text"
  default  = "10.0.0.0/16"
  required = false

  [[groups.fields]]
  key      = "publicSubnetCidr"
  label    = "Public Subnet CIDR"
  type     = "text"
  default  = "10.0.1.0/24"
  required = false

  [[groups.fields]]
  key      = "privateSubnetCidr"
  label    = "Private Subnet CIDR"
  type     = "text"
  default  = "10.0.2.0/24"
  required = false

  [[groups.fields]]
  key         = "sshSourceCidr"
  label       = "SSH Source CIDR"
  type        = "text"
  default     = "0.0.0.0/0"
  required    = false
  description = "Restrict to your IP for production security"

  [[groups.fields]]
  key      = "shape"
  label    = "Instance Shape"
  type     = "oci-shape"
  default  = "VM.Standard.A1.Flex"
  required = false

  [[groups.fields]]
  key         = "imageId"
  label       = "OCI Image"
  type        = "oci-image"
  required    = true
  description = "Oracle Linux image for your region"

[[groups]]
key   = "compute"
label = "Compute & Storage"

  [[groups.fields]]
  key      = "bootVolSizeGb"
  label    = "Boot Volume (GB)"
  type     = "number"
  default  = "50"
  required = false

  [[groups.fields]]
  key      = "glusterVolSizeGb"
  label    = "GlusterFS Volume (GB)"
  type     = "number"
  default  = "100"
  required = false

[[groups]]
key   = "software"
label = "Software Versions"

  [[groups.fields]]
  key      = "nomadVersion"
  label    = "Nomad Version"
  type     = "text"
  default  = "1.10.3"
  required = false

  [[groups.fields]]
  key      = "consulVersion"
  label    = "Consul Version"
  type     = "text"
  default  = "1.21.3"
  required = false
```

### Schema notes

- `program.runtime` references the built-in Go implementation by its `Program.Name()` value. For built-in programs this is the same as `program.name`. For custom programs (Phase 3), `runtime` would reference an existing built-in that the custom program reuses.
- Field `type` values match the existing `ConfigField.Type` enum: `text`, `number`, `textarea`, `select`, `oci-shape`, `oci-image`.
- `options` is only valid when `type = "select"`.
- `oci-shape` and `oci-image` fields are dynamically populated from the selected OCI account — the manifest does not need to enumerate shapes or images.
- The manifest does NOT include stack-level config (OCI account, passphrase, SSH key) — those are metadata on the stack, not program config fields.

---

## Import/Export API Design ⛔ Superseded

### Export — `GET /api/programs/{name}/manifest`

Returns the program's config schema as a TOML manifest file.

```
GET /api/programs/nomad-cluster/manifest
→ 200 OK
Content-Type: application/toml
Content-Disposition: attachment; filename="nomad-cluster.toml"
```

The handler calls `programs.Get(name)`, iterates `ConfigFields()`, groups them, and serializes to TOML. The `runtime` field is set to `program.Name()`.

### Import — `POST /api/programs/import`

Accepts a TOML manifest file (multipart form or JSON with `content` field). Parses the manifest and either:

1. Registers a new program entry in a `custom_programs` table (Phase 3), or
2. Returns a validation error if the `runtime` value does not reference a known built-in.

For Phase 2 (export/import of built-in program metadata), the import simply validates and returns a confirmation — it does not need to persist anything because the built-in program is already registered.

```
POST /api/programs/import
Content-Type: multipart/form-data
Body: manifest file

→ 200 OK
{ "name": "nomad-cluster", "displayName": "Nomad Cluster", "isNew": false }
```

### TOML parsing

Use `github.com/BurntSushi/toml` (or `github.com/pelletier/go-toml/v2`). Both are pure Go, no CGO. Parse into an intermediate struct, then convert to `ProgramMeta` / `[]ConfigField`.

---

## Program Editor UI Concept ⛔ Superseded

**Note:** This approach was superseded by Go-templated Pulumi YAML programs. See `docs/09-yaml-programs.md` for the implemented solution.

Phase 3 introduces a `/programs` page where users can:

1. **View** all registered programs (built-in + custom) with their metadata
2. **Export** any program's manifest as a TOML file (calls `GET /api/programs/{name}/manifest`)
3. **Import** a manifest TOML file to register a custom program
4. **Edit** a custom program's manifest inline (simple textarea editor with TOML syntax highlighting)
5. **Delete** a custom program (only allowed if no stacks reference it)

The page renders a list of program cards. Built-in programs show a read-only view. Custom programs show Edit and Delete buttons.

The manifest editor does not expose the Go implementation — it only edits the config schema (groups, fields, defaults, labels). The `runtime` value determines which built-in Go implementation is used to execute the program.

---

## Implementation Phases

### Phase 1: Config groups in the UI (no new API) ✅ Implemented

**Goal**: render the `nomad-cluster` config form in labeled sections.

**Changes:**
1. Two fields were added to `ConfigField` in `internal/programs/registry.go`: `Group string` and `GroupLabel string`
2. `NomadClusterProgram.ConfigFields()` was updated to set `Group` and `GroupLabel` on each field
3. `ConfigForm.svelte` was updated to detect grouped fields and render section headings
4. The TypeScript `ConfigField` type gained optional `group` and `groupLabel` fields

**Scope:** Zero API changes. Zero DB changes. Frontend-only rendering change + Go struct extension. Fully backward-compatible (fields without groups continue to render flat).

**Outcome:** The `nomad-cluster` config form shows four labeled sections — IAM & Permissions, Infrastructure, Compute & Storage, Software Versions — with fields organized within each section.

### Phase 2: TOML export/import of built-in programs ⛔ Superseded

**Note:** This approach was superseded by Go-templated Pulumi YAML programs. See `docs/09-yaml-programs.md` for the implemented solution.

**Goal**: allow operators to export and import a program's config schema as a TOML file.

**Changes:**
1. Add TOML serialization logic (`internal/programs/manifest.go`) — converts `ProgramMeta` + `[]ConfigField` to the TOML manifest structure
2. Add `GET /api/programs/{name}/manifest` endpoint
3. Add `POST /api/programs/import` endpoint (validation only — built-ins cannot be overridden)
4. Add "Export manifest" button on the program detail view in the UI
5. Add "Import manifest" flow (file upload → preview → confirm) on the `/programs` page

**Scope:** Two new API routes. No DB changes. The import endpoint in Phase 2 is read-only (validates + confirms; does not persist).

**Outcome:** Operators can download a `.toml` file describing any built-in program's config schema and share it with teammates or check it into documentation repos.

### Phase 3: Custom programs and the program editor ⛔ Superseded

**Note:** This approach was superseded by Go-templated Pulumi YAML programs. See `docs/09-yaml-programs.md` for the implemented solution.

**Goal**: allow operators to register custom programs that reuse a built-in Go implementation with a modified config schema (different defaults, additional documentation, hidden fields, renamed labels).

**Changes:**
1. Add `custom_programs` table to SQLite:
   ```sql
   CREATE TABLE IF NOT EXISTS custom_programs (
       name         TEXT    NOT NULL PRIMARY KEY,
       runtime      TEXT    NOT NULL,   -- built-in program name
       display_name TEXT    NOT NULL,
       description  TEXT    NOT NULL,
       manifest     TEXT    NOT NULL,   -- raw TOML
       created_at   INTEGER NOT NULL DEFAULT (unixepoch())
   );
   ```
2. Update `programs.Register` or add a parallel `RegisterCustom` path that loads from the DB at startup
3. Update `GET /api/programs` to include custom programs
4. Update `POST /api/programs/import` to actually persist to `custom_programs`
5. Add `GET /api/programs/{name}`, `PUT /api/programs/{name}`, `DELETE /api/programs/{name}` for custom program CRUD
6. Build the `/programs` UI page with the editor

**Scope:** One new DB table, several new API routes, one new frontend page. Built-in programs remain read-only; custom programs are fully editable.

**Outcome:** An operator can take the `nomad-cluster` manifest, modify it (e.g. set `skipDynamicGroup` to `true` by default for a tenancy that lacks IAM permissions), save it as `nomad-cluster-nodynamicgroup`, and create stacks from that custom program.

---

## What Stays the Same and Why

**The "stack = instance of a program" model is correct.** No rename needed. Stacks are Pulumi stacks — they hold state, they have names, they are the unit of `pulumi up`. Programs are the blueprints. This maps cleanly to Pulumi's own `Pulumi.yaml` / `Pulumi.<stack>.yaml` distinction and should not be conflated.

**The Go inline program model stays.** The Pulumi Automation API's `UpsertStackInlineSource` keeps working unchanged. The manifest is metadata that describes a program's inputs — it does not replace or wrap the Go `PulumiFn`. When a stack operation runs, the engine still calls `programs.Get(programName).Run(cfg)` exactly as today.

**The config YAML stored per stack stays.** `StackConfig` YAML in SQLite is the stack's parameter binding. The manifest only defines the schema (allowed keys, types, defaults, groups). The stack's actual values continue to be stored as `config: { key: value }` pairs.

**Groups are purely presentational in Phase 1.** Adding `Group` to `ConfigField` changes how the UI renders the form, not how configs are validated or stored. A stack's config map is still `map[string]string` — no grouping structure in storage.

**The `oci-shape` and `oci-image` types remain dynamic.** The manifest declares these field types but does not enumerate values. The UI fetches available shapes and images from the selected OCI account at form-render time, exactly as it does today.

---

## Implementation Status

| Phase | Feature | Status |
|---|---|---|
| Phase 1 | Config groups in UI (`Group` + `GroupLabel` on `ConfigField`) | ✅ Implemented |
| Phase 2 | TOML manifest format for import/export | ⛔ Superseded by YAML programs |
| Phase 3 | Custom program registry in DB | ✅ Implemented (via `custom_programs` table + YAML programs) |
