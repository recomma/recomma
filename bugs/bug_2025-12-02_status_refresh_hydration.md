# Bug Report — Take-profit reconciliation never re-enables after status refresh failure

Source artifacts:

- Source: code inspection (`filltracker/service.go`, `cmd/recomma/app.go`)
- Reproduction: `filltracker/service_test.go:TestReconcileTakeProfits_RecoversAfterStatusRefreshFailure`

## Summary

When the process boots we call `FillTracker.RequireStatusHydration()` (`cmd/recomma/app.go:466`) and only clear that gate after `StatusRefresher.Refresh` succeeds once (`cmd/recomma/app.go:763-773`). If that *single* refresh attempt fails or times out—common when Hyperliquid is under load—the service keeps running but `statusHydrated` stays `false` forever. Every `FillTracker.ReconcileTakeProfits` invocation hits the guard at `filltracker/service.go:195-199` and exits without recreating reduce-only orders, so deals that exit base/safety fills never get any take-profit protection until the whole process is restarted and the refresher happens to succeed.

## Evidence

### Code references

- `cmd/recomma/app.go:431-470` — `RequireStatusHydration` is called during startup before any Hyperliquid status replay has happened.
- `cmd/recomma/app.go:763-773` — the only call to `FillTracker.MarkStatusesHydrated()` sits behind the initial `statusRefresher.Refresh`. Errors are logged but ignored, so we never re-run the refresher nor clear the gate later.
- `filltracker/service.go:195-199` — `ReconcileTakeProfits` returns early while the hydration flag is `false`, meaning reduce-only reconciliations stay disabled for the lifetime of the process.

### Reproduction steps

1. Start the service while forcing `hl.StatusRefresher.Refresh` to return an error (e.g., make the Hyperliquid info client time out).
2. Allow Hyperliquid websocket/status updates to catch up; `FillTracker.UpdateStatus` keeps ingesting data but `statusHydrated` remains `false`.
3. Trigger `FillTracker.ReconcileTakeProfits` (either via the periodic resync ticker or manually). It will log `hyperliquid statuses not hydrated` and skip forever.
4. Run `go test ./filltracker -run TestReconcileTakeProfits_RecoversAfterStatusRefreshFailure`. The regression test (currently skipped) captures the expected behavior once this bug is fixed.

## Impact

- Every crash-loop or cold boot that happens while Hyperliquid status refresh fails leaves all mirrored deals without take-profit recreation, so no reduce-only sells are issued or cleaned up.
- Operators cannot rely on restarts to recover failed websocket sessions because a single transient refresh error permanently disables reconciliation.
- Manual TP cancels are never honored again until a restart, so the service can drift from 3Commas’ intent indefinitely.

## Next steps

1. Retry or background the initial `StatusRefresher.Refresh` until it completes successfully, then call `FillTracker.MarkStatusesHydrated()`.
2. Alternatively, have `FillTracker` automatically mark itself hydrated once it ingests a fresh Hyperliquid status for every tracked order so a later sync/websocket catch-up clears the gate.
3. Unskip `filltracker/service_test.go:TestReconcileTakeProfits_RecoversAfterStatusRefreshFailure` after implementing the fix so CI enforces the contract.
