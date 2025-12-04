# Bug Report — Reduce-only take profits rejected after DCA fill lag

Source artifacts:

- Database: `2025-12-01_0730/testing31-rerretry.sqlite` (plus `-wal`/`-shm`)

## Summary

While replaying 3Commas deal `2388611506` (bot `16598267`) and deal `2388618197` (bot `16601256`) we sized their reduce-only take profits to the *target* stack size even though only part of the base had filled. Hyperliquid rejected both CLOIDs with `Reduce only order would increase position`, the emitter logged the warning, and no follow-up TP was placed after the remaining DCAs completed. Both deals therefore sit `status='bought'` with live exposure and no exchange-side exit order.

## Evidence

### Logs (from `app_logs` inside the snapshot)

```
sqlite> select datetime(timestamp_utc/1000,'unixepoch') ts, scope, message
   ...> from app_logs
   ...> where timestamp_utc between strftime('%s','2025-12-01 00:12:32')*1000
   ...>   and strftime('%s','2025-12-01 00:12:34')*1000;
2025-12-01 00:12:32|engine|creating take profit with tracked size
2025-12-01 00:12:32|engine|creating take profit with tracked size
2025-12-01 00:12:33|hyperliquid.emitter|could not submit (reduce only order, increase position), ignoring

sqlite> select datetime(timestamp_utc/1000,'unixepoch') ts, scope, message
   ...> from app_logs
   ...> where timestamp_utc between strftime('%s','2025-12-01 00:25:28')*1000
   ...>   and strftime('%s','2025-12-01 00:25:31')*1000;
2025-12-01 00:25:29|filltracker|recreated take profit
2025-12-01 00:25:30|hyperliquid.emitter|could not submit (reduce only order, increase position), ignoring
```

### Hyperliquid statuses

```
sqlite> select substr(order_id,1,34) order_id,
   ...>        json_extract(payload_blob,'$.status') status,
   ...>        datetime(recorded_at_utc/1000,'unixepoch') ts
   ...> from hyperliquid_status_history
   ...> where order_id in (
   ...>   '0x00fd44fb8e5f51b2eae18bf9897d88fa',
   ...>   '0x00fd50a88e5f6bd5eae18bf962145b4d');
0x00fd44fb8e5f51b2eae18bf9897d88fa|reduceOnlyRejected|2025-12-01 00:12:33
0x00fd50a88e5f6bd5eae18bf962145b4d|reduceOnlyRejected|2025-12-01 00:25:30
```

### Fill vs. TP sizing

```
sqlite> select order_id, stack_index, order_side, original_size
   ...> from scaled_orders
   ...> where deal_id=2388611506
   ...>   and order_side='sell';
0x00fd44fb8e5f51b2eae18bf9897d88fa#0#...|0|sell|1974.0

sqlite> select order_id, json_extract(payload_blob,'$.status'), datetime(recorded_at_utc/1000,'unixepoch')
   ...> from hyperliquid_status_history
   ...> where order_id in (
   ...>   '0x00fd44fb8e5f51b27248b661b31c7496',
   ...>   '0x00fd44fb8e5f51b2a32cdf1b66c5815a');
0x00fd44fb8e5f51b27248b661b31c7496|filled|2025-12-01 00:10:23
0x00fd44fb8e5f51b2a32cdf1b66c5815a|filled|2025-12-01 00:10:38
```

Only the first 320 units of the stack had filled when we sent a 1 974-unit reduce-only TP. The newer deal shows the same mismatch: `scaled_orders` sizes the TP to 136 units, but the position is still flat when the TP fires, so Hyperliquid rejects it.

## Impact

- Deals `2388611506` (1 974 units) and `2388618197` (136 units) are mirrored on Hyperliquid without any exit order even though their bots expect an automatic take-profit.
- Every subsequent run replaying a partially filled DCA reproduces the bug, so vault exposure keeps growing with no unattended close path.
- Operators must either place manual TPs or babysit fills until the full stack completes, defeating the automation.

## Next steps

1. Clamp reduce-only TP size to the cumulative filled base size (as tracked by filltracker) before submitting to Hyperliquid; expand only after new fills post.
2. When Hyperliquid returns `Reduce only order would increase position`, requeue the TP so it retries immediately after the next fill instead of treating the error as final.
3. Add regression coverage around partially filled DCAs to ensure we never emit a reduce-only order larger than the mirrored position.
