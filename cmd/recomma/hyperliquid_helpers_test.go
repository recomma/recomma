package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/recomma/recomma/hl"
	"github.com/recomma/recomma/internal/vault"
	"github.com/recomma/recomma/recomma"
	"github.com/stretchr/testify/require"
)

type stubBBOSubscriber struct {
	ch     chan hl.BestBidOffer
	err    error
	calls  *int
	closed bool
}

func (s *stubBBOSubscriber) SubscribeBBO(ctx context.Context, coin string) (<-chan hl.BestBidOffer, error) {
	if s.calls != nil {
		*s.calls++
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.ch, nil
}

func newDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
}

func TestPriceSourceMultiplexer_SubscribeBBO(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	channel := make(chan hl.BestBidOffer)
	primary := &stubBBOSubscriber{ch: channel}

	mux := newPriceSourceMultiplexer(newDiscardLogger(), "venue:primary", nil, map[recomma.VenueID]bboSubscriber{
		"venue:primary": primary,
	})

	ch, err := mux.SubscribeBBO(ctx, "DOGE")
	require.NoError(t, err)
	require.Equal(t, (<-chan hl.BestBidOffer)(channel), ch)
}

func TestPriceSourceMultiplexer_Fallback(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	primaryErr := errors.New("primary")
	var primaryCalls int
	primary := &stubBBOSubscriber{err: primaryErr, calls: &primaryCalls}
	fallbackCh := make(chan hl.BestBidOffer)
	fallback := &stubBBOSubscriber{ch: fallbackCh}

	mux := newPriceSourceMultiplexer(newDiscardLogger(), "venue:primary", []recomma.VenueID{"venue:primary", "venue:secondary"}, map[recomma.VenueID]bboSubscriber{
		"venue:primary":   primary,
		"venue:secondary": fallback,
	})

	ch, err := mux.SubscribeBBO(ctx, "DOGE")
	require.NoError(t, err)
	require.Equal(t, (<-chan hl.BestBidOffer)(fallbackCh), ch)
	require.Equal(t, 1, primaryCalls, "primary should be attempted exactly once")
}

func TestPriceSourceMultiplexer_AllFail(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	errPrimary := errors.New("primary down")
	errSecondary := errors.New("secondary down")

	mux := newPriceSourceMultiplexer(newDiscardLogger(), "venue:primary", []recomma.VenueID{"venue:primary", "venue:secondary"}, map[recomma.VenueID]bboSubscriber{
		"venue:primary":   &stubBBOSubscriber{err: errPrimary},
		"venue:secondary": &stubBBOSubscriber{err: errSecondary},
	})

	ch, err := mux.SubscribeBBO(ctx, "DOGE")
	require.Nil(t, ch)
	require.Error(t, err)
	require.ErrorIs(t, err, errPrimary)
	require.ErrorIs(t, err, errSecondary)
}

func TestPriceSourceMultiplexer_NoSources(t *testing.T) {
	t.Parallel()

	mux := newPriceSourceMultiplexer(newDiscardLogger(), "", nil, nil)
	ch, err := mux.SubscribeBBO(context.Background(), "DOGE")
	require.Nil(t, ch)
	require.EqualError(t, err, "no hyperliquid price sources configured")
}

func TestDecorateVenueFlags(t *testing.T) {
	t.Parallel()

	require.Nil(t, decorateVenueFlags(nil, false))

	flags := decorateVenueFlags(nil, true)
	require.Equal(t, map[string]interface{}{"is_primary": true}, flags)

	src := map[string]interface{}{"foo": "bar"}
	decorated := decorateVenueFlags(src, false)
	require.Equal(t, map[string]interface{}{"foo": "bar", "is_primary": false}, decorated)
	require.Equal(t, "bar", src["foo"], "source map should remain unchanged")
}

func TestCloneVenueFlags(t *testing.T) {
	t.Parallel()

	require.Nil(t, cloneVenueFlags(nil))
	require.NotNil(t, cloneVenueFlags(map[string]interface{}{}))

	src := map[string]interface{}{"foo": "bar"}
	clone := cloneVenueFlags(src)
	require.Equal(t, src, clone)
	clone["foo"] = "baz"
	require.Equal(t, "bar", src["foo"])
}

func TestShouldUseSentinelDefaultHyperliquidWallet(t *testing.T) {
	t.Parallel()

	venues := []vault.VenueSecret{
		{ID: "hyperliquid:primary", Type: "hyperliquid", Wallet: "0xPRIME"},
		{ID: "hyperliquid:secondary", Type: "hyperliquid", Wallet: "0xABC"},
		{ID: "hyperliquid:default", Type: "hyperliquid", Wallet: "default"},
	}

	require.False(t, shouldUseSentinelDefaultHyperliquidWallet(venues, "hyperliquid:primary", "", "hyperliquid:default"))
	require.False(t, shouldUseSentinelDefaultHyperliquidWallet(venues, "hyperliquid:primary", "0xPRIME", "hyperliquid:default"))
	require.True(t, shouldUseSentinelDefaultHyperliquidWallet(venues, "hyperliquid:primary", "0xABC", "hyperliquid:default"))

	// Non-hyperliquid venues should be ignored.
	venues = append(venues, vault.VenueSecret{ID: "spot:one", Type: "spot", Wallet: "0xABC"})
	require.True(t, shouldUseSentinelDefaultHyperliquidWallet(venues, "hyperliquid:primary", "0xABC", "hyperliquid:default"))
}
