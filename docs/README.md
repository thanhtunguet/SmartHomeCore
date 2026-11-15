# HomeCore Documentation

Start with the [requirements overview](requirements/overview.md). It maps every point of the
original brief ([`BRIEF.md`](../BRIEF.md)) to its status and to the document that covers it.

```
docs/
├── requirements/   WHAT the system must do (with status and verifying tests)
├── design/         HOW it is built (architecture, database, protocols, decisions)
├── plans/          WHEN / what's next (roadmap, backlog)
├── reports/        Findings and gap analyses (point-in-time, not requirements)
└── changelogs/     WHAT changed, session by session
```

## requirements/

| Document | Content |
|----------|---------|
| [overview.md](requirements/overview.md) | Product summary, goals, platforms, MVP scope, non-goals, **brief traceability matrix** |
| [runtime-and-configuration.md](requirements/runtime-and-configuration.md) | `REQ-RT`: modes, configuration, lifecycle, logging, mobile bindings, cross-compile |
| [http-api.md](requirements/http-api.md) | `REQ-API`: REST foundation, docs, health, authN/authZ, middleware |
| [authentication.md](requirements/authentication.md) | `REQ-AUTH`: passwords, JWT, refresh rotation and grace window, audit, `homecore user`, legacy client tokens (`homecore token`) |
| [devices-and-commands.md](requirements/devices-and-commands.md) | `REQ-DEV`, `REQ-MON`, `REQ-CMD`, `REQ-RET`: device registry, monitor, wake and command lifecycle, retention |
| [storage-and-secrets.md](requirements/storage-and-secrets.md) | `REQ-ST`, `REQ-SEC`: SQLite, migrations, master key, sealing |
| [ddns.md](requirements/ddns.md) | `REQ-DDNS`, `REQ-SYS`: public-IP quorum, Cloudflare client, DDNS configs and syncer, system metrics |
| [shutdown-agent.md](requirements/shutdown-agent.md) | `REQ-AG`: signed commands, replay protection, least-privilege execution |
| [alerts.md](requirements/alerts.md) | `REQ-ALERT`: alert channels (Telegram), events for IP change, wake/shutdown and out-of-control status changes, delivery |
| [mcp.md](requirements/mcp.md) | `REQ-MCP`: MCP server for AI agents (`POST /api/v1/mcp`), tools, authentication by service account key |
| [web-ui.md](requirements/web-ui.md) | `REQ-UI`: frontend submodule, embedded build, session, flows against the REST API |

## design/

| Document | Content |
|----------|---------|
| [architecture.md](design/architecture.md) | Architecture design: brief analysis, runtime model, code layout, modules, state machines, API, security, platforms |
| [database.md](design/database.md) | Database design: conventions, settings, ER diagram, every table, indexes, lifecycle, migrations |
| [agent-protocol.md](design/agent-protocol.md) | Normative Controller → Agent protocol v1 |
| [decisions.md](design/decisions.md) | Architecture Decision Records (ADR-001…) |

The REST contract itself is [`api/openapi.yaml`](../api/openapi.yaml) (source of truth), and a
running Controller serves it at `/api/docs`.

## plans/

| Document | Content |
|----------|---------|
| [roadmap.md](plans/roadmap.md) | Phase checklists, next steps, backlog of known gaps |

## reports/

| Document | Content |
|----------|---------|
| [frontend-api-integration.md](reports/frontend-api-integration.md) | Frontend ↔ API wiring: what was connected, gaps (`GAP-*`) and inconsistencies (`INC-*`), verification |

## changelogs/

| Document | Content |
|----------|---------|
| [README.md](changelogs/README.md) | Convention, template and index |

## Documentation rules

The rules agents and contributors must follow (which document to read before working and which to
update with each kind of change) are in [`AGENTS.md`](../AGENTS.md#documentation-rules).
