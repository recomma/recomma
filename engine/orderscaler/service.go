package orderscaler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/hl"
	"github.com/recomma/recomma/metadata"
	"github.com/recomma/recomma/storage"
	"github.com/sonirico/go-hyperliquid"
)

// ErrBelowMinimum is returned when the scaled order would violate venue minimums.
var ErrBelowMinimum = errors.New("scaled order below venue minimum")

// Store defines the storage subset required by the scaler.
type Store interface {
	ResolveEffectiveOrderScaler(ctx context.Context, md metadata.Metadata) (storage.EffectiveOrderScaler, error)
	RecordScaledOrder(ctx context.Context, params storage.RecordScaledOrderParams) (storage.ScaledOrderAudit, storage.EffectiveOrderScaler, error)
}

// Constraints resolves Hyperliquid rounding requirements.
type Constraints interface {
	Resolve(ctx context.Context, coin string) (hl.CoinConstraints, error)
}

// Service applies multiplier + rounding rules to Hyperliquid orders.
type Service struct {
	store         Store
	constraints   Constraints
	logger        *slog.Logger
	maxMultiplier float64
}

// New constructs a scaling service.
func New(store Store, constraints Constraints, logger *slog.Logger, opts ...Option) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	svc := &Service{store: store, constraints: constraints, logger: logger.WithGroup("orderscaler")}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

// Option configures optional service behaviour.
type Option func(*Service)

// WithMaxMultiplier overrides the maximum multiplier safety clamp. Values <= 0 disable clamping.
func WithMaxMultiplier(max float64) Option {
	return func(s *Service) {
		if max > 0 {
			s.maxMultiplier = max
		}
	}
}

// Request captures the details required to scale an order.
type Request struct {
	Metadata     metadata.Metadata
	Coin         string
	Side         string
	OriginalSize float64
	Price        float64
	StackIndex   int
	Timestamp    time.Time
}

// Result contains the scaled order + audit metadata.
type Result struct {
	Size       float64
	Price      float64
	Audit      storage.ScaledOrderAudit
	Effective  storage.EffectiveOrderScaler
	Multiplier float64
}

// Scale mutates the provided order request with multiplier + rounding rules.
func (s *Service) Scale(ctx context.Context, req Request, order *hyperliquid.CreateOrderRequest) (Result, error) {
	if order == nil {
		return Result{}, fmt.Errorf("order is nil")
	}
	if req.Coin == "" {
		req.Coin = order.Coin
	}

	effective, err := s.store.ResolveEffectiveOrderScaler(ctx, req.Metadata)
	if err != nil {
		return Result{}, fmt.Errorf("resolve multiplier: %w", err)
	}

	multiplier := effective.Multiplier
	if s.maxMultiplier > 0 && multiplier > s.maxMultiplier {
		s.logger.Warn("effective multiplier exceeds configured max; clamping", slog.Float64("multiplier", multiplier), slog.Float64("max", s.maxMultiplier), slog.Uint64("deal_id", uint64(req.Metadata.DealID)), slog.Uint64("bot_id", uint64(req.Metadata.BotID)))
		multiplier = s.maxMultiplier
	}
	scaledSize := req.OriginalSize * multiplier

	constraints, err := s.constraints.Resolve(ctx, req.Coin)
	if err != nil {
		return Result{}, fmt.Errorf("resolve constraints: %w", err)
	}

	roundedPrice := order.Price
	if req.Price > 0 {
		roundedPrice = req.Price
	}
	roundedPrice = constraints.RoundPrice(roundedPrice)

	roundedSize := constraints.RoundSize(scaledSize)
	roundedSize = clampToNonNegative(roundedSize)

	notional := math.Abs(roundedSize * roundedPrice)
	if constraints.NotionalBelowMinimum(roundedSize, roundedPrice) {
		s.logger.Warn("scaled order violates minimum notional", slog.String("coin", req.Coin), slog.Float64("price", roundedPrice), slog.Float64("size", roundedSize), slog.Float64("notional", notional), slog.Float64("min", constraints.MinNotional), slog.Float64("multiplier", multiplier), slog.Uint64("deal_id", uint64(req.Metadata.DealID)), slog.Uint64("bot_id", uint64(req.Metadata.BotID)))
		return Result{}, ErrBelowMinimum
	}

	order.Price = roundedPrice
	order.Size = roundedSize

	auditParams := storage.RecordScaledOrderParams{
		Metadata:          req.Metadata,
		DealID:            req.Metadata.DealID,
		BotID:             req.Metadata.BotID,
		OriginalSize:      req.OriginalSize,
		ScaledSize:        roundedSize,
		AppliedMultiplier: &multiplier,
		StackIndex:        req.StackIndex,
		OrderSide:         req.Side,
		CreatedAt:         req.Timestamp,
	}

	audit, effectiveAfter, err := s.store.RecordScaledOrder(ctx, auditParams)
	if err != nil {
		return Result{}, fmt.Errorf("record scaled order: %w", err)
	}

	return Result{
		Size:       roundedSize,
		Price:      roundedPrice,
		Audit:      audit,
		Effective:  effectiveAfter,
		Multiplier: multiplier,
	}, nil
}

func clampToNonNegative(v float64) float64 {
	if v < 0 {
		return 0
	}
	return v
}

// BuildRequest from a bot event + order.
func BuildRequest(md metadata.Metadata, evt tc.BotEvent, order hyperliquid.CreateOrderRequest) Request {
	side := "sell"
	if order.IsBuy {
		side = "buy"
	}
	stackIndex := 0
	if evt.OrderPosition > 0 {
		stackIndex = evt.OrderPosition - 1
	}

	price := order.Price
	if price <= 0 {
		price = evt.Price
	}

	size := evt.Size
	if size <= 0 {
		size = order.Size
	}

	return Request{
		Metadata:     md,
		Coin:         order.Coin,
		Side:         side,
		OriginalSize: size,
		Price:        price,
		StackIndex:   stackIndex,
		Timestamp:    evt.CreatedAt,
	}
}
