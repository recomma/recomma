-- name: InsertThreeCommasBotEvent :one
INSERT INTO threecommas_botevents (
    md,
    bot_id,
    deal_id,
    botevent_id,
    created_at_utc,
    payload
) VALUES (
    sqlc.arg(md),
    sqlc.arg(bot_id),
    sqlc.arg(deal_id),
    sqlc.arg(botevent_id),
    sqlc.arg(created_at_utc),
    sqlc.arg(payload)
)
ON CONFLICT(md, botevent_id, created_at_utc) DO NOTHING
RETURNING id;

-- name: InsertThreeCommasBotEventLog :one
INSERT INTO threecommas_botevents_log (
    md,
    bot_id,
    deal_id,
    botevent_id,
    created_at_utc,
    payload
) VALUES (
    sqlc.arg(md),
    sqlc.arg(bot_id),
    sqlc.arg(deal_id),
    sqlc.arg(botevent_id),
    sqlc.arg(created_at_utc),
    sqlc.arg(payload)
)
ON CONFLICT(md, botevent_id, created_at_utc) DO NOTHING
RETURNING id;

-- name: ListThreeCommasBotEventsForOrder :many
SELECT id, payload
FROM threecommas_botevents
WHERE bot_id = sqlc.arg(bot_id)
  AND deal_id = sqlc.arg(deal_id)
  AND botevent_id = sqlc.arg(botevent_id)
ORDER BY created_at_utc ASC, observed_at_utc ASC, id ASC;

-- name: HasThreeCommasMetadata :one
SELECT EXISTS(
    SELECT 1 FROM threecommas_botevents WHERE md = sqlc.arg(md)
);

-- name: FetchThreeCommasBotEvent :one
SELECT payload
FROM threecommas_botevents
WHERE md = sqlc.arg(md)
ORDER BY observed_at_utc DESC, id DESC
LIMIT 1;

-- name: UpsertHyperliquidCreate :exec
INSERT INTO hyperliquid_submissions (
    md,
    action_kind,
    create_payload,
    modify_payloads,
    cancel_payload,
    updated_at_utc,
    botevent_row_id
) VALUES (
    sqlc.arg(md),
    'create',
    json(sqlc.arg(create_payload)),
    CAST('[]' AS BLOB),
    NULL,
    CAST(unixepoch('now','subsec') * 1000 AS INTEGER),
    sqlc.arg(botevent_row_id)
)
ON CONFLICT(md) DO UPDATE SET
    create_payload = excluded.create_payload,
    action_kind    = 'create',
    updated_at_utc = CAST(unixepoch('now','subsec') * 1000 AS INTEGER);

-- name: AppendHyperliquidModify :exec
INSERT INTO hyperliquid_submissions (
    md,
    action_kind,
    create_payload,
    modify_payloads,
    cancel_payload,
    updated_at_utc,
    botevent_row_id
) VALUES (
    sqlc.arg(md),
    'modify',
    NULL,
    json_array(json(sqlc.arg(modify_payload))),
    NULL,
    CAST(unixepoch('now','subsec') * 1000 AS INTEGER),
    sqlc.arg(botevent_row_id)
)
ON CONFLICT(md) DO UPDATE SET
    modify_payloads = json_insert(
        COALESCE(hyperliquid_submissions.modify_payloads, CAST('[]' AS BLOB)),
        '$[#]',
        json(json_extract(excluded.modify_payloads, '$[0]'))
    ),
    action_kind    = 'modify',
    updated_at_utc = CAST(unixepoch('now','subsec') * 1000 AS INTEGER);

-- name: UpsertHyperliquidCancel :exec
INSERT INTO hyperliquid_submissions (
    md,
    action_kind,
    create_payload,
    modify_payloads,
    cancel_payload,
    updated_at_utc,
    botevent_row_id
) VALUES (
    sqlc.arg(md),
    'cancel',
    NULL,
    '[]',
    json(sqlc.arg(cancel_payload)),
    CAST(unixepoch('now','subsec') * 1000 AS INTEGER),
    sqlc.arg(botevent_row_id)
)
ON CONFLICT(md) DO UPDATE SET
    cancel_payload = excluded.cancel_payload,
    action_kind    = 'cancel',
    updated_at_utc = CAST(unixepoch('now','subsec') * 1000 AS INTEGER);

-- name: InsertHyperliquidStatus :exec
INSERT INTO hyperliquid_status_history (
    md,
    status,
    recorded_at_utc
) VALUES (
    sqlc.arg(md),
    sqlc.arg(status),
    sqlc.arg(recorded_at_utc)
);

-- name: FetchLatestHyperliquidStatus :one
SELECT status
FROM hyperliquid_status_history
WHERE md = sqlc.arg(md)
ORDER BY recorded_at_utc DESC, id DESC
LIMIT 1;

-- name: ListHyperliquidStatuses :many
SELECT status, recorded_at_utc
FROM hyperliquid_status_history
WHERE md = sqlc.arg(md)
ORDER BY recorded_at_utc ASC, id ASC;

-- name: FetchHyperliquidSubmission :one
SELECT
    action_kind,
    CAST(create_payload AS BLOB)  AS create_payload,
    CAST(modify_payloads AS BLOB) AS modify_payloads,
    CAST(cancel_payload AS BLOB)  AS cancel_payload
FROM hyperliquid_submissions
WHERE md = sqlc.arg(md);

-- name: ListLatestHyperliquidSafetyStatuses :many
WITH latest_status AS (
    SELECT md, id
    FROM (
        SELECT
            md,
            id,
            ROW_NUMBER() OVER (
                PARTITION BY md
                ORDER BY recorded_at_utc DESC, id DESC
            ) AS rn
        FROM hyperliquid_status_history
    )
    WHERE rn = 1
)
SELECT
    b.md AS md,
    b.bot_id AS bot_id,
    b.deal_id AS deal_id,
    CAST(json_extract(b.payload, '$.OrderType') AS TEXT)        AS order_type,
    CAST(json_extract(b.payload, '$.OrderPosition') AS INTEGER) AS order_position,
    CAST(json_extract(b.payload, '$.OrderSize') AS INTEGER)        AS order_size,
    CAST(json_extract(h.status, '$.status') AS TEXT)            AS hl_status,
    CAST(json_extract(h.status, '$.statusTimestamp') AS INTEGER) AS hl_status_timestamp,
    h.recorded_at_utc AS recorded_at_utc
FROM latest_status AS latest
JOIN hyperliquid_status_history AS h
  ON h.id = latest.id
JOIN threecommas_botevents AS b
  ON b.md = latest.md
WHERE b.deal_id = sqlc.arg(deal_id)
  AND CAST(json_extract(b.payload, '$.OrderType') AS TEXT) = 'Safety'
ORDER BY order_position ASC;

-- name: UpsertBot :exec
INSERT INTO threecommas_bots (
    bot_id,
    payload,
    last_synced_utc
) VALUES (
    sqlc.arg(bot_id),
    sqlc.arg(payload),
    sqlc.arg(last_synced_utc)
)
ON CONFLICT(bot_id) DO UPDATE SET
    payload = excluded.payload,
    last_synced_utc = excluded.last_synced_utc;

-- name: FetchBot :one
SELECT payload, last_synced_utc
FROM threecommas_bots
WHERE bot_id = sqlc.arg(bot_id);

-- name: UpdateBotSync :exec
UPDATE threecommas_bots
SET last_synced_utc = sqlc.arg(last_synced_utc)
WHERE bot_id = sqlc.arg(bot_id);

-- name: UpsertDeal :exec
INSERT INTO threecommas_deals (
    deal_id,
    bot_id,
    created_at_utc,
    updated_at_utc,
    payload
) VALUES (
    sqlc.arg(deal_id),
    sqlc.arg(bot_id),
    sqlc.arg(created_at_utc),
    sqlc.arg(updated_at_utc),
    sqlc.arg(payload)
)
ON CONFLICT(deal_id) DO UPDATE SET
    bot_id         = excluded.bot_id,
    created_at_utc = excluded.created_at_utc,
    updated_at_utc = excluded.updated_at_utc,
    payload        = excluded.payload;

-- name: FetchDeal :one
SELECT payload
FROM threecommas_deals
WHERE deal_id = sqlc.arg(deal_id);

-- API specific

-- name: ListThreeCommasBots :many
SELECT
    bot_id,
    payload,
    last_synced_utc
FROM threecommas_bots
WHERE bot_id = COALESCE(sqlc.arg(bot_id), bot_id)
  AND last_synced_utc >= COALESCE(sqlc.arg(updated_from), last_synced_utc)
  AND last_synced_utc <= COALESCE(sqlc.arg(updated_to), last_synced_utc)
  AND (
        sqlc.arg(cursor_last_synced) IS NULL
        OR last_synced_utc < sqlc.arg(cursor_last_synced)
        OR (
            last_synced_utc = sqlc.arg(cursor_last_synced)
            AND bot_id < sqlc.arg(cursor_bot_id)
        )
      )
ORDER BY last_synced_utc DESC, bot_id DESC
LIMIT sqlc.arg(limit);

-- name: ListThreeCommasDeals :many
SELECT
    deal_id,
    bot_id,
    created_at_utc,
    updated_at_utc,
    payload
FROM threecommas_deals
WHERE deal_id = COALESCE(sqlc.arg(deal_id), deal_id)
  AND bot_id = COALESCE(sqlc.arg(bot_id), bot_id)
  AND updated_at_utc >= COALESCE(sqlc.arg(updated_from), updated_at_utc)
  AND updated_at_utc <= COALESCE(sqlc.arg(updated_to), updated_at_utc)
  AND (
        sqlc.arg(cursor_updated_at) IS NULL
        OR updated_at_utc < sqlc.arg(cursor_updated_at)
        OR (
            updated_at_utc = sqlc.arg(cursor_updated_at)
            AND deal_id < sqlc.arg(cursor_deal_id)
        )
      )
ORDER BY updated_at_utc DESC, deal_id DESC
LIMIT sqlc.arg(limit);

-- name: ListThreeCommasBotEvents :many
SELECT
    id,
    md,
    bot_id,
    deal_id,
    botevent_id,
    created_at_utc,
    observed_at_utc,
    payload
FROM threecommas_botevents
WHERE bot_id = COALESCE(sqlc.arg(bot_id), bot_id)
  AND deal_id = COALESCE(sqlc.arg(deal_id), deal_id)
  AND botevent_id = COALESCE(sqlc.arg(bot_event_id), botevent_id)
  AND observed_at_utc >= COALESCE(sqlc.arg(observed_from), observed_at_utc)
  AND observed_at_utc <= COALESCE(sqlc.arg(observed_to), observed_at_utc)
  AND (
        sqlc.arg(metadata_prefix) IS NULL
        OR LOWER(md) LIKE LOWER(sqlc.arg(metadata_prefix)) || '%'
      )
  AND (
        sqlc.arg(cursor_observed_at) IS NULL
        OR observed_at_utc < sqlc.arg(cursor_observed_at)
        OR (
            observed_at_utc = sqlc.arg(cursor_observed_at)
            AND id < sqlc.arg(cursor_id)
        )
      )
ORDER BY observed_at_utc DESC, id DESC
LIMIT sqlc.arg(limit);

-- name: ListThreeCommasBotEventLogsForMetadata :many
SELECT
    id,
    md,
    bot_id,
    deal_id,
    botevent_id,
    created_at_utc,
    observed_at_utc,
    payload
FROM threecommas_botevents_log
WHERE md = sqlc.arg(metadata)
  AND observed_at_utc >= COALESCE(sqlc.arg(observed_from), observed_at_utc)
  AND observed_at_utc <= COALESCE(sqlc.arg(observed_to), observed_at_utc)
ORDER BY observed_at_utc ASC, id ASC;

-- name: ListHyperliquidStatusesForMetadata :many
SELECT
    id,
    status,
    recorded_at_utc
FROM hyperliquid_status_history
WHERE md = sqlc.arg(metadata)
  AND recorded_at_utc >= COALESCE(sqlc.arg(observed_from), recorded_at_utc)
  AND recorded_at_utc <= COALESCE(sqlc.arg(observed_to), recorded_at_utc)
ORDER BY recorded_at_utc ASC, id ASC;

-- name: ListHyperliquidSubmissionsByMetadata :many
SELECT
    md,
    action_kind,
    CAST(create_payload AS BLOB)  AS create_payload,
    CAST(modify_payloads AS BLOB) AS modify_payloads,
    CAST(cancel_payload AS BLOB)  AS cancel_payload,
    updated_at_utc,
    botevent_row_id
FROM hyperliquid_submissions
WHERE md IN (
    SELECT value FROM json_each(sqlc.arg(metadata_list))
);

-- name: ListDealIDs :many
select deal_id from threecommas_deals;

-- name: GetTPForDeal :one
SELECT md,
       botevent_id,
       created_at_utc,
       payload
FROM threecommas_botevents
WHERE deal_id = sqlc.arg(deal_id)
  AND json_extract(payload, '$.OrderType') = 'Take Profit'
ORDER BY created_at_utc DESC
LIMIT 1;