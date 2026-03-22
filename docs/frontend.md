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
| `frontend/src/lib/components/NewStackDialog.svelte` | Program + account + passphrase + SSH key selector + config form |
| `frontend/src/lib/components/EditStackDialog.svelte` | Edit an existing stack's config fields (same form as New, pre-filled) |
| `frontend/src/lib/components/ConfigForm.svelte` | Dynamic form from `ProgramMeta.configFields` |
| `frontend/src/lib/components/OciImportDialog.svelte` | Multi-step OCI config import wizard (file upload or ZIP) |
| `frontend/src/lib/components/StackCard.svelte` | Card shown on Dashboard for each stack |
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

ConfigForm renders these pickers when it encounters the corresponding `field.type`.

---

## Stack Creation Wizard (NewStackDialog)

The wizard has **3 steps**, each with a single semantic purpose:

### Step 1 — Name & Program
Fields: stack name, program selection.
Purpose: define what you are creating. No security or infrastructure concerns here.

### Step 2 — Security & Access
Fields: OCI account (required), passphrase (required), VM Access Key (optional).
Purpose: define who can access the stack and how state is protected.
- If no passphrases exist, show the inline passphrase creation panel prominently (not buried at the bottom).
- Show a clear explanation of passphrase immutability: *"The passphrase cannot be changed after stack creation. It encrypts your Pulumi state — changing it would permanently break access to all deployed resources."*
- SSH key is labelled **"VM Access Key"** with tooltip: *"Injected into OCI instance metadata for SSH access to VMs. Overrides the key stored in the OCI account."*

### Step 3 — Configure [Program Name]
Renders ConfigForm for the selected program's config fields.
Fields are rendered in layer order: infrastructure → compute → bootstrap.
Derived fields are shown read-only with a computed-from tooltip.

`PUT /api/stacks/{name}` body:
```json
{
  "program": "nomad-cluster",
  "description": "Production cluster",
  "ociAccountId": "uuid",
  "passphraseId": "uuid",
  "sshKeyId": "uuid",
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

Two separate SSH key mechanisms exist. Always distinguish them clearly in the UI:

| Term | What it is | Where it appears |
|---|---|---|
| **VM Access Key** | Key injected into OCI instance metadata; used for `ssh user@host` | Step 2 of wizard, EditStackDialog |
| **Program SSH Key** | A config field value passed into the Pulumi program template | ConfigForm (field type `ssh-public-key`) |

These are different fields. Do not conflate them. If a program config has a `ssh-public-key` field, label it **"Program SSH Key"** with a tooltip explaining it is a program config value, not the VM access key.

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
- Each card shows the display name, internal name, description, and config field count
- **Creating a custom program** — "New Program" opens a full-screen YAML editor with:
  - Internal name (immutable after creation) and display name
  - Description field
  - YAML textarea (monospace font) with drag-and-drop file import
  - A default template stub is pre-filled to get started
  - Import from file button for `.yaml` / `.yml` files
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

The project uses a copy-paste shadcn-svelte component setup (`components.json` + `svelte.config.js`) with:
- **bits-ui v2** — headless primitives (Select, Dialog, Tabs, Combobox)
- **Tailwind CSS v4** — utility classes
- **lucide-svelte** — icons (ChevronsUpDown, Check, X)
- **class-variance-authority (cva)** — variant management for Button

The `Combobox` component (`src/lib/components/ui/combobox/`) is used for the OCI shape and image dropdowns in `ConfigForm`. It supports:
- Searchable filtering (label + sublabel)
- Async item loading with `$effect(() => { if (!open) inputValue = selectedLabel; })` to keep the input in sync after items arrive
- Optional `badge` field per item (used for "Always Free" shape tags)

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
  outputs: Record<string, unknown>;
  resources: number;
  lastUpdated: string | null;
  status: string;
  running: boolean;
}

interface ConfigField {
  key: string;
  label: string;
  type: 'text' | 'number' | 'textarea' | 'select' | 'oci-shape' | 'oci-image' | 'ssh-public-key';
  required?: boolean;
  default?: string;
  description?: string;
  options?: string[];       // for 'select' type
  group?: string;           // stable group key, e.g. "iam"
  groupLabel?: string;      // display heading, e.g. "IAM & Permissions"
  configLayer?: string;     // 'infrastructure' | 'compute' | 'bootstrap' | 'derived'
  validationHint?: string;  // 'cidr' | 'ocid' | 'semver' | 'url'
}

interface ProgramMeta {
  name: string;
  displayName: string;
  description: string;
  configFields: ConfigField[];
  isCustom: boolean;  // false for built-in Go programs, true for user-defined YAML programs
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

`vite.config.ts` sets `outDir: '../cmd/server/frontend/dist'`. In dev mode, the Vite proxy (`/api → http://localhost:8080`) forwards API calls to the running Go server.

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
