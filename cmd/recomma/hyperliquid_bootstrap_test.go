package main

import (
	"context"
	"errors"
	"testing"

	"github.com/recomma/recomma/emitter"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
	"github.com/recomma/recomma/storage"
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

func TestShouldSkipHyperliquidVenueUpsert(t *testing.T) {
	primaryIdent := recomma.VenueID("hyperliquid:primary")
	primaryWallet := "hl-primary-wallet"

	tests := []struct {
		name         string
		venueIdent   recomma.VenueID
		wallet       string
		isPrimary    bool
		expectedSkip bool
	}{
		{
			name:         "skip primary with matching wallet",
			venueIdent:   primaryIdent,
			wallet:       primaryWallet,
			isPrimary:    true,
			expectedSkip: true,
		},
		{
			name:         "do not skip when not marked primary",
			venueIdent:   primaryIdent,
			wallet:       primaryWallet,
			isPrimary:    false,
			expectedSkip: false,
		},
		{
			name:         "do not skip when wallet differs",
			venueIdent:   primaryIdent,
			wallet:       "other-wallet",
			isPrimary:    true,
			expectedSkip: false,
		},
		{
			name:         "do not skip when venue differs",
			venueIdent:   recomma.VenueID("hyperliquid:secondary"),
			wallet:       primaryWallet,
			isPrimary:    true,
			expectedSkip: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			skip := shouldSkipHyperliquidVenueUpsert(tt.venueIdent, primaryIdent, tt.wallet, primaryWallet, tt.isPrimary)
			if skip != tt.expectedSkip {
				t.Fatalf("expected skip=%t, got %t", tt.expectedSkip, skip)
			}
		})
	}
}
