# Bug Report — TP recreated before Hyperliquid statuses refresh on startup

Source artifacts:

- Database snapshots: `2025-11-30_1337/testing31-retry.sqlite`, `2025-11-30_1354/testing31-retry.sqlite`
- Hyperliquid UI screenshots shared in chat (multiple reduce-only slices recreated at 12:54 UTC+1)

## Summary

Restarting the service when deals are mid-flight causes filltracker to immediately recreate the take-profit even though Hyperliquid already has a live reduce-only order. During boot, `App.Start` calls `FillTracker.ReconcileTakeProfits` before the Hyperliquid `StatusRefresher` has a chance to replay the live book, so the tracker concludes “no TP exists” and fires a cascade of `ActionCreate`s sized 169 → 1 711 DOGE. Hyperliquid happily accepts the newest request and cancels the preceding slices, effectively spamming the venue every time the process starts. This happens even when the previous run left a perfectly good TP on the exchange — any reboot (planned or accidental) reissues the order chain and risks duplicate cancels/fills.

## Evidence

### App logs (`2025-11-30_1354/testing31-retry.sqlite:app_logs`)

```
2025-11-30 12:54:04|DEBUG|hyperliquid.ws|OrderUpdates|{... "sz":"169.0","cloid":"0x00fd44fb8e5bd996eae18bf9bb90b398"}|status":"open"
2025-11-30 12:54:04|INFO |hyperliquid.emitter|Order sent|{"requested_size":400,"reason":"recreate missing take profit"}
...
2025-11-30 12:54:06|INFO |hyperliquid.emitter|Order sent|{"requested_size":1711,"reason":"recreate missing take profit"}
2025-11-30 12:54:06|DEBUG|hyperliquid.ws|OrderUpdates|{"status":"reduceOnlyCanceled","sz":"1325.0","cloid":"0x00fd44fb8e5bd996eae18bf9bb90b398"}
```

During a single startup window (12:54:04–12:54:08) the log shows eleven `filltracker|recreated take profit` messages and matching Hyperliquid submissions, despite the fact that the previous run already had an open TP at 0.15212. The websocket updates prove that each new create forced Hyperliquid to cancel the prior slices (`status":"reduceOnlyCanceled"`).

### Database state

Before reboot (`2025-11-30_1337/testing31-retry.sqlite`) we still have an open safety (`order_id = 0x00fd44fb8e5bd996f92f0c766bc8609e`) and a live TP on the exchange. After copying to `2025-11-30_1354/testing31-retry.sqlite` and restarting, `hyperliquid_status_history` momentarily lacks a fresh `open` row for the TP, so `ReconcileTakeProfits` assumes it is missing and recreates it before the websocket catch-up (`StatusRefresher.Refresh`) runs. Once the refresher reconnects, it reports the new cascade of orders we just emitted.

## Impact

- Any reboot (crash, deploy, operator restart) spams Hyperliquid with a flurry of reduce-only creates until the websocket session catches up.
- Manual cancellations (e.g., cleaning up duplicate slices) do not survive restart; the system immediately recreates them, so operators cannot safely “cancel then reboot” to get back to a single TP.
- Multiple reduce-only orders in flight increase confusion on the exchange UI and risk race conditions if the market moves while we are recreating them.

## Proposed fix

1. Do **not** call `FillTracker.ReconcileTakeProfits` until `StatusRefresher.Refresh` has completed at least one pass (or until we observe a live `open` status for each deal). The tracker should only reconcile once the exchange’s view is loaded.
2. Alternatively, persist a “status refresh completed” flag per deal and have `ensureTakeProfit` refuse to emit creates when Hyperliquid statuses are stale.
3. Add startup integration coverage (see test below) to assert that we skip TP recreation until a fresh Hyperliquid status is recorded.

## Regression test

- `filltracker/service_test.go:TestReconcileTakeProfits_WaitsForStatusRefreshBeforeRecreation` (currently skipped) describes the desired behavior: after `Rebuild`, and before any Hyperliquid status is loaded, calling `ReconcileTakeProfits` should not enqueue a recreate. Once the bug is fixed, remove the `t.Skip(...)` and ensure the test passes.
