# Frontend

## Overview

The frontend is a pure Svelte 5 SPA built with Vite, embedded in the Go binary via `go:embed`. It requires authentication and supports multiple OCI accounts, named passphrases, and named SSH keys per user.

---

## File Structure

| File | Purpose |
|---|---|
| `frontend/src/App.svelte` | Root: auth guard, client-side routing |
| `frontend/src/lib/router.ts` | Custom `history.pushState` router |
| `frontend/src/lib/auth.ts` | Auth store (`currentUser`), `login`/`logout`/`register`/`fetchMe` |
| `frontend/src/lib/api.ts` | Typed API client, SSE streaming, accounts + passphrases + SSH keys CRUD |
| `frontend/src/lib/types.ts` | Shared types: `User`, `OciAccount`, `Passphrase`, `SshKey`, `StackSummary`, etc. |
| `frontend/src/pages/Dashboard.svelte` | Stack list, create button, prerequisite banners, loads passphrases for dialog |
| `frontend/src/pages/StackDetail.svelte` | Stack detail, deploy/destroy/refresh/preview, SSE log stream |
| `frontend/src/pages/Settings.svelte` | Named passphrases management, state backend info, health check |
| `frontend/src/lib/pages/Accounts.svelte` | OCI account management, credential verification, export/import |
| `frontend/src/lib/pages/SSHKeys.svelte` | SSH key management (list, create, generate, download, delete) |
| `frontend/src/pages/Programs.svelte` | Programs page: list built-in + custom programs, create/edit/delete custom YAML programs |
| `frontend/src/lib/pages/Login.svelte` | Login form |
| `frontend/src/lib/pages/Register.svelte` | First-run registration form |
| `frontend/src/lib/components/Nav.svelte` | Top nav with Accounts, SSH Keys, Programs, Settings links and Sign out |
| `frontend/src/lib/components/NewStackDialog.svelte` | Program + account + passphrase selector + config form (SSH key is a program config field in Step 3, not a wizard-level picker) |
| `frontend/src/lib/components/EditStackDialog.svelte` | Edit an existing stack's config fields (same form as New, pre-filled) |
| `frontend/src/lib/components/ConfigForm.svelte` | Dynamic form from `ProgramMeta.configFields` |
| `frontend/src/lib/components/OciImportDialog.svelte` | Multi-step OCI config import wizard (file upload or ZIP) |
| `frontend/src/lib/components/ApplicationSelector.svelte` | Application catalog selector for stack creation (ApplicationProvider programs) |
| `frontend/src/lib/components/StackCard.svelte` | Card shown on Dashboard for each stack (with Agent Connect indicator) |
| `frontend/src/lib/components/WebTerminal.svelte` | Interactive web terminal via xterm.js + WebSocket to agent /shell (Nebula mesh) |
| `frontend/src/lib/components/ui/` | shadcn-svelte component library (Button, Input, Select, Dialog, Tabs, Badge, Combobox, etc.) |

---

## Routes

| Path | Component | Auth required |
|---|---|---|
| `/login` | `Login.svelte` | No |
| `/register` | `Register.svelte` | No (only shown when no users exist) |
| `/` | `Dashboard.svelte` | Yes |
| `/accounts` | `Accounts.svelte` | Yes |
| `/ssh-keys` | `SSHKeys.svelte` | Yes |
| `/programs` | `Programs.svelte` | Yes |
| `/settings` | `Settings.svelte` | Yes |
| `/stacks/{name}` | `StackDetail.svelte` | Yes |
| `/programs/:name/edit` | `ProgramEditor.svelte` | Yes |
| `/programs/:name/fork` | `ProgramEditor.svelte` (fork mode) | Yes |
| `/programs/docs` | Program reference documentation | Yes |

---

## Auth Flow

```
App.svelte on mount
  ├─ fetchMe() → 200 → user logged in → render app
  ├─ fetchMe() → 401 → authStatus()
  │     ├─ hasUsers: false → navigate('/register')
  │     └─ hasUsers: true  → navigate('/login')
  └─ error → navigate('/login')
```

The `currentUser` writable store (in `auth.ts`) is the source of truth. `Nav.svelte` reacts to it — shows nav links and username only when a user is present.

---

## Component Responsibilities

### Page components (`src/pages/`)
- Fetch their own data on mount.
- Own the top-level state for their route.
- Pass data to child components as props.
- Do not contain reusable UI logic.

### Dialog / wizard components (`src/lib/components/`)
- Receive the data they need as props.
- Emit events or call callbacks on completion.
- Do not fetch data independently, except for picker components (see below).

### ConfigForm (`src/lib/components/ConfigForm.svelte`)

**ConfigForm is a pure layout renderer.** It:
- Receives `fields: ConfigField[]`, `values: Record<string, string>`, `accountId`
- Renders field groups and delegates to the correct input widget by `field.type`
- Calls `onSubmit(values)` when the form is submitted
- Validates format hints on blur (using `field.validationHint`)

ConfigForm does **not** fetch OCI resources. That responsibility belongs to picker components.

### Picker components
- `OciShapePicker.svelte` — receives `accountId`, fetches shapes via `listShapes()`, renders a combobox.
- `OciImagePicker.svelte` — receives `accountId`, fetches images via `listImages()`, auto-selects Ubuntu Minimal if available.
- `SshKeyPicker.svelte` — fetches SSH keys via `listSSHKeys()`, renders a combobox.

ConfigForm also renders inline pickers for `oci-compartment` (via `listCompartments()`) and `oci-ad` (via `listAvailabilityDomains()`) field types, using the same Combobox pattern with loading/error/fallback states.

---

## Stack Creation Wizard (NewStackDialog)

The wizard has **3 or 4 steps**, depending on the selected program. Programs implementing `ApplicationProvider` show an additional application selection step.

### Step 1 — Name & Program
Fields: stack name, program selection.
Purpose: define what you are creating. No security or infrastructure concerns here.

### Step 2 — Security & Access
Fields: OCI account (required), passphrase (required).
Purpose: define who can access the stack and how state is protected.
- If no passphrases exist, show the inline passphrase creation panel prominently (not buried at the bottom).
- Show a clear explanation of passphrase immutability: *"The passphrase cannot be changed after stack creation. It encrypts your Pulumi state — changing it would permanently break access to all deployed resources."*

### Step 3 — Configure [Program Name]
Renders ConfigForm for the selected program's config fields.
Fields are rendered in layer order: infrastructure → compute → bootstrap.
Derived fields are shown read-only with a computed-from tooltip.

### Step 3b — Applications (conditional)
Shown only for programs with an `applications` catalog. Renders `ApplicationSelector.svelte` which lists available applications from the program's catalog. Required applications are shown as always-on (cannot be deselected). Optional applications can be toggled on/off. Dependencies are enforced (e.g., selecting Traefik auto-selects Nomad if it depends on it). The selected applications are stored in the stack config and determine which apps the deployer installs post-infrastructure.

`PUT /api/stacks/{name}` body:
```json
{
  "program": "nomad-cluster",
  "description": "Production cluster",
  "ociAccountId": "uuid",
  "passphraseId": "uuid",
  "config": {
    "nodeCount": "3",
    "compartmentName": "nomad-prod"
  }
}
```

---

## Dashboard Prerequisites

Before showing the "New Stack" button as active, check **both**:
- `hasAccounts` — at least one OCI account exists
- `hasPassphrases` — at least one passphrase exists

If either is missing, show an actionable banner:
```
⚠ No OCI accounts configured. [Add Account →]
⚠ No passphrases configured. [Go to Settings →]
```
Do not just disable the button silently.

The `Dashboard.svelte` loads passphrases at startup and passes them to `NewStackDialog` via `bind:passphrases`. If the user creates a passphrase inline in the dialog, the dashboard list updates too.

---

## SSH Key Distinction

SSH public keys for VM access are provided as program config fields (type `ssh-public-key`) in the ConfigForm (Step 3 of the wizard). There is no longer a separate "VM Access Key" picker in Step 2 — the key is now part of the program's declared config.

| Term | What it is | Where it appears |
|---|---|---|
| **Program SSH Key** | A config field value passed into the Pulumi program template | ConfigForm (field type `ssh-public-key`, Step 3) |

If a program config has a `ssh-public-key` field it renders as a key picker (combobox of named SSH keys from Settings). Label it clearly in the program's config field description.

---

## Edit Stack Dialog

`EditStackDialog.svelte` allows editing an existing stack's configuration without losing the associated account, passphrase, and SSH key. It is opened from the `StackDetail` page. The form is pre-populated from `StackInfo` and submits the same `PUT /api/stacks/{name}` request.

### Immutable Fields

Some fields cannot be changed after a stack is created:
- **Passphrase** — immutable; changing it would break the encrypted Pulumi state.
- **Stack name** — is the primary key; cannot be renamed.
- **Program** — determines the config schema; cannot be changed post-creation.

In `EditStackDialog`:
- Do not hide immutable fields. Show them as read-only with an explanation.
- Example for passphrase: grey input, lock icon, tooltip or label: *"Cannot be changed — modifying would break encrypted state."*

---

## ConfigLayer Rendering

When a `ConfigField` has a `configLayer`, ConfigForm renders layer headings:

```
── Infrastructure ─────────────────────
  nodeCount, compartmentName, vcnCidr …

── Compute & Sizing ───────────────────
  shape, imageId, bootVolSizeGb …

── Bootstrap Configuration ────────────
  nomadVersion, consulVersion …

── Derived Values (read-only) ─────────
  NOMAD_CLIENT_CPU = 3000 (from nodeCount × 3000)
  NOMAD_CLIENT_MEMORY = 5632 (from nodeCount, 6 GB − 512 MB)
```

Fields without a `configLayer` fall back to their `group` / `groupLabel` rendering.

---

## Validation

ConfigForm runs `onBlur` validation using `field.validationHint`:

| Hint | Validator |
|---|---|
| `"cidr"` | Regex: `^\d{1,3}(\.\d{1,3}){3}/\d{1,2}$` |
| `"ocid"` | Must start with `ocid1.` |
| `"semver"` | Regex: `^\d+\.\d+\.\d+` |

Show error messages inline beneath the field. Block step navigation and form submission until all required fields with hints pass validation. Never suppress errors silently.

---

## Program Editor Validation

`ProgramEditor.svelte` runs client-side validation before save and live during editing:

### Backend validation (on save)
The backend `ValidateProgram` pipeline runs seven levels (see `docs/programs.md` — Validation section). Errors are shown in the validation panel below the mode bar.

### Visual mode validation (`collectVisualErrors`)
Before saving in visual mode, `collectVisualErrors()` checks:
- Every resource has a name and a type.
- Required properties (from the schema) are all present and non-empty.
- Loop variables start with `$`.
- **Undefined variable references**: any `${varName}` in a property value is checked against the graph's defined variables and resource names. References containing `:` (e.g., `${oci:tenancyOcid}`) are skipped as provider config refs. Undefined references are flagged as errors.
- **Missing "practically required" properties** (level 4 warnings): optional object properties whose nested fields include required sub-fields (e.g. `createVnicDetails` with `subnetId`) are flagged as non-blocking warnings. The warning index is built by `buildWarnByType()` from `$lib/program-graph/schema-utils.ts`.

Errors (level 5) block saving and are shown in a destructive alert. Warnings (level 4) are shown in a separate warning-variant alert and **do not block** saving.

### Agent Connect Toggle
The program editor header contains an **Agent Connect** toggle visible in both visual and YAML modes. When toggled:
- **Visual mode**: sets `graph.metadata.agentAccess` which the serializer emits as `meta.agentAccess: true`.
- **YAML mode**: patches the YAML text directly via `insertAgentAccess()` / `removeAgentAccess()` from `$lib/program-graph/agent-access.ts`. These are pure functions extracted for testability (see `agent-access.test.ts`).
- An informational banner below the mode bar lists all resources auto-injected at deploy time: user_data on compute instances, NSG rules (added to existing or created from VCN), NLB (added to existing or created from subnet), backend sets/listeners/backends.
- State syncs on visual↔YAML mode switches, template selection, fork, and load.

### Agent Access Networking Scaffold
When a Level 7 validation error detects that `agentAccess` is enabled but no networking context exists, an **"Add VCN + Subnet"** button appears inline in the validation error panel. Clicking it:
- Adds `agent-vcn` (VCN) and `agent-subnet` (Subnet) resources to the program.
- Wires `createVnicDetails.subnetId: ${agent-subnet.id}` on each compute instance.
- Adds `compartmentId` as a config field if not already present.
- Works in both visual and YAML modes. In visual mode, resources are prepended to the first section. In YAML mode, resources are inserted after `resources:` and the instance is patched inline.

The logic is extracted into pure functions in `$lib/program-graph/scaffold-networking.ts` (`scaffoldNetworkingGraph` for visual mode, `scaffoldNetworkingYaml` for YAML mode), covered by `scaffold-networking.test.ts` (16 tests).

Level 7 validation errors are **non-blocking** — the program can still be saved even if the warning is shown. Only Levels 1–6 block saving. This is enforced by `hasBlockingErrors()` in the backend API handler.

### Resource Rename Propagation
Renaming a resource in the visual editor automatically updates all references across the entire program graph:
- **Property values**: `${oldName.id}` → `${newName.id}`, `${oldName[0].name}` → `${newName[0].name}`
- **dependsOn arrays**: `oldName` → `newName`
- **Output values**: `${oldName.publicIp}` → `${newName.publicIp}`
- Propagation descends into **loops** and **conditionals** (including else branches) at any nesting depth.

The rename is triggered on blur of the resource name input in `ResourceCard.svelte`. The `onRename` callback propagates up through `SectionEditor` / `LoopBlock` / `ConditionalBlock` to `ProgramEditor.handleRenameResource()`, which calls `propagateRename()` from `$lib/program-graph/rename-resource.ts`.

In **YAML mode**, press **F2** (or right-click → "Rename Resource") with the cursor on a resource name. A prompt asks for the new name, and `propagateRenameYaml()` updates all `${oldName...}` references in the YAML text.

Logic is in `$lib/program-graph/rename-resource.ts`, with 23 Vitest unit tests in `rename-resource.test.ts`.

### Promote to Variable
`PropertyEditor` offers two promotion actions for empty required property values:

- **`→ config`** — adds a `ConfigField` and sets the value to `{{ .Config.<key> }}`. Auto-detects `oci-shape`, `oci-image`, `oci-compartment`, `oci-ad`, `ssh-public-key` types.
- **`→ variable`** — for keys with known OCI patterns (e.g. `availabilityDomain`), auto-scaffolds a `variables:` entry with the correct `fn::invoke` call and sets the property to the Pulumi interpolation (e.g. `${availabilityDomains[0].name}`). Uses `KNOWN_VARIABLE_TEMPLATES` in `ProgramEditor.svelte`. For unknown keys, sets value to `${key}`.

### Structured Object Property Editor
Object-type properties (e.g. `createVnicDetails`, `sourceDetails`, `shapeConfig`, `routeRules`) with sub-field definitions in the schema are rendered as a structured sub-field editor instead of a raw textarea. `ObjectPropertyEditor.svelte` provides:

- **Per-sub-field rows** with key labels, required markers (`*`), and tooltips from the schema.
- **Full reference picker support** — each sub-field value has the same `⊕` picker as regular properties, supporting config refs, variable refs, and resource output refs.
- **Chip rendering** — `{{ .Config.KEY }}` and `${resource.attr}` values render as colored chips (same as `PropertyEditor`).
- **Optional field buttons** — sub-fields not yet present show `+ fieldName` buttons to add them from the schema.
- **Array support** — for `type: "array"` properties with `items.properties` (e.g. `routeRules`), the editor renders a list of item editors with add/remove item controls.
- **Fallback** — if the compact value string cannot be parsed, the editor falls back to a raw textarea.

The compact value format (`{ key: "val", ref: "${subnet.id}" }`) is parsed/serialized by `$lib/program-graph/object-value.ts` using a state-machine tokenizer that respects nested `{}`, `[]`, quotes, and template expressions. Tests in `object-value.test.ts` (32 tests).

Schema sub-field definitions come from the backend's `PropertySchema.Properties` and `PropertySchema.Items` fields, populated either by resolving `$ref` from the live Pulumi OCI provider schema or from the hardcoded `fallbackSchema()` in `internal/oci/schema.go`.

---

## Settings Page

Three tabs:

**Passphrases tab** — the primary tab:
- Lists all named passphrases with their name, stack count, and created date
- Rename button (edits name in-place; passphrase value is immutable)
- Delete button (disabled when `stackCount > 0`; shows tooltip)
- Create form: name + passphrase value (reveal toggle)

**State Backend tab** — informational:
- Shows active backend (local `/data/state` volume)
- Placeholder for future OCI Object Storage support

**Status tab** — live health check:
- Encryption Key, Database, OCI Accounts, Pulumi State Backend, Passphrases
- Refresh button re-fetches from `/api/settings/health`

---

## OCI Accounts Page

Located at `/accounts`. Allows:
- Listing all OCI accounts with status badge (Unverified / Verified / Verification failed)
- Adding a new account (name, tenancy name, tenancy OCID, region, user OCID, fingerprint, private key PEM, SSH public key)
- Generating a fresh RSA-2048 key pair in-browser via `POST /api/accounts/generate-keypair` (private key + public key PEM + fingerprint + SSH public key are returned; the user copies the public key PEM to the OCI Console)
- Testing credentials ("Test credentials" button) — calls `POST /api/accounts/{id}/verify`; shows error detail on failure
- Deleting an account
- **Exporting all accounts** as a ZIP archive (`GET /api/accounts/export`) — produces a standard OCI `config` file plus per-account PEM files
- **Importing accounts** via `OciImportDialog` — supports:
  - **File upload**: user selects a config file + separate key PEM files
  - **ZIP upload**: user uploads the pulumi-ui export ZIP (or any compatible ZIP with a `config` file + `.pem` files)
  - Two-step flow: preview parsed profiles, confirm to create accounts

Credentials are write-only: the API never returns raw values.

---

## SSH Keys Page

Located at `/ssh-keys`. Allows:
- Listing all SSH keys with name, public key (truncated), whether a private key is stored, stack count, and creation date
- **Adding a key** — user provides a name and either:
  - Pastes an existing SSH public key (public-key-only, no private key stored)
  - Pastes both a public key and a private key PEM
  - Requests server-side generation (`generate: true`) — the server generates an Ed25519 key pair, stores the public key and the encrypted private key, and returns the private key once in `generatedPrivateKey` for immediate download
- **Downloading the private key** — `GET /api/ssh-keys/{id}/private-key` returns the decrypted PEM as a file download (only available if a private key was stored)
- **Deleting a key** — protected: refuses if any stacks reference it

---

## Programs Page

Located at `/programs`. Allows:
- Listing all programs — both built-in (read-only) and custom (editable)
- Built-in programs show a "Built-in" badge; custom programs show a "Custom" badge
- Programs with Agent Connect enabled show a globe icon (&#x1f310;) with a tooltip
- Each card shows the display name, internal name, description, and config field count
- **Creating a custom program (Visual)** — "New Program (Visual)" opens the visual editor with a **template gallery** overlay:
  - 11 templates across 7 categories (Networking, Compute, Web, Security, Data, High Availability, Cluster, Architecture)
  - Text search filters by name, description, tags, and category
  - Category pill filters for quick browsing
  - Templates with Agent Connect show a globe icon
  - "Start from scratch" option for a blank program
- **Creating a custom program (YAML)** — "New Program (YAML)" opens a YAML editor with a default stub
- **Editing a custom program** — opens the visual/YAML editor (`/programs/:name/edit`)
- **Deleting a custom program** — confirmation dialog; blocked if any stacks reference the program

New/updated programs are live-registered: the program is available for stack creation immediately after saving, without a server restart.

---

## Stack Detail Page

Located at `/stacks/{name}`. Shows:
- Stack info (status, last updated, running indicator)
- Stack outputs (after a successful `up`)
- Action buttons: Deploy (Up), Preview, Refresh, Destroy, Cancel, Unlock, Edit Config, Remove Stack
- Live SSE log viewer (auto-scrolls, color-coded output) pulled from `GET /stacks/{name}/logs`
- Warning banner if `info.passphraseId == null` — operations will fail until a passphrase is assigned

The `running` flag from `StackInfo` is used to show a spinner and disable action buttons while an operation is in progress.

### Preview operation

**Preview** (`POST /stacks/{name}/preview`) runs `pulumi preview` — it streams the diff of what would be created, updated, or deleted without actually executing changes. The output is rendered in the same SSE log viewer. Preview operations are persisted to the `operations` table so they appear in the log history.

### Unlock

**Unlock** (`POST /stacks/{name}/unlock`) calls `stack.CancelUpdate()` via the Pulumi Automation API to clear a stale Pulumi state lock. This is needed when a previous operation crashed mid-run and left the stack in a locked state. It does not roll back any changes — it only releases the lock so new operations can proceed.

---

## API Client Rules

All backend calls go through `src/lib/api.ts`. No raw `fetch` calls in components.

```typescript
// CORRECT
import { listStacks } from '$lib/api';
const stacks = await listStacks();

// WRONG
const resp = await fetch('/api/stacks');
```

Config values are always `Record<string, string>` — all values are strings, even for number fields. The backend parses them back to the correct types.

---

## SSE Streaming

Operations (up/destroy/refresh/preview) stream output via SSE:

```typescript
const stop = streamOperation(stackName, 'up', (event) => {
    // handle event.type: 'output' | 'error' | 'done'
}, () => { /* done */ });
```

```typescript
// frontend/src/lib/api.ts
export function streamOperation(name, op, onEvent, onDone): () => void {
  const controller = new AbortController();
  (async () => {
    const res = await fetch(`/api/stacks/${name}/${op}`, {
      method: 'POST', body: '{}', signal: controller.signal,
    });
    const reader = res.body!.getReader();
    let buffer = '';
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      for (const line of buffer.split('\n')) {
        if (line.startsWith('data: ')) {
          const event = JSON.parse(line.slice(6));
          if (event.type === 'done') { onDone(event.data); return; }
          onEvent(event);
        }
      }
    }
  })();
  return () => controller.abort();
}
```

If the user navigates away and returns while an operation is running, the SSE connection is lost. `StackDetail.svelte` detects this via `info.running === true` and enters polling mode (every 2 seconds) until the operation finishes.

Do not try to reconnect the SSE stream — poll instead.

---

## UI Component Library

The project uses **shadcn-svelte** with CLI-managed components:

```bash
# Install or update a component (run from frontend/)
npx shadcn-svelte@latest add <component-name>

# Overwrite an existing component to get the latest version
npx shadcn-svelte@latest add <component-name> --overwrite
```

Configuration is in `frontend/components.json`. Never hand-edit component files in `src/lib/components/ui/` — always use the CLI to install/update them.

### Dependencies

- **bits-ui v2** — headless primitives (Select, Dialog, Tabs, Combobox, Tooltip)
- **Tailwind CSS v4** — utility classes via `@theme inline` tokens
- **lucide-svelte** — icons (ChevronsUpDown, Check, X)
- **class-variance-authority (cva)** — variant management for Button

### Theme Tokens (app.css)

The project defines design tokens in `src/app.css` using `@theme inline`. Only token-based colors are available — **raw Tailwind color classes like `bg-amber-50` or `text-red-500` will not render** because they are not registered in the theme.

Available color tokens:

| Token | Light | Dark | Usage |
|---|---|---|---|
| `primary` / `primary-foreground` | Dark blue | Light blue | Primary actions, info banners |
| `destructive` / `destructive-foreground` | Red | Dark red | Errors, delete buttons |
| `warning` / `warning-foreground` | Amber | Amber | Warnings, degraded state |
| `muted` / `muted-foreground` | Gray | Dark gray | Secondary text, disabled |
| `accent` / `accent-foreground` | Light gray | Dark gray | Hover states |

Use these tokens in Tailwind classes: `bg-warning/10`, `text-warning`, `border-destructive/50`, `text-muted-foreground`, etc.

### Combobox

The `Combobox` component (`src/lib/components/ui/combobox/`) is used for OCI shape, image, compartment, and availability domain dropdowns in `ConfigForm`. It supports:
- Searchable filtering (label + sublabel)
- Async item loading with `$effect(() => { if (!open) inputValue = selectedLabel; })` to keep the input in sync after items arrive
- Optional `badge` field per item (used for "Always Free" shape tags)

---

## UI/UX Design Guidelines

These guidelines must be followed for all new frontend work to maintain visual and behavioral consistency.

### Tooltips

A `Tooltip.Provider` wraps the entire app in `App.svelte`. Use shadcn `Tooltip` components (`$lib/components/ui/tooltip`) to provide contextual help. Guidelines:

- **Action buttons** — every non-obvious action button should have a tooltip explaining what it does (e.g., "Sync Pulumi state with actual cloud resources" for Refresh).
- **Status badges** — tooltip on badges to explain the status meaning.
- **Disabled elements** — tooltip on disabled buttons to explain *why* they are disabled (e.g., "Cannot delete — remove all associated stacks first").
- **Credential/config labels** — tooltip on labels like "Passphrase", "SSH Key", "OCI Account" to explain their purpose and any constraints (e.g., immutability).
- **Health status items** — tooltip on each service name explaining what it represents.
- Keep tooltip text concise (one sentence, no period at the end).
- Use `cursor-default` class on non-interactive tooltip triggers to avoid misleading pointer changes.

```svelte
<Tooltip.Root>
  <Tooltip.Trigger>
    <Button>Action</Button>
  </Tooltip.Trigger>
  <Tooltip.Content>Explanation of what this action does</Tooltip.Content>
</Tooltip.Root>
```

### Status Badges

Use the shadcn `Badge` component with consistent variant mapping:

| Status | Badge variant |
|---|---|
| succeeded, verified | `default` |
| failed, error | `destructive` |
| running, cancelled, not deployed, unverified | `secondary` |

> **Note:** If you need custom badge colors beyond the theme tokens (e.g. green for "succeeded"), those specific raw Tailwind classes must be safelisted or the colors added to `@theme inline` in `app.css`.

### Confirmation Dialogs

**Never use `window.confirm()`.** All destructive or dangerous actions use shadcn `Dialog` components:

```svelte
<Dialog.Root bind:open={confirmOpen}>
  <Dialog.Content class="max-w-sm">
    <Dialog.Header>
      <Dialog.Title>Action title</Dialog.Title>
      <Dialog.Description>Explain consequences clearly.</Dialog.Description>
    </Dialog.Header>
    <Dialog.Footer>
      <Button variant="outline" onclick={() => { confirmOpen = false; }}>Cancel</Button>
      <Button variant="destructive" onclick={doAction}>Confirm</Button>
    </Dialog.Footer>
  </Dialog.Content>
</Dialog.Root>
```

Pattern: store `confirmOpen` boolean in `$state`, open it from the action handler, perform the action in a separate `doAction()` function.

### Alerts and Error Display

Use shadcn `Alert` + `AlertTitle` + `AlertDescription` for all banners:

| Variant | Usage |
|---|---|
| `destructive` | Validation errors, operation failures |
| `warning` | Degraded state, scaffold removal prompt, non-blocking notices |
| `info` | Agent Access ON banner, feature descriptions |
| `default` | General informational messages |

```svelte
<Alert variant="warning">
  <AlertTitle>Notice</AlertTitle>
  <AlertDescription>Explanation of the warning.</AlertDescription>
</Alert>
```

Rules:
- **Never** use raw `<div>` with hand-crafted background/border/text color classes for banners
- **Never** use raw Tailwind color classes (`bg-amber-50`, `text-red-500`) — use theme tokens instead
- Use `class="rounded-none border-x-0 border-t-0"` for full-width section banners (no rounded corners, only bottom border)
- Use `Button` components inside alerts for actions, not raw `<button>` elements

### Relative Times

Display times as relative ("3h ago", "just now") in compact contexts (headers, card summaries). Show full timestamps (`toLocaleString()`) in detail views.

### Page Layout Patterns

- **Page header**: `h1` title + `text-sm text-muted-foreground` description + action buttons on the right
- **StackDetail**: header + action bar + `Tabs` (Logs/Details/Outputs/Configuration)
- **List pages** (Accounts, SSH Keys): header + list items with inline actions
- **Card grids** (Dashboard, Programs): responsive grid with consistent card structure

---

## Type Definitions

Key types in `frontend/src/lib/types.ts`:

```typescript
interface Passphrase {
  id: string;
  name: string;
  stackCount: number;
  createdAt: number;  // Unix timestamp
}

interface SshKey {
  id: string;
  name: string;
  publicKey: string;
  hasPrivateKey: boolean;
  stackCount: number;
  createdAt: number;  // Unix timestamp
}

interface StackSummary {
  name: string;
  program: string;
  ociAccountId: string | null;
  passphraseId: string | null;
  sshKeyId: string | null;
  lastOperation: string | null;
  status: string;
  resourceCount: number;
}

interface StackInfo {
  name: string;
  program: string;
  ociAccountId: string | null;
  passphraseId: string | null;
  sshKeyId: string | null;
  config: Record<string, string>;
  applications?: Record<string, boolean>;  // selected apps (ApplicationProvider programs)
  appConfig?: Record<string, string>;      // app-specific config
  outputs: Record<string, unknown>;
  resources: number;
  lastUpdated: string | null;
  status: string;
  running: boolean;
  mesh?: MeshStatus;  // Nebula mesh connection state
}

interface ConfigField {
  key: string;
  label: string;
  type: 'text' | 'number' | 'textarea' | 'select' | 'oci-shape' | 'oci-image' | 'oci-compartment' | 'oci-ad' | 'ssh-public-key';
  required?: boolean;
  default?: string;
  description?: string;
  options?: string[];       // for 'select' type
  group?: string;           // stable group key, e.g. "iam"
  groupLabel?: string;      // display heading, e.g. "IAM & Permissions"
  configLayer?: string;     // 'infrastructure' | 'compute' | 'bootstrap' | 'derived'
  validationHint?: string;  // 'cidr' | 'ocid' | 'semver' | 'url'
}

type ApplicationTier = 'bootstrap' | 'workload';
type TargetMode = 'all' | 'first' | 'any';

interface ApplicationDef {
  key: string;
  name: string;
  description?: string;
  tier: ApplicationTier;
  target: TargetMode;
  required: boolean;
  defaultOn: boolean;
  dependsOn?: string[];
  configFields?: ConfigField[];
}

interface MeshStatus {
  connected: boolean;
  lighthouseAddr?: string;
  agentNebulaIp?: string;
  agentRealIp?: string;
  nebulaSubnet?: string;
  lastSeenAt?: number;
}

interface AgentHealth {
  status: string;
  hostname: string;
  os: string;
  arch: string;
  uptime?: string;
}

interface AgentService {
  name: string;
  active: string;
}

interface ProgramMeta {
  name: string;
  displayName: string;
  description: string;
  configFields: ConfigField[];
  isCustom: boolean;  // false for built-in Go programs, true for user-defined YAML programs
  applications?: ApplicationDef[];  // present when program implements ApplicationProvider
  agentAccess?: boolean;  // true when agent networking auto-injected (meta.agentAccess)
}

interface OciAccount {
  id: string;
  name: string;
  tenancyName: string;
  tenancyOcid: string;
  region: string;
  status: 'unverified' | 'verified' | 'error';
  verifiedAt: string | null;
  createdAt: string;
  stackCount: number;
}
```

---

## Build

```bash
cd frontend && npm install && npm run build
# Outputs to cmd/server/frontend/dist/ (picked up by go:embed)
```

`vite.config.ts` sets `outDir: '../cmd/server/frontend/dist'`. In dev mode, the Vite proxy forwards all `/api` traffic — both HTTP and WebSocket — to the running Go server (`target: http://localhost:8080, ws: true`). The `ws: true` flag is required for the agent shell terminal (`/api/stacks/{name}/agent/shell`) to work in dev mode.

### Development

```bash
# Both Go server and Vite HMR in one command (parallel):
make dev-watch

# Or separately:
make run            # terminal 1 — Go server on :8080
make watch-frontend # terminal 2 — Vite HMR on :5173
# Visit http://localhost:5173
```

---

## Svelte 5 Notes

- Uses runes (`$state`, `$derived`, `$effect`, `$props`, `$bindable`)
- `untrack()` used in `ConfigForm` to initialize `$state` from props without triggering reactive warnings
- bits-ui v2 components (Select, Dialog, Tabs, Combobox): use `bind:value` not `onValueChange` callbacks
- `Combobox.Input` requires `bind:value={inputValue}` + `$effect(() => { if (!open) inputValue = selectedLabel; })` to reactively show the selected label after async item load (bits-ui v2 does not have a `Combobox.Empty` export — use a plain `<div>`)
- `currentUser` in `auth.ts` is a Svelte store — components subscribe with `$currentUser`

---

## Stack Config Format

```yaml
apiVersion: pulumi.io/v1
kind: Stack

metadata:
  name: production
  program: nomad-cluster
  description: "Production Nomad cluster — eu-frankfurt-1"

config:
  nodeCount: "3"
  compartmentName: nomad-prod
  vcnCidr: 10.0.0.0/16
  shape: VM.Standard.A1.Flex
  imageId: ocid1.image.oc1.eu-frankfurt-1.aaaaa...
  bootVolSizeGb: "50"
  skipDynamicGroup: "false"
  adminGroupName: ""
  identityDomain: ""
```
