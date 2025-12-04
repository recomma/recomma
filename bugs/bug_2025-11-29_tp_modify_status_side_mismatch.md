# Bug Report — TP modify loop cancels order when HL reports side "A"

Source artifacts:

- Database: `2025-11-29_0123/testing29.sqlite`
- Logs: `2025-11-29_0123/full_log.jsonl`
- Code: `emitter/emitter.go`

## Summary

Deal `2388384150` (bot `16598267`) scaled its take-profit from 160 → 320 after opening nine safety orders, but Hyperliquid never kept the modified order live. Every modify action for cloid `0x00fd44fb8e5bd996eae18bf9bb90b398` succeeded on the exchange, which implements modify as “cancel old order, rest new one”. Right after each modify, our `confirmModifyViaStatus` fallback re-queried the WebSocket snapshot, failed to match the status to the desired order, logged `error submitting order, forgetting`, and requeued the modify. This loop cancelled and recreated the TP 6+ times, exhausted the rate limit gate, and ultimately left no TP resting even though only the base order had filled.

The root cause is `ordersMatch` (`emitter/emitter.go:705-741`) comparing the WebSocket `side` field to `"S"` for sells. Hyperliquid sends `"A"` (ask) for sell orders, so the comparison always fails and the fallback treats the modify as an error.

## Evidence

### Logs

```
2025-11-29_0123/full_log.jsonl:690-824
{"msg":"emit modify","engine":{"deal-id":2388384150,"botevent-id":3940649977,"latest":{"Size":320}}}
{"msg":"emit","hyperliquid":{"orderid":"0x00fd44fb8e5bd996eae18bf9bb90b398","orderwork":{"Action":{"Type":2,"Modify":{"Order":{"Size":320}}}}}}
{"time":"2025-11-29T01:20:06.774407+01:00","level":"DEBUG","msg":"error submitting order, forgetting","error":"modify error was successful but returned an error: queried order does not match desired state"}
... (same error repeats at 01:20:09, 01:20:12, 01:20:18, 01:20:27, 01:20:44)

2025-11-29_0123/full_log.jsonl:699-822
{"msg":"OrderUpdates","hyperliquid":{"ws":{"orders":[{"order":{"side":"A","limitPx":"0.15348","sz":"320.0","oid":44140015284},"status":"open"}, {"status":"canceled"}]}}}
{"msg":"updated order status","filltracker":{"orderid":"0x00fd44fb8e5bd996eae18bf9bb90b398","status":"canceled"}}

2025-11-29_0123/full_log.jsonl:128,553,586,609,652,674,827,849,860,882,893,915
{"level":"WARN","msg":"rate limit queue wait exceeded threshold","ratelimit":{"workflow_id":"deal:2388384150:16598267"}}
```

### Database

```
sqlite> select botevent_id,json_extract(payload,'$.Type'),json_extract(payload,'$.Status')
   ...> from threecommas_botevents where deal_id=2388384150;
... Safety botevents 1-9 all show Status='Active', only the base order and TP events exist.

sqlite> select datetime(recorded_at_utc/1000,'unixepoch') as ts,
   ...>        json_extract(payload_blob,'$.status') as status,
   ...>        json_extract(payload_blob,'$.order.side') as side,
   ...>        json_extract(payload_blob,'$.order.limitPx') as px
   ...> from hyperliquid_status_history
   ...> where order_id='0x00fd44fb8e5bd996eae18bf9bb90b398';
2025-11-29 00:15:50|open     |A|0.15357
2025-11-29 00:20:06|open     |A|0.15348
2025-11-29 00:20:06|canceled |A|0.15357
2025-11-29 00:20:08|open     |A|0.15348
2025-11-29 00:20:08|canceled |A|0.15348
... repeats through 00:20:44
```

All statuses show `side="A"`, proving the data we query cannot pass the `"S"` comparison and will never satisfy `ordersMatch`.

### Code

```
emitter/emitter.go:705-741
side := strings.ToUpper(status.Order.Side)
if desired.IsBuy {
    if side != "B" { return false }
} else {
    if side != "S" { return false } // Hyperliquid publishes "A" here
}
```

## Impact

- Deal `2388384150` never has a stable TP on Hyperliquid despite the 3Commas bot expecting one, leaving 9 safety entries without an exit plan.
- The repeated modify attempts hammer the shared rate limit queue (5 req/min tier), slowing other workflows and risking further gaps.
- Because the emitter “forgets” the work item each time, a restart will not recreate the TP unless a new botevent arrives.

## Next steps

1. Update `ordersMatch` (or normalize WebSocket payloads earlier) to treat `"A"`/`"B"` as valid sell/buy markers alongside `"S"`/`"B"`.
2. Add a unit test that feeds a `WsOrder` with `side:"A"` into `ordersMatch` to prevent regressions.
3. Once matching works, keep the last successful modify persisted so a restart can recover state without thrashing the rate gate.
