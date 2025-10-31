PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;

-- threecommas_botevents are all the botevents we acted upon
CREATE TABLE IF NOT EXISTS threecommas_botevents (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    md              TEXT    NOT NULL,
    bot_id          INTEGER NOT NULL,
    deal_id         INTEGER NOT NULL,
    botevent_id        INTEGER NOT NULL,
    created_at_utc  INTEGER NOT NULL,
    observed_at_utc INTEGER NOT NULL DEFAULT(unixepoch('now','subsec') * 1000),
    payload         BLOB    NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_threecommas_botevents_md_evt_created
    ON threecommas_botevents(md, botevent_id, created_at_utc);

-- threecommas_botevents_log is a full log of ALL botevents
CREATE TABLE IF NOT EXISTS threecommas_botevents_log (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    md              TEXT    NOT NULL,
    bot_id          INTEGER NOT NULL,
    deal_id         INTEGER NOT NULL,
    botevent_id        INTEGER NOT NULL,
    created_at_utc  INTEGER NOT NULL,
    observed_at_utc INTEGER NOT NULL DEFAULT(unixepoch('now','subsec') * 1000),
    payload         BLOB    NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_threecommas_botevents_log_md_evt_created
    ON threecommas_botevents_log(md, botevent_id, created_at_utc);

CREATE TABLE IF NOT EXISTS threecommas_bots (
    bot_id          INTEGER PRIMARY KEY,
    payload         BLOB    NOT NULL,
    last_synced_utc INTEGER NOT NULL DEFAULT(unixepoch('now','subsec') * 1000)
);

CREATE TABLE IF NOT EXISTS threecommas_deals (
    deal_id         INTEGER PRIMARY KEY,
    bot_id          INTEGER NOT NULL,
    created_at_utc  INTEGER NOT NULL,
    updated_at_utc  INTEGER NOT NULL,
    payload         BLOB    NOT NULL,
    inserted_at_utc INTEGER NOT NULL DEFAULT(unixepoch('now','subsec') * 1000)
);

CREATE TABLE IF NOT EXISTS hyperliquid_submissions (
  md              TEXT PRIMARY KEY,
  action_kind     TEXT NOT NULL CHECK(action_kind IN ('create','modify','cancel')),
  create_payload  JSON,
  modify_payloads JSON NOT NULL DEFAULT (CAST('[]' AS BLOB)),
  cancel_payload  JSON,
  updated_at_utc  INTEGER NOT NULL DEFAULT(unixepoch('now','subsec') * 1000),
  botevent_row_id INTEGER NOT NULL,
  CHECK (create_payload  IS NULL OR json_valid(create_payload)),
  CHECK (modify_payloads IS NULL OR json_valid(modify_payloads)),
  CHECK (cancel_payload  IS NULL OR json_valid(cancel_payload))
);

CREATE TABLE IF NOT EXISTS hyperliquid_status_history (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    md              TEXT    NOT NULL,
    status          BLOB    NOT NULL,
    recorded_at_utc INTEGER NOT NULL DEFAULT(unixepoch('now','subsec') * 1000)
);

-- Vault tables for WebAuthn-backed secret storage
CREATE TABLE IF NOT EXISTS vault_users (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    username        TEXT    NOT NULL UNIQUE,
    created_at_utc  INTEGER NOT NULL DEFAULT(unixepoch('now','subsec') * 1000)
);

CREATE TABLE IF NOT EXISTS vault_payloads (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id          INTEGER NOT NULL,
    version          TEXT    NOT NULL,
    ciphertext       BLOB    NOT NULL,
    nonce            BLOB    NOT NULL,
    associated_data  BLOB,
    prf_params       JSON,
    updated_at_utc   INTEGER NOT NULL DEFAULT(unixepoch('now','subsec') * 1000),
    FOREIGN KEY(user_id) REFERENCES vault_users(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_vault_payloads_user_id
    ON vault_payloads(user_id);

-- WebAuthn credentials associated with vault users
CREATE TABLE IF NOT EXISTS webauthn_credentials (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id        INTEGER NOT NULL,
    credential_id  BLOB    NOT NULL,
    credential     JSON    NOT NULL,
    created_at_utc INTEGER NOT NULL DEFAULT(unixepoch('now','subsec') * 1000),
    updated_at_utc INTEGER NOT NULL DEFAULT(unixepoch('now','subsec') * 1000),
    FOREIGN KEY(user_id) REFERENCES vault_users(id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_webauthn_credentials_credential_id
    ON webauthn_credentials(credential_id);

CREATE INDEX IF NOT EXISTS idx_webauthn_credentials_user_id
    ON webauthn_credentials(user_id);

CREATE TABLE IF NOT EXISTS order_scalers (
    id              INTEGER PRIMARY KEY CHECK (id = 1),
    multiplier      REAL    NOT NULL,
    updated_at_utc  INTEGER NOT NULL DEFAULT (CAST(unixepoch('now','subsec') * 1000 AS INTEGER)),
    updated_by      TEXT    NOT NULL,
    notes           TEXT
);

INSERT OR IGNORE INTO order_scalers (id, multiplier, updated_by)
VALUES (1, 1.0, 'system');

CREATE TABLE IF NOT EXISTS bot_order_scalers (
    bot_id             INTEGER PRIMARY KEY,
    multiplier         REAL,
    notes              TEXT,
    effective_from_utc INTEGER NOT NULL DEFAULT (CAST(unixepoch('now','subsec') * 1000 AS INTEGER)),
    updated_at_utc     INTEGER NOT NULL DEFAULT (CAST(unixepoch('now','subsec') * 1000 AS INTEGER)),
    updated_by         TEXT    NOT NULL,
    FOREIGN KEY(bot_id) REFERENCES threecommas_bots(bot_id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS scaled_orders (
    id                     INTEGER PRIMARY KEY AUTOINCREMENT,
    md                     TEXT    NOT NULL,
    deal_id                INTEGER NOT NULL,
    bot_id                 INTEGER NOT NULL,
    original_size          REAL    NOT NULL,
    scaled_size            REAL    NOT NULL,
    multiplier             REAL    NOT NULL,
    rounding_delta         REAL    NOT NULL,
    stack_index            INTEGER NOT NULL,
    order_side             TEXT    NOT NULL,
    multiplier_updated_by  TEXT    NOT NULL,
    created_at_utc         INTEGER NOT NULL DEFAULT (CAST(unixepoch('now','subsec') * 1000 AS INTEGER)),
    submitted_order_id     TEXT,
    FOREIGN KEY(deal_id) REFERENCES threecommas_deals(deal_id) ON DELETE CASCADE,
    FOREIGN KEY(bot_id) REFERENCES threecommas_bots(bot_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_scaled_orders_md_created
    ON scaled_orders(md, created_at_utc);

CREATE INDEX IF NOT EXISTS idx_scaled_orders_deal_stack
    ON scaled_orders(deal_id, stack_index);
