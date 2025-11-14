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
	if len(m.sources) == 0 {
		return nil, errors.New("no hyperliquid price sources configured")
	}

	var errs []error
	try := func(id recomma.VenueID) (<-chan hl.BestBidOffer, bool) {
		client, ok := m.sources[id]
		if !ok || client == nil {
			return nil, false
		}
		ch, err := client.SubscribeBBO(ctx, coin)
		if err != nil {
			if m.logger != nil {
				m.logger.Warn("price subscription failed",
					slog.String("venue", string(id)),
					slog.String("coin", coin),
					slog.String("error", err.Error()),
				)
			}
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
			return nil, false
		}
		if m.logger != nil {
			m.logger.Debug("price subscription registered",
				slog.String("venue", string(id)),
				slog.String("coin", coin),
			)
		}
		return ch, true
	}

	if m.primary != "" {
		if ch, ok := try(m.primary); ok {
			return ch, nil
		}
	}

	for _, id := range m.order {
		if id == m.primary {
			continue
		}
		if ch, ok := try(id); ok {
			return ch, nil
		}
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return nil, errors.New("no hyperliquid price source available")
}

// decorateVenueFlags adds venue-specific flags to the configuration
func decorateVenueFlags(src map[string]interface{}, isPrimary bool) map[string]interface{} {
	flags := cloneVenueFlags(src)
	if flags == nil && !isPrimary {
		return nil
	}
	if flags == nil {
		flags = make(map[string]interface{}, 1)
	}
	flags["is_primary"] = isPrimary
	return flags
}

// cloneVenueFlags creates a shallow copy of venue flags
func cloneVenueFlags(src map[string]interface{}) map[string]interface{} {
	if len(src) == 0 {
		if src == nil {
			return nil
		}
		return map[string]interface{}{}
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
