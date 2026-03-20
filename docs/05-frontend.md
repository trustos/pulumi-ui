# Frontend

## Overview

The frontend is a pure Svelte 5 SPA built with Vite, embedded in the Go binary via `go:embed`. It requires authentication and supports multiple OCI accounts and named passphrases per user.

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
```

---

## File Structure

| File | Purpose |
|---|---|
| `frontend/src/App.svelte` | Root: auth guard, client-side routing |
| `frontend/src/lib/router.ts` | Custom `history.pushState` router |
| `frontend/src/lib/auth.ts` | Auth store (`currentUser`), `login`/`logout`/`register`/`fetchMe` |
| `frontend/src/lib/api.ts` | Typed API client, SSE streaming, accounts + passphrases CRUD |
| `frontend/src/lib/types.ts` | Shared types: `User`, `OciAccount`, `Passphrase`, `StackSummary`, etc. |
| `frontend/src/pages/Dashboard.svelte` | Stack list, create button, loads passphrases for dialog |
| `frontend/src/pages/StackDetail.svelte` | Stack detail, deploy/destroy/refresh, SSE log stream |
| `frontend/src/pages/Settings.svelte` | Named passphrases management, state backend info, health check |
| `frontend/src/lib/pages/Accounts.svelte` | OCI account management + credential verification |
| `frontend/src/lib/pages/Login.svelte` | Login form |
| `frontend/src/lib/pages/Register.svelte` | First-run registration form |
| `frontend/src/lib/components/Nav.svelte` | Top nav with Accounts, Settings links and Sign out |
| `frontend/src/lib/components/NewStackDialog.svelte` | Program + account + passphrase selector + config form |
| `frontend/src/lib/components/ConfigForm.svelte` | Dynamic form from `ProgramMeta.configFields` |
| `frontend/src/lib/components/StackCard.svelte` | Card shown on Dashboard for each stack |
| `frontend/src/lib/components/ui/` | shadcn-svelte component library (Button, Input, Select, Dialog, Tabs, Badge, Combobox, etc.) |

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

## Routes

| Path | Component | Auth required |
|---|---|---|
| `/login` | `Login.svelte` | No |
| `/register` | `Register.svelte` | No (only shown when no users exist) |
| `/` | `Dashboard.svelte` | Yes |
| `/accounts` | `Accounts.svelte` | Yes |
| `/settings` | `Settings.svelte` | Yes |
| `/stacks/{name}` | `StackDetail.svelte` | Yes |

---

## New Stack Dialog

A two-step dialog. Step 1 collects:
1. Stack name (free text)
2. Program (dropdown from `/api/programs`)
3. OCI Account (**required** — dropdown from `/api/accounts`; if no accounts exist, shows inline prompt to add one)
4. Passphrase (**required** — dropdown from `/api/passphrases`; if no passphrases exist, shows inline create form with name + value fields — Option B)

Step 2 shows the dynamic config form (`ConfigForm`) for the selected program.

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

The `Dashboard.svelte` loads passphrases at startup and passes them to `NewStackDialog` via `bind:passphrases`. If the user creates a passphrase inline in the dialog, the dashboard list updates too.

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
- Adding a new account (name, tenancy OCID, region, user OCID, fingerprint, private key PEM, SSH public key)
- Testing credentials ("Test credentials" button) — calls `POST /api/accounts/{id}/verify`; shows error detail on failure
- Deleting an account

Credentials are write-only: the API never returns raw values.

---

## Stack Detail Page

Located at `/stacks/{name}`. Shows:
- Stack info (status, last updated)
- Stack outputs (after a successful `up`)
- Action buttons: Deploy (Up), Refresh, Destroy, Cancel, Remove Stack
- Live SSE log viewer (auto-scrolls, color-coded output)
- Warning banner if `info.passphraseId == null` — operations will fail until a passphrase is assigned

The passphrase check is derived directly from `StackInfo.passphraseId`; no separate health API call is made.

---

## SSE Streaming

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

interface StackSummary {
  name: string;
  program: string;
  passphraseId: string | null;
  lastOperation: string | null;
  status: string;
  resourceCount: number;
}

interface StackInfo {
  name: string;
  program: string;
  passphraseId: string | null;
  config: Record<string, string>;
  outputs: Record<string, unknown>;
  resources: number;
  lastUpdated: string | null;
  status: string;
}

interface ConfigField {
  key: string;
  label: string;
  type: 'text' | 'number' | 'textarea' | 'select' | 'oci-shape' | 'oci-image';
  required?: boolean;
  default?: string;
  description?: string;
  options?: string[];  // for 'select' type
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
