package emitter

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/hl"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
	"github.com/recomma/recomma/storage"
	"github.com/sonirico/go-hyperliquid"
	"github.com/stretchr/testify/require"

	gethCrypto "github.com/ethereum/go-ethereum/crypto"

	mockserver "github.com/recomma/hyperliquid-mock/server"
)

type stubExchange struct {
	mu          sync.Mutex
	orderErrors []error
	orderCalls  int
	orders      []hyperliquid.CreateOrderRequest
}

func (s *stubExchange) Order(ctx context.Context, req hyperliquid.CreateOrderRequest, builder *hyperliquid.BuilderInfo) (hyperliquid.OrderStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.orderCalls
	s.orderCalls++
	s.orders = append(s.orders, req)
	if idx < len(s.orderErrors) && s.orderErrors[idx] != nil {
		return hyperliquid.OrderStatus{}, s.orderErrors[idx]
	}
	return hyperliquid.OrderStatus{}, nil
}

func (s *stubExchange) CancelByCloid(ctx context.Context, coin, cloid string) (*hyperliquid.APIResponse[hyperliquid.CancelOrderResponse], error) {
	return nil, nil
}

func (s *stubExchange) ModifyOrder(ctx context.Context, req hyperliquid.ModifyOrderRequest) (hyperliquid.OrderStatus, error) {
	return hyperliquid.OrderStatus{}, nil
}

type constraintsStub struct {
	constraint hl.CoinConstraints
}

func (c constraintsStub) Resolve(context.Context, string) (hl.CoinConstraints, error) {
	return c.constraint, nil
}

type missingDataExchange struct {
	stubExchange
	lastModify hyperliquid.ModifyOrderRequest
}

func (m *missingDataExchange) ModifyOrder(ctx context.Context, req hyperliquid.ModifyOrderRequest) (hyperliquid.OrderStatus, error) {
	m.mu.Lock()
	m.lastModify = req
	m.mu.Unlock()
	return hyperliquid.OrderStatus{}, fmt.Errorf("missing response.data field in successful response")
}

type recordingRateGate struct {
	mu        sync.Mutex
	waitCalls int
	cooldowns []time.Duration
	waitErr   error
}

func (g *recordingRateGate) Wait(context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.waitCalls++
	return g.waitErr
}

func (g *recordingRateGate) Cooldown(d time.Duration) {
	g.mu.Lock()
	g.cooldowns = append(g.cooldowns, d)
	g.mu.Unlock()
}

func (g *recordingRateGate) waits() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.waitCalls
}

func (g *recordingRateGate) cooldownSnapshot() []time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]time.Duration, len(g.cooldowns))
	copy(out, g.cooldowns)
	return out
}

func NewMockExchange(t *testing.T, logger *slog.Logger) *hyperliquid.Exchange {
	t.Helper()
	exchange, _ := newMockExchangeWithServer(t, logger)
	return exchange
}

func newMockExchangeWithServer(t *testing.T, logger *slog.Logger) (*hyperliquid.Exchange, *mockserver.TestServer) {
	t.Helper()

	opts := []mockserver.TestServerOption{}
	if logger != nil {
		opts = append(opts, mockserver.WithLogger(logger))
	}

	ts := mockserver.NewTestServer(t, opts...)
	ctx := context.Background()

	privateKey, err := gethCrypto.GenerateKey()
	require.NoError(t, err)

	pub := privateKey.Public()
	pubECDSA, ok := pub.(*ecdsa.PublicKey)
	require.True(t, ok, "expected ECDSA public key")
	walletAddr := gethCrypto.PubkeyToAddress(*pubECDSA).Hex()

	exchange := hyperliquid.NewExchange(
		ctx,
		privateKey,
		ts.URL(), // Use mock server URL
		nil,      // meta
		"",       // vaultAddr (empty for non-vault)
		walletAddr,
		nil, // spotMeta
	)

	return exchange, ts
}

func TestHyperLiquidEmitterIOCRetriesLogSuccess(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	exchange := &stubExchange{orderErrors: []error{
		fmt.Errorf("Order could not immediately match against any resting orders"),
		fmt.Errorf("Order could not immediately match against any resting orders"),
		nil,
	}}

	store := newTestStore(t)
	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	info := hl.NewInfo(context.Background(), hl.ClientConfig{BaseURL: ts.URL(), Wallet: "0xabc"})
	cache := hl.NewOrderIdCache(info)

	emitter := NewHyperLiquidEmitter(exchange, "", nil, store, cache,
		WithHyperLiquidEmitterLogger(logger),
		WithHyperLiquidEmitterConfig(HyperLiquidEmitterConfig{MaxIOCRetries: 3}),
	)

	oid := orderid.OrderId{BotID: 1, DealID: 2, BotEventID: 3}
	cloid := oid.Hex()
	order := hyperliquid.CreateOrderRequest{
		Coin:          "BTC",
		IsBuy:         true,
		Price:         100,
		Size:          1,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifIoc},
		},
	}
	ident := defaultIdentifier(t, store, ctx, oid)
	work := recomma.OrderWork{
		Identifier: ident,
		OrderId:    oid,
		Action:     recomma.Action{Type: recomma.ActionCreate, Create: order},
		BotEvent:   recomma.BotEvent{RowID: 1},
	}

	err := emitter.Emit(ctx, work)
	require.NoError(t, err)

	logs := parseLogs(t, buf.String())

	warnCount := 0
	infoRetryCount := 0
	foundFinalLog := false
	for _, entry := range logs {
		if entry.Level == "WARN" {
			warnCount++
		}
		if entry.Level == "INFO" && entry.Message == "IOC did not immediately match; retrying" {
			infoRetryCount++
		}
		if entry.Level == "INFO" && entry.Message == "Order sent after IOC retries" {
			foundFinalLog = true
			require.Equal(t, float64(2), entry.Attrs["ioc-retries"], "expected two IOC retries")
			lastErr, ok := entry.Attrs["last-error"].(string)
			require.True(t, ok, "expected last-error attribute to be a string")
			require.Contains(t, lastErr, "Order could not immediately match")
			requested, ok := entry.Attrs["requested_size"].(float64)
			require.True(t, ok)
			require.InDelta(t, 1.0, requested, 1e-9)
			executed, ok := entry.Attrs["executed_size"].(float64)
			require.True(t, ok)
			require.InDelta(t, 0.0, executed, 1e-9)
		}
	}

	require.Equal(t, 0, warnCount, "expected no warnings when retries succeed")
	require.Equal(t, 2, infoRetryCount, "expected info logs for each retry before success")
	require.True(t, foundFinalLog, "expected final success log noting IOC retries")
}

func TestHyperLiquidEmitterIOCRetriesWarnOnFailure(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	exchange := &stubExchange{orderErrors: []error{
		fmt.Errorf("Order could not immediately match against any resting orders"),
		fmt.Errorf("Order could not immediately match against any resting orders"),
		fmt.Errorf("Order could not immediately match against any resting orders"),
	}}

	store := newTestStore(t)

	ts := mockserver.NewTestServer(t)
	info := hl.NewInfo(context.Background(), hl.ClientConfig{BaseURL: ts.URL(), Wallet: "0xabc"})
	cache := hl.NewOrderIdCache(info)

	emitter := NewHyperLiquidEmitter(exchange, "", nil, store, cache,
		WithHyperLiquidEmitterLogger(logger),
		WithHyperLiquidEmitterConfig(HyperLiquidEmitterConfig{MaxIOCRetries: 3}),
	)

	oid := orderid.OrderId{BotID: 4, DealID: 5, BotEventID: 6}
	cloid := oid.Hex()
	order := hyperliquid.CreateOrderRequest{
		Coin:          "ETH",
		IsBuy:         true,
		Price:         200,
		Size:          1,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifIoc},
		},
	}
	ctx := context.Background()
	ident := defaultIdentifier(t, store, ctx, oid)
	work := recomma.OrderWork{
		Identifier: ident,
		OrderId:    oid,
		Action:     recomma.Action{Type: recomma.ActionCreate, Create: order},
		BotEvent:   recomma.BotEvent{RowID: 2},
	}

	err := emitter.Emit(ctx, work)
	require.Error(t, err)

	logs := parseLogs(t, buf.String())

	warnCount := 0
	for _, entry := range logs {
		if entry.Level == "WARN" && strings.Contains(fmt.Sprint(entry.Attrs["error"]), "Order could not immediately match") {
			warnCount++
		}
	}

	require.Equal(t, 1, warnCount, "expected a single warning when retries exhaust")
}

func TestHyperLiquidEmitterTreatsMissingResponseDataAsSuccess(t *testing.T) {
	// TODO: fix this!
	t.Skip()
	t.Parallel()

	ctx := context.Background()
	exchange := &missingDataExchange{}
	store := newTestStore(t)
	ts := mockserver.NewTestServer(t)
	info := hl.NewInfo(context.Background(), hl.ClientConfig{BaseURL: ts.URL(), Wallet: "0xabc"})
	cache := hl.NewOrderIdCache(info)

	assignment, err := store.ResolveDefaultAlias(ctx)
	require.NoError(t, err)

	emitter := NewHyperLiquidEmitter(exchange, assignment.VenueID, nil, store, cache)

	oid := orderid.OrderId{BotID: 123, DealID: 456, BotEventID: 789}
	ident := recomma.NewOrderIdentifier(assignment.VenueID, "0xabc", oid)
	cloid := oid.Hex()

	createReq := hyperliquid.CreateOrderRequest{
		Coin:          "DOGE",
		IsBuy:         false,
		Price:         0.165,
		Size:          155,
		ClientOrderID: &cloid,
		ReduceOnly:    true,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}
	require.NoError(t, store.RecordHyperliquidOrderRequest(ctx, ident, createReq, 0))

	modifyReq := hyperliquid.ModifyOrderRequest{
		Cloid: &hyperliquid.Cloid{Value: cloid},
		Order: hyperliquid.CreateOrderRequest{
			Coin:          "DOGE",
			IsBuy:         false,
			Price:         0.172,
			Size:          310,
			ClientOrderID: &cloid,
			ReduceOnly:    true,
			OrderType: hyperliquid.OrderType{
				Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
			},
		},
	}

	work := recomma.OrderWork{
		Identifier: ident,
		OrderId:    oid,
		Action:     recomma.Action{Type: recomma.ActionModify, Modify: modifyReq},
		BotEvent:   recomma.BotEvent{RowID: 42},
	}

	err = emitter.Emit(ctx, work)
	require.NoError(t, err, "modify should succeed even when exchange omits response.data")
}

func TestHyperLiquidEmitterUsesInjectedRateGate(t *testing.T) {
	t.Parallel()

	gate := &recordingRateGate{}
	store := newTestStore(t)
	ctx := context.Background()
	ts := mockserver.NewTestServer(t)
	info := hl.NewInfo(context.Background(), hl.ClientConfig{BaseURL: ts.URL(), Wallet: "0xabc"})
	cache := hl.NewOrderIdCache(info)
	emitter := NewHyperLiquidEmitter(&stubExchange{}, "", nil, store, cache,
		WithHyperLiquidRateGate(gate),
	)

	oid := orderid.OrderId{BotID: 10, DealID: 20, BotEventID: 30}
	cloid := oid.Hex()
	order := hyperliquid.CreateOrderRequest{
		Coin:          "ATOM",
		IsBuy:         true,
		Price:         11,
		Size:          1,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifIoc},
		},
	}
	ident := defaultIdentifier(t, store, ctx, oid)
	work := recomma.OrderWork{
		Identifier: ident,
		OrderId:    oid,
		Action:     recomma.Action{Type: recomma.ActionCreate, Create: order},
		BotEvent:   recomma.BotEvent{RowID: 3},
	}

	err := emitter.Emit(ctx, work)
	require.NoError(t, err)
	require.Equal(t, 1, gate.waits(), "expected the rate gate to be used for pacing")
}

func TestHyperLiquidEmitterPropagatesCooldownToRateGate(t *testing.T) {
	t.Parallel()

	gate := &recordingRateGate{}
	exchange := &stubExchange{orderErrors: []error{fmt.Errorf("429 rate limit")}}
	ts := mockserver.NewTestServer(t)
	info := hl.NewInfo(context.Background(), hl.ClientConfig{BaseURL: ts.URL(), Wallet: "0xabc"})
	cache := hl.NewOrderIdCache(info)
	store := newTestStore(t)
	ctx := context.Background()
	emitter := NewHyperLiquidEmitter(exchange, "", nil, store, cache,
		WithHyperLiquidRateGate(gate),
		WithHyperLiquidEmitterConfig(HyperLiquidEmitterConfig{MaxIOCRetries: 1}),
	)

	oid := orderid.OrderId{BotID: 11, DealID: 21, BotEventID: 31}
	cloid := oid.Hex()
	order := hyperliquid.CreateOrderRequest{
		Coin:          "SOL",
		IsBuy:         true,
		Price:         22,
		Size:          1,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifIoc},
		},
	}
	ident := defaultIdentifier(t, store, ctx, oid)
	work := recomma.OrderWork{
		Identifier: ident,
		OrderId:    oid,
		Action:     recomma.Action{Type: recomma.ActionCreate, Create: order},
		BotEvent:   recomma.BotEvent{RowID: 4},
	}

	err := emitter.Emit(ctx, work)
	require.Error(t, err)
	require.Equal(t, 1, gate.waits(), "expected a single pacing attempt")

	cooldowns := gate.cooldownSnapshot()
	require.Len(t, cooldowns, 1, "expected a cooldown when rate limits are hit")
	require.Equal(t, 10*time.Second, cooldowns[0])
}

func TestHyperLiquidEmitterMockExchangeCancelOrder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	exchange, ts := newMockExchangeWithServer(t, nil)
	info := hl.NewInfo(ctx, hl.ClientConfig{BaseURL: ts.URL(), Wallet: "0xabc"})
	cache := hl.NewOrderIdCache(info)
	store := newTestStore(t)

	assignment, err := store.ResolveDefaultAlias(ctx)
	require.NoError(t, err)

	emitter := NewHyperLiquidEmitter(exchange, assignment.VenueID, nil, store, cache,
		WithHyperLiquidEmitterConfig(HyperLiquidEmitterConfig{MaxIOCRetries: 1}),
	)

	oid := orderid.OrderId{BotID: 12, DealID: 34, BotEventID: 56}
	cloid := oid.Hex()
	order := hyperliquid.CreateOrderRequest{
		Coin:          "ETH",
		IsBuy:         true,
		Price:         3000,
		Size:          0.25,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	createWork := recomma.OrderWork{
		Identifier: defaultIdentifier(t, store, ctx, oid),
		OrderId:    oid,
		Action:     recomma.Action{Type: recomma.ActionCreate, Create: order},
		BotEvent:   recomma.BotEvent{RowID: 1},
	}

	require.NoError(t, emitter.Emit(ctx, createWork))

	storedOrder, exists := ts.GetOrder(cloid)
	require.True(t, exists)
	require.Equal(t, "open", strings.ToLower(storedOrder.Status))

	cancelReq := hyperliquid.CancelOrderRequestByCloid{
		Coin:  order.Coin,
		Cloid: cloid,
	}
	cancelWork := recomma.OrderWork{
		Identifier: defaultIdentifier(t, store, ctx, oid),
		OrderId:    oid,
		Action:     recomma.Action{Type: recomma.ActionCancel, Cancel: cancelReq},
		BotEvent:   recomma.BotEvent{RowID: 2},
	}

	require.NoError(t, emitter.Emit(ctx, cancelWork))

	ident := defaultIdentifier(t, store, ctx, oid)
	action, found, err := store.LoadHyperliquidSubmission(ctx, ident)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, recomma.ActionCancel, action.Type)
	require.NotNil(t, action.Cancel)
	require.Equal(t, cloid, action.Cancel.Cloid)

	canceledOrder, exists := ts.GetOrder(cloid)
	require.True(t, exists)
	require.Equal(t, "canceled", strings.ToLower(canceledOrder.Status))
}

func TestApplyIOCOffsetIgnoresNonIOCOrders(t *testing.T) {
	t.Parallel()

	emitter := NewHyperLiquidEmitter(&stubExchange{}, "", nil, newTestStore(t), nil,
		WithHyperLiquidEmitterConfig(HyperLiquidEmitterConfig{
			InitialIOCOffsetBps: 25,
		}),
	)

	order := hyperliquid.CreateOrderRequest{
		Coin:  "DOGE",
		IsBuy: true,
		Price: 0.12,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc},
		},
	}

	adjusted := emitter.applyIOCOffset(order, 0)
	require.Equal(t, order.Price, adjusted.Price, "non-IOC orders must not receive an offset bump")
}

type logEntry struct {
	Level   string
	Message string
	Attrs   map[string]any
}

func parseLogs(t *testing.T, raw string) []logEntry {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	var entries []logEntry
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var payload map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &payload))
		entry := logEntry{}
		if level, ok := payload["level"].(string); ok {
			entry.Level = strings.ToUpper(level)
		}
		if msg, ok := payload["msg"].(string); ok {
			entry.Message = msg
		}
		entry.Attrs = make(map[string]any)
		for k, v := range payload {
			switch k {
			case "level", "msg", "time":
				continue
			default:
				entry.Attrs[k] = v
			}
		}
		entries = append(entries, entry)
	}
	return entries
}

func TestHyperLiquidEmitterMockExchangeIOCRetrySuccess(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	exchange, ts := newMockExchangeWithServer(t, logger)
	info := hl.NewInfo(ctx, hl.ClientConfig{BaseURL: ts.URL(), Wallet: "0xabc"})
	cache := hl.NewOrderIdCache(info)
	store := newTestStore(t)

	emitter := NewHyperLiquidEmitter(exchange, "", nil, store, cache,
		WithHyperLiquidEmitterLogger(logger),
		WithHyperLiquidEmitterConfig(HyperLiquidEmitterConfig{MaxIOCRetries: 2}),
		WithHyperLiquidRateGate(NewRateGate(0)),
	)

	oid := orderid.OrderId{BotID: 9001, DealID: 42, BotEventID: 7}
	cloid := oid.Hex()
	originalPrice := 87000.0
	order := hyperliquid.CreateOrderRequest{
		Coin:          "BTC",
		IsBuy:         true,
		Price:         originalPrice,
		Size:          0.5,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifIoc},
		},
	}

	work := recomma.OrderWork{
		Identifier: defaultIdentifier(t, store, ctx, oid),
		OrderId:    oid,
		Action:     recomma.Action{Type: recomma.ActionCreate, Create: order},
		BotEvent:   recomma.BotEvent{RowID: 1001},
	}

	err := emitter.Emit(ctx, work)
	require.NoError(t, err)

	ident := defaultIdentifier(t, store, ctx, oid)
	storedReq, found, err := store.LoadHyperliquidRequest(ctx, ident)
	require.NoError(t, err)
	require.True(t, found, "expected create request persisted after success")
	require.NotNil(t, storedReq)
	require.Greater(t, storedReq.Price, originalPrice, "retry offset should lift IOC price above the rejection threshold")
	require.InDelta(t, order.Size, storedReq.Size, 1e-9)

	logs := parseLogs(t, buf.String())

	retryInfos := 0
	warnCount := 0
	var final *logEntry
	for i := range logs {
		entry := logs[i]
		if entry.Level == "INFO" && entry.Message == "IOC did not immediately match; retrying" {
			retryInfos++
		}
		if entry.Level == "WARN" {
			warnCount++
		}
		if entry.Level == "INFO" && entry.Message == "Order sent after IOC retries" {
			final = &logs[i]
		}
	}

	require.Zero(t, warnCount, "successful retries should not warn")
	require.Equal(t, 1, retryInfos, "expected a single retry before success")
	require.NotNil(t, final, "missing final success log")
	retries, ok := final.Attrs["ioc-retries"].(float64)
	require.True(t, ok, "expected ioc-retries attribute")
	require.Equal(t, float64(1), retries)
	lastErr, ok := final.Attrs["last-error"].(string)
	require.True(t, ok, "expected last-error attribute")
	require.Contains(t, lastErr, "Order could not immediately match")
}

func TestHyperLiquidEmitterMockExchangeIOCRetriesExhausted(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	exchange, ts := newMockExchangeWithServer(t, logger)
	info := hl.NewInfo(ctx, hl.ClientConfig{BaseURL: ts.URL(), Wallet: "0xabc"})
	cache := hl.NewOrderIdCache(info)
	store := newTestStore(t)

	assignment, err := store.ResolveDefaultAlias(ctx)
	require.NoError(t, err)

	emitter := NewHyperLiquidEmitter(exchange, assignment.VenueID, nil, store, cache,
		WithHyperLiquidEmitterLogger(logger),
		WithHyperLiquidEmitterConfig(HyperLiquidEmitterConfig{MaxIOCRetries: 3}),
		WithHyperLiquidRateGate(NewRateGate(0)),
	)

	oid := orderid.OrderId{BotID: 9002, DealID: 43, BotEventID: 8}
	cloid := oid.Hex()
	order := hyperliquid.CreateOrderRequest{
		Coin:          "BTC",
		IsBuy:         true,
		Price:         100.0,
		Size:          0.25,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifIoc},
		},
	}

	work := recomma.OrderWork{
		Identifier: defaultIdentifier(t, store, ctx, oid),
		OrderId:    oid,
		Action:     recomma.Action{Type: recomma.ActionCreate, Create: order},
		BotEvent:   recomma.BotEvent{RowID: 2002},
	}

	err = emitter.Emit(ctx, work)
	require.Error(t, err)
	require.Contains(t, err.Error(), "could not place order")

	ident := defaultIdentifier(t, store, ctx, oid)
	storedReq, found, err := store.LoadHyperliquidRequest(ctx, ident)
	require.NoError(t, err)
	require.False(t, found, "failed IOC retries must not persist the request")
	require.Nil(t, storedReq)

	logs := parseLogs(t, buf.String())
	retryInfos := 0
	warnCount := 0
	for _, entry := range logs {
		if entry.Level == "INFO" && entry.Message == "IOC did not immediately match; retrying" {
			retryInfos++
		}
		if entry.Level == "WARN" && strings.Contains(fmt.Sprint(entry.Attrs["error"]), "Order could not immediately match") {
			warnCount++
		}
	}

	require.Equal(t, 2, retryInfos, "max IOC retries should log before the final attempt fails")
	require.Equal(t, 1, warnCount, "expected a single warning when retries exhaust")
}

func TestHyperLiquidEmitterAppliesConstraintsBeforeSubmitting(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newTestStore(t)
	exchange := &stubExchange{}

	constraint := hl.CoinConstraints{Coin: "DOGE", PriceSigFigs: 5}
	stubConstraints := constraintsStub{constraint: constraint}

	emitter := NewHyperLiquidEmitter(exchange, "", nil, store, stubConstraints)

	oid := orderid.OrderId{BotID: 1, DealID: 2, BotEventID: 3}
	cloid := oid.Hex()
	order := hyperliquid.CreateOrderRequest{
		Coin:          "DOGE",
		IsBuy:         true,
		Price:         0.15332662,
		Size:          156,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifIoc},
		},
	}

	work := recomma.OrderWork{
		Identifier: defaultIdentifier(t, store, ctx, oid),
		OrderId:    oid,
		Action:     recomma.Action{Type: recomma.ActionCreate, Create: order},
		BotEvent:   recomma.BotEvent{RowID: 42},
	}

	require.NoError(t, emitter.Emit(ctx, work))

	expected := constraint.RoundPrice(order.Price)

	exchange.mu.Lock()
	require.Len(t, exchange.orders, 1)
	gotPrice := exchange.orders[0].Price
	exchange.mu.Unlock()

	require.InDelta(t, expected, gotPrice, 1e-9, "emitter must snap price to constraints before submission")
}

func newTestStore(t *testing.T) *storage.Storage {
	t.Helper()
	store, err := storage.New(":memory:")
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})
	return store
}

func TestHyperLiquidEmitterRoundsHalfEven(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	exchange, ts := newMockExchangeWithServer(t, nil) // uses real SDK + mock HTTP server
	store := newTestStore(t)

	info := hl.NewInfo(ctx, hl.ClientConfig{BaseURL: ts.URL(), Wallet: "0xabc"})
	cache := hl.NewOrderIdCache(info)

	emitter := NewHyperLiquidEmitter(exchange, "", nil, store, cache)

	oid := orderid.OrderId{BotID: 16567027, DealID: 2385553190, BotEventID: 1917367905}
	cloid := oid.Hex()

	order := hyperliquid.CreateOrderRequest{
		Coin:          "DOGE",
		IsBuy:         true,
		Price:         0.172556235, // ninth decimal triggers floatToWire failure
		Size:          86,
		ClientOrderID: &cloid,
		OrderType: hyperliquid.OrderType{
			Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifIoc},
		},
	}

	work := recomma.OrderWork{
		Identifier: recomma.NewOrderIdentifier("hyperliquid:main", "0xfeed", oid),
		OrderId:    oid,
		Action:     recomma.Action{Type: recomma.ActionCreate, Create: order},
		BotEvent: recomma.BotEvent{
			RowID: 13,
			BotEvent: tc.BotEvent{
				Action: "Placing",
				Coin:   "DOGE",
			},
		},
	}

	err := emitter.Emit(ctx, work)
	if err != nil {
		require.NotContains(t, err.Error(), "float_to_wire causes rounding")
	}
}

func TestRoundHalfEven(t *testing.T) {
	tests := []struct {
		name string // description of this test case
		// Named input parameters for target function.
		x    float64
		want float64
	}{
		{
			x:    0.172556235,
			want: 0.17255624,
		},
		{
			x:    0.172556236,
			want: 0.17255624,
		},
		{
			x:    0.172556234,
			want: 0.17255623,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RoundHalfEven(tt.x)

			gotS := fmt.Sprintf("%.8f", got)
			wantS := fmt.Sprintf("%.8f", tt.want)

			if gotS != wantS {
				t.Errorf("RoundHalfEven() = %s, want %s", gotS, wantS)
			}
		})
	}
}

func defaultIdentifier(t *testing.T, store *storage.Storage, ctx context.Context, oid orderid.OrderId) recomma.OrderIdentifier {
	t.Helper()
	assignment, err := store.ResolveDefaultAlias(ctx)
	require.NoError(t, err)
	return recomma.NewOrderIdentifier(assignment.VenueID, assignment.Wallet, oid)
}
