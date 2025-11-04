package main

import (
	"github.com/recomma/recomma/emitter"
	"github.com/recomma/recomma/recomma"
)

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
