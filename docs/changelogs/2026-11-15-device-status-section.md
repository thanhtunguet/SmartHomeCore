# 2026-11-15 — Device sheet: "Device Status" section with Power on / Power off

| | |
|---|---|
| Author | Claude |
| Scope | `frontend/src/components/devices/DeviceDetailSheet.tsx`, `DevicesScreen.tsx`, `common/PowerStateBadge.tsx`, `docs/requirements/web-ui.md` |
| Requirements | `REQ-UI-027` added |

## Changed
- The device sheet's action bar (Check Status Now, Wake Device, Shutdown) is now a **Device Status** section.
- Header row: title and a status badge on the left (ON green / OFF grey / Unknown, with a dot; amber pulsing "Waking" / "Shutting down" during a power transition), a refresh icon button on the right (runs the existing manual probe, spins while probing).
- Two buttons, **Power on** and **Power off**, are always shown and mutually exclusive by state: Power on is enabled when the device is not online (and WoL is enabled), Power off when it is online (and the Agent integration is enabled). Both stay disabled during a power transition or the 1 s guard. Tooltips give the reason when disabled.
- Behaviour change: Wake used to stay enabled while the device was online; it is now disabled then. Unknown status is treated as not online, so Power on stays available.
- The badge is a shared component, `common/PowerStateBadge`, also used on the Devices page (card and table views) in place of the plain status badge. Dashboard and the command palette keep `StatusBadge`; the sheet header keeps it too.
- No API, schema or config change.

## Verification
- `cd frontend && pnpm lint` — passed.
- `cd frontend && pnpm test` — passed (13 files, 198 tests); no test covers this component.
- `cd frontend && pnpm build` — passed.
- Not run: browser check, `make test`, `make lint`, `make cross`, embedded UI rebuild. GitNexus impact analysis could not run (LadybugDB failed to load: missing `libssl.3.dylib`); callers were checked with grep (only `App.tsx` uses the component).
