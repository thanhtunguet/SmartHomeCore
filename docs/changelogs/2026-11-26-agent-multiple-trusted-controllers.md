# 2026-11-26 — Agent with several trusted Controllers: verified, tests added

| | |
|---|---|
| Author | Claude |
| Scope | tests and docs only; no production code changed |
| Requirements | REQ-AG-002, REQ-AG-017 |

## Finding

The Agent already trusts any number of Controllers. `agent.trusted_controllers` is a list,
`--trusted-controller` is repeatable, `agent.NewServer` loads every entry and `protocol.NewVerifier`
keeps a `key_id -> public key` map, so a command signed by any listed key verifies. `agentsetup`
(`Params.Args`, systemd unit, Windows shortcut) repeats the flag for every key. What was missing is
proof: the only verifier test used one key.

The one place that is single-key is the web UI command generator (`frontend/src/api/agentCommand.ts`):
it emits one `--trusted-controller` (this Controller's key). A second Controller's key must be
appended by hand (`--trusted-controller <key>` again, or another entry in `trusted_controllers`).

## Added

- `protocol.TestVerifyAcceptsEveryTrustedKeyAndOnlyThose`: two trusted keys both accepted, a third
  unlisted key gets `401`.
- `agentsetup.TestSeveralTrustedControllersSurviveParseAndRender`: two flags survive `Parse`, `Args`
  and `RenderUnit` in order.

## Verification

- `go test ./internal/agent/... ./internal/agentsetup/` — passed.
- Mutation check: with `NewVerifier` temporarily limited to the first key, the new protocol test
  failed ("key 1 is trusted but was rejected"); restored, it passes.
- Not run: an end-to-end run with two real Controllers, `make test` as a whole, and the Windows shortcut.

## Known gaps / follow-ups

- The UI generates the command with one key only; letting it add extra keys is optional work.
