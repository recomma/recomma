# Bug Report — Deal 2388356732 TP rejected before base filled

Source artifacts:

- Database: `2025-11-29_0123/testing29.sqlite`
- Logs: `2025-11-29_0123/full_log.jsonl`

## Summary

While mirroring deal `2388356732` (bot `16601256`), we emitted the take-profit (cloid `0x00fd50a88e5b6e7ceae18bf9bba1130a`) immediately after 3Commas announced it, even though the base order (cloid `0x00fd50a88e5b6e7c7248b66181c0ef66`) was still retrying an IOC fill. Hyperliquid rejected the TP with `Reduce only order would increase position`, the emitter logged the warning, and we never retried after the base finally filled ~5 seconds later. The deal therefore ran without any TP on the exchange.

## Evidence

### Logs

```
2025-11-29_0123/full_log.jsonl:56-78
{"msg":"emit create","engine":{"deal-id":2388356732,"botevent-id":3940649977,"latest":{"Type":"Take Profit","Size":131}}}
{"msg":"emit","hyperliquid":{"orderid":"0x00fd50a88e5b6e7ceae18bf9bba1130a","bot-event":{"Type":"Take Profit","Size":131}}}
{"time":"2025-11-29T01:14:57.981437+01:00","level":"WARN","msg":"could not submit (reduce only order, increase position), ignoring","hyperliquid":{"orderid":"0x00fd50a88e5b6e7ceae18bf9bba1130a","error":"Reduce only order would increase position. asset=173"}}

2025-11-29_0123/full_log.jsonl:107-124
{"time":"2025-11-29T01:14:58.382965+01:00","level":"INFO","msg":"IOC did not immediately match; retrying","hyperliquid":{"orderid":"0x00fd50a88e5b6e7c7248b66181c0ef66"}}
{"time":"2025-11-29T01:14:59.364177+01:00","level":"INFO","msg":"Order sent after IOC retries","hyperliquid":{"orderid":"0x00fd50a88e5b6e7c7248b66181c0ef66","requested_size":131,"executed_size":131,"hl_status":"filled"}}
```

### Database

```
sqlite> select datetime(recorded_at_utc/1000,'unixepoch') as recorded,
   ...>        json_extract(payload_blob,'$.status') as status
   ...> from hyperliquid_status_history
   ...> where order_id='0x00fd50a88e5b6e7ceae18bf9bba1130a';
2025-11-29 00:14:57|reduceOnlyRejected

sqlite> select json_extract(payload_blob,'$.status'), json_extract(payload_blob,'$.order.origSz')
   ...> from hyperliquid_status_history
   ...> where order_id='0x00fd50a88e5b6e7c7248b66181f14ff4';
open|160.0
filled|0.0
```

The TP order was permanently rejected, while the base order finally filled right afterward. The bot never emitted another TP or modify for this deal.

## Impact

- Deal `2388356732` is mirrored on Hyperliquid without any protective TP even though the 3Commas bot expects to close on a +2.27% move.
- If the position rallies, we cannot auto-close or trail; manual intervention is required.
- Additional runs will repeat the behaviour for any bot that sends the TP immediately after the base because we do not wait for a live position before issuing reduce-only sells.

## Next steps

1. Block TP submissions (and any reduce-only sell) until we confirm the base order created a positive position for the wallet, or automatically retry once the base fills.
2. Teach the emitter to detect `Reduce only order would increase position` and requeue the TP after we observe a fill instead of treating it as terminal.
3. Add a regression test to ensure we do not submit reduce-only orders while the mirrored position size is zero.
