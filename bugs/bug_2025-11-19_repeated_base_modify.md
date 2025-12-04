# Bug Report — Filled base order keeps getting modified

Source artifacts:

- Database: `2025-11-19_0307/testing5.sqlite`
- Logs: `2025-11-19_0307/full_log.jsonl`

## Summary

Deal `2386805693` base order (`cloid 0x00fd50ad8e43c3bd7248b661ee91bbdb`, botevent `1917367905`) filled on Hyperliquid at 02:01:37Z, yet the emitter kept replaying modify actions roughly every two seconds. Each attempt was rejected with `Cannot modify canceled or filled order`, logged as a failure, and the work item was forgotten. Because `threecommas_botevents` still shows the event as `Active`, the engine will continue to enqueue modify work and waste rate limit budget even though Hyperliquid already completed the trade.

## Evidence

### Logs

```
2025-11-19_0307/full_log.jsonl:760-767
{"time":"2025-11-19T03:01:38.083466+01:00","level":"DEBUG","msg":"emit","hyperliquid":{"emitter":{"orderid":"0x00fd50ad8e43c3bd7248b661ee91bbdb"},...}}
{"time":"2025-11-19T03:01:39.328085+01:00","level":"WARN","msg":"could not modify order","hyperliquid":{"emitter":{"orderid":"0x00fd50ad8e43c3bd7248b661ee91bbdb"},"error":"failed to modify order: Cannot modify canceled or filled order"}}
{"time":"2025-11-19T03:01:39.32828+01:00","level":"DEBUG","msg":"error submitting order, forgetting","order-work":{...},"error":"could not modify order: failed to modify order: Cannot modify canceled or filled order"}
... (identical WARN+DEBUG pairs repeat at 03:01:42, 03:01:47, 03:01:56, 03:02:13)
```

### Database

```
sqlite> select datetime(recorded_at_utc/1000,'unixepoch'), json_extract(payload_blob,'$.status')
   ... from hyperliquid_status_history where order_id='0x00fd50ad8e43c3bd7248b661ee91bbdb'
   ... order by recorded_at_utc;
2025-11-19 01:59:29|open
2025-11-19 02:01:37|open
2025-11-19 02:01:37|canceled
2025-11-19 02:01:37|filled

sqlite> select order_id,action_kind,datetime(updated_at_utc/1000,'unixepoch')
   ... from hyperliquid_submissions where order_id='0x00fd50ad8e43c3bd7248b661ee91bbdb';
0x00fd50ad8e43c3bd7248b661ee91bbdb|create|2025-11-19 01:59:29
```

## Impact

- Queue workers spend their slots retrying a hopeless modify, starving real work and driving `Could not modify order` spam.
- Rate limiting backs up (`produce:all-bots` stays queued) because these retries reserve capacity without making progress.
- If the process restarts it will treat the base order as unfilled (no modify payload persisted) and may re-emit bogus work.

## Next steps

1. Teach the emitter/fill tracker to stop emitting modify requests once Hyperliquid marks an order `canceled` or `filled`.
2. Persist modify attempts (even on errors) so we can detect duplicate work across restarts.
3. Consider marking the botevent as locally filled when Hyperliquid reports `filled` to prevent the engine from enqueueing more work for the same cloid.

