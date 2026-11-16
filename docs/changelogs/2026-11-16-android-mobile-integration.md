# 2026-11-16 — Android mobile Controller integration

- Added a loopback-only `POST` → `PATCH` method override for the Android gateway, since Android's `HttpURLConnection` rejects PATCH as an outgoing method. Non-loopback clients cannot use the override.
- Hardened mobile startup so database creation/migrations complete before `Start` succeeds; added Android account-count, initial-account, password verification and password-change bindings with lifecycle serialization coverage.
- Updated the runtime and architecture docs to describe the mobile host integration and remaining AAR/device-runtime limitations.
- Verification: the new test expectation was corrected to 501 because Auth is intentionally absent in that fixture. Final `make test` passed on an approved rerun (the sandboxed attempt could not bind loopback test ports); `make lint` passed `go vet ./...` while `golangci-lint` was unavailable and skipped; `make cross` passed for configured targets, including Android. Android device/AAR runtime validation remains open.
