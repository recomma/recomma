# recomma

Recomma keeps a Hyperliquid account in sync with active 3Commas bots. It polls the 3Commas API for bot and deal updates, mirrors the resulting orders into Hyperliquid, and exposes an HTTP API plus a bundled web UI for monitoring.

## Download

1. Visit the GitHub Releases page: https://github.com/terwey/recomma/releases
2. Download the archive that matches your operating system and CPU (for example `recomma-<version>-linux-amd64.tar.gz`).
3. Extract the archive and keep the `recomma` binary somewhere convenient (inside the extracted folder or on your `PATH`).

## Configure

Set your credentials and optional tweaks with environment variables or the equivalent flags.

| Variable | Purpose | Required | Default |
| --- | --- | --- | --- |
| `THREECOMMAS_API_KEY` | 3Commas API key | Yes | - |
| `THREECOMMAS_PRIVATE_KEY` or `THREECOMMAS_PRIVATE_KEY_FILE` | Private key PEM for 3Commas signing | Yes | - |
| `HYPERLIQUID_WALLET` | Hyperliquid wallet address | Yes | - |
| `HYPERLIQUID_PRIVATE_KEY` | Hyperliquid signing key | Yes | - |
| `THREECOMMAS_API_URL` | Override the 3Commas API base URL | No | 3Commas default |
| `HYPERLIQUID_API_URL` | Hyperliquid API base URL | No | Hyperliquid testnet |
| `RECOMMA_STORAGE_PATH` | SQLite database location | No | `db.sqlite3` (next to the binary) |
| `RECOMMA_HTTP_LISTEN` | HTTP listen address | No | `:8080` |
| `RECOMMA_DEAL_WORKERS` | Concurrent deal-processing workers | No | `25` |
| `RECOMMA_ORDER_WORKERS` | Concurrent Hyperliquid emit workers | No | `5` |
| `RECOMMA_RESYNC_INTERVAL` | Deal resync interval (Go duration) | No | `15s` |
| `RECOMMA_LOG_LEVEL` | Log level (`debug`, `info`, ...) | No | `info` |
| `RECOMMA_LOG_JSON` | Set to `true` for JSON logs | No | `false` |

Flags mirror these names (for example `--threecommas-api-key`, `--http-listen`). Run `./recomma --help` to see everything that can be tuned.

## Run

```bash
export THREECOMMAS_API_KEY=...
export THREECOMMAS_PRIVATE_KEY_FILE=/path/to/3commas.pem
export HYPERLIQUID_WALLET=0x...
export HYPERLIQUID_PRIVATE_KEY=...

./recomma \
  --http-listen=":8080" \
  --storage-path="./db.sqlite3"
```

When the service starts it:

1. Opens (or creates) the SQLite database and applies the schema automatically.
2. Polls enabled 3Commas bots and queues their active deals.
3. Relays the resulting orders to Hyperliquid with built-in rate limiting.
4. Serves the HTTP API, SSE stream, and web UI on the configured port.

## Use the UI and API

- Web UI: `http://localhost:8080/`
- REST API: `http://localhost:8080/api` (contract described in `openapi.yaml`)
- SSE stream: `http://localhost:8080/sse`

## Upgrading

Stop the running binary, download the latest release archive, replace the `recomma` binary, and start it again with your existing configuration.
