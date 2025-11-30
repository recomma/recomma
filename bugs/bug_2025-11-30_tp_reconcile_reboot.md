# Bug Report — Reboot wipes active DOGE TP via forced reconciliation

Source artifacts:

- Database: `2025-11-30_0436/testing31.sqlite`
- Embedded logs: `2025-11-30_0436/testing31.sqlite:app_logs`

## Summary

After rebooting the app on 2025‑11‑30 the DOGE deal `2388384150` already had an open Hyperliquid TP (cloid `0x00fd44fb8e5bd996eae18bf9bb90b398`). When the process restarted, the fill tracker’s `reconcileSnapshot` ran before the TP submission/status metadata had been rehydrated, so it believed no TP existed and called `ensureTakeProfit` repeatedly as it discovered each incremental filled quantity. The emitter blasted more than a dozen `ActionCreate` requests with the same CLOID. Hyperliquid treated every new request as superseding the previous reduce-only order and returned `status:"reduceOnlyCanceled"` for all prior instances, so the surviving TP ended up in a canceled state even though nothing on the exchange had been wrong before the reboot.

## Evidence

### Existing TP was still open before restart

```
sqlite> SELECT datetime(recorded_at_utc/1000,'unixepoch'), json_extract(payload_blob,'$.status')
   ...> FROM hyperliquid_status_history
   ...> WHERE order_id='0x00fd44fb8e5bd996eae18bf9bb90b398'
   ...> ORDER BY recorded_at_utc ASC;
2025-11-29 20:37:33|open
2025-11-29 20:37:34|open
```

No cancel or fill was recorded until the restart window.

### Rebuild triggered TP recreation loop immediately after reboot

`app_logs` between `03:35:25` and `03:35:32` show the fill tracker recreating the TP nine times, ramping `target_qty` from `198` to `1711` (all with `reason:"recreate missing take profit"`), followed by Hyperliquid websocket updates that mark each previous TP as `reduceOnlyCanceled`:

```
sqlite> SELECT datetime(timestamp_utc/1000,'unixepoch'), scope, message, attrs
   ...> FROM app_logs
   ...> WHERE timestamp_utc BETWEEN 1764473725000 AND 1764473733000
   ...>   AND (scope IN ('filltracker','hyperliquid.ws') OR message LIKE '%recreated%')
   ...> ORDER BY timestamp_utc;
2025-11-30 03:35:25|filltracker|recreated take profit|{"net_qty":198, ...}
...
2025-11-30 03:35:25|filltracker|recreated take profit|{"net_qty":1711, ...}
2025-11-30 03:35:30|hyperliquid.ws|OrderUpdates|{"orders":[{"status":"reduceOnlyCanceled",...}]}
2025-11-30 03:35:32|hyperliquid.ws|OrderUpdates|{"orders":[{"status":"reduceOnlyCanceled",...}]}
```

Every websocket payload references `cloid":"0x00fd44fb8e5bd996eae18bf9bb90b398"` with `status:"reduceOnlyCanceled"`, matching what was observed in the UI.

### Root cause in code

`reconcileSnapshot` immediately calls `ensureTakeProfit` for any venue with a positive net quantity but no active TP snapshot (`filltracker/service.go:200-222`). During restart the tracker had not yet reloaded TP submission/status metadata, so `buildTakeProfitIdentifier`/`lookupTakeProfitContext` fell back to the stored TP order id and emitted new work. Because `hyperliquid_submissions` did not record the original create before the reboot, the dedup guard `shouldSkipSubmission` (`filltracker/service.go:502-525`) returned `false`, so the emitter churned through the recreate path until Hyperliquid canceled the chain of reduce-only orders.

## Impact

- Live TP was removed (final status `reduceOnlyCanceled`) even though the bot expected it to remain `open`. The mirrored position is now unprotected unless operators manually recreate it.
- Every reboot will hammer Hyperliquid with duplicate reduce-only creates for every filled deal, burning through rate limits and causing confusing audit trails.

## Proposed next steps

1. Gate `reconcileSnapshot` until after the TP submission + status rows have been restored from storage (e.g. only run once `Rebuild` has seen an `open` status for the identifier).
2. Persist TP submissions as soon as they are first sent, so `shouldSkipSubmission` can recognize an already-open TP immediately after a restart.
3. Add a regression test that simulates a restart with an existing TP to ensure the tracker does not emit duplicate take-profit creates until it detects an actual mismatch.
