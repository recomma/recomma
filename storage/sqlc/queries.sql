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
    venue_id,
    wallet,
    order_id,
    action_kind,
    create_payload,
    modify_payloads,
    cancel_payload,
    payload_type,
    payload_blob,
    updated_at_utc,
    botevent_row_id
) VALUES (
    sqlc.arg(venue_id),
    sqlc.arg(wallet),
    sqlc.arg(order_id),
    'create',
    json(sqlc.arg(create_payload)),
    CAST('[]' AS BLOB),
    NULL,
    sqlc.arg(payload_type),
    sqlc.arg(payload_blob),
    CAST(unixepoch('now','subsec') * 1000 AS INTEGER),
    sqlc.arg(botevent_row_id)
)
ON CONFLICT(venue_id, wallet, order_id) DO UPDATE SET
    order_id             = excluded.order_id,
    create_payload = excluded.create_payload,
    action_kind    = 'create',
    payload_type   = excluded.payload_type,
    payload_blob   = excluded.payload_blob,
    updated_at_utc = CAST(unixepoch('now','subsec') * 1000 AS INTEGER);

-- name: AppendHyperliquidModify :exec
INSERT INTO hyperliquid_submissions (
    venue_id,
    wallet,
    order_id,
    action_kind,
    create_payload,
    modify_payloads,
    cancel_payload,
    payload_type,
    payload_blob,
    updated_at_utc,
    botevent_row_id
) VALUES (
    sqlc.arg(venue_id),
    sqlc.arg(wallet),
    sqlc.arg(order_id),
    'modify',
    NULL,
    json_array(json(sqlc.arg(modify_payload))),
    NULL,
    sqlc.arg(payload_type),
    sqlc.arg(payload_blob),
    CAST(unixepoch('now','subsec') * 1000 AS INTEGER),
    sqlc.arg(botevent_row_id)
)
ON CONFLICT(venue_id, wallet, order_id) DO UPDATE SET
    order_id = excluded.order_id,
    modify_payloads = json_insert(
        COALESCE(hyperliquid_submissions.modify_payloads, CAST('[]' AS BLOB)),
        '$[#]',
        json(json_extract(excluded.modify_payloads, '$[0]'))
    ),
    action_kind    = 'modify',
    payload_type   = excluded.payload_type,
    payload_blob   = excluded.payload_blob,
    updated_at_utc = CAST(unixepoch('now','subsec') * 1000 AS INTEGER);

-- name: UpsertHyperliquidCancel :exec
INSERT INTO hyperliquid_submissions (
    venue_id,
    wallet,
    order_id,
    action_kind,
    create_payload,
    modify_payloads,
    cancel_payload,
    payload_type,
    payload_blob,
    updated_at_utc,
    botevent_row_id
) VALUES (
    sqlc.arg(venue_id),
    sqlc.arg(wallet),
    sqlc.arg(order_id),
    'cancel',
    NULL,
    '[]',
    json(sqlc.arg(cancel_payload)),
    sqlc.arg(payload_type),
    sqlc.arg(payload_blob),
    CAST(unixepoch('now','subsec') * 1000 AS INTEGER),
    sqlc.arg(botevent_row_id)
)
ON CONFLICT(venue_id, wallet, order_id) DO UPDATE SET
    order_id             = excluded.order_id,
    cancel_payload = excluded.cancel_payload,
    action_kind    = 'cancel',
    payload_type   = excluded.payload_type,
    payload_blob   = excluded.payload_blob,
    updated_at_utc = CAST(unixepoch('now','subsec') * 1000 AS INTEGER);

-- name: CloneHyperliquidSubmissionsToWallet :exec
INSERT INTO hyperliquid_submissions (
    venue_id,
    wallet,
    order_id,
    action_kind,
    create_payload,
    modify_payloads,
    cancel_payload,
    payload_type,
    payload_blob,
    updated_at_utc,
    botevent_row_id
)
SELECT
    src.venue_id,
    sqlc.arg(to_wallet) AS wallet,
    src.order_id,
    src.action_kind,
    src.create_payload,
    src.modify_payloads,
    src.cancel_payload,
    src.payload_type,
    src.payload_blob,
    src.updated_at_utc,
    src.botevent_row_id
FROM hyperliquid_submissions AS src
WHERE src.venue_id = sqlc.arg(venue_id)
  AND src.wallet = sqlc.arg(from_wallet)
ON CONFLICT(venue_id, wallet, order_id) DO NOTHING;

-- name: CloneHyperliquidStatusesToWallet :exec
INSERT INTO hyperliquid_status_history (
    venue_id,
    wallet,
    order_id,
    payload_type,
    payload_blob,
    recorded_at_utc
)
SELECT
    src.venue_id,
    sqlc.arg(to_wallet) AS wallet,
    src.order_id,
    src.payload_type,
    src.payload_blob,
    src.recorded_at_utc
FROM hyperliquid_status_history AS src
WHERE src.venue_id = sqlc.arg(venue_id)
  AND src.wallet = sqlc.arg(from_wallet)
ON CONFLICT(venue_id, wallet, order_id, recorded_at_utc) DO NOTHING;

-- name: DeleteHyperliquidStatusesForWallet :exec
DELETE FROM hyperliquid_status_history
WHERE venue_id = sqlc.arg(venue_id)
  AND wallet = sqlc.arg(wallet);

-- name: DeleteHyperliquidSubmissionsForWallet :exec
DELETE FROM hyperliquid_submissions
WHERE venue_id = sqlc.arg(venue_id)
  AND wallet = sqlc.arg(wallet);

-- name: InsertHyperliquidStatus :exec
INSERT INTO hyperliquid_status_history (
    venue_id,
    wallet,
    order_id,
    payload_type,
    payload_blob,
    recorded_at_utc
) VALUES (
    sqlc.arg(venue_id),
    sqlc.arg(wallet),
    sqlc.arg(order_id),
    sqlc.arg(payload_type),
    sqlc.arg(payload_blob),
    sqlc.arg(recorded_at_utc)
);

-- name: FetchLatestHyperliquidStatus :one
SELECT
    payload_type,
    payload_blob
FROM hyperliquid_status_history
WHERE venue_id = sqlc.arg(venue_id)
  AND wallet = sqlc.arg(wallet)
  AND order_id = sqlc.arg(order_id)
ORDER BY recorded_at_utc DESC
LIMIT 1;

-- name: ListHyperliquidStatuses :many
SELECT
    payload_type,
    payload_blob,
    recorded_at_utc
FROM hyperliquid_status_history
WHERE venue_id = sqlc.arg(venue_id)
  AND wallet = sqlc.arg(wallet)
  AND order_id = sqlc.arg(order_id)
ORDER BY recorded_at_utc ASC;

-- name: FetchHyperliquidSubmission :one
SELECT
    order_id,
    action_kind,
    CAST(create_payload AS BLOB)  AS create_payload,
    CAST(modify_payloads AS BLOB) AS modify_payloads,
    CAST(cancel_payload AS BLOB)  AS cancel_payload,
    payload_type,
    payload_blob,
    updated_at_utc,
    botevent_row_id
FROM hyperliquid_submissions
WHERE venue_id = sqlc.arg(venue_id)
  AND wallet = sqlc.arg(wallet)
  AND order_id = sqlc.arg(order_id);

-- name: FetchLatestHyperliquidSubmissionAnyIdentifier :one
SELECT
    venue_id,
    wallet,
    order_id,
    action_kind,
    CAST(create_payload  AS BLOB) AS create_payload,
    CAST(modify_payloads AS BLOB) AS modify_payloads,
    CAST(cancel_payload  AS BLOB) AS cancel_payload,
    payload_type,
    payload_blob,
    updated_at_utc,
    botevent_row_id
FROM hyperliquid_submissions
WHERE order_id = sqlc.arg(order_id)
ORDER BY updated_at_utc DESC
LIMIT 1;

-- name: FetchLatestHyperliquidStatusAnyIdentifier :one
SELECT
    venue_id,
    wallet,
    payload_type,
    payload_blob,
    recorded_at_utc
FROM hyperliquid_status_history
WHERE order_id = sqlc.arg(order_id)
ORDER BY recorded_at_utc DESC
LIMIT 1;

-- name: ListHyperliquidOrderIds :many
SELECT
    venue_id,
    wallet,
    order_id,
    order_id
FROM hyperliquid_submissions
WHERE venue_id = COALESCE(sqlc.arg(venue_id), venue_id)
ORDER BY venue_id ASC, wallet ASC, order_id ASC;

-- name: ListLatestHyperliquidSafetyStatuses :many
WITH ranked_status AS (
    SELECT
        venue_id,
        wallet,
        order_id,
        payload_type,
        payload_blob,
        recorded_at_utc,
        ROW_NUMBER() OVER (
            PARTITION BY venue_id, wallet, order_id
            ORDER BY recorded_at_utc DESC
        ) AS rn
    FROM hyperliquid_status_history
)
SELECT
    b.order_id AS order_id,
    b.bot_id AS bot_id,
    b.deal_id AS deal_id,
    CAST(json_extract(b.payload, '$.OrderType') AS TEXT)        AS order_type,
    CAST(json_extract(b.payload, '$.OrderPosition') AS INTEGER) AS order_position,
    CAST(json_extract(b.payload, '$.OrderSize') AS INTEGER)        AS order_size,
    latest.payload_type AS payload_type,
    latest.payload_blob AS payload_blob,
    CAST(json_extract(latest.payload_blob, '$.status') AS TEXT)            AS hl_status,
    CAST(json_extract(latest.payload_blob, '$.statusTimestamp') AS INTEGER) AS hl_status_timestamp,
    latest.recorded_at_utc AS recorded_at_utc
FROM ranked_status AS latest
JOIN hyperliquid_submissions AS s
  ON s.venue_id = latest.venue_id
 AND s.wallet = latest.wallet
 AND s.order_id = latest.order_id
JOIN threecommas_botevents AS b
  ON b.id = s.botevent_row_id
WHERE latest.rn = 1
  AND s.venue_id = sqlc.arg(venue_id)
  AND b.deal_id = sqlc.arg(deal_id)
  AND CAST(json_extract(b.payload, '$.OrderType') AS TEXT) = 'Safety'
  AND (sqlc.arg(wallet) IS NULL OR latest.wallet = sqlc.arg(wallet))
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

-- Venue inventory

-- name: UpsertVenue :exec
INSERT INTO venues (
    id,
    type,
    display_name,
    wallet,
    flags
) VALUES (
    sqlc.arg(id),
    sqlc.arg(type),
    sqlc.arg(display_name),
    sqlc.arg(wallet),
    json(sqlc.arg(flags))
)
ON CONFLICT(id) DO UPDATE SET
    type         = excluded.type,
    display_name = excluded.display_name,
    wallet       = excluded.wallet,
    flags        = json(excluded.flags);

-- name: DeleteVenue :exec
DELETE FROM venues
WHERE id = sqlc.arg(id);

-- name: GetVenue :one
SELECT
    id,
    type,
    display_name,
    wallet,
    CAST(flags AS BLOB) AS flags
FROM venues
WHERE id = sqlc.arg(id);

-- name: ListVenues :many
SELECT
    id,
    type,
    display_name,
    wallet,
    CAST(flags AS BLOB) AS flags
FROM venues
ORDER BY id ASC;

-- Bot-to-venue assignments

-- name: UpsertBotVenueAssignment :exec
INSERT INTO bot_venue_assignments (
    bot_id,
    venue_id,
    is_primary
) VALUES (
    sqlc.arg(bot_id),
    sqlc.arg(venue_id),
    sqlc.arg(is_primary)
)
ON CONFLICT(bot_id, venue_id) DO UPDATE SET
    is_primary      = excluded.is_primary,
    assigned_at_utc = CAST(unixepoch('now','subsec') * 1000 AS INTEGER);

-- name: DeleteBotVenueAssignment :exec
DELETE FROM bot_venue_assignments
WHERE bot_id = sqlc.arg(bot_id)
  AND venue_id = sqlc.arg(venue_id);

-- name: ListBotVenueAssignments :many
SELECT
    bot_id,
    venue_id,
    is_primary,
    assigned_at_utc
FROM bot_venue_assignments
WHERE bot_id = sqlc.arg(bot_id)
ORDER BY venue_id ASC;

-- name: ListVenueAssignments :many
SELECT
    bot_id,
    venue_id,
    is_primary,
    assigned_at_utc
FROM bot_venue_assignments
WHERE venue_id = sqlc.arg(venue_id)
ORDER BY bot_id ASC;

-- name: GetPrimaryVenueForBot :one
SELECT venue_id
FROM bot_venue_assignments
WHERE bot_id = sqlc.arg(bot_id)
  AND is_primary = 1
LIMIT 1;

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
    venue_id,
    wallet,
    order_id,
    payload_type,
    payload_blob,
    recorded_at_utc
FROM hyperliquid_status_history
WHERE venue_id = sqlc.arg(venue_id)
  AND order_id = sqlc.arg(order_id)
  AND (sqlc.arg(wallet) IS NULL OR wallet = sqlc.arg(wallet))
  AND (sqlc.arg(order_id) IS NULL OR order_id = sqlc.arg(order_id))
  AND recorded_at_utc >= COALESCE(sqlc.arg(observed_from), recorded_at_utc)
  AND recorded_at_utc <= COALESCE(sqlc.arg(observed_to), recorded_at_utc)
ORDER BY recorded_at_utc ASC;

-- name: ListHyperliquidSubmissionsByOrderId :many
SELECT
    venue_id,
    wallet,
    order_id,
    action_kind,
    CAST(create_payload AS BLOB)  AS create_payload,
    CAST(modify_payloads AS BLOB) AS modify_payloads,
    CAST(cancel_payload AS BLOB)  AS cancel_payload,
    updated_at_utc,
    botevent_row_id,
    payload_type,
    payload_blob
FROM hyperliquid_submissions
WHERE venue_id = sqlc.arg(venue_id)
  AND order_id IN (
    SELECT value FROM json_each(sqlc.arg(orderid_list))
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

-- name: InsertScaledOrder :exec
INSERT INTO scaled_orders (
    venue_id,
    wallet,
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
    skipped,
    skip_reason,
    payload_type,
    payload_blob
) VALUES (
    sqlc.arg(venue_id),
    sqlc.arg(wallet),
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
    sqlc.arg(skipped),
    sqlc.narg(skip_reason),
    sqlc.narg(payload_type),
    sqlc.narg(payload_blob)
);

-- name: ListScaledOrdersByOrderId :many
SELECT
    venue_id,
    wallet,
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
    skipped,
    skip_reason,
    payload_type,
    payload_blob
FROM scaled_orders
WHERE order_id = sqlc.arg(order_id)
   OR order_id LIKE sqlc.arg(order_id_prefix)
ORDER BY created_at_utc ASC, order_id ASC;

-- name: ListScaledOrdersByDeal :many
SELECT
    venue_id,
    wallet,
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
    skipped,
    skip_reason,
    payload_type,
    payload_blob
FROM scaled_orders
WHERE deal_id = sqlc.arg(deal_id)
ORDER BY created_at_utc ASC, order_id ASC;

-- name: ListScaledOrderAuditsForOrderId :many
SELECT
    venue_id,
    wallet,
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
    skipped,
    skip_reason,
    payload_type,
    payload_blob
FROM scaled_orders
WHERE venue_id = sqlc.arg(venue_id)
  AND (
        order_id = sqlc.arg(order_id)
        OR order_id LIKE sqlc.arg(order_id_prefix)
    )
  AND created_at_utc >= sqlc.arg(observed_from)
  AND created_at_utc <= sqlc.arg(observed_to)
ORDER BY created_at_utc ASC, order_id ASC;
