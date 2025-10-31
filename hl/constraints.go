package hl

import (
	"context"
	"fmt"
	"math"
	"sync"

	"github.com/sonirico/go-hyperliquid"
)

// CoinConstraints captures Hyperliquid-specific rounding requirements for a coin.
type CoinConstraints struct {
	Coin         string
	SizeDecimals int
	SizeStep     float64
	PriceSigFigs int
	MinNotional  float64
}

// RoundSize snaps the provided size to the nearest allowed lot increment.
func (c CoinConstraints) RoundSize(size float64) float64 {
	if c.SizeStep <= 0 {
		return size
	}
	return math.Round(size/c.SizeStep) * c.SizeStep
}

// RoundPrice applies Hyperliquid's significant-figure rounding to the price.
func (c CoinConstraints) RoundPrice(price float64) float64 {
	sig := c.PriceSigFigs
	if sig <= 0 {
		sig = 5
	}
	return roundToSignificantFigures(price, sig)
}

// NotionalBelowMinimum reports whether the notional violates the venue minimum.
func (c CoinConstraints) NotionalBelowMinimum(size, price float64) bool {
	if c.MinNotional <= 0 {
		return false
	}
	notional := math.Abs(size * price)
	return notional+1e-12 < c.MinNotional
}

// ConstraintsResolver fetches/caches Hyperliquid metadata for rounding.
type ConstraintsResolver interface {
	Resolve(ctx context.Context, coin string) (CoinConstraints, error)
}

// InfoProvider describes the subset of hyperliquid.Info used for metadata discovery.
type InfoProvider interface {
	MetaAndAssetCtxs(ctx context.Context) (*hyperliquid.MetaAndAssetCtxs, error)
	SpotMetaAndAssetCtxs(ctx context.Context) (*hyperliquid.SpotMetaAndAssetCtxs, error)
}

// MetadataCache resolves rounding metadata once and keeps it hot for callers.
type MetadataCache struct {
	info InfoProvider

	once sync.Once
	mu   sync.RWMutex
	// coin name (e.g. ETH) -> decimals
	decimals map[string]int
}

// NewMetadataCache builds a resolver backed by the provided Info client.
func NewMetadataCache(info InfoProvider) *MetadataCache {
	return &MetadataCache{
		info:     info,
		decimals: make(map[string]int),
	}
}

// Resolve implements ConstraintsResolver.
func (m *MetadataCache) Resolve(ctx context.Context, coin string) (CoinConstraints, error) {
	if coin == "" {
		return CoinConstraints{}, fmt.Errorf("coin is required")
	}

	m.ensureLoaded(ctx)

	m.mu.RLock()
	decimals, ok := m.decimals[coin]
	m.mu.RUnlock()
	if !ok {
		return CoinConstraints{}, fmt.Errorf("unknown hyperliquid coin %q", coin)
	}

	step := math.Pow10(-decimals)
	return CoinConstraints{
		Coin:         coin,
		SizeDecimals: decimals,
		SizeStep:     step,
		PriceSigFigs: 5,
		// Hyperliquid enforces minimum notionals per asset. Absent a direct feed,
		// treat the smallest permissible lot as the baseline and let the caller
		// scale by price. Callers may override if venue data is available.
		MinNotional: 0,
	}, nil
}

func (m *MetadataCache) ensureLoaded(ctx context.Context) {
	m.once.Do(func() {
		perpMeta, perpErr := m.info.MetaAndAssetCtxs(ctx)
		if perpErr == nil {
			m.mu.Lock()
			for _, asset := range perpMeta.Meta.Universe {
				m.decimals[asset.Name] = asset.SzDecimals
			}
			m.mu.Unlock()
		}

		spotMeta, spotErr := m.info.SpotMetaAndAssetCtxs(ctx)
		if spotErr == nil {
			m.mu.Lock()
			for _, asset := range spotMeta.Meta.Universe {
				if asset.Index >= 0 && asset.Index < len(spotMeta.Meta.Tokens) {
					token := spotMeta.Meta.Tokens[asset.Tokens[0]]
					m.decimals[asset.Name] = token.SzDecimals
				}
			}
			m.mu.Unlock()
		}
	})
}

func roundToSignificantFigures(price float64, sigFigs int) float64 {
	if sigFigs <= 0 {
		return price
	}
	if price == 0 {
		return 0
	}

	absPrice := math.Abs(price)
	integerPart := math.Floor(absPrice)

	numIntegerDigits := 0
	if integerPart > 0 {
		temp := int(integerPart)
		for temp > 0 {
			temp /= 10
			numIntegerDigits++
		}

		if numIntegerDigits >= sigFigs {
			return math.Copysign(integerPart, price)
		}

		sigFigsLeft := sigFigs - numIntegerDigits
		rounded := roundToDecimals(absPrice, sigFigsLeft)
		return math.Copysign(rounded, price)
	}

	multiplications := 0
	for absPrice < 1 {
		absPrice *= 10
		multiplications++
	}

	rounded := roundToDecimals(absPrice, sigFigs-1)
	return math.Copysign(rounded/math.Pow(10, float64(multiplications)), price)
}

func roundToDecimals(value float64, decimals int) float64 {
	pow := math.Pow(10, float64(decimals))
	return math.Round(value*pow) / pow
}
