# 2026-11-14 — Windows power adapter

| | |
|---|---|
| Author | agent |
| Scope | `internal/platform` (`power_windows.go`, `power_windows_test.go`, `power_other.go`), docs, `deploy/README.md` |
| Requirements | REQ-AG-011 (note), REQ-AG-015 (new) |

## Added
- `internal/platform/power_windows.go`: `PowerController` for Windows. Enables `SeShutdownPrivilege` on the
  process token, then `InitiateSystemShutdownExW` (local machine, timeout 0, fixed message and reason,
  applications closed by force). Expects the Agent to run elevated.
- Unit tests with fakes for the two Win32 calls (privilege, shutdown).

## Changed
- `power_other.go` build tag now excludes Windows (`(!linux || android) && !windows`); Linux, macOS and
  Android behaviour is unchanged.

## Notes
- **Force-close is a decision**: without it an open "save changes?" dialog keeps the machine on and the
  command silently times out; with it, unsaved data in open applications is lost. Not configurable yet.
- Not a Windows Service: start the Agent from an elevated console or Task Scheduler. `data_dir` must be set
  explicitly (default `./data` is relative to the working directory).
- GitNexus impact: `NewPowerController` HIGH (agent startup flows); it was not modified.

## Verification
- `CGO_ENABLED=0 go build ./internal/platform/ ./cmd/homecore` for windows/amd64, windows/arm64,
  linux/amd64, darwin/arm64, android/arm64: all compile.
- `GOOS=windows go vet ./internal/platform/` clean; `GOOS=windows go test -c` compiles the tests.
- `gofmt -l`, `go vet ./...`, `go test ./...` and `make cross` (all 7 targets): clean/pass on macOS.
- **Not run**: the Windows tests and the adapter itself (no Windows host: no real shutdown, privilege
  or `ERROR_ACCESS_DENIED` path observed).

## Known gaps / follow-ups
- Run it on a real Windows host (elevated and non-elevated).
- Windows Service wrapper and a Windows release package (roadmap).
- Optional config key for non-forced shutdown.
