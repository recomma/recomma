# Bug Report — Orderscaler never sees full TP stack

Source artifacts:

- Database: `2025-11-19_0307/testing5.sqlite`
- Logs: `2025-11-19_0307/full_log.jsonl`

## Summary

Whenever a new safety order arrives, `orderscaler` tries to rebuild the take-profit stack but only finds a single leg. For deal `2386805263` it expected five legs; for deal `2386805693` it expected three. Because the scaler bails out, we never enqueue the remaining TP legs on Hyperliquid and `scaled_orders` only records the initial stack index (`0`). That leaves the mirrored position under-hedged as soon as subsequent TP levels should exist.

## Evidence

### Logs

```
2025-11-19_0307/full_log.jsonl:222
{"time":"2025-11-19T02:57:26.796483+01:00","level":"DEBUG","msg":"resolve stack sizes","orderscaler":{"error":"incomplete take profit stack: expected 5 legs, got 1","deal_id":2386805263,"stack_size":5}}

2025-11-19_0307/full_log.jsonl:606/618
{"time":"2025-11-19T03:01:34.966871+01:00","level":"DEBUG","msg":"resolve stack sizes","orderscaler":{"error":"incomplete take profit stack: expected 3 legs, got 1","deal_id":2386805693,"stack_size":3}}
{"time":"2025-11-19T03:01:34.969752+01:00","level":"DEBUG","msg":"resolve stack sizes","orderscaler":{"error":"incomplete take profit stack: expected 3 legs, got 1","deal_id":2386805693,"stack_size":3}}
```

### Database

```
sqlite> select order_id,stack_index,order_side from scaled_orders where deal_id=2386805263;
0x00fd50a88e43c20f7248b661055d7645#0...|0|buy
0x00fd50a88e43c20feae18bf93f3c8a29#0...|0|sell
0x00fd50a88e43c20feae18bf93f3c8a29#0...|0|sell
0x00fd50a88e43c20fa9364ddce8fffac0#1...|1|buy

sqlite> select order_id,stack_index,order_side from scaled_orders where deal_id=2386805693;
0x00fd50ad8e43c3bd7248b661ee91bbdb#0...|0|buy
0x00fd50ad8e43c3bd7248b661ee91bbdb#0...|0|buy
0x00fd50ad8e43c3bdeae18bf9d4f047b7#0...|0|sell
0x00fd50ad8e43c3bd4183879f0dad4d9c#1...|1|buy
0x00fd50ad8e43c3bdfc49eb51cb190351#2...|2|buy
0x00fd50ad8e43c3bd21df32d42b91afae#2...|2|buy
```

Only the base (`stack_index=0`) TP exists for each deal even though the 3Commas payload describes multi-leg stacks.

## Impact

- Hyperliquid never receives the upper TP levels, so we cannot unwind the scaled-in position at the same ladder prices that 3Commas configured.
- Fill tracker/scaled_orders history misrepresents the true ladder and makes it impossible to rebuild state after a restart.
- The scaler logs error every time it runs, masking real issues.

## Next steps

1. Delay scaling until `orderscaler` sees the complete TP stack (or fetch the stack explicitly from storage) instead of assuming it rides along with each safety order event.
2. Cache TP legs per deal so repeated safety orders can reuse the previously parsed stack.
3. Add tests asserting that when 3Commas exposes N legs we persist N entries in `scaled_orders` and emit matching work to Hyperliquid.

