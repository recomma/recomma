package emitter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
	"github.com/recomma/recomma/storage"
	"github.com/sonirico/go-hyperliquid"
	"github.com/stretchr/testify/require"
)

type stubExchange struct {
	mu          sync.Mutex
	orderErrors []error
	orderCalls  int
}

func (s *stubExchange) Order(ctx context.Context, req hyperliquid.CreateOrderRequest, builder *hyperliquid.BuilderInfo) (hyperliquid.OrderStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.orderCalls
	s.orderCalls++
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

	emitter := NewHyperLiquidEmitter(exchange, "", nil, store,
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
	work := recomma.OrderWork{
		Identifier: storage.DefaultHyperliquidIdentifier(oid),
		OrderId:    oid,
		Action:     recomma.Action{Type: recomma.ActionCreate, Create: &order},
		BotEvent:   recomma.BotEvent{RowID: 1},
	}

	err := emitter.Emit(context.Background(), work)
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

	emitter := NewHyperLiquidEmitter(exchange, "", nil, store,
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
	work := recomma.OrderWork{
		Identifier: storage.DefaultHyperliquidIdentifier(oid),
		OrderId:    oid,
		Action:     recomma.Action{Type: recomma.ActionCreate, Create: &order},
		BotEvent:   recomma.BotEvent{RowID: 2},
	}

	err := emitter.Emit(context.Background(), work)
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

func TestApplyIOCOffsetIgnoresNonIOCOrders(t *testing.T) {
	t.Parallel()

	emitter := NewHyperLiquidEmitter(&stubExchange{}, "", nil, newTestStore(t),
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
