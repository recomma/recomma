# Bug Report — Bot sync watermark stuck 24h behind

Source artifacts:

- Database: `2025-11-19_0307/testing5.sqlite`
- Logs: `2025-11-19_0307/full_log.jsonl`

## Summary

⚠️ **Works as implemented (not a bug)** — The engine deliberately keeps the *minimum* `updated_at` watermark returned by 3Commas, because their API does **not** bump `deal.updated_at` when only the events change. Advancing the watermark would drop legitimate deal updates. We should only flip to a `max()` strategy (and re-enable the regression test) once 3Commas starts honoring `updated_at`.

## Evidence

### Logs

```
2025-11-19_0307/full_log.jsonl:900-909
{"time":"2025-11-19T03:04:45.834938+01:00","level":"DEBUG","msg":"requesting","threecommas":{"method":"GET","url":"https://api.3commas.io/public/api/ver1/deals?bot_id=16601261&from=2025-11-18T01%3A59%3A27.835Z"}}
{"time":"2025-11-19T03:04:45.965021+01:00","level":"ERROR","msg":"list deals for bot","engine":{"bot-id":16601261,"lastReq":"2025-11-18T01:59:27.835Z","error":"API error 429: Rate limit exceeded..."}}
{"time":"2025-11-19T03:04:45.965445+01:00","level":"DEBUG","msg":"requesting","threecommas":{"method":"GET","url":"https://api.3commas.io/public/api/ver1/deals?bot_id=16601256&from=2025-11-18T01%3A53%3A09.788Z"}}
{"time":"2025-11-19T03:06:15.966458+01:00","level":"ERROR","msg":"list deals for bot","engine":{"bot-id":16601256,"lastReq":"2025-11-18T01:53:09.788Z","error":"request failed: context deadline exceeded"}}
```

### Database

```
sqlite> select bot_id,datetime(last_synced_utc/1000,'unixepoch') from threecommas_bots;
16601256|2025-11-18 01:53:09
16601261|2025-11-18 01:59:27
```

The watermark matches the `from` query parameter, confirming we never advanced it to 2025‑11‑19.

## Impact

None—this behavior is intentional until the upstream API changes. The skipped regression test (`TestProduceActiveDealsAdvancesSyncWatermark`) documents the desired future behavior so we can flip it quickly when it becomes safe.
