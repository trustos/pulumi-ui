# Project Rules — pulumi-ui

Self-hosted OCI infrastructure provisioning via Pulumi. Go backend + Svelte 5 frontend,
SQLite database, Pulumi Automation API, OCI Terraform provider v4.3.1.

## Repo structure
- `internal/api/` — HTTP handlers (thin — no business logic)
- `internal/engine/` — Pulumi orchestration (Up/Destroy/Refresh/Preview)
- `internal/programs/` — program registry, built-in programs, YAML program support
- `internal/db/` — SQLite stores (persistence only)
- `internal/services/` — business logic (target state; extract here as you refactor)
- `internal/ports/` — repository interfaces (target state)
- `frontend/src/` — Svelte 5 SPA

## Critical invariants — never break these

1. **OCI credentials**: always `oci:privateKey` (inline, Secret:true) + `OCI_PRIVATE_KEY` env.
   Never `oci:privateKeyPath` or temp PEM files. Reason: avoids `~/.oci/config` fallback.

2. **YAML type checking**: `PULUMI_DEBUG_YAML_DISABLE_TYPE_CHECKING=true` must always be set.
   OCI v4 schema has nil element types that crash pulumi-yaml. Set in main.go and engine.

3. **Cloud-init**: always gzip-compress before base64. OCI metadata limit is 32 KB;
   uncompressed script is ~39 KB base64. cloud-init auto-detects gzip.

4. **NLB serialization**: all NLB port resources must be chained via `dependsOn`.
   OCI NLB rejects concurrent mutations (409 Conflict).

5. **YAML template interpolation**: use `{{ printf "${%s}" $var }}` not `${{{ $var }}}`.
   The latter breaks Go's template parser.

## Architecture principles (full detail in docs/12-coding-principles.md)
- Handlers call services, not stores directly
- Stores implement interfaces from `internal/ports/`
- Every ConfigField has a ConfigLayer: infrastructure | compute | bootstrap | derived
- Programs register via RegisterBuiltins(), never via init()
- No business logic in DB stores

## Frontend principles (full detail in docs/13-frontend-guidelines.md)
- ConfigForm is a pure layout renderer — OCI fetching belongs in picker components
- Wizard steps are semantically bounded: one step = one concern
- All API calls go through src/lib/api.ts
- "VM Access Key" (instance metadata SSH) ≠ "Program SSH Key" (config field)

## Improvement roadmap (full detail in docs/11-architecture-roadmap.md)
Part 0: ConfigLayer taxonomy → BE-1: CredentialService → BE-2: Engine dedup →
FE-1: 3-step wizard → BE-3: Repo interfaces → FE-2: Picker extraction →
BE-4: Handler decomposition → BE-5: Thread-safe registry → FE-4: Validation
