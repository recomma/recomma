package engine

import (
	"testing"

	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
	"github.com/recomma/recomma/storage"
	"github.com/stretchr/testify/require"
)

func TestNormalizeSubmissionIdentifiersPrefersAssignments(t *testing.T) {
	oid := orderid.OrderId{BotID: 1, DealID: 2, BotEventID: 3}

	assignments := []storage.VenueAssignment{
		{
			VenueID: recomma.VenueID("hyperliquid:test"),
			Wallet:  "0xabc",
		},
		{
			VenueID: recomma.VenueID("hyperliquid:secondary"),
			Wallet:  "0xdef",
		},
	}

	stored := []recomma.OrderIdentifier{
		recomma.NewOrderIdentifier(recomma.VenueID("hyperliquid:default"), "0xAbC", oid),
		recomma.NewOrderIdentifier(recomma.VenueID("hyperliquid:test"), "0xabc", oid),
		recomma.NewOrderIdentifier(recomma.VenueID("hyperliquid:secondary"), "0xdef", oid),
		recomma.NewOrderIdentifier(recomma.VenueID("hyperliquid:ignored"), "0xbeef", oid),
	}

	got := normalizeSubmissionIdentifiers(assignments, stored)

	require.Len(t, got, 2)
	require.Contains(t, got, recomma.NewOrderIdentifier(recomma.VenueID("hyperliquid:test"), "0xabc", oid))
	require.Contains(t, got, recomma.NewOrderIdentifier(recomma.VenueID("hyperliquid:secondary"), "0xdef", oid))
}

func TestNormalizeSubmissionIdentifiersNoAssignments(t *testing.T) {
	oid := orderid.OrderId{BotID: 42, DealID: 9, BotEventID: 77}

	stored := []recomma.OrderIdentifier{
		recomma.NewOrderIdentifier(recomma.VenueID("hyperliquid:test"), "0xabc", oid),
		recomma.NewOrderIdentifier(recomma.VenueID("hyperliquid:test"), "0xabc", oid),
	}

	got := normalizeSubmissionIdentifiers(nil, stored)

	require.Len(t, got, 1)
	require.Equal(t, recomma.NewOrderIdentifier(recomma.VenueID("hyperliquid:test"), "0xabc", oid), got[0])
}

