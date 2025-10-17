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

CREATE TABLE IF NOT EXISTS kv_state (
    k TEXT PRIMARY KEY,
    v BLOB NOT NULL,
    updated_at_utc INTEGER NOT NULL DEFAULT(unixepoch('now','subsec') * 1000)
);
