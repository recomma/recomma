-- name: InsertThreeCommasBotEvent :one
INSERT INTO threecommas_botevents (
    order_id,
    bot_id,
    deal_id,
    botevent_id,
    created_at_utc,
    payload
) VALUES (
    sqlc.arg(order_id),
    sqlc.arg(bot_id),
    sqlc.arg(deal_id),
    sqlc.arg(botevent_id),
    sqlc.arg(created_at_utc),
    sqlc.arg(payload)
)
ON CONFLICT(order_id, botevent_id, created_at_utc) DO NOTHING
RETURNING id;

-- name: InsertThreeCommasBotEventLog :one
INSERT INTO threecommas_botevents_log (
    order_id,
    bot_id,
    deal_id,
    botevent_id,
    created_at_utc,
    payload
) VALUES (
    sqlc.arg(order_id),
    sqlc.arg(bot_id),
    sqlc.arg(deal_id),
    sqlc.arg(botevent_id),
    sqlc.arg(created_at_utc),
    sqlc.arg(payload)
)
ON CONFLICT(order_id, botevent_id, created_at_utc) DO NOTHING
RETURNING id;

-- name: ListThreeCommasBotEventsForOrder :many
SELECT id, payload
FROM threecommas_botevents
WHERE bot_id = sqlc.arg(bot_id)
  AND deal_id = sqlc.arg(deal_id)
  AND botevent_id = sqlc.arg(botevent_id)
ORDER BY created_at_utc ASC, observed_at_utc ASC, id ASC;

-- name: HasThreeCommasOrderId :one
SELECT EXISTS(
    SELECT 1 FROM threecommas_botevents WHERE order_id = sqlc.arg(order_id)
);

-- name: GetOrderIdForDeal :many
SELECT DISTINCT order_id
    FROM threecommas_botevents
    WHERE deal_id = ?;

-- name: FetchThreeCommasBotEvent :one
SELECT payload
FROM threecommas_botevents
WHERE order_id = sqlc.arg(order_id)
ORDER BY observed_at_utc DESC, id DESC
LIMIT 1;

-- name: UpsertHyperliquidCreate :exec
INSERT INTO hyperliquid_submissions (
    order_id,
    action_kind,
    create_payload,
    modify_payloads,
    cancel_payload,
    updated_at_utc,
    botevent_row_id
) VALUES (
    sqlc.arg(order_id),
    'create',
    json(sqlc.arg(create_payload)),
    CAST('[]' AS BLOB),
    NULL,
    CAST(unixepoch('now','subsec') * 1000 AS INTEGER),
    sqlc.arg(botevent_row_id)
)
ON CONFLICT(order_id) DO UPDATE SET
    create_payload = excluded.create_payload,
    action_kind    = 'create',
    updated_at_utc = CAST(unixepoch('now','subsec') * 1000 AS INTEGER);

-- name: AppendHyperliquidModify :exec
INSERT INTO hyperliquid_submissions (
    order_id,
    action_kind,
    create_payload,
    modify_payloads,
    cancel_payload,
    updated_at_utc,
    botevent_row_id
) VALUES (
    sqlc.arg(order_id),
    'modify',
    NULL,
    json_array(json(sqlc.arg(modify_payload))),
    NULL,
    CAST(unixepoch('now','subsec') * 1000 AS INTEGER),
    sqlc.arg(botevent_row_id)
)
ON CONFLICT(order_id) DO UPDATE SET
    modify_payloads = json_insert(
        COALESCE(hyperliquid_submissions.modify_payloads, CAST('[]' AS BLOB)),
        '$[#]',
        json(json_extract(excluded.modify_payloads, '$[0]'))
    ),
    action_kind    = 'modify',
    updated_at_utc = CAST(unixepoch('now','subsec') * 1000 AS INTEGER);

-- name: UpsertHyperliquidCancel :exec
INSERT INTO hyperliquid_submissions (
    order_id,
    action_kind,
    create_payload,
    modify_payloads,
    cancel_payload,
    updated_at_utc,
    botevent_row_id
) VALUES (
    sqlc.arg(order_id),
    'cancel',
    NULL,
    '[]',
    json(sqlc.arg(cancel_payload)),
    CAST(unixepoch('now','subsec') * 1000 AS INTEGER),
    sqlc.arg(botevent_row_id)
)
ON CONFLICT(order_id) DO UPDATE SET
    cancel_payload = excluded.cancel_payload,
    action_kind    = 'cancel',
    updated_at_utc = CAST(unixepoch('now','subsec') * 1000 AS INTEGER);

-- name: InsertHyperliquidStatus :exec
INSERT INTO hyperliquid_status_history (
    order_id,
    status,
    recorded_at_utc
) VALUES (
    sqlc.arg(order_id),
    sqlc.arg(status),
    sqlc.arg(recorded_at_utc)
);

-- name: FetchLatestHyperliquidStatus :one
SELECT status
FROM hyperliquid_status_history
WHERE order_id = sqlc.arg(order_id)
ORDER BY recorded_at_utc DESC, id DESC
LIMIT 1;

-- name: ListHyperliquidStatuses :many
SELECT status, recorded_at_utc
FROM hyperliquid_status_history
WHERE order_id = sqlc.arg(order_id)
ORDER BY recorded_at_utc ASC, id ASC;

-- name: FetchHyperliquidSubmission :one
SELECT
    action_kind,
    CAST(create_payload AS BLOB)  AS create_payload,
    CAST(modify_payloads AS BLOB) AS modify_payloads,
    CAST(cancel_payload AS BLOB)  AS cancel_payload
FROM hyperliquid_submissions
WHERE order_id = sqlc.arg(order_id);

-- name: ListHyperliquidOrderIds :many
SELECT order_id
FROM hyperliquid_submissions
ORDER BY order_id ASC;

-- name: ListLatestHyperliquidSafetyStatuses :many
WITH latest_status AS (
    SELECT order_id, id
    FROM (
        SELECT
            order_id,
            id,
            ROW_NUMBER() OVER (
                PARTITION BY order_id
                ORDER BY recorded_at_utc DESC, id DESC
            ) AS rn
        FROM hyperliquid_status_history
    )
    WHERE rn = 1
)
SELECT
    b.order_id AS order_id,
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
  ON b.order_id = latest.order_id
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
    order_id,
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
        sqlc.arg(order_id_prefix) IS NULL
        OR LOWER(order_id) LIKE LOWER(sqlc.arg(order_id_prefix)) || '%'
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

-- name: ListThreeCommasBotEventLogsForOrderId :many
SELECT
    id,
    order_id,
    bot_id,
    deal_id,
    botevent_id,
    created_at_utc,
    observed_at_utc,
    payload
FROM threecommas_botevents_log
WHERE order_id = sqlc.arg(order_id)
  AND observed_at_utc >= COALESCE(sqlc.arg(observed_from), observed_at_utc)
  AND observed_at_utc <= COALESCE(sqlc.arg(observed_to), observed_at_utc)
ORDER BY observed_at_utc ASC, id ASC;

-- name: ListThreeCommasBotEventLogs :many
SELECT
    id,
    order_id,
    bot_id,
    deal_id,
    botevent_id,
    created_at_utc,
    observed_at_utc,
    payload
FROM threecommas_botevents_log
ORDER BY created_at_utc ASC, id ASC;

-- name: ListHyperliquidStatusesForOrderId :many
SELECT
    id,
    status,
    recorded_at_utc
FROM hyperliquid_status_history
WHERE order_id = sqlc.arg(order_id)
  AND recorded_at_utc >= COALESCE(sqlc.arg(observed_from), recorded_at_utc)
  AND recorded_at_utc <= COALESCE(sqlc.arg(observed_to), recorded_at_utc)
ORDER BY recorded_at_utc ASC, id ASC;

-- name: ListHyperliquidSubmissionsByOrderId :many
SELECT
    order_id,
    action_kind,
    CAST(create_payload AS BLOB)  AS create_payload,
    CAST(modify_payloads AS BLOB) AS modify_payloads,
    CAST(cancel_payload AS BLOB)  AS cancel_payload,
    updated_at_utc,
    botevent_row_id
FROM hyperliquid_submissions
WHERE order_id IN (
    SELECT value FROM json_each(sqlc.arg(order_id_list))
);

-- name: ListDealIDs :many
select deal_id from threecommas_deals;

-- name: GetTPForDeal :one
SELECT order_id,
       botevent_id,
       created_at_utc,
       payload
FROM threecommas_botevents
WHERE deal_id = sqlc.arg(deal_id)
  AND json_extract(payload, '$.OrderType') = 'Take Profit'
ORDER BY created_at_utc DESC
LIMIT 1;

-- name: ListLatestTakeProfitStackSizes :many
WITH ranked AS (
    SELECT
        CAST(json_extract(payload, '$.OrderPosition') AS INTEGER) AS order_position,
        CAST(json_extract(payload, '$.OrderSize') AS INTEGER) AS order_size,
        CAST(json_extract(payload, '$.Size') AS REAL) AS size,
        created_at_utc,
        id,
        ROW_NUMBER() OVER (
            PARTITION BY CAST(json_extract(payload, '$.OrderPosition') AS INTEGER)
            ORDER BY created_at_utc DESC, id DESC
        ) AS rn
    FROM threecommas_botevents
    WHERE deal_id = sqlc.arg(deal_id)
      AND CAST(json_extract(payload, '$.OrderType') AS TEXT) = 'Take Profit'
      AND CAST(json_extract(payload, '$.OrderSize') AS INTEGER) = CAST(sqlc.arg(order_size) AS INTEGER)
)
SELECT
    order_position,
    size
FROM ranked
WHERE rn = 1
ORDER BY order_position ASC;

-- Vault management

-- name: EnsureVaultUser :one
INSERT INTO vault_users (
    username
) VALUES (
    sqlc.arg(username)
)
ON CONFLICT(username) DO UPDATE SET
    username = excluded.username
RETURNING id, username, created_at_utc;

-- name: GetVaultUser :one
SELECT id, username, created_at_utc
FROM vault_users
ORDER BY id ASC
LIMIT 1;

-- name: GetVaultUserByUsername :one
SELECT id, username, created_at_utc
FROM vault_users
WHERE username = sqlc.arg(username)
LIMIT 1;

-- name: GetVaultUserByID :one
SELECT id, username, created_at_utc
FROM vault_users
WHERE id = sqlc.arg(id)
LIMIT 1;

-- name: DeleteVaultUserByID :exec
DELETE FROM vault_users
WHERE id = sqlc.arg(id);

-- name: UpsertVaultPayload :exec
INSERT INTO vault_payloads (
    user_id,
    version,
    ciphertext,
    nonce,
    associated_data,
    prf_params,
    updated_at_utc
) VALUES (
    sqlc.arg(user_id),
    sqlc.arg(version),
    sqlc.arg(ciphertext),
    sqlc.arg(nonce),
    sqlc.arg(associated_data),
    json(sqlc.arg(prf_params)),
    sqlc.arg(updated_at_utc)
)
ON CONFLICT(user_id) DO UPDATE SET
    version = excluded.version,
    ciphertext = excluded.ciphertext,
    nonce = excluded.nonce,
    associated_data = excluded.associated_data,
    prf_params = excluded.prf_params,
    updated_at_utc = excluded.updated_at_utc;

-- name: GetVaultPayloadForUser :one
SELECT
    id,
    user_id,
    version,
    ciphertext,
    nonce,
    associated_data,
    CAST(prf_params AS BLOB) AS prf_params,
    updated_at_utc
FROM vault_payloads
WHERE user_id = sqlc.arg(user_id);

-- name: DeleteVaultPayloadForUser :exec
DELETE FROM vault_payloads
WHERE user_id = sqlc.arg(user_id);

-- WebAuthn credential management

-- name: UpsertWebauthnCredential :exec
INSERT INTO webauthn_credentials (
    user_id,
    credential_id,
    credential
) VALUES (
    sqlc.arg(user_id),
    sqlc.arg(credential_id),
    json(sqlc.arg(credential))
)
ON CONFLICT(credential_id) DO UPDATE SET
    user_id = excluded.user_id,
    credential = excluded.credential,
    updated_at_utc = CAST(unixepoch('now','subsec') * 1000 AS INTEGER);

-- name: ListWebauthnCredentialsByUser :many
SELECT
    id,
    user_id,
    credential_id,
    CAST(credential AS BLOB) AS credential,
    created_at_utc,
    updated_at_utc
FROM webauthn_credentials
WHERE user_id = sqlc.arg(user_id)
ORDER BY id ASC;

-- name: GetWebauthnCredentialByID :one
SELECT
    id,
    user_id,
    credential_id,
    CAST(credential AS BLOB) AS credential,
    created_at_utc,
    updated_at_utc
FROM webauthn_credentials
WHERE credential_id = sqlc.arg(credential_id);

-- name: DeleteWebauthnCredentialByID :exec
DELETE FROM webauthn_credentials
WHERE credential_id = sqlc.arg(credential_id);

-- name: GetOrderScaler :one
SELECT id, multiplier, updated_at_utc, updated_by, notes
FROM order_scalers
WHERE id = 1;

-- name: UpsertOrderScaler :exec
INSERT INTO order_scalers (
    id,
    multiplier,
    updated_by,
    notes
) VALUES (
    1,
    sqlc.arg(multiplier),
    sqlc.arg(updated_by),
    sqlc.narg(notes)
)
ON CONFLICT(id) DO UPDATE SET
    multiplier     = excluded.multiplier,
    updated_by     = excluded.updated_by,
    notes          = excluded.notes,
    updated_at_utc = CAST(unixepoch('now','subsec') * 1000 AS INTEGER);

-- name: ListBotOrderScalers :many
SELECT bot_id, multiplier, notes, effective_from_utc, updated_at_utc, updated_by
FROM bot_order_scalers
ORDER BY bot_id ASC;

-- name: GetBotOrderScaler :one
SELECT bot_id, multiplier, notes, effective_from_utc, updated_at_utc, updated_by
FROM bot_order_scalers
WHERE bot_id = sqlc.arg(bot_id);

-- name: UpsertBotOrderScaler :exec
INSERT INTO bot_order_scalers (
    bot_id,
    multiplier,
    notes,
    updated_by
) VALUES (
    sqlc.arg(bot_id),
    sqlc.narg(multiplier),
    sqlc.narg(notes),
    sqlc.arg(updated_by)
)
ON CONFLICT(bot_id) DO UPDATE SET
    multiplier     = excluded.multiplier,
    notes          = excluded.notes,
    updated_by     = excluded.updated_by,
    updated_at_utc = CAST(unixepoch('now','subsec') * 1000 AS INTEGER);

-- name: DeleteBotOrderScaler :exec
DELETE FROM bot_order_scalers
WHERE bot_id = sqlc.arg(bot_id);

-- name: InsertScaledOrder :one
INSERT INTO scaled_orders (
    order_id,
    deal_id,
    bot_id,
    original_size,
    scaled_size,
    multiplier,
    rounding_delta,
    stack_index,
    order_side,
    multiplier_updated_by,
    created_at_utc,
    submitted_order_id,
    skipped,
    skip_reason
) VALUES (
    sqlc.arg(order_id),
    sqlc.arg(deal_id),
    sqlc.arg(bot_id),
    sqlc.arg(original_size),
    sqlc.arg(scaled_size),
    sqlc.arg(multiplier),
    sqlc.arg(rounding_delta),
    sqlc.arg(stack_index),
    sqlc.arg(order_side),
    sqlc.arg(multiplier_updated_by),
    sqlc.arg(created_at_utc),
    sqlc.narg(submitted_order_id),
    sqlc.arg(skipped),
    sqlc.narg(skip_reason)
)
RETURNING id;

-- name: ListScaledOrdersByOrderId :many
SELECT
    id,
    order_id,
    deal_id,
    bot_id,
    original_size,
    scaled_size,
    multiplier,
    rounding_delta,
    stack_index,
    order_side,
    multiplier_updated_by,
    created_at_utc,
    submitted_order_id,
    skipped,
    skip_reason
FROM scaled_orders
WHERE order_id = sqlc.arg(order_id)
ORDER BY created_at_utc ASC, id ASC;

-- name: ListScaledOrdersByDeal :many
SELECT
    id,
    order_id,
    deal_id,
    bot_id,
    original_size,
    scaled_size,
    multiplier,
    rounding_delta,
    stack_index,
    order_side,
    multiplier_updated_by,
    created_at_utc,
    submitted_order_id,
    skipped,
    skip_reason
FROM scaled_orders
WHERE deal_id = sqlc.arg(deal_id)
ORDER BY created_at_utc ASC, id ASC;

-- name: ListScaledOrderAuditsForOrderId :many
SELECT
    id,
    order_id,
    deal_id,
    bot_id,
    original_size,
    scaled_size,
    multiplier,
    rounding_delta,
    stack_index,
    order_side,
    multiplier_updated_by,
    created_at_utc,
    submitted_order_id,
    skipped,
    skip_reason
FROM scaled_orders
WHERE order_id = sqlc.arg(order_id)
  AND created_at_utc >= sqlc.arg(observed_from)
  AND created_at_utc <= sqlc.arg(observed_to)
ORDER BY created_at_utc ASC, id ASC;
