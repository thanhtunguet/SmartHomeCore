# HomeCore

Lightweight, always-on home infrastructure service in Go. HomeCore watches devices on your LAN,
wakes them with Wake-on-LAN and shuts them down safely using signed commands. It also keeps a
Cloudflare DNS record pointed at your home IP. One code base builds a Linux/macOS/Windows binary
and an Android library (gomobile) for phones and TV boxes.

| Role | What it does |
|------|--------------|
| **Controller** | REST API (JWT) + Swagger UI, device registry, TCP online monitor, Wake-on-LAN, signed shutdown dispatch, Cloudflare DDNS, SQLite |
| **Agent** | Tiny listener on a managed PC that verifies Ed25519-signed commands and runs an allowlisted power action (no shell, ever) |

```
homecore --mode controller | agent | both
```

## Quick start

Requirements: Go ≥ 1.26 (developed with 1.27). No CGO and no external services needed.

```bash
make run            # Controller on http://127.0.0.1:8080 with configs/dev.yaml
open http://127.0.0.1:8080/api/docs/     # Swagger UI (offline)
go run ./cmd/homecore user add --config configs/dev.yaml alice   # first account (prompts for the password; there is no default account)
curl -s localhost:8080/readyz
make test           # unit + integration tests
make cross          # binaries for linux/{amd64,arm64,arm}, windows, darwin, android/arm64
```

Try the signed-command flow against a dry-run agent:

```bash
make devtools
build/devtools keygen -out /tmp/ctl.key           # prints public_key
# paste the public_key into configs/dev.yaml → agent.trusted_controllers
make run-both
build/devtools send -key /tmp/ctl.key -agent http://127.0.0.1:9750 \
  -agent-id "$(cat data/agent/agent_id)"          # → HTTP 202, agent logs "DRY RUN: would power off"
```

## Documentation

All project documentation lives in [`docs/`](docs/README.md):

- [docs/requirements/overview.md](docs/requirements/overview.md): goals, scope and a point-by-point traceability of `BRIEF.md`
- [docs/requirements/](docs/requirements/): requirement docs (IDs, status, verifying tests) for the implemented areas
- [docs/design/architecture.md](docs/design/architecture.md) · [database.md](docs/design/database.md) · [agent-protocol.md](docs/design/agent-protocol.md) · [decisions.md](docs/design/decisions.md)
- [docs/plans/roadmap.md](docs/plans/roadmap.md): phases, next steps, backlog
- [docs/changelogs/](docs/changelogs/README.md): change history by session
- [api/openapi.yaml](api/openapi.yaml): REST contract, the **source of truth** for routes and auth
- [deploy/README.md](deploy/README.md): systemd + polkit installation
- [AGENTS.md](AGENTS.md): rules for AI coding agents, including documentation duties

## Layout

```
cmd/homecore/        entrypoint                 internal/agent/       Agent listener + executor
cmd/devtools/        dev utilities              internal/agent/protocol  signed command format (shared)
mobile/              gomobile bindings          internal/api/         router, middleware, handlers
api/openapi.yaml     REST spec (source)         internal/api/gen/     generated from the spec (do not edit)
web/                 embedded UI + Swagger UI   internal/storage/     SQLite, migrations
configs/ deploy/     config & service files     internal/{auth,devices,monitoring,wol,commands,ddns,system,secrets,scheduler,platform}
```

## Development workflow

1. **API change**: edit `api/openapi.yaml` → `make generate` → implement the method on
   `internal/api/handlers.Handlers`. Access control comes from the operation's `security` in the spec.
2. **Schema change**: add `internal/storage/migrations/NNNN_description.sql`. Never edit an applied migration.
3. Before committing, run `make lint test` (and `make check-generated` when the spec changed).

## Status

Phase 1 foundation is in place: config, lifecycle and modes, HTTP server, spec-first API, offline
Swagger UI, SQLite with the full schema, and the agent protocol with replay protection. The other
modules are stubs; protected endpoints answer `401` until JWT auth lands and `501` until they are
implemented. See [docs/plans/roadmap.md](docs/plans/roadmap.md).
