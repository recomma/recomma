# Recomma

Recomma bridges your 3Commas bots to Hyperliquid. It watches every order 3Commas plans, mirrors the same intent on Hyperliquid, and cleans up take-profit orders once all safety legs are filled. A built-in web app guides you through setup with passkeys and keeps your API secrets safe.

## What You Need
- An active 3Commas account with bots you want to mirror.
- A Hyperliquid wallet and private key.
- A machine that can run Docker (Linux, macOS, or Windows with WSL).
- A URL or hostname you control (for example `https://recomma.example.com`). Passkeys will refuse to work if this doesn’t match your real browser origin.

## How Recomma Helps
1. Polls your enabled 3Commas bots and captures new deals and order updates.
2. Replays the same creates, modifies, and cancels on Hyperliquid with built-in pacing so you stay within venue limits.
3. Stores an encrypted copy of your API keys, unseals them only after you authenticate with a passkey, and shows live status through the web UI.

## Quick Start (Docker)
1. Pull the container image:
   ```bash
   docker pull ghcr.io/recomma/recomma:latest
   ```
2. Create a `.env` file next to where you run Docker:
   ```env
   # where users will access the UI – must match exactly (protocol + host + optional port)
   RECOMMA_PUBLIC_ORIGIN=https://recomma.example.com
   
   # optional tweaks
   RECOMMA_HTTP_LISTEN=:8080
   RECOMMA_STORAGE_PATH=/var/lib/recomma/db.sqlite3
   RECOMMA_LOG_LEVEL=info
   RECOMMA_LOG_JSON=true
   # widen the first IOC price by N basis points when matching against the book
   RECOMMA_HYPERLIQUID_IOC_OFFSET_BPS=0
   ```
3. Start the container:
   ```bash
   docker run \
     --env-file .env \
     -p 8080:8080 \
     -v $(pwd)/data:/var/lib/recomma \
     ghcr.io/recomma/recomma:latest
   ```
   The first time you visit the UI you’ll register a passkey and paste in your 3Commas and Hyperliquid credentials. They are encrypted in your browser before being stored on the server.

> Tip: If you deploy behind a reverse proxy with TLS, point `RECOMMA_PUBLIC_ORIGIN` to the public HTTPS URL (for example `https://trading.yourdomain.com`). Using `http://localhost` is fine for local testing but the value must match whatever appears in the browser address bar, otherwise passkey login will fail.

### Prefer docker-compose?
`docker-compose.yml` is included in the repository with two services:
- `recomma` builds from the local source tree (handy for development).
- `recomma-ghcr` pulls the published image.

Copy it next to your `.env`, then launch with:
```bash
docker compose --profile ghcr up recomma-ghcr
```
Mounts and environment variables match the quick-start above, so `RECOMMA_PUBLIC_ORIGIN` still needs to reflect the exact URL you will open in the browser.

## Hyperliquid IOC retry logging

- Immediate-or-cancel orders may miss the book on their first try. When that happens the emitter logs an `INFO` line (`IOC did not immediately match; retrying`) instead of warning, then resubmits up to the configured retry limit.
- Successful retries emit `Order sent after IOC retries` with the retry count and the last exchange error so operators can see the hiccup without treating it as a failure.
- Tune the initial price aggressiveness without a redeploy by setting `RECOMMA_HYPERLIQUID_IOC_OFFSET_BPS` to the number of basis points you want added (for buys) or subtracted (for sells) on the first attempt.

## First Sign-In Flow
1. Browse to the origin you configured. The wizard asks you to create a passkey (WebAuthn/FIDO2) so only you can unlock the vault.
2. Enter your 3Commas API key and private signing key, plus your Hyperliquid wallet address and private key. The page encrypts everything client-side and stores only ciphertext in the SQLite database.
3. On future logins you authenticate with the same passkey. During the handshake the server sends the encrypted blob back to the browser, the browser decrypts it, and the vault is unsealed in memory for the running process.

You can reseal the vault or rotate credentials at any time from the UI. If the process restarts, it will wait for you to log in again before resuming order replication.

## Getting Releases
- **Docker image**: [`ghcr.io/recomma/recomma`](https://github.com/orgs/recomma/packages/container/package/recomma)
- **Binary archives and checksums**: [GitHub Releases](https://github.com/recomma/recomma/releases)

Download the format that suits your setup; every artifact bundles the web UI and API so nothing else is required.
