# Bug Report — Old CLOID stuck `open` despite deal closing

Source artifacts:

- Database: `2025-12-01_0730/testing31-rerretry.sqlite` (plus `-wal`/`-shm`)

## Summary

Order `0x00fd44fb8e5bd996eae18bf9bb90b398` from deal `2388384150` (bot `16598267`) has never received a terminal Hyperliquid status. The snapshot still shows it as `open` on 2025-11-29 even though the corresponding 3Commas deal finished with `status='stop_loss_finished'`. Filltracker therefore thinks an old buy remains working on the venue, skewing “all buys filled” calculations and potentially hiding real risk.

## Evidence

### Deal status

```
sqlite> select deal_id, json_extract(payload,'$.status') status
   ...> from threecommas_deals
   ...> where deal_id=2388384150;
2388384150|stop_loss_finished
```

### Hyperliquid status history

```
sqlite> select order_id,
   ...>        json_extract(payload_blob,'$.status') status,
   ...>        datetime(recorded_at_utc/1000,'unixepoch') ts
   ...> from hyperliquid_status_history
   ...> where order_id='0x00fd44fb8e5bd996eae18bf9bb90b398'
   ...> order by recorded_at_utc;
0x00fd44fb8e5bd996eae18bf9bb90b398|open|2025-11-29 20:37:33
0x00fd44fb8e5bd996eae18bf9bb90b398|open|2025-11-29 20:37:34
0x00fd44fb8e5bd996eae18bf9bb90b398|open|2025-11-29 20:37:34
```

No fill, cancel, or rejection was ever recorded for this CLOID.

## Impact

- Filltracker assumes an outstanding buy is live, so it will refuse to mark the deal “all buys filled” and will continue to block TP placement or cleanup flows tied to that condition.
- Operational dashboards show phantom exposure, making it harder to detect real in-flight orders.
- Without a terminal status, we cannot reconcile position history if Hyperliquid later claims the order executed.

## Next steps

1. Backfill a synthetic terminal status (e.g., `canceled` with a clearly annotated payload) for the stuck CLOID if Hyperliquid confirms it is no longer live; otherwise issue a manual cancel to clear it.
2. Add watchdog tooling that flags any order which stays `open` for more than N minutes so we can reconcile before deals exit.
3. Ensure storage/filltracker can self-heal by probing the exchange for the latest state when it sees an `open` entry without progress for an extended period.
