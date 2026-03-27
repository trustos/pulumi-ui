# Docs Consolidation — Task List

## Remove (completed work, no longer needed)

- [ ] `builtin-yaml-migration.md` (38KB) — Migration plan from Go→YAML built-ins. Completed: nomad-cluster is now YAML. Archive or delete.
- [ ] `multi-node-connectivity-fix-plan.md` (12KB) — Draft investigation plan for per-node connectivity. All fixes implemented. Delete.

## Merge

- [ ] `nlb-agent-connectivity.md` (20KB) → merge key design decisions into `application-catalog-architecture.md` Phase 2/3 sections and `oci-networking-rules.md`. Then delete.
- [ ] `visual-editor-simplification.md` (4KB) → merge into `visual-editor.md` as a "Simplification Roadmap" section. Then delete.
- [ ] `programs.md` + `yaml-programs.md` → combine into single `programs.md`. Currently overlap on program concepts, config fields, template functions.

## Update

- [ ] `roadmap.md` — add items from this session:
  - Visual editor property system refactoring (3 phases from visual-editor-simplification.md)
  - Private-instance NLB templates: bastion-host, database-server, multi-tier-app need NLBs for agent access
  - Serializer expanded YAML format (done) — mark complete
  - NLB serialization fix (done) — mark complete
  - Per-node health/terminal (done) — mark complete
  - Level 6 dependsOn validation (done) — mark complete
  - Built-in program fork support (done) — mark complete
- [ ] `phase1-manual-tests.md` — update test results, mark completed tests
- [ ] `application-catalog-architecture.md` — verify Phase 1-3 sections reflect current state
- [ ] `api.md` — verify all endpoints documented (agent proxy ?node=N was added)

## Keep as-is (still relevant)

- `architecture.md` — layer diagram, design overview
- `coding-principles.md` — code style rules
- `database.md` — SQLite, migrations, encryption
- `deployment.md` — Docker, env vars, Nomad
- `frontend.md` — SPA structure, components, UX rules
- `oci-networking-rules.md` — networking patterns (recently created)

## Target state: 12 docs (down from 17)

1. `architecture.md`
2. `api.md`
3. `database.md`
4. `deployment.md`
5. `frontend.md`
6. `programs.md` (merged with yaml-programs.md)
7. `visual-editor.md` (merged with simplification roadmap)
8. `application-catalog-architecture.md` (merged with NLB connectivity)
9. `oci-networking-rules.md`
10. `coding-principles.md`
11. `roadmap.md` (updated)
12. `phase1-manual-tests.md`
