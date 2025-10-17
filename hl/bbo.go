package hl

import (
	"time"

	"github.com/sonirico/go-hyperliquid"
)

type BestBidOffer struct {
	Coin string
	Time time.Time
	Bid  Level
	Ask  Level
}

type Level struct {
	Price float64
	Size  float64
}

func WsBBOToBBO(in hyperliquid.Bbo) BestBidOffer {
	out := BestBidOffer{
		Coin: in.Coin,
		Time: time.UnixMilli(in.Time),
		Bid: Level{
			Price: in.Bbo[0].Px,
			Size:  in.Bbo[0].Sz,
		},
		Ask: Level{
			Price: in.Bbo[1].Px,
			Size:  in.Bbo[1].Sz,
		},
	}

	return out
}
