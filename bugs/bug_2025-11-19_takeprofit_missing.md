# Bug Report — Deal 2386805263 TP dropped after reduce-only cancel

Source artifacts:

- Database: `2025-11-19_0307/testing5.sqlite`
- Logs: `2025-11-19_0307/full_log.jsonl`

## Summary

The DOGE take-profit mirrored from 3Commas botevent `3940649977` (deal `2386805263`) kept emitting modify actions after the safety buy landed, but Hyperliquid started returning successful responses that omitted the `response.data` body. The emitter treated that as a failure, forgot the work item, and never re-issued the TP. Minutes later Hyperliquid canceled the order with `status=reduceOnlyCanceled`, leaving the deal without any TP on the exchange even though the bot still expects a five-leg stack.

## Evidence

### Logs

```
2025-11-19_0307/full_log.jsonl:259
{"time":"2025-11-19T02:57:28.053499+01:00","level":"DEBUG","msg":"error submitting order, forgetting",...,"error":"modify error was successful but returned an error: failed to modify order: missing response.data field in successful response"}

2025-11-19_0307/full_log.jsonl:433-444
{"time":"2025-11-19T02:58:29.220146+01:00","level":"DEBUG","msg":"OrderUpdates",...,"status":"reduceOnlyCanceled","cloid":"0x00fd50a88e43c20feae18bf93f3c8a29"}
{"time":"2025-11-19T02:58:29.223944+01:00","level":"DEBUG","msg":"status","storage":{"RecordHyperliquidStatus":{"ident":{"OrderId":{"BotEventID":3940649977}}},"payload":{"status":"reduceOnlyCanceled"}}}
```

### Database

```
sqlite> select datetime(recorded_at_utc/1000,'unixepoch'), json_extract(payload_blob,'$.status')
   ... from hyperliquid_status_history where order_id='0x00fd50a88e43c20feae18bf93f3c8a29'
   ... order by recorded_at_utc desc limit 5;
2025-11-19 01:58:29|reduceOnlyCanceled
2025-11-19 01:58:29|open
2025-11-19 01:58:04|canceled
2025-11-19 01:58:04|open
2025-11-19 01:57:47|canceled

sqlite> select order_id,action_kind,datetime(updated_at_utc/1000,'unixepoch')
   ... from hyperliquid_submissions where order_id='0x00fd50a88e43c20feae18bf93f3c8a29';
0x00fd50a88e43c20feae18bf93f3c8a29|create|2025-11-19 01:58:29

sqlite> select order_id,stack_index,order_side from scaled_orders where deal_id=2386805263;
0x00fd50a88e43c20f7248b661055d7645#0#1763517194785000000|0|buy
0x00fd50a88e43c20feae18bf93f3c8a29#0#1763517195388000000|0|sell
0x00fd50a88e43c20feae18bf93f3c8a29#0#1763517391920000000|0|sell
0x00fd50a88e43c20fa9364ddce8fffac0#1#1763517391212000000|1|buy
```

## Impact

- Hyperliquid no longer has a protective TP for deal `2386805263`, so any upward move leaves the mirrored position naked.
- Fill tracker already marked the order canceled, so it will never try to recreate it unless we intervene manually.
- Because `hyperliquid_submissions` only holds the original `create`, a restart would rebuild the order at a stale size/price even if we detect the gap later.

## Next steps

1. Treat successful Hyperliquid responses that omit `response.data` as confirmation (or immediately fetch the order status) instead of forgetting the work item.
2. Persist modify payloads/cancel results so we can recreate the TP on restart.
3. Detect `reduceOnlyCanceled` statuses and re-create the TP using the latest scaler snapshot so the stack matches 3Commas expectations.

