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
- Codegen: `go generate` runs `oapi-codegen` (OpenAPI -> `internal/api/ops.gen.go`) and `sqlc` (SQL -> `storage/sqlcgen`).
- Tests: `go test ./...` (notably in `engine`, `filltracker`, `storage`, `internal/api`).
- Docker: multi-stage image builds a static binary; `docker-compose.yml` has dev vs. released image profiles.
- Optional local SDK work: uncomment the `replace` directives in `go.mod` to point at sibling checkouts of `3commas-sdk-go` or `go-hyperliquid`.

## Generated Artifacts
- Do not edit generated code. Rerun `go generate ./...` after changing `openapi.yaml`, `oapi.yaml`, or anything under `storage/sqlc/`.
- The outputs land in `internal/api/ops.gen.go` and the entire `storage/sqlcgen/` directory (including `storage/sqlcgen/models.go`). Treat these files as read-only or they will be overwritten and drift from the source schema/spec.
- If you are unable to generate the code, do not edit the files anyway. Let the maintainer generate.

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
- Sqlc queries (`storage/sqlc/queries.sql`) power both API pagination and internal helpers (e.g., metadata lookups, take-profit queries).
- Storage methods publish `StreamEvent`s so the API can fan-out live updates without polling.

## API & Eventing
- OpenAPI spec lives in `openapi.yaml`; generation config is `oapi.yaml`.
- `internal/api/handler.go` implements the strict server interface, wraps vault + WebAuthn services, and exposes Hyperliquid price subscriptions.
- SSE is delivered through `internal/api/stream_controller.go`; filters support metadata prefix, bot/deal IDs, timestamps.
- Manual order actions go through the API to the queue emitter to reuse worker pacing.

## Testing & Fixtures
- Unit tests across `engine`, `filltracker`, `storage`, and `internal/api` validate diffing logic, fill tracking, and API contracts.
- `internal/testutil` hosts builders for 3Commas bot events.

## Documentation
- Keep docs-as-code: place updates in AsciiDoc under `docs/modules/ROOT/pages/`.
- Add new topics as separate pages, then wire them into `docs/modules/ROOT/nav.adoc` so Antora exposes them.
- Refresh `docs/modules/ROOT/pages/index.adoc` when high-level positioning or quick-start details change; the GitHub README will be generated from these sources.
- Note any required `go generate ./...` runs when schema or API changes land so the documentation stays tied to regenerated artifacts.

## Common Agent Tasks
- **Add/modify API fields**: update `openapi.yaml`, run `go generate ./...`, update handlers/tests accordingly.
- **Adjust storage queries**: edit `storage/sqlc/schema.sql` or `queries.sql`, run `go generate ./...`, then revise callers.
- **Debug queue behaviour**: use `engine` + `emitter` logs; consider toggling log level via `RECOMMA_LOG_LEVEL=debug`.
- **Work on SDKs**: enable `replace` entries, rebuild, and run targeted tests.
- **Local run**: `go run ./cmd/recomma --public-origin=http://localhost:8080` (or use `docker compose up recomma`).

## Misc Notes
- Web UI assets are prebuilt; avoid touching `webui/app` unless you intend to rebuild the SPA.
- Hyperliquid emitter enforces rate limiting and retries price discovery; queue backlog is expected while ratelimited.
- SSE buffers are finite; dropping events logs a warning—apply backpressure by consuming streams promptly.
- Keep generated files checked in; CI relies on repo consistency without rerunning `go generate`.

## Commit Messages
- All PR titles and commit messages must follow the Conventional Commits specification (https://www.conventionalcommits.org/).
- Start each message with a valid `<type>[optional scope]:` prefix (e.g. `feat:`, `fix(ui):`, `chore:`), keep the subject in the imperative mood, elaborate in the body.
