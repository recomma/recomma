package orderscaler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"time"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/hl"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
	"github.com/recomma/recomma/storage"
	"github.com/sonirico/go-hyperliquid"
)

// ErrBelowMinimum is returned when the scaled order would violate venue minimums.
var ErrBelowMinimum = errors.New("scaled order below venue minimum")

// Store defines the storage subset required by the scaler.
type Store interface {
	ResolveEffectiveOrderScaler(ctx context.Context, oid orderid.OrderId) (storage.EffectiveOrderScaler, error)
	RecordScaledOrder(ctx context.Context, params storage.RecordScaledOrderParams) (storage.ScaledOrderAudit, storage.EffectiveOrderScaler, error)
	ListTakeProfitStackSizes(ctx context.Context, oid orderid.OrderId, stackSize int) ([]float64, error)
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
	Identifier   recomma.OrderIdentifier
	OrderId      orderid.OrderId
	Coin         string
	Side         string
	OriginalSize float64
	Price        float64
	StackIndex   int
	StackSize    int
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

	effective, err := s.store.ResolveEffectiveOrderScaler(ctx, req.OrderId)
	if err != nil {
		return Result{}, fmt.Errorf("resolve multiplier: %w", err)
	}

	multiplier := effective.Multiplier
	if s.maxMultiplier > 0 && multiplier > s.maxMultiplier {
		s.logger.Warn("effective multiplier exceeds configured max; clamping", slog.Float64("multiplier", multiplier), slog.Float64("max", s.maxMultiplier), slog.Uint64("deal_id", uint64(req.OrderId.DealID)), slog.Uint64("bot_id", uint64(req.OrderId.BotID)))
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

	if req.StackSize > 1 && req.StackIndex >= 0 {
		stackSizes, stackErr := s.store.ListTakeProfitStackSizes(ctx, req.OrderId, req.StackSize)
		if stackErr != nil {
			s.logger.Debug("resolve stack sizes", slog.Any("error", stackErr), slog.Uint64("deal_id", uint64(req.OrderId.DealID)), slog.Int("stack_size", req.StackSize))
		} else if len(stackSizes) == req.StackSize {
			stackRounded := computeStackRoundedSizes(stackSizes, multiplier, constraints.SizeStep)
			if req.StackIndex >= 0 && req.StackIndex < len(stackRounded) {
				roundedSize = stackRounded[req.StackIndex]
			}
		}
	}

	roundedSize = clampToNonNegative(roundedSize)

	notional := math.Abs(roundedSize * roundedPrice)
	if constraints.NotionalBelowMinimum(roundedSize, roundedPrice) {
		reason := fmt.Sprintf("scaled order below minimum notional (%.4f < %.4f)", notional, constraints.MinNotional)
		auditParams := storage.RecordScaledOrderParams{
			Identifier:        req.Identifier,
			DealID:            req.OrderId.DealID,
			BotID:             req.OrderId.BotID,
			OriginalSize:      req.OriginalSize,
			ScaledSize:        roundedSize,
			AppliedMultiplier: &multiplier,
			StackIndex:        req.StackIndex,
			OrderSide:         req.Side,
			CreatedAt:         req.Timestamp,
			Skipped:           true,
			SkipReason:        &reason,
		}

		audit, effectiveAfter, recordErr := s.store.RecordScaledOrder(ctx, auditParams)
		if recordErr != nil {
			return Result{}, fmt.Errorf("record skipped scaled order: %w", recordErr)
		}

		s.logger.Warn("scaled order violates minimum notional", slog.String("coin", req.Coin), slog.Float64("price", roundedPrice), slog.Float64("size", roundedSize), slog.Float64("notional", notional), slog.Float64("min", constraints.MinNotional), slog.Float64("multiplier", multiplier), slog.Uint64("deal_id", uint64(req.OrderId.DealID)), slog.Uint64("bot_id", uint64(req.OrderId.BotID)), slog.String("reason", reason))

		return Result{
			Size:       roundedSize,
			Price:      roundedPrice,
			Audit:      audit,
			Effective:  effectiveAfter,
			Multiplier: multiplier,
		}, ErrBelowMinimum
	}

	order.Price = roundedPrice
	order.Size = roundedSize

	auditParams := storage.RecordScaledOrderParams{
		Identifier:        req.Identifier,
		DealID:            req.OrderId.DealID,
		BotID:             req.OrderId.BotID,
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

const (
	ratioWeight     = 1.0
	totalWeight     = 0.1
	roundingWeight  = 0.05
	equalityPenalty = 1.0
	ratioTolerance  = 1e-9
)

func computeStackRoundedSizes(original []float64, multiplier float64, sizeStep float64) []float64 {
	n := len(original)
	if n == 0 {
		return nil
	}

	targets := make([]float64, n)
	for i := range original {
		targets[i] = original[i] * multiplier
	}

	if sizeStep <= 0 {
		out := make([]float64, n)
		copy(out, targets)
		return out
	}

	options := make([][]int, n)
	for i := range targets {
		base := targets[i] / sizeStep
		lower := int(math.Floor(base))
		upper := int(math.Ceil(base))
		if upper < lower {
			upper = lower
		}
		optionSet := map[int]struct{}{}
		if lower >= 0 {
			optionSet[lower] = struct{}{}
		}
		if upper >= 0 {
			optionSet[upper] = struct{}{}
		}
		if len(optionSet) == 0 {
			optionSet[0] = struct{}{}
		}
		opts := make([]int, 0, len(optionSet))
		for v := range optionSet {
			opts = append(opts, v)
		}
		sort.Ints(opts)
		options[i] = opts
	}

	best := make([]float64, n)
	bestCost := math.Inf(1)
	steps := make([]int, n)

	var backtrack func(int)
	backtrack = func(idx int) {
		if idx == n {
			sizes := make([]float64, n)
			for i, step := range steps {
				sizes[i] = float64(step) * sizeStep
			}
			cost := evaluateStackCost(original, targets, sizes)
			if cost < bestCost {
				bestCost = cost
				copy(best, sizes)
			}
			return
		}

		for _, option := range options[idx] {
			steps[idx] = option
			backtrack(idx + 1)
		}
	}

	backtrack(0)

	if math.IsInf(bestCost, 1) {
		fallback := make([]float64, n)
		for i := range targets {
			fallback[i] = math.Round(targets[i]/sizeStep) * sizeStep
		}
		return fallback
	}

	return best
}

func evaluateStackCost(original, targets, sizes []float64) float64 {
	totalTarget := 0.0
	totalSize := 0.0
	for i := range targets {
		totalTarget += targets[i]
		totalSize += sizes[i]
	}

	cost := totalWeight * math.Abs(totalSize-totalTarget)

	baseOriginal := original[0]
	baseSize := sizes[0]
	for i := range original {
		cost += roundingWeight * math.Abs(sizes[i]-targets[i])
		if i == 0 {
			continue
		}
		if original[i] > original[i-1]+ratioTolerance && sizes[i] <= sizes[i-1] {
			cost += equalityPenalty
		}
		if baseOriginal > ratioTolerance && baseSize > ratioTolerance {
			desiredRatio := original[i] / baseOriginal
			actualRatio := sizes[i] / baseSize
			cost += ratioWeight * math.Abs(actualRatio-desiredRatio)
		}
	}

	return cost
}

// BuildRequest from a bot event + order + identifier.
func BuildRequest(ident recomma.OrderIdentifier, evt tc.BotEvent, order hyperliquid.CreateOrderRequest) Request {
	side := "sell"
	if order.IsBuy {
		side = "buy"
	}
	stackIndex := 0
	if evt.OrderPosition > 0 {
		stackIndex = evt.OrderPosition - 1
	}
	stackSize := evt.OrderSize
	if stackSize <= 0 {
		stackSize = 1
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
		Identifier:   ident,
		OrderId:      ident.OrderId,
		Coin:         order.Coin,
		Side:         side,
		OriginalSize: size,
		Price:        price,
		StackIndex:   stackIndex,
		StackSize:    stackSize,
		Timestamp:    evt.CreatedAt,
	}
}
