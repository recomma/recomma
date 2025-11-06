# Recomma - Exhaustive Architectural Review

**Date:** 2025-11-06
**Status:** Pre-Production
**Scope:** Major architectural and design issues

---

## Executive Summary

Recomma is a trading orchestration system that mirrors 3Commas bot orders to Hyperliquid. The codebase (~21k LOC Go) is well-structured but has several **critical architectural issues** that should be addressed before production deployment. The system uses SQLite for persistence, WebAuthn for authentication, and work queues for order processing.

**Key Concerns:**
- **Single-threaded database bottleneck** due to SQLite with MaxOpenConns=1
- **Missing distributed system considerations** (no leader election, split-brain scenarios)
- **State synchronization gaps** between 3Commas and Hyperliquid
- **No circuit breakers** or backpressure mechanisms for external APIs
- **Complex stateful logic** spread across multiple components
- **Unclear failure recovery** semantics for partially-executed orders

---

## 1. Architecture & System Design

### 1.1 Single-Point-of-Failure Database Architecture

**Location:** `storage/storage.go:58-61`

```go
db.SetMaxOpenConns(1)
db.SetMaxIdleConns(1)
db.SetConnMaxLifetime(0)
```

**Issue:** SQLite is configured with a single connection, making it a global bottleneck. Every operation across all workers (deal workers, order workers, fill tracker, API handlers) must serialize through this single database connection.

**Impact:**
- All concurrent operations are serialized by the database mutex
- High latency under load as workers wait for database access
- No horizontal scaling possible
- Single point of failure with no replication

**Recommendation:**
- **Short-term:** Document that this is a single-instance system only
- **Medium-term:** Consider PostgreSQL with connection pooling
- **Long-term:** Move to proper distributed storage with replication

### 1.2 No Distributed System Design

**Location:** `cmd/recomma/main.go` (entire application)

**Issue:** The application assumes it's the only instance running. There's no:
- Leader election mechanism
- Distributed locking
- Instance coordination
- Split-brain prevention

**Scenarios:**
1. User deploys two instances accidentally → both poll 3Commas → duplicate orders
2. Kubernetes restarts pod during order processing → lost in-flight work
3. Network partition → no detection or recovery

**Impact:**
- Cannot run multiple instances for high availability
- No graceful failover capabilities
- Risk of duplicate order submissions causing financial loss

**Recommendation:**
- Implement distributed locking (Redis, etcd, or PostgreSQL advisory locks)
- Add instance ID and leadership election
- Document single-instance requirement clearly

### 1.3 Missing Event Sourcing / Audit Log

**Location:** `storage/storage.go` (schema)

**Issue:** The system maintains mutable state without a complete event log. While there are:
- `threecommas_botevents_log` (all events observed)
- `threecommas_botevents` (acted-upon events)
- `hyperliquid_submissions` (upsert pattern)

The submission table uses **upsert semantics** which overwrites history:

```go
// UpsertHyperliquidCreate overwrites the create payload
func (s *Storage) RecordHyperliquidOrderRequest(...)
```

**Impact:**
- Cannot reconstruct full history of what happened
- Difficult to debug "why did this order change?"
- No way to replay events for recovery
- Compliance/audit trail gaps

**Recommendation:**
- Make all operations append-only
- Add sequence numbers to all events
- Implement proper event sourcing pattern
- Add CQRS for read views if needed

### 1.4 Polling-Based Architecture Limitations

**Location:** `cmd/recomma/main.go:332-365` (resync ticker)

**Issue:** The system polls 3Commas every 60 seconds. This creates:
- **Latency:** Up to 60s delay in detecting new orders
- **API waste:** Fetching data that hasn't changed
- **Rate limit pressure:** Constant polling even when idle
- **Backlog accumulation:** If processing takes >60s, work accumulates

**Current Implementation:**
```go
resync := cfg.ResyncInterval  // default 60s
go func() {
    ticker := time.NewTicker(resync)
    defer ticker.Stop()
    for {
        select {
        case <-ticker.C:
            produceOnce(appCtx)        // No backpressure check!
            reconcileTakeProfits(appCtx)
        }
    }
}()
```

**Impact:**
- Orders execute with up to 60s delay
- Cannot react to fast market conditions
- May overwhelm queue if processing is slow
- No adaptive polling based on activity

**Recommendation:**
- Add **backpressure detection** (check queue depth before producing more work)
- Implement **adaptive polling** (faster when active, slower when idle)
- Consider 3Commas webhooks if available
- Add circuit breakers for failure scenarios

---

## 2. State Management & Consistency

### 2.1 Fill Tracker In-Memory State Synchronization

**Location:** `filltracker/service.go:29-44`

**Issue:** Fill tracker maintains critical position state in memory:

```go
type Service struct {
    mu     sync.RWMutex
    orders map[string]*orderState  // In-memory only!
    deals  map[uint32]*dealState
}
```

**Problems:**
1. **State lost on restart** → Must rebuild from storage (slow, error-prone)
2. **No persistence of computed state** → Position calculations redone every restart
3. **Race conditions** between rebuild and new updates
4. **Memory leak** → No cleanup of old deals
5. **No bounds checking** → Unlimited growth

**Impact:**
- Restart causes ~10-30s gap where fill tracking is unavailable
- During rebuild, orders may be submitted with wrong sizes
- Long-running instances accumulate unbounded state
- Hard to debug position mismatches

**Recommendation:**
- **Persist aggregated state** to database
- **Implement state expiration** (archive deals older than X days)
- **Add state validation** against source of truth
- **Implement state machine** with explicit transitions
- **Add memory limits** and LRU eviction

### 2.2 Order Scaling State Not Versioned

**Location:** `engine/orderscaler/service.go:88-195`

**Issue:** Order scaling decisions are made using the current multiplier, but:
- Multiplier can change between event processing and order submission
- No versioning or "decided-at" timestamp
- No way to know what multiplier was active when decision was made

**Example Flow:**
1. Bot event arrives → multiplier is 1.5
2. User changes multiplier to 2.0 via API
3. Order submitted with multiplier 2.0
4. Audit trail shows different value than what triggered the action

**Impact:**
- Confusing audit trails
- Cannot reproduce decisions
- Race conditions between config changes and order processing

**Recommendation:**
- Capture effective multiplier at decision time
- Store it with the order metadata
- Make scaling decisions immutable once made
- Add config versioning

### 2.3 Event Reduction Algorithm Complexity

**Location:** `engine/engine.go:300-379` (`reduceOrderEvents`)

**Issue:** Complex stateful reduction logic with multiple edge cases:

```go
func (e *Engine) reduceOrderEvents(...) {
    // Load all events for this order
    // Find previous distinct state
    // Load Hyperliquid submission
    // Decide action based on complex state machine

    // Multiple guard conditions:
    if !hasLocalOrder {
        prev = nil  // Pretend no previous!
    }

    // Guard against modify-before-create
    if !hasLocalOrder {
        // Fall back to create
    }

    // Skip cancel if never created locally
    // ...
}
```

**Problems:**
1. Hard to reason about all state combinations
2. No formal specification of the state machine
3. Edge cases discovered in production
4. Testing requires extensive fixture setup
5. Implicit state (what if submission exists but status doesn't?)

**Impact:**
- Bugs in order state transitions
- Difficulty adding new order types
- Fragile to 3Commas API changes
- Hard to maintain

**Recommendation:**
- **Extract to explicit state machine** (use state pattern or enum-based approach)
- **Document all transitions** with diagrams
- **Add property-based testing** for state transitions
- **Simplify by separating concerns** (detection vs. action vs. submission)

### 2.4 Take-Profit Reconciliation Complexity

**Location:** `filltracker/service.go:153-180` (`ReconcileTakeProfits`)

**Issue:** Take-profit reconciliation involves complex logic:
- Check if all buys filled
- Calculate net position
- Find existing TP orders
- Decide: cancel, modify, or create
- Handle missing metadata
- Fall back to storage lookups

**Problems:**
1. **Race conditions:** Position changes while reconciling
2. **Partial failures:** Cancel succeeds but create fails
3. **Missing orders:** What if TP exists on exchange but not in tracker?
4. **Idempotency:** Running twice may cause issues
5. **No retry logic:** Fails silently

**Impact:**
- Take-profits may be wrong size
- Manual intervention required
- Position leakage possible

**Recommendation:**
- Make TP reconciliation **idempotent**
- Add **optimistic locking** on position state
- **Separate detection from action** (two-phase)
- Add **reconciliation metrics** and alerts
- Implement **self-healing** checks

---

## 3. Concurrency & Threading

### 3.1 Global Database Mutex Creates Contention

**Location:** `storage/storage.go:28-32`

```go
type Storage struct {
    db      *sql.DB
    queries *sqlcgen.Queries
    mu      sync.Mutex  // Global lock!
    stream  api.StreamPublisher
}
```

**Issue:** Nearly every storage operation takes the global mutex:

```go
func (s *Storage) RecordThreeCommasBotEvent(...) {
    s.mu.Lock()
    defer s.mu.Unlock()
    // Do database work
}
```

**Impact:**
- All workers serialize on this lock
- Order workers, deal workers, API handlers all blocked
- High lock contention under load
- P99 latency spikes

**Why It Exists:** SQLite limitation (single writer) + stream publishing needs atomicity

**Recommendation:**
- **Remove global mutex** (SQLite handles its own locking)
- **Separate stream publishing** (use channels or separate lock)
- **Batch operations** where possible
- **Use IMMEDIATE transactions** to reduce lock time

### 3.2 Worker Pool Starvation

**Location:** `cmd/recomma/main.go:320-330`

```go
for i := 0; i < cfg.OrderWorkers; i++ {
    wg.Add(1)
    go runOrderWorker(workerCtx, &wg, oq, submitter)
}
```

**Issue:** Fixed-size worker pools with no dynamic scaling. Configuration:
- 4 deal workers (default)
- 4 order workers (default)

**Problems:**
1. **Slow deals block everything:** One long-running GetDealForID() blocks a worker
2. **No priority queuing:** Important orders wait behind low-priority work
3. **No fair scheduling:** One bot with 100 deals starves other bots
4. **Fixed sizing:** Cannot adapt to load

**Impact:**
- Under high load, queues back up
- No way to prioritize urgent orders
- Bursty behavior during resyncs

**Recommendation:**
- **Implement priority queues** (safety orders before TPs)
- **Add per-bot fairness** (round-robin between bots)
- **Dynamic worker pool** sizing based on queue depth
- **Separate fast/slow paths** (different workers for different operations)

### 3.3 No Timeout on External API Calls

**Location:** `engine/engine.go:187-193`

```go
func (e *Engine) HandleDeal(ctx context.Context, wi WorkKey) error {
    deal, err := e.client.GetDealForID(ctx, tc.DealPathId(dealID))
    // What if this hangs for 5 minutes?
}
```

**Issue:** While contexts exist, the actual timeout is:

```go
reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
```

But there's **no global circuit breaker** for:
- 3Commas API being down
- Hyperliquid API being slow
- Network issues

**Impact:**
- Workers stuck waiting for timeouts
- Queue backlog accumulates
- No automatic degradation

**Recommendation:**
- **Implement circuit breakers** (sony/gobreaker, or similar)
- **Add bulkheads** (separate thread pools per external service)
- **Fail fast** when service is known to be down
- **Shed load** when falling behind

### 3.4 Rate Limiter Is Too Simplistic

**Location:** `emitter/emitter.go:125-142` (`waitTurn`)

```go
func (e *HyperLiquidEmitter) waitTurn(ctx context.Context) error {
    for {
        e.mu.Lock()
        wait := time.Until(e.nextAllowed)
        if wait <= 0 {
            e.nextAllowed = time.Now().Add(e.minSpacing)  // 300ms
            e.mu.Unlock()
            return nil
        }
        e.mu.Unlock()
        // Wait and retry
    }
}
```

**Problems:**
1. **Global pacing** doesn't account for:
   - Different endpoints having different limits
   - Burst allowances
   - Token bucket algorithms
   - Time-window based limits (10 req/second, not 1 per 300ms)

2. **Cooldown is naive:**
   ```go
   if strings.Contains(err.Error(), "429") {
       e.applyCooldown(10 * time.Second)  // Fixed 10s
   }
   ```

**Impact:**
- Inefficient use of rate limit budget
- Cannot burst when needed
- Overly conservative
- No per-coin limits (Hyperliquid may rate limit per asset)

**Recommendation:**
- **Implement token bucket** rate limiter
- **Support burst capacity**
- **Exponential backoff** for 429s
- **Per-resource rate limiting** if needed
- Use a library (golang.org/x/time/rate)

---

## 4. Database & Persistence

### 4.1 No Transaction Boundaries for Multi-Step Operations

**Location:** `engine/engine.go:241-254`

**Issue:** Multi-step operations aren't atomic:

```go
func (e *Engine) processDeal(...) {
    for _, event := range events {
        // Step 1: Record event log (one transaction)
        _, err := e.store.RecordThreeCommasBotEventLog(ctx, oid, event)

        // Step 2: Record acted-upon event (another transaction)
        lastInsertedId, err := e.store.RecordThreeCommasBotEvent(ctx, oid, event)

        // Step 3: Later, emit order (yet another transaction)
        e.emitter.Emit(ctx, work)
    }
}
```

**Problems:**
- Crash between steps = partial state
- Cannot rollback on later failure
- Inconsistent views during processing

**Impact:**
- Database contains partial work
- Restart may see incomplete state
- Difficult to reason about what's committed

**Recommendation:**
- **Use transactions** for multi-step operations
- **Implement saga pattern** for distributed transactions
- **Add idempotency keys** for external operations
- **Store intent before execution** (two-phase commit)

### 4.2 No Database Schema Migrations

**Location:** `storage/storage.go:65-68`

```go
if _, err := db.ExecContext(ctx, schemaDDL); err != nil {
    db.Close()
    return nil, fmt.Errorf("apply schema: %w", err)
}
```

**Issue:** Schema is applied directly with no versioning:
- No way to upgrade production databases
- No rollback mechanism
- Breaking changes require manual intervention
- No tracking of what version is deployed

**Impact:**
- Cannot deploy schema changes safely
- Downtime required for schema updates
- Risk of data loss during manual migrations

**Recommendation:**
- **Add migration framework** (golang-migrate, goose, or atlas)
- **Version the schema**
- **Support forward and backward migrations**
- **Test migrations in CI**

### 4.3 Unbounded Storage Growth

**Location:** All storage tables

**Issue:** No cleanup or archival strategy:
- `threecommas_botevents_log` grows forever
- `hyperliquid_statuses` grows forever
- Old deals never cleaned up
- No partitioning or retention policy

**Impact:**
- Database grows without bound
- Queries slow down over time
- Disk fills up eventually
- Restart takes longer (fill tracker rebuild)

**Recommendation:**
- **Implement retention policies** (archive after 90 days)
- **Add cleanup jobs** for old data
- **Partition large tables** by date
- **Export to cold storage** for long-term retention

### 4.4 No Database Backup Strategy

**Issue:** No documented backup/restore process for SQLite.

**Impact:**
- Data loss on disk failure
- No point-in-time recovery
- Cannot replicate to standby

**Recommendation:**
- **Implement continuous backup** (WAL archival + periodic snapshots)
- **Document restore process**
- **Test recovery procedures**
- **Consider cloud-native database** (RDS, Cloud SQL) for production

---

## 5. Error Handling & Observability

### 5.1 Errors Are Logged But Not Surfaced

**Location:** Throughout codebase

**Example:** `engine/engine.go:289-290`

```go
if err := e.emitter.Emit(ctx, work); err != nil {
    e.logger.Warn("could not submit order", ...)  // Logged and forgotten!
}
```

**Problems:**
1. **Silent failures:** Errors logged but execution continues
2. **No metrics:** Can't alert on error rate
3. **No dead letter queue:** Failed work is lost
4. **No retry tracking:** Don't know if retries are exhausted

**Impact:**
- Orders silently not submitted
- No visibility into failure rate
- Cannot debug issues in production

**Recommendation:**
- **Add metrics** (Prometheus/StatsD)
- **Implement DLQ** for failed work items
- **Surface errors to UI** (show failed orders)
- **Add alerting** on error thresholds
- **Track retry attempts** explicitly

### 5.2 No Structured Tracing

**Issue:** No distributed tracing (OpenTelemetry, Jaeger, etc.)

**Impact:**
- Cannot trace a deal through the entire pipeline
- No visibility into latency breakdown
- Hard to debug slow operations
- Cannot see dependencies between services

**Recommendation:**
- **Add OpenTelemetry tracing**
- **Propagate trace context** through workers
- **Instrument all external calls**
- **Add custom spans** for business logic

### 5.3 Insufficient Health Checks

**Location:** No dedicated health check endpoints

**Issue:** No way to determine if system is healthy:
- Is vault unsealed?
- Are workers processing?
- Is queue backed up?
- Is Hyperliquid reachable?

**Impact:**
- Cannot use proper Kubernetes liveness/readiness probes
- No automated recovery
- Manual monitoring required

**Recommendation:**
- **Add /healthz endpoint** (liveness: is process alive?)
- **Add /readyz endpoint** (readiness: can serve traffic?)
- **Include component health** (DB, vault, external APIs)
- **Add /debug endpoints** (queue depth, worker status)

### 5.4 Limited Metrics

**Issue:** No exported metrics for:
- Queue depth
- Worker utilization
- Order submission rate/latency
- Fill tracker state size
- API error rates
- Rate limit headroom

**Recommendation:**
- **Add Prometheus metrics**
- **Instrument all critical paths**
- **Add business metrics** (orders per minute, volume)
- **Export queue metrics**

---

## 6. Scalability & Performance

### 6.1 No Load Shedding

**Issue:** System tries to process all work even when falling behind.

**Impact:**
- Queue fills up indefinitely
- No graceful degradation
- Eventually crashes due to memory

**Recommendation:**
- **Check queue depth before producing**
- **Skip non-essential work** when backlogged
- **Implement backpressure** signals
- **Reject new work** when at capacity

### 6.2 Inefficient Polling Pattern

**Location:** `engine/engine.go:87-178` (`ProduceActiveDeals`)

**Issue:** Fetches all deals for all bots every 60 seconds:

```go
deals, err := e.client.GetListOfDeals(callCtx,
    tc.WithBotIdForListDeals(b.Id),
    tc.WithFromForListDeals(lastReq),  // Last 24 hours by default!
)
```

**Problems:**
1. Fetches same deals repeatedly
2. No incremental updates
3. Network overhead
4. API quota waste

**Recommendation:**
- **Use cursor-based pagination** if available
- **Track per-deal update timestamps**
- **Only fetch updated deals**
- **Implement change data capture** if API supports it

### 6.3 N+1 Query Pattern in Fill Tracker

**Location:** `filltracker/service.go:495-557` (`reloadDeal`)

**Issue:** Rebuild loads orders one at a time:

```go
for _, oid := range oids {
    event, err := s.store.LoadThreeCommasBotEvent(ctx, oid)    // Query 1
    status, found, err := s.store.LoadHyperliquidStatus(ctx, oid)  // Query 2
    scaledOrders, err := s.store.ListScaledOrdersByOrderId(ctx, oid)  // Query 3
}
```

**Impact:**
- Slow restarts
- Database load spikes on startup
- Long rebuild times for deals with many orders

**Recommendation:**
- **Batch load operations**
- **Add bulk query methods**
- **Cache frequently accessed data**

### 6.4 Memory Leaks in Long-Running Processes

**Location:** Multiple places

**Potential Leaks:**
1. Fill tracker `deals` map never cleaned up
2. Stream controller subscriber map grows
3. No limits on queue sizes
4. Unbounded context values

**Recommendation:**
- **Add memory profiling** (pprof endpoints)
- **Implement LRU caching** with size limits
- **Clean up old data** periodically
- **Monitor memory usage** in production

---

## 7. Security & Configuration

### 7.1 No Secrets Rotation Strategy

**Location:** `internal/vault/controller.go`

**Issue:** Secrets are sealed/unsealed but:
- No rotation mechanism
- No expiration
- Cannot update without restart
- Session doesn't expire

**Recommendation:**
- **Implement secret rotation**
- **Add session expiration**
- **Support hot-reload** of credentials
- **Audit secret access**

### 7.2 Debug Mode Is Risky

**Location:** `internal/debugmode/enabled.go`

**Issue:** Debug mode loads secrets from environment variables:

```go
func LoadSecretsFromEnv() (*vault.Secrets, error) {
    // Loads from env vars - bypasses WebAuthn!
}
```

**Problems:**
- Easy to accidentally enable in production
- Secrets in environment (visible in logs, process list)
- No authentication required

**Recommendation:**
- **Remove debug mode** or make it compile-time only
- **Never load secrets from env in production builds**
- **Add explicit warning** if debug mode is used
- **Require separate build tags**

### 7.3 No Input Validation on API

**Location:** `internal/api/handler.go` (various endpoints)

**Issue:** Limited validation on user inputs:
- No bounds checking on bot IDs
- No validation of multipliers beyond max
- No rate limiting on API calls
- No authentication on some endpoints

**Example:** `handler.go:550-555`

```go
func normalizeBotID(raw int64) (uint32, bool) {
    if raw <= 0 || raw > math.MaxUint32 {
        return 0, false
    }
    return uint32(raw), true
}
```

But no check for reasonable ranges (e.g., bot ID > 1 trillion is suspicious)

**Recommendation:**
- **Add comprehensive input validation**
- **Validate business constraints**
- **Rate limit API calls**
- **Add CSRF protection**

### 7.4 Insufficient Audit Logging

**Issue:** No audit trail for:
- Who changed order scaler settings?
- Who canceled orders manually?
- When was vault unsealed?
- Who accessed secrets?

**Recommendation:**
- **Add audit log table**
- **Log all mutations**
- **Include actor and timestamp**
- **Make audit log tamper-evident**

---

## 8. Code Organization & Modularity

### 8.1 God Object: `main.go`

**Location:** `cmd/recomma/main.go` (500 lines)

**Issue:** Main function does everything:
- Config parsing
- Service initialization
- HTTP server setup
- Worker management
- Graceful shutdown

**Impact:**
- Hard to test
- Difficult to refactor
- Cannot reuse components
- Hard to understand flow

**Recommendation:**
- **Extract application struct** with lifecycle methods
- **Separate concerns** (config, setup, run, shutdown)
- **Make testable** without full integration
- **Improve modularity**

### 8.2 Circular Dependencies

**Issue:** Some packages depend on each other implicitly through interfaces:

- `engine` → `emitter` (interface)
- `emitter` → `storage` → streams → `api` types
- `filltracker` → `recomma` types → `adapter`

**Recommendation:**
- **Define clear boundaries**
- **Use dependency injection**
- **Move shared types to common package**
- **Draw architecture diagrams**

### 8.3 Large Functions

**Examples:**
- `engine.processDeal`: 100+ lines
- `emitter.Emit`: 200+ lines
- `filltracker.reconcileActiveTakeProfit`: complex logic

**Impact:**
- Hard to test
- Hard to understand
- Difficult to modify
- High cyclomatic complexity

**Recommendation:**
- **Extract methods** for clarity
- **Use strategy pattern** for complex branches
- **Add unit tests** for extracted functions

### 8.4 Missing Domain Models

**Issue:** Business logic scattered across multiple layers:
- Order state transitions in `adapter`
- Fill tracking in `filltracker`
- Scaling in `orderscaler`
- Submission in `emitter`
- Persistence in `storage`

**Recommendation:**
- **Define domain entities** (Deal, Order, Position)
- **Encapsulate behavior** in domain objects
- **Use repository pattern** for persistence
- **Separate domain from infrastructure**

---

## 9. Testing & Quality

### 9.1 Limited Test Coverage

**Observation:** Some packages have tests, but:
- No integration tests for full flow
- No property-based tests for state machines
- No chaos/failure testing
- Limited edge case coverage

**Recommendation:**
- **Add integration tests** for key flows
- **Use property-based testing** (rapid)
- **Test failure scenarios** explicitly
- **Add benchmark tests**

### 9.2 No Contract Tests for External APIs

**Issue:** 3Commas and Hyperliquid APIs may change without notice.

**Recommendation:**
- **Add contract tests** (Pact or similar)
- **Mock external APIs** for testing
- **Test API version changes**
- **Monitor API deprecations**

---

## 10. Critical Recommendations by Priority

### P0 - Must Fix Before Production

1. **Add distributed locking** to prevent multiple instances
2. **Implement proper error handling** and retries
3. **Add metrics and alerting**
4. **Document single-instance limitation**
5. **Add health check endpoints**
6. **Implement backup strategy**

### P1 - Fix Within First Month

1. **Add circuit breakers** for external APIs
2. **Implement state persistence** for fill tracker
3. **Add database migrations**
4. **Improve rate limiting**
5. **Add load shedding**
6. **Remove or secure debug mode**

### P2 - Architectural Improvements

1. **Refactor to event sourcing**
2. **Extract domain models**
3. **Improve modularity**
4. **Add tracing**
5. **Implement data retention**

### P3 - Performance & Scale

1. **Move to PostgreSQL**
2. **Add caching layer**
3. **Optimize queries**
4. **Implement horizontal scaling**

---

## 11. Positive Aspects

Despite the issues, the codebase has many strengths:

✅ **Well-structured packages** with clear responsibilities
✅ **Good use of interfaces** for testability
✅ **Comprehensive logging** throughout
✅ **Proper context usage** for cancellation
✅ **Type-safe code generation** (sqlc, oapi-codegen)
✅ **Graceful shutdown** handling
✅ **Work queue pattern** for reliability
✅ **Detailed comments** in complex areas

---

## 12. Conclusion

Recomma has a solid foundation but needs **significant architectural work** before production deployment. The main concerns are:

1. **Reliability:** No HA, no recovery, silent failures
2. **Scalability:** Single-threaded bottlenecks, no horizontal scaling
3. **Observability:** Limited metrics, no tracing, poor error surfacing
4. **State Management:** Complex in-memory state, consistency issues

**Recommendation:** Spend 4-6 weeks addressing P0/P1 issues before any production deployment handling real money.

---

**Reviewer:** Claude (Sonnet 4.5)
**Lines Reviewed:** ~21,000 LOC across 50+ files
**Focus:** Architecture, design, scalability, reliability
