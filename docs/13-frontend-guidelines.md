# Frontend Guidelines

This document specifies how we build the Svelte 5 frontend. It covers component
responsibilities, the user journey, data flow, and UX rules.

---

## 1. Component Responsibilities

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

ConfigForm does **not** fetch OCI resources. That responsibility belongs to picker
components.

### Picker components
- `OciShapePicker.svelte` — receives `accountId`, fetches shapes via `listShapes()`,
  renders a combobox.
- `OciImagePicker.svelte` — receives `accountId`, fetches images via `listImages()`,
  auto-selects Ubuntu Minimal if available.
- `SshKeyPicker.svelte` — fetches SSH keys via `listSSHKeys()`, renders a combobox.

ConfigForm renders these pickers when it encounters the corresponding `field.type`.

---

## 2. Stack Creation Wizard (NewStackDialog)

The wizard has **3 steps**, each with a single semantic purpose:

### Step 1 — Name & Program
Fields: stack name, program selection.
Purpose: define what you are creating.
No security or infrastructure concerns here.

### Step 2 — Security & Access
Fields: OCI account (required), passphrase (required), VM Access Key (optional).
Purpose: define who can access the stack and how state is protected.
- If no passphrases exist, show the inline passphrase creation panel prominently
  (not buried at the bottom).
- Show a clear explanation of passphrase immutability: *"The passphrase cannot be
  changed after stack creation. It encrypts your Pulumi state — changing it would
  permanently break access to all deployed resources."*
- SSH key is labelled **"VM Access Key"** with tooltip: *"Injected into OCI instance
  metadata for SSH access to VMs. Overrides the key stored in the OCI account."*

### Step 3 — Configure [Program Name]
Renders ConfigForm for the selected program's config fields.
Fields are rendered in layer order: infrastructure → compute → bootstrap.
Derived fields are shown read-only with a computed-from tooltip.

---

## 3. Dashboard Prerequisites

Before showing the "New Stack" button as active, check **both**:
- `hasAccounts` — at least one OCI account exists
- `hasPassphrases` — at least one passphrase exists

If either is missing, show an actionable banner:
```
⚠ No OCI accounts configured. [Add Account →]
⚠ No passphrases configured. [Go to Settings →]
```
Do not just disable the button silently.

---

## 4. SSH Key Distinction

Two separate SSH key mechanisms exist. Always distinguish them clearly in the UI:

| Term | What it is | Where it appears |
|---|---|---|
| **VM Access Key** | Key injected into OCI instance metadata; used for `ssh user@host` | Step 2 of wizard, EditStackDialog |
| **Program SSH Key** | A config field value passed into the Pulumi program template | ConfigForm (field type `ssh-public-key`) |

These are different fields. Do not conflate them. If a program config has a
`ssh-public-key` field, label it **"Program SSH Key"** with a tooltip explaining
it is a program config value, not the VM access key.

---

## 5. Immutable Fields

Some fields cannot be changed after a stack is created:
- **Passphrase** — immutable; changing it would break the encrypted Pulumi state.
- **Stack name** — is the primary key; cannot be renamed.
- **Program** — determines the config schema; cannot be changed post-creation.

In `EditStackDialog`:
- Do not hide immutable fields. Show them as read-only with an explanation.
- Example for passphrase: grey input, lock icon, tooltip or label:
  *"Cannot be changed — modifying would break encrypted state."*

---

## 6. API Client Rules

All backend calls go through `src/lib/api.ts`. No raw `fetch` calls in components.

```typescript
// CORRECT
import { listStacks } from '$lib/api';
const stacks = await listStacks();

// WRONG
const resp = await fetch('/api/stacks');
```

Config values are always `Record<string, string>` — all values are strings, even for
number fields. The backend parses them back to the correct types.

---

## 7. Real-Time Operations

Operations (up/destroy/refresh/preview) stream output via SSE:
```typescript
const stop = streamOperation(stackName, 'up', (event) => {
    // handle event.type: 'output' | 'error' | 'done'
}, () => { /* done */ });
```

If the user navigates away and returns while an operation is running, the SSE
connection is lost. `StackDetail.svelte` detects this via `info.running === true`
and enters polling mode (every 2 seconds) until the operation finishes.

Do not try to reconnect the SSE stream — poll instead.

---

## 8. ConfigLayer Rendering

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

## 9. Validation

ConfigForm runs `onBlur` validation using `field.validationHint`:

| Hint | Validator |
|---|---|
| `"cidr"` | Regex: `^\d{1,3}(\.\d{1,3}){3}/\d{1,2}$` |
| `"ocid"` | Must start with `ocid1.` |
| `"semver"` | Regex: `^\d+\.\d+\.\d+` |

Show error messages inline beneath the field. Block step navigation and form submission
until all required fields with hints pass validation. Never suppress errors silently.
