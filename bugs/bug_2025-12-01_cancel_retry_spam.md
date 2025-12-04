# Bug Report — Cancel storm after IOC fill leaves no audit trail

Source artifacts:

- Database: `2025-12-01_0730/testing31-rerretry.sqlite` (plus `-wal`/`-shm`)

## Summary

After the base order for deal `2388611506` (CLOID `0x00fd44fb8e5bd9965ac2f8e58cfa73c1`) finally filled at 00:09:25Z, the Hyperliquid emitter kept reissuing cancel calls for ~35 seconds even though the order was already `filled`. Each attempt logged `could not cancel order`, yet `hyperliquid_submissions` contains zero `cancel` rows—only the original `create`. We therefore waste rate-limit capacity, generate noisy logs, and have no persisted record that cancel attempts occurred.

## Evidence

### Logs

```
sqlite> select datetime(timestamp_utc/1000,'unixepoch') ts, message
   ...> from app_logs
   ...> where scope='hyperliquid.emitter'
   ...>   and message like 'could not cancel order%'
   ...>   and timestamp_utc between strftime('%s','2025-12-01 00:09:25')*1000
   ...>                           and strftime('%s','2025-12-01 00:10:05')*1000;
2025-12-01 00:09:26|could not cancel order
2025-12-01 00:09:27|could not cancel order
2025-12-01 00:09:30|could not cancel order
2025-12-01 00:09:35|could not cancel order
2025-12-01 00:09:43|could not cancel order
2025-12-01 00:10:00|could not cancel order
```

### Hyperliquid statuses

```
sqlite> select json_extract(payload_blob,'$.status') status,
   ...>        datetime(recorded_at_utc/1000,'unixepoch') ts
   ...> from hyperliquid_status_history
   ...> where order_id='0x00fd44fb8e5bd9965ac2f8e58cfa73c1';
iocCancelRejected|2025-12-01 00:09:23
open|2025-12-01 00:09:25
filled|2025-12-01 00:09:25
```

The order was already terminal (`filled`) before the cancel spam began.

### Missing submissions

```
sqlite> select distinct action_kind from hyperliquid_submissions;
create
```

No `cancel` action was ever persisted even though the emitter clearly attempted one multiple times.

## Impact

- Repeated cancel attempts chew through the rate-limit budget right after a fill, delaying actual work (e.g., fetching new deals).
- Operators cannot audit what was asked of Hyperliquid because the queue never records the cancel payloads.
- The log noise makes it harder to see real failures and hides the underlying race that keeps scheduling the same cancel.

## Next steps

1. Stop scheduling cancels once `hyperliquid_status_history` (or the websocket event) reports a terminal state for the order.
2. When we do attempt a cancel, persist the request to `hyperliquid_submissions` before making the API call so we retain an audit trail even if the exchange rejects it.
3. Add regression coverage to assert we never emit cancel attempts for CLOIDs that are already `filled` or `canceled`.
