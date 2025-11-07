package hl_test

import (
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
)

const (
	hyperliquidTestVenue  = recomma.VenueID("hyperliquid:test")
	hyperliquidTestWallet = "0xtest"
)

func testIdentifier(oid orderid.OrderId) recomma.OrderIdentifier {
	return recomma.NewOrderIdentifier(hyperliquidTestVenue, hyperliquidTestWallet, oid)
}
