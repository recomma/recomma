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
