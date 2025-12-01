# Recomma Agent Guide

## TL;DR
- Go backend that replays 3Commas bot activity onto Hyperliquid, guarded by a WebAuthn-sealed secrets vault.
- Main binary lives in `cmd/recomma`; everything else wires queues, storage and API layers to keep state in SQLite.
- Code generation is handled via `go generate` (OpenAPI + sqlc). Run it after touching `openapi.yaml`, `oapi.yaml`, or files under `storage/sqlc`.
- Ignore `webui/app`; the compiled assets are embedded via `webui/embed.go`.

## Coding rules
- An error should never be ignored and always handled or returned to caller.

## Repository Layout
- `cmd/recomma/` — entrypoint; parses config, builds services, blocks until the vault is unsealed.
- `engine/` — polls 3Commas, diffs deal events, decides on Hyperliquid actions, and feeds work queues.
- `emitter/` — queue-backed forwarders; includes the Hyperliquid client wrapper with pacing/retry logic.
- `filltracker/` — reconstructs deal fills from stored bot events + Hyperliquid status to drive take-profit cleanup.
- `hl/` — thin Hyperliquid helpers (REST client bootstrap, websocket subscription, BBO fan-out).
- `storage/` — SQLite wrapper produced by sqlc; owns schema, queries, and SSE publication hooks.
- `internal/api/` — OpenAPI-generated handler + glue for WebAuthn, vault lifecycle, SSE streams.
- `internal/vault/` — in-memory state machine that gates startup and keeps decrypted secrets ephemeral.
- `recomma/` — shared types used across engine/emitter/storage.
- `docker/`, `Dockerfile*`, `docker-compose.yml` — container builds and local dev wiring.
- `specs/` — design docs for larger efforts (e.g., vault design) in AsciiDoc (can be linked from Antora)
- `docs/` Antora / AsciiDoc public facing documentation

## Build & Tooling
- Go toolchain: `go 1.25.0` (module is `github.com/recomma/recomma`).
- Primary binary: `go build ./cmd/recomma` or `go run ./cmd/recomma --help`.
- Codegen: `go generate ./...` runs `oapi-codegen` (OpenAPI -> `internal/api/ops.gen.go`) and `sqlc` (SQL -> `storage/sqlcgen`).
- Tests: `go test ./...` (notably in `engine`, `filltracker`, `storage`, `internal/api`).
- Docker: multi-stage image builds a static binary; `docker-compose.yml` has dev vs. released image profiles.

### Git Submodules
- Clone with submodules so the 3Commas spec is available: `git clone --recurse-submodules git@github.com:recomma/recomma.git`
- For existing clones, pull the spec by running: `git submodule update --init --recursive`

## Configuration & Secrets
- Flags/env live in `cmd/recomma/internal/config`; every flag has an env fallback (prefix `RECOMMA_...`).
- Secret material (3Commas, Hyperliquid) is not read from env at runtime; instead the WebAuthn-backed vault stores ciphertext in SQLite and unseals into memory only after login.
- `webui` serves the embedded SPA; it fetches runtime config from `/config.js` for the UI origin and debug flag.
- Local `.env` in the repo is sample data only—treat actual credentials via the vault setup flow.

## External Services
- 3Commas API (SDK: `github.com/recomma/3commas-sdk-go`) powers bot/deal polling.
- Hyperliquid REST/Websocket (`github.com/sonirico/go-hyperliquid`) handles order submission and fills.
- WebAuthn ceremonies via `github.com/go-webauthn/webauthn`; passkey flows create/consume vault sessions.

## Runtime Flow
1. Process boots, parses flags/env, configures logging, and starts HTTP + SSE handlers.
2. SQLite storage opens/initializes schema and starts streaming events to subscribers.
3. Vault controller inspects stored payload; if sealed it waits for unseal before spinning workers.
4. When unsealed, 3Commas client + Hyperliquid exchange/websocket are constructed from decrypted secrets.
5. Workqueues (deal + order) drain through worker goroutines:
   - `engine` polls bots, records deals/events, enqueues work.
   - `emitter.QueueEmitter` pushes manual actions, `emitter.HyperLiquidEmitter` executes them with pacing.
   - `filltracker` tracks order/fill state, cancels stale take-profits, exposes snapshots to the API.
6. Web API exposes cached data, vault endpoints, manual order hooks, and streams via SSE.

## Storage & Data Modeling
- SQLite schema (`storage/sqlc/schema.sql`) covers 3Commas bots/deals/events, Hyperliquid submissions/status history, vault payloads, and WebAuthn credentials.
- Sqlc queries (`storage/sqlc/queries.sql`) power both API pagination and internal helpers (e.g., OrderId lookups, take-profit queries).
- Storage methods publish `StreamEvent`s so the API can fan-out live updates without polling.

## API & Eventing
- OpenAPI spec lives in `openapi.yaml`; generation config is `oapi.yaml`.
- `internal/api/handler.go` implements the strict server interface, wraps vault + WebAuthn services, and exposes Hyperliquid price subscriptions.
- SSE is delivered through `internal/api/stream_controller.go`; filters support OrderID prefix, bot/deal IDs, timestamps.
- Manual order actions go through the API to the queue emitter to reuse worker pacing.

## Testing & Fixtures
- Unit tests across `engine`, `filltracker`, `storage`, and `internal/api` validate diffing logic, fill tracking, and API contracts.
- `internal/testutil` hosts builders for 3Commas bot events.

### Testing Philosophy: Validate Contracts, Not Implementation

**Tests must validate the documented contract (spec + godoc), never the implementation.**

When writing tests:
1. **Read the godoc first** - the godoc defines the expected behavior
2. **If godoc is unclear, fix it first** - vague godoc leads to tests that follow buggy implementation
3. **Tests prove the contract holds** - they should fail when behavior doesn't match documentation

**Anti-pattern:** Reading implementation code to understand what to test
- This creates tests that validate bugs instead of catching them
- Example: A test that "works around" a deadlock instead of failing on it

**Correct pattern:** Reading godoc/spec to understand expected behavior
- Write tests that verify the documented contract
- If implementation doesn't match, the test fails (exposing the bug)

#### Case Study: Rate Limiter Deadlock

The `ratelimit` package had a deadlock bug that went undetected because:

1. **Spec** (`specs/rate_limit.adoc`): Correctly stated "waiting workflows are immediately re-evaluated when window resets" ✅
2. **Godoc** (`ratelimit/limiter.go`): Too vague - "Blocks until granted or context cancelled" ❌
3. **Implementation**: Didn't auto-wake on window resets (bug) ❌
4. **Tests**: Followed buggy implementation instead of spec/godoc ❌

The test `TestLimiter_WindowReset` originally required a 3rd workflow to "poke" the limiter:
```go
// workflow-1 exhausts capacity and releases
// workflow-2 tries to reserve and queues
time.Sleep(600 * time.Millisecond) // Window resets
// workflow-3 starts - THIS IS THE WORKAROUND ❌
// workflow-3's Reserve() triggers reset detection
// This wakes up workflow-2
```

**The test validated the bug instead of exposing it.**

The fix required:
1. **Update godoc** to be explicit: "automatically detected by background ticker"
2. **Fix implementation** to match spec and godoc
3. **Rewrite test** to validate the contract (auto-wake without 3rd workflow)

### Godoc as Contract

**Godoc is the bridge between specification and implementation.**

When godoc is clear:
- Developers know what behavior to implement
- Test authors know what behavior to verify
- Users know what behavior to expect

When godoc is vague:
- Implementation may diverge from spec
- Tests follow implementation bugs
- Users face surprising behavior

**Writing good godoc:**
- Be **explicit** about timing ("blocks until", "immediately", "automatically")
- Document **all conditions** that trigger state changes
- Specify **exactly when** async operations complete
- Include **edge cases** (empty queues, window resets, context cancellation)

**Example of vague godoc:**
```go
// Reserve requests slots. Blocks until granted or cancelled.
```

**Example of clear godoc:**
```go
// Reserve requests N slots for a workflow.
//
// If capacity is immediately available, the reservation is granted and returns.
// If exhausted, the request queues and blocks until:
//   - Capacity becomes available through Release() or AdjustDown()
//   - The rate limit window resets (automatically detected by background ticker)
//   - The context is cancelled
//
// Queued workflows are woken in FIFO order.
```

The second version makes it **impossible** to write a test that ignores auto-wake behavior.

## Documentation
- Keep docs-as-code: place updates in AsciiDoc under `docs/modules/ROOT/pages/`.
- Add new topics as separate pages, then wire them into `docs/modules/ROOT/nav.adoc` so Antora exposes them.
- Refresh `docs/modules/ROOT/pages/index.adoc` when high-level positioning or quick-start details change; the GitHub README will be generated from these sources.
- Note any required `go generate ./gen/api` runs when schema or API changes land so the documentation stays tied to regenerated artifacts.

## Common Agent Tasks
- **Add/modify API fields**: update `openapi.yaml`, run `go generate ./gen/api`, update handlers/tests accordingly.
- **Adjust storage queries**: edit `storage/sqlc/schema.sql` or `queries.sql`, run `go generate ./gen/storage`, then revise callers.
- **Debug queue behaviour**: use `engine` + `emitter` logs; consider toggling log level via `RECOMMA_LOG_LEVEL=debug`.
- **Work on SDKs**: enable `replace` entries, rebuild, and run targeted tests.
- **Local run**: `go run ./cmd/recomma --public-origin=http://localhost:8080` (or use `docker compose up recomma`).
- **Bug reports**: whenever you uncover a defect from a snapshot/log review, document it under `./bugs/bug_<yyyy-mm-dd>_<slug>.md` using the existing format (source artifacts, summary, evidence, impact, next steps) so the maintainer can triage it later.

### Generated Artifacts
- Do not edit generated code. Rerun the appropriate `go generate` after changing `openapi.yaml`, `oapi.yaml`, or anything under `storage/sqlc/`.
- The outputs land in `internal/api/ops.gen.go` and the entire `storage/sqlcgen/` directory (including `storage/sqlcgen/models.go`). Treat these files as read-only or they will be overwritten and drift from the source schema/spec.
- If you are unable to generate the code, do not edit the files anyway. Let the maintainer generate.

## Misc Notes
- Web UI assets are prebuilt; avoid touching `webui/app` unless you intend to rebuild the SPA.
- Hyperliquid emitter enforces rate limiting and retries price discovery; queue backlog is expected while ratelimited.
- SSE buffers are finite; dropping events logs a warning—apply backpressure by consuming streams promptly.
- Keep generated files checked in; CI relies on repo consistency without rerunning `go generate`.
- The default Hyperliquid venue must be resolved via `storage.ResolveDefaultAlias`; do not hard-code `hyperliquid:default` or its wallet—always derive the identifier from storage before emitting work or recording submissions.

### Debugging From a Provided SQLite Snapshot
When the user shares a `.sqlite` capture, follow this checklist so we can replay exactly what they saw:
1. **Inspect the schema/tables**: `sqlite3 <db> '.tables'` and note whether WAL files (`-wal`, `-shm`) are present; copy them too so no state is lost.
2. **Dump the logs**: `sqlite3 <db> "SELECT datetime(timestamp_utc/1000,'unixepoch'), level, scope, message FROM app_logs ORDER BY timestamp_utc;"` filters quickly (`scope IN (...)`) when we just need filltracker/emitter activity.
3. **Check Hyperliquid state**: query both `hyperliquid_status_history` and `hyperliquid_submissions` for the CLOIDs/DealIDs in question to see what was “live” when the snapshot was taken.
4. **Look for outstanding safeties**: use `SELECT DISTINCT order_id FROM hyperliquid_status_history WHERE json_extract(payload_blob,'$.status')='open'` to confirm whether any buys were still in flight (this determines `AllBuysFilled`).
5. **Annotate findings**: reference the sqlite file name in every bug report or summary so we can reproduce later (e.g. “using `2025-11-30_1354/testing31-retry.sqlite`”).
6. **Never mutate the source DB**: copy it before running destructive queries so the user can re-run the same analysis if needed.

## Commits
Prefer smaller commits instead of one massive one. This allows to track the changes more easily.
Add a co-authored to the commit message, use your own verified email if present else agent@terwey.me

   Co-authored-by: Codex <agent@terwey.me>

### Commit Messages
- All PR titles and commit messages must follow the Conventional Commits specification (https://www.conventionalcommits.org/).
- Start each message with a valid `<type>[optional scope]:` prefix (e.g. `feat:`, `fix(ui):`, `chore:`), keep the subject in the imperative mood, elaborate in the body.
