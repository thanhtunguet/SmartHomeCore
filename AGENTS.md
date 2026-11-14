# AGENTS.md: guidance for AI coding agents

This file is the instruction set for every AI coding agent (`CLAUDE.md` links here). The project
documentation in `docs/` is the contract for what to build and how; **follow it and keep it current**.
See [Documentation rules](#documentation-rules).

## Commands

```bash
make test            # all tests (must pass)
make lint            # gofmt check + go vet (+ golangci-lint if installed)
make generate        # after editing api/openapi.yaml
make cross           # verify all targets still compile with CGO_ENABLED=0
make run-both        # local Controller + dry-run Agent (configs/dev.yaml)
```

## Scope guardrails (MVP)

Build only: Controller + Agent (macOS/Linux), device CRUD, TCP monitoring, WOL, signed shutdown
agent, SQLite, JWT auth, Cloudflare DDNS, Swagger UI, static serving.
**Do not add**: clustering, distributed scheduling, WebSocket/SSE, plugin systems, extra DB
engines, message brokers, Redis, ORMs, DI frameworks, or new heavy dependencies without an ADR.

## Rules

- **Spec-first API.** Change `api/openapi.yaml` first, run `make generate`, then implement on
  `internal/api/handlers.Handlers`. Never edit `internal/api/gen/*.gen.go`. Auth rules come from
  each operation's `security` (`bearerAuth: []` = any authenticated user, `security: []` = public;
  there are no roles, ADR-011, so scopes on `bearerAuth` abort startup).
- **Migrations are append-only**: new file `internal/storage/migrations/NNNN_name.sql`; STRICT
  tables; timestamps INTEGER unix ms; IDs TEXT UUIDv7.
- **Dependency direction**: handlers → domain packages → storage. Domain packages define their own
  small repository interfaces. Only `internal/app` wires everything. No globals, no `init()` side
  effects. `internal/agent/protocol` stays stdlib-only.
- **Long-running components** expose `Run(ctx) error` and stop when ctx is cancelled.
- **Platform code** lives in `internal/platform` behind interfaces. `GOOS=android` matches the
  `linux` build tag, so Linux-only files need `//go:build linux && !android`.
- **CGO stays off** (`modernc.org/sqlite`). Anything that breaks `make cross` is a regression.

## Security invariants (never weaken)

- The Agent executes only enum actions (`shutdown`, `reboot`). No shell, no arguments from requests.
- Every agent command is verified in the order defined in `docs/design/agent-protocol.md` §3:
  signature before parsing, then agent_id, allowlist, timestamp window, start-time floor, nonce,
  idempotency.
- No plaintext secrets in the DB (seal with `internal/secrets`), none in API responses or logs.
- JWT signing key, Controller Ed25519 key and master key are distinct.
- Every `/api/v1` operation except the public ones requires authentication; there are no roles
  (ADR-011). Shutdown writes `audit_log` with the acting user.
- Wake/shutdown are idempotent and limited to one request per device per second (ADR-012).
- Wake/shutdown endpoints return `202` + command; never block until the machine changes state.

## Documentation rules

Documentation is part of the definition of done. A change is not complete until the docs below
describe the code as it now is.

### Map

| Folder | Holds | Main files |
|--------|-------|------------|
| `docs/requirements/` | What the system must do; requirement IDs, status, verifying tests | `overview.md` (brief traceability), `runtime-and-configuration.md`, `http-api.md`, `storage-and-secrets.md`, `shutdown-agent.md`, `web-ui.md` |
| `docs/design/` | How it is built | `architecture.md`, `database.md`, `agent-protocol.md`, `decisions.md` (ADRs) |
| `docs/plans/` | What's next | `roadmap.md` (phase checklists + backlog) |
| `docs/reports/` | Point-in-time findings: gap and inconsistency analyses with stable IDs | `frontend-api-integration.md` |
| `docs/changelogs/` | What changed, per session | `YYYY-MM-DD-slug.md`, index in `README.md` |

`BRIEF.md` is the original input. Do not edit it; its points are tracked in
`docs/requirements/overview.md` §6.

### Before starting work

1. Read `docs/requirements/overview.md` and the requirement document for the area you are touching.
2. Read the relevant design docs: `architecture.md` always; `database.md` for any SQL or schema
   work; `agent-protocol.md` for anything on the Agent or the dispatcher; `decisions.md` before
   going against an existing choice.
3. Check `docs/plans/roadmap.md` so the work fits the current phase and is not out of MVP scope.
4. If the docs and the code disagree, raise it with the user. Do not silently pick one; fixing the
   mismatch is part of the task.

### While working: update docs in the same change

| When you… | Update |
|-----------|--------|
| Implement, change or remove behaviour | The requirement doc: add or edit the `REQ-*` row, set its **Status**, name the verifying test. New area with no doc yet → create `docs/requirements/<area>.md` and link it from `overview.md` §5 |
| Complete or partly complete a brief point | `requirements/overview.md` §6 traceability row (status + reference) |
| Change structure, module responsibilities, flows, state machines, security model | `design/architecture.md` |
| Add a migration or change SQL semantics | `design/database.md` (tables, columns, indexes, lifecycle, brief mapping) |
| Change the Agent wire format or verification | `design/agent-protocol.md` (normative; bump the protocol version for incompatible changes) |
| Make or reverse a significant technical decision (dependency, protocol, storage, layering) | New ADR in `design/decisions.md`. Never rewrite an accepted ADR; supersede it |
| Change the REST API | `api/openapi.yaml` first (spec-first), then the endpoint status table in `requirements/http-api.md` |
| Finish a roadmap item or discover new work/gaps | Tick or add items in `plans/roadmap.md`; put gaps in its Backlog and the requirement doc's "Known gaps" |
| Change config keys | `configs/homecore.example.yaml`, `configs/dev.yaml`, `REQ-RT-*` |

### At the end of every session or change set

- Add or extend a changelog entry `docs/changelogs/YYYY-MM-DD-short-slug.md` using the template in
  `docs/changelogs/README.md`, and add it to the index table there (newest first).
- The changelog must list **verification honestly**: the commands you ran with their results,
  and what was not run.
- Update the "Last reviewed/updated" date of each document you changed.

### Writing conventions

- English, concise, tables over prose for requirements and schema.
- Requirement IDs (`REQ-<AREA>-NNN`) are stable: never renumber or reuse; mark removed ones `Removed`.
- Status vocabulary only: `Implemented`, `Implemented (untested)`, `Partial`, `Changed`, `Planned`.
  Do not mark something `Implemented` without a test or a recorded smoke test.
- Use relative links between docs; reference code by path (`internal/agent/server.go`), not by line number.

<!-- gitnexus:start -->
# GitNexus — Code Intelligence

This project is indexed by GitNexus as **SmartHomeCore** (1145 symbols, 2546 relationships, 82 execution flows).

> Index stale? Run `node .gitnexus/run.cjs analyze --index-only` from the project root — it auto-selects an available runner. No `.gitnexus/run.cjs` yet? Bootstrap with `npx`, `bunx`, or `pnpm dlx` — e.g. `bunx gitnexus@latest analyze` (npm 11 npx crash; #1939).

## Always Do

- **MUST run impact before editing.** Use `impact({target: "symbolName", direction: "upstream"})` or `node .gitnexus/run.cjs impact "symbolName" --direction upstream --repo .`; report callers, processes, and risk. Never substitute grep for graph analysis.
- **MUST analyze graph changes before committing.** Use `detect_changes({scope: "all"})` (MCP) or `node .gitnexus/run.cjs detect-changes --scope all --repo .` (CLI fallback). `partial: true` or `truncated: true` is not a clean check — a zero means unseen, not unaffected; re-run it. For regression review: `detect_changes({scope: "compare", base_ref: "main"})` or `node .gitnexus/run.cjs detect-changes --scope compare --base-ref "main" --repo .`.
- MUST warn on HIGH/CRITICAL `risk` pre-edit; never use `riskSharedAxes` to waive a HIGH/CRITICAL `risk` warning. Compare File/symbol: MCP File omits axes; Graph-RAG expands File.
- **MUST treat `risk: UNKNOWN` as unresolved, not as low.** An empty caller set is not evidence the symbol is unused — it can also mean the callers are not resolvable by the index (plain-object property access, dynamic dispatch, cross-language calls). `impact` pairs `UNKNOWN` with a `riskNote` saying so. Confirm with a text search before treating the symbol as safe to change or delete; do not proceed on the strength of a zero.
- **MUST use `query({search_query: "concept"})` for concepts/flows, `context({name: "symbolName"})` for a named symbol, or `impact` for blast radius, on read-only callers, dependencies, imports, or execution flow.** Graph first; text search only for empty/`UNKNOWN`/literals.
- For security review, `explain({target: "fileOrSymbol"})` lists taint findings (source→sink flows; needs `analyze --pdg`).

## Never Do

- NEVER edit a function, class, or method before MCP/CLI impact analysis.
- NEVER ignore HIGH or CRITICAL risk warnings from impact analysis, and never read `UNKNOWN` as an all-clear — it means the walk could not answer, which is the one verdict that requires confirming by other means.
- NEVER rename symbols with find-and-replace — use `rename` which understands the call graph.
- NEVER commit before MCP/CLI graph change analysis.

## Resources

| Resource | Use for |
| --- | --- |
| `gitnexus://repo/SmartHomeCore/context` | Codebase overview, check index freshness |
| `gitnexus://repo/SmartHomeCore/clusters` | All functional areas |
| `gitnexus://repo/SmartHomeCore/processes` | All execution flows |
| `gitnexus://repo/SmartHomeCore/process/{name}` | Step-by-step execution trace |

## CLI

| Task | Read this skill file |
| --- | --- |
| Understand architecture / "How does X work?" | `.claude/skills/gitnexus-exploring/SKILL.md` |
| Blast radius / "What breaks if I change X?" | `.claude/skills/gitnexus-impact-analysis/SKILL.md` |
| Trace bugs / "Why is X failing?" | `.claude/skills/gitnexus-debugging/SKILL.md` |
| Rename / extract / split / refactor | `.claude/skills/gitnexus-refactoring/SKILL.md` |
| Tools, resources, schema reference | `.claude/skills/gitnexus-guide/SKILL.md` |
| Index, status, clean, wiki CLI commands | `.claude/skills/gitnexus-cli/SKILL.md` |

<!-- gitnexus:end -->
