package main

import (
	"context"

	"github.com/recomma/recomma/emitter"
	"github.com/recomma/recomma/hl"
	"github.com/recomma/recomma/hl/ws"
	"github.com/recomma/recomma/recomma"
	"github.com/sonirico/go-hyperliquid"
)

type hyperliquidStatusClient interface {
	QueryOrderByCloid(ctx context.Context, cloid string) (*hyperliquid.OrderQueryResult, error)
}

func registerHyperliquidEmitter(queue *emitter.QueueEmitter, submitter recomma.Emitter, venueIdent, primaryIdent, defaultIdent recomma.VenueID) {
	if queue == nil || submitter == nil {
		return
	}

	queue.Register(venueIdent, submitter)

	if venueIdent != primaryIdent {
		return
	}
	if defaultIdent == "" || defaultIdent == venueIdent {
		return
	}

	queue.Register(defaultIdent, submitter)
}

func registerHyperliquidStatusClient(reg hl.StatusClientRegistry, client hyperliquidStatusClient, venueIdent, primaryIdent, defaultIdent recomma.VenueID) {
	if reg == nil || client == nil {
		return
	}

	reg[venueIdent] = client

	if venueIdent != primaryIdent {
		return
	}
	if defaultIdent == "" || defaultIdent == venueIdent {
		return
	}

	reg[defaultIdent] = client
}

func registerHyperliquidWsClient(reg map[recomma.VenueID]*ws.Client, client *ws.Client, venueIdent, primaryIdent, defaultIdent recomma.VenueID) {
	if reg == nil || client == nil {
		return
	}

	reg[venueIdent] = client

	if venueIdent != primaryIdent {
		return
	}
	if defaultIdent == "" || defaultIdent == venueIdent {
		return
	}

	reg[defaultIdent] = client
}

func shouldSkipHyperliquidVenueUpsert(venueIdent, primaryIdent recomma.VenueID, wallet, primaryWallet string, isPrimary bool) bool {
	if !isPrimary {
		return false
	}
	if venueIdent != primaryIdent {
		return false
	}
	if wallet == "" || primaryWallet == "" {
		return false
	}
	return wallet == primaryWallet
}
