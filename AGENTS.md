# CLAUDE.md: guidance for AI coding agents

Read `docs/architecture.md`, `docs/decisions.md` and (for agent work) `docs/agent-protocol.md`
before changing behaviour. They are the design contract; update them in the same change if a
decision changes.

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
  each operation's `security` (`bearerAuth: [admin]` = admin only, `security: []` = public).
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
- Every agent command is verified in the order defined in `docs/agent-protocol.md` §3: signature
  before parsing, then agent_id, allowlist, timestamp window, start-time floor, nonce, idempotency.
- No plaintext secrets in the DB (seal with `internal/secrets`), none in API responses or logs.
- JWT signing key, Controller Ed25519 key and master key are distinct.
- Shutdown requires the `admin` role and writes `audit_log`.
- Wake/shutdown endpoints return `202` + command; never block until the machine changes state.

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
