package main

import (
	"context"
	"errors"
	"testing"

	"github.com/recomma/recomma/emitter"
	"github.com/recomma/recomma/hl"
	"github.com/recomma/recomma/hl/ws"
	"github.com/recomma/recomma/internal/vault"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
	"github.com/recomma/recomma/storage"
	"github.com/sonirico/go-hyperliquid"
)

type stubQueue struct {
	items []recomma.OrderWork
}

func (q *stubQueue) Add(item recomma.OrderWork) {
	q.items = append(q.items, item)
}

type stubEmitter struct {
	calls int
	last  recomma.OrderWork
}

func (e *stubEmitter) Emit(_ context.Context, w recomma.OrderWork) error {
	e.calls++
	e.last = w
	return nil
}

type stubStatusClient struct{}

func (stubStatusClient) QueryOrderByCloid(context.Context, string) (*hyperliquid.OrderQueryResult, error) {
	return nil, nil
}

func TestRegisterHyperliquidEmitterRegistersDefaultAlias(t *testing.T) {
	queue := emitter.NewQueueEmitter(&stubQueue{})
	submitter := &stubEmitter{}

	primaryIdent := recomma.VenueID("primary")
	defaultIdent := storage.DefaultHyperliquidIdentifier(orderid.OrderId{}).VenueID

	registerHyperliquidEmitter(queue, submitter, primaryIdent, primaryIdent, defaultIdent)

	oid := orderid.OrderId{BotID: 1, DealID: 2, BotEventID: 3}
	ident := storage.DefaultHyperliquidIdentifier(oid)
	work := recomma.OrderWork{Identifier: ident, OrderId: oid}

	if err := queue.Dispatch(context.Background(), work); err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}

	if submitter.calls != 1 {
		t.Fatalf("expected submitter to be called once, got %d", submitter.calls)
	}
	if submitter.last.Identifier.VenueID != ident.VenueID {
		t.Fatalf("unexpected venue dispatched: got %s want %s", submitter.last.Identifier.VenueID, ident.VenueID)
	}
}

func TestRegisterHyperliquidEmitterDoesNotAliasNonPrimary(t *testing.T) {
	queue := emitter.NewQueueEmitter(&stubQueue{})
	submitter := &stubEmitter{}

	venueIdent := recomma.VenueID("secondary")
	primaryIdent := recomma.VenueID("primary")
	defaultIdent := storage.DefaultHyperliquidIdentifier(orderid.OrderId{}).VenueID

	registerHyperliquidEmitter(queue, submitter, venueIdent, primaryIdent, defaultIdent)

	oid := orderid.OrderId{BotID: 4, DealID: 5, BotEventID: 6}
	venueWork := recomma.OrderWork{Identifier: recomma.NewOrderIdentifier(venueIdent, "wallet", oid), OrderId: oid}
	if err := queue.Dispatch(context.Background(), venueWork); err != nil {
		t.Fatalf("expected direct venue dispatch to succeed: %v", err)
	}
	if submitter.calls != 1 {
		t.Fatalf("expected submitter to be called once, got %d", submitter.calls)
	}

	submitter.calls = 0

	defaultWork := recomma.OrderWork{Identifier: storage.DefaultHyperliquidIdentifier(oid), OrderId: oid}
	err := queue.Dispatch(context.Background(), defaultWork)
	if !errors.Is(err, emitter.ErrUnregisteredVenueEmitter) {
		t.Fatalf("expected unregistered venue error, got %v", err)
	}
	if submitter.calls != 0 {
		t.Fatalf("expected submitter not to be called, got %d", submitter.calls)
	}
}

func TestRegisterHyperliquidStatusClientAliasesDefault(t *testing.T) {
	registry := make(hl.StatusClientRegistry)
	client := stubStatusClient{}

	primaryIdent := recomma.VenueID("hyperliquid:primary")
	defaultIdent := storage.DefaultHyperliquidIdentifier(orderid.OrderId{}).VenueID

	registerHyperliquidStatusClient(registry, client, primaryIdent, primaryIdent, defaultIdent)

	if _, ok := registry[primaryIdent]; !ok {
		t.Fatalf("expected primary client to be registered")
	}
	alias, ok := registry[defaultIdent]
	if !ok {
		t.Fatalf("expected default alias to be registered")
	}
	if alias != client {
		t.Fatalf("expected alias to reference same client")
	}
}

func TestRegisterHyperliquidStatusClientDoesNotAliasNonPrimary(t *testing.T) {
	registry := make(hl.StatusClientRegistry)
	client := stubStatusClient{}

	primaryIdent := recomma.VenueID("hyperliquid:primary")
	secondaryIdent := recomma.VenueID("hyperliquid:secondary")
	defaultIdent := storage.DefaultHyperliquidIdentifier(orderid.OrderId{}).VenueID

	registerHyperliquidStatusClient(registry, client, secondaryIdent, primaryIdent, defaultIdent)

	if _, ok := registry[secondaryIdent]; !ok {
		t.Fatalf("expected secondary client to be registered")
	}
	if _, ok := registry[defaultIdent]; ok {
		t.Fatalf("expected default alias not to be registered for non-primary venue")
	}
}

func TestRegisterHyperliquidWsClientAliasesDefault(t *testing.T) {
	registry := make(map[recomma.VenueID]*ws.Client)
	client := &ws.Client{}

	primaryIdent := recomma.VenueID("hyperliquid:primary")
	defaultIdent := storage.DefaultHyperliquidIdentifier(orderid.OrderId{}).VenueID

	registerHyperliquidWsClient(registry, client, primaryIdent, primaryIdent, defaultIdent)

	if registry[primaryIdent] != client {
		t.Fatalf("expected primary websocket client to be registered")
	}
	if registry[defaultIdent] != client {
		t.Fatalf("expected default alias to reference websocket client")
	}
}

func TestRegisterHyperliquidWsClientDoesNotAliasNonPrimary(t *testing.T) {
	registry := make(map[recomma.VenueID]*ws.Client)
	client := &ws.Client{}

	primaryIdent := recomma.VenueID("hyperliquid:primary")
	secondaryIdent := recomma.VenueID("hyperliquid:secondary")
	defaultIdent := storage.DefaultHyperliquidIdentifier(orderid.OrderId{}).VenueID

	registerHyperliquidWsClient(registry, client, secondaryIdent, primaryIdent, defaultIdent)

	if registry[secondaryIdent] != client {
		t.Fatalf("expected secondary websocket client to be registered")
	}
	if _, ok := registry[defaultIdent]; ok {
		t.Fatalf("expected default alias not to be registered for non-primary venue")
	}
}

func TestShouldUseSentinelDefaultHyperliquidWalletSkipsPrimary(t *testing.T) {
	defaultIdent := storage.DefaultHyperliquidIdentifier(orderid.OrderId{}).VenueID
	venues := []vault.VenueSecret{
		{
			ID:      "hyperliquid:primary",
			Type:    "hyperliquid",
			Wallet:  "hl-primary-wallet",
			Primary: true,
		},
	}

	if shouldUseSentinelDefaultHyperliquidWallet(venues, recomma.VenueID("hyperliquid:primary"), "hl-primary-wallet", defaultIdent) {
		t.Fatalf("expected sentinel wallet guard to ignore the primary venue")
	}
}

func TestShouldUseSentinelDefaultHyperliquidWalletDetectsDuplicateWallet(t *testing.T) {
	defaultIdent := storage.DefaultHyperliquidIdentifier(orderid.OrderId{}).VenueID
	venues := []vault.VenueSecret{
		{
			ID:      "hyperliquid:primary",
			Type:    "hyperliquid",
			Wallet:  "hl-primary-wallet",
			Primary: true,
		},
		{
			ID:     "hyperliquid:alias",
			Type:   "hyperliquid",
			Wallet: "hl-primary-wallet",
		},
	}

	if !shouldUseSentinelDefaultHyperliquidWallet(venues, recomma.VenueID("hyperliquid:primary"), "hl-primary-wallet", defaultIdent) {
		t.Fatalf("expected sentinel wallet guard to activate when duplicate wallets exist")
	}
}
