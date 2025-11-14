package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/recomma/recomma/hl"
	"github.com/recomma/recomma/hl/ws"
	"github.com/recomma/recomma/internal/api"
	"github.com/recomma/recomma/internal/vault"
	"github.com/recomma/recomma/recomma"
	"github.com/recomma/recomma/storage"
)

// priceSourceMultiplexer manages multiple WebSocket price sources with failover
type priceSourceMultiplexer struct {
	logger  *slog.Logger
	primary recomma.VenueID
	order   []recomma.VenueID
	sources map[recomma.VenueID]*ws.Client
}

// newPriceSourceMultiplexer creates a new price source multiplexer
func newPriceSourceMultiplexer(logger *slog.Logger, primary recomma.VenueID, order []recomma.VenueID, sources map[recomma.VenueID]*ws.Client) *priceSourceMultiplexer {
	// Create defensive copies to prevent external mutations
	orderCopy := append([]recomma.VenueID(nil), order...)
	sourceCopy := make(map[recomma.VenueID]*ws.Client, len(sources))
	for id, src := range sources {
		sourceCopy[id] = src
	}

	return &priceSourceMultiplexer{
		logger:  logger,
		primary: primary,
		order:   orderCopy,
		sources: sourceCopy,
	}
}

// SubscribeBBO attempts to subscribe to best bid/offer from venues in priority order
func (m *priceSourceMultiplexer) SubscribeBBO(ctx context.Context, coin string) (<-chan hl.BestBidOffer, error) {
	var lastErr error

	for _, venueID := range m.order {
		client, ok := m.sources[venueID]
		if !ok {
			continue
		}

		ch, err := client.SubscribeBBO(ctx, coin)
		if err == nil {
			if venueID != m.primary {
				m.logger.Warn("using fallback price source",
					slog.String("coin", coin),
					slog.String("venue", string(venueID)),
					slog.String("primary", string(m.primary)))
			}
			return ch, nil
		}

		lastErr = err
		m.logger.Debug("price source unavailable",
			slog.String("coin", coin),
			slog.String("venue", string(venueID)),
			slog.String("error", err.Error()))
	}

	if lastErr != nil {
		return nil, fmt.Errorf("all price sources failed: %w", lastErr)
	}

	return nil, errors.New("no price sources configured")
}

// decorateVenueFlags adds venue-specific flags to the configuration
func decorateVenueFlags(src map[string]interface{}, isPrimary bool) map[string]interface{} {
	if src == nil {
		src = make(map[string]interface{})
	}

	dst := cloneVenueFlags(src)
	dst["is_primary"] = isPrimary

	return dst
}

// cloneVenueFlags creates a shallow copy of venue flags
func cloneVenueFlags(src map[string]interface{}) map[string]interface{} {
	if src == nil {
		return make(map[string]interface{})
	}

	dst := make(map[string]interface{}, len(src))
	for k, v := range src {
		dst[k] = v
	}

	return dst
}

// shouldUseSentinelDefaultHyperliquidWallet determines if a sentinel default wallet should be used
func shouldUseSentinelDefaultHyperliquidWallet(
	venues []vault.VenueSecret,
	primaryIdent recomma.VenueID,
	primaryWallet string,
	defaultIdent recomma.VenueID,
) bool {
	trimmedPrimary := strings.TrimSpace(primaryWallet)
	if trimmedPrimary == "" {
		return false
	}
	for _, venue := range venues {
		if !strings.EqualFold(venue.Type, "hyperliquid") {
			continue
		}
		venueID := recomma.VenueID(strings.TrimSpace(venue.ID))
		if venueID == defaultIdent {
			continue
		}
		if primaryIdent != "" && strings.EqualFold(string(venueID), string(primaryIdent)) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(venue.Wallet), trimmedPrimary) {
			return true
		}
	}
	return false
}

// syncPrimaryVenueAssignments ensures all bots have a primary venue assignment
func syncPrimaryVenueAssignments(
	ctx context.Context,
	store *storage.Storage,
	logger *slog.Logger,
	primaryVenue recomma.VenueID,
) error {
	if primaryVenue == "" {
		return nil
	}

	opts := api.ListBotsOptions{Limit: 100}
	for {
		bots, next, err := store.ListBots(ctx, opts)
		if err != nil {
			return fmt.Errorf("list bots: %w", err)
		}
		if len(bots) == 0 {
			break
		}

		for _, bot := range bots {
			botID := int64(bot.Bot.Id)
			if botID <= 0 {
				continue
			}

			hasPrimary, err := store.HasPrimaryVenueAssignment(ctx, uint32(botID))
			if err != nil {
				return fmt.Errorf("check primary venue for bot %d: %w", botID, err)
			}
			if hasPrimary {
				continue
			}

			if _, err := store.UpsertVenueAssignment(ctx, string(primaryVenue), botID, true); err != nil {
				return fmt.Errorf("assign primary venue for bot %d: %w", botID, err)
			}

			if logger != nil {
				logger.Debug("assigned primary venue to bot",
					slog.Int64("bot_id", botID),
					slog.String("venue", string(primaryVenue)),
				)
			}
		}

		if next == nil || *next == "" {
			break
		}
		opts.PageToken = *next
	}

	return nil
}
