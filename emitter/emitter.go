package emitter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/recomma/recomma/hl"
	"github.com/recomma/recomma/hl/ws"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
	"github.com/recomma/recomma/storage"
	"github.com/sonirico/go-hyperliquid"
)

type OrderQueue interface {
	Add(item recomma.OrderWork)
}

type QueueEmitter struct {
	q        OrderQueue
	logger   *slog.Logger
	mu       sync.RWMutex
	emitters map[recomma.VenueID]recomma.Emitter
}

var (
	ErrMissingOrderIdentifier   = errors.New("queue emitter: order identifier required")
	ErrUnregisteredVenueEmitter = errors.New("queue emitter: no emitter registered for venue")
	ErrOrderIdentifierMismatch  = errors.New("queue emitter: order identifier mismatch")
)

func NewQueueEmitter(q OrderQueue) *QueueEmitter {
	return &QueueEmitter{
		q:        q,
		logger:   slog.Default().WithGroup("emitter"),
		emitters: make(map[recomma.VenueID]recomma.Emitter),
	}
}

// Register associates a venue with a concrete emitter implementation. Existing
// registrations are overwritten so callers can update emitters during
// reconfiguration.
func (e *QueueEmitter) Register(venue recomma.VenueID, emitter recomma.Emitter) {
	if emitter == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.emitters[venue] = emitter
}

func (e *QueueEmitter) emitterFor(venue recomma.VenueID) (recomma.Emitter, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	emitter, ok := e.emitters[venue]
	return emitter, ok
}

func (e *QueueEmitter) Emit(ctx context.Context, w recomma.OrderWork) error {
	if w.Identifier == (recomma.OrderIdentifier{}) {
		return ErrMissingOrderIdentifier
	}
	if w.OrderId != (orderid.OrderId{}) && w.Identifier.OrderId != w.OrderId {
		return ErrOrderIdentifierMismatch
	}
	if _, ok := e.emitterFor(w.Identifier.VenueID); !ok {
		return fmt.Errorf("%w: %s", ErrUnregisteredVenueEmitter, w.Identifier.Venue())
	}
	e.logger.Debug("emit", slog.Any("order-work", w))
	e.q.Add(w)
	return nil
}

// Dispatch forwards the work item to the venue-specific emitter registered for
// its identifier. Workers call this after dequeuing an item so pacing and
// retries remain local to the concrete emitter implementation.
func (e *QueueEmitter) Dispatch(ctx context.Context, w recomma.OrderWork) error {
	if w.Identifier == (recomma.OrderIdentifier{}) {
		return ErrMissingOrderIdentifier
	}
	emitter, ok := e.emitterFor(w.Identifier.VenueID)
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnregisteredVenueEmitter, w.Identifier.Venue())
	}
	return emitter.Emit(ctx, w)
}

type hyperliquidExchange interface {
	Order(ctx context.Context, req hyperliquid.CreateOrderRequest, builder *hyperliquid.BuilderInfo) (hyperliquid.OrderStatus, error)
	CancelByCloid(ctx context.Context, coin, cloid string) (*hyperliquid.APIResponse[hyperliquid.CancelOrderResponse], error)
	ModifyOrder(ctx context.Context, req hyperliquid.ModifyOrderRequest) (hyperliquid.OrderStatus, error)
}

type hyperliquidStatusClient interface {
	QueryOrderByCloid(ctx context.Context, cloid string) (*hyperliquid.OrderQueryResult, error)
}

type HyperLiquidEmitterConfig struct {
	MaxIOCRetries       int
	InitialIOCOffsetBps float64
}

type HyperLiquidEmitterOption func(*HyperLiquidEmitter)

var defaultHyperLiquidEmitterConfig = HyperLiquidEmitterConfig{
	MaxIOCRetries: 3,
}

const (
	iocRetryBumpRatio                 = 0.0005
	missingModifyResponseDataFragment = "missing response.data field in successful response"
	cannotModifyFilledFragment        = "cannot modify canceled or filled order"
)

type iocRetryOrderId struct {
	count     int
	lastError string
}

// Constraints resolves Hyperliquid rounding requirements.
type Constraints interface {
	Resolve(ctx context.Context, coin string) (hl.CoinConstraints, error)
}

type HyperLiquidEmitter struct {
	exchange     hyperliquidExchange
	store        *storage.Storage
	gate         RateGate
	logger       *slog.Logger
	cfg          HyperLiquidEmitterConfig
	wsMu         sync.RWMutex
	wsClients    map[recomma.VenueID]*ws.Client
	primaryVenue recomma.VenueID
	constraints  Constraints
	statusClient hyperliquidStatusClient
}

func NewHyperLiquidEmitter(exchange hyperliquidExchange, primaryVenue recomma.VenueID, wsClient *ws.Client, store *storage.Storage, constraints Constraints, opts ...HyperLiquidEmitterOption) *HyperLiquidEmitter {
	emitter := &HyperLiquidEmitter{
		exchange:     exchange,
		store:        store,
		gate:         NewRateGate(0),
		logger:       slog.Default().WithGroup("hl-emitter"),
		cfg:          defaultHyperLiquidEmitterConfig,
		primaryVenue: primaryVenue,
		constraints:  constraints,
	}

	if wsClient != nil && primaryVenue != "" {
		emitter.RegisterWsClient(primaryVenue, wsClient)
	}

	for _, opt := range opts {
		if opt != nil {
			opt(emitter)
		}
	}

	emitter.cfg = normalizeHyperLiquidEmitterConfig(emitter.cfg)

	return emitter
}

func WithHyperLiquidEmitterLogger(logger *slog.Logger) HyperLiquidEmitterOption {
	return func(e *HyperLiquidEmitter) {
		if logger != nil {
			e.logger = logger
		}
	}
}

func WithHyperLiquidEmitterConfig(cfg HyperLiquidEmitterConfig) HyperLiquidEmitterOption {
	return func(e *HyperLiquidEmitter) {
		e.cfg = cfg
	}
}

// WithHyperLiquidRateGate injects a shared rate gate so emitters that target the
// same upstream wallet coordinate pacing.
func WithHyperLiquidRateGate(gate RateGate) HyperLiquidEmitterOption {
	return func(e *HyperLiquidEmitter) {
		if gate != nil {
			e.gate = gate
		}
	}
}

func WithHyperLiquidStatusClient(client hyperliquidStatusClient) HyperLiquidEmitterOption {
	return func(e *HyperLiquidEmitter) {
		if client != nil {
			e.statusClient = client
		}
	}
}

func normalizeHyperLiquidEmitterConfig(cfg HyperLiquidEmitterConfig) HyperLiquidEmitterConfig {
	if cfg.MaxIOCRetries <= 0 {
		cfg.MaxIOCRetries = defaultHyperLiquidEmitterConfig.MaxIOCRetries
	}
	if cfg.InitialIOCOffsetBps < 0 {
		cfg.InitialIOCOffsetBps = 0
	}
	return cfg
}

func (e *HyperLiquidEmitter) RegisterWsClient(venue recomma.VenueID, client *ws.Client) {
	if venue == "" || client == nil {
		return
	}
	e.wsMu.Lock()
	if e.wsClients == nil {
		e.wsClients = make(map[recomma.VenueID]*ws.Client)
	}
	e.wsClients[venue] = client
	e.wsMu.Unlock()
}

func (e *HyperLiquidEmitter) wsFor(venue recomma.VenueID) *ws.Client {
	e.wsMu.RLock()
	defer e.wsMu.RUnlock()
	if len(e.wsClients) == 0 {
		return nil
	}
	if client, ok := e.wsClients[venue]; ok && client != nil {
		return client
	}
	if e.primaryVenue != "" {
		if client, ok := e.wsClients[e.primaryVenue]; ok && client != nil {
			return client
		}
	}
	for _, client := range e.wsClients {
		if client != nil {
			return client
		}
	}
	return nil
}

// waitTurn enforces a simple global pacing for all Hyperliquid actions
// to avoid bursting into HL rate limits. It spaces calls by minSpacing,
// and can be tightened by applying a longer cooldown when 429s are seen.
func (e *HyperLiquidEmitter) waitTurn(ctx context.Context) error {
	if e.gate == nil {
		return nil
	}
	return e.gate.Wait(ctx)
}

func (e *HyperLiquidEmitter) applyCooldown(d time.Duration) {
	if e.gate == nil {
		return
	}
	e.gate.Cooldown(d)
}

func (e *HyperLiquidEmitter) setMarketPrice(ctx context.Context, wsClient *ws.Client, order hyperliquid.CreateOrderRequest) hyperliquid.CreateOrderRequest {
	if wsClient == nil {
		order.Price = RoundHalfEven(order.Price)
		order.Size = RoundHalfEven(order.Size)
		return order
	}

	wsClient.EnsureBBO(order.Coin)
	if order.Price == 0 {
		bboCtx, bboCancel := context.WithTimeout(ctx, time.Second*30)
		defer bboCancel()
		bbo := wsClient.WaitForBestBidOffer(bboCtx, order.Coin)
		if bbo != nil {
			if order.IsBuy {
				order.Price = bbo.Ask.Price
			} else {
				order.Price = bbo.Bid.Price
			}
		}
	}

	// to prevent triggering an error in the SDK converting the float to 8 decimals
	// we round it up ourselves.
	order.Price = RoundHalfEven(order.Price)
	order.Size = RoundHalfEven(order.Size)

	return order
}

func (e *HyperLiquidEmitter) applyIOCOffset(order hyperliquid.CreateOrderRequest, attempt int) hyperliquid.CreateOrderRequest {
	if order.OrderType.Limit == nil || order.OrderType.Limit.Tif != hyperliquid.TifIoc {
		return order
	}

	if order.Price <= 0 {
		return order
	}

	offsetRatio := 0.0
	if attempt == 0 {
		offsetRatio += e.cfg.InitialIOCOffsetBps / 10000
	}
	if attempt > 0 {
		offsetRatio += float64(attempt) * iocRetryBumpRatio
	}

	if offsetRatio == 0 {
		return order
	}

	if order.IsBuy {
		order.Price = order.Price * (1 + offsetRatio)
	} else {
		adjusted := 1 - offsetRatio
		if adjusted < 0 {
			adjusted = 0
		}
		order.Price = order.Price * adjusted
	}

	order.Price = RoundHalfEven(order.Price)

	return order
}

func (e *HyperLiquidEmitter) Emit(ctx context.Context, w recomma.OrderWork) error {
	if w.Identifier == (recomma.OrderIdentifier{}) {
		return ErrMissingOrderIdentifier
	}
	if w.OrderId != (orderid.OrderId{}) && w.Identifier.OrderId != w.OrderId {
		return ErrOrderIdentifierMismatch
	}

	ident := w.Identifier
	wsClient := e.wsFor(ident.VenueID)

	logger := e.logger.With("orderid", ident.Hex()).With("venue", ident.Venue()).With("bot-event", w.BotEvent)
	logger.Debug("emit", slog.Any("orderwork", w))

	if w.Action.Type == recomma.ActionNone {
		// order already reconciled; no Hyperliquid interaction needed
		return nil
	}

	if err := e.waitTurn(ctx); err != nil {
		return err
	}

	didSubmit := false
	var executedAction recomma.Action
	var retryMeta *iocRetryOrderId
	var lastStatus *hyperliquid.OrderStatus

	// TODO: decide if we want to persist the result we get back here, it's not interesting ususally as it just states `resting`
	switch w.Action.Type {
	case recomma.ActionCreate:
		order := e.setMarketPrice(ctx, wsClient, w.Action.Create)
		if constrainedPrice, err := e.constrainPrice(ctx, order.Coin, order.Price); err != nil {
			logger.Warn("could not constrain price", slog.String("error", err.Error()))
		} else {
			order.Price = constrainedPrice
		}
		w.Action.Create = order

		if wsClient != nil {
			if status, ok := wsClient.Get(ctx, w.OrderId); ok && isLiveStatus(status) {
				var latestSubmission *hyperliquid.CreateOrderRequest
				if submission, found, err := e.store.LoadHyperliquidSubmission(ctx, ident); err != nil {
					logger.Warn("could not load latest submission", slog.String("error", err.Error()))
				} else if found {
					switch submission.Type {
					case recomma.ActionModify:
						current := submission.Modify.Order
						latestSubmission = &current
					case recomma.ActionCreate:
						latestSubmission = &submission.Create
					}
				}

				if ordersMatch(status, latestSubmission, order) {
					logger.Debug("order already matches desired state; skipping create")
					return recomma.ErrOrderAlreadySatisfied
				}

				modifyReq := hyperliquid.ModifyOrderRequest{Cloid: &hyperliquid.Cloid{Value: w.OrderId.Hex()}, Order: order}
				status, err := e.submitModify(ctx, logger, w, modifyReq)
				if err != nil {
					return err
				}
				if status != nil {
					lastStatus = status
				}
				w.Action.Type = recomma.ActionModify
				w.Action.Modify = modifyReq
				executedAction = w.Action
				didSubmit = true
				break
			}
		}

		maxAttempts := e.cfg.MaxIOCRetries
		if maxAttempts <= 0 {
			maxAttempts = 1
		}
		retryInfo := struct {
			count   int
			lastErr string
		}{}
		success := false

		for attempt := 0; attempt < maxAttempts; attempt++ {
			if attempt > 0 {
				if err := e.waitTurn(ctx); err != nil {
					return err
				}
			}

			order := e.setMarketPrice(ctx, wsClient, w.Action.Create)
			order = e.applyIOCOffset(order, attempt)
			if constrainedPrice, err := e.constrainPrice(ctx, order.Coin, order.Price); err != nil {
				logger.Warn("could not constrain price", slog.String("error", err.Error()))
			} else {
				order.Price = constrainedPrice
			}
			w.Action.Create = order

			status, err := e.exchange.Order(ctx, w.Action.Create, nil)
			if err != nil {
				if strings.Contains(err.Error(), "Order must have minimum value") {
					if err := e.store.RecordHyperliquidOrderRequest(ctx, ident, w.Action.Create, w.BotEvent.RowID); err != nil {
						logger.Warn("could not add to store", slog.String("error", err.Error()))
					}
					logger.Warn("could not submit (order value), ignoring", slog.String("error", err.Error()))
					return nil
				}

				if strings.Contains(err.Error(), "Reduce only order would increase position") {
					if err := e.store.RecordHyperliquidOrderRequest(ctx, ident, w.Action.Create, w.BotEvent.RowID); err != nil {
						logger.Warn("could not add to store", slog.String("error", err.Error()))
					}
					logger.Warn("could not submit (reduce only order, increase position), ignoring", slog.String("error", err.Error()))
					return nil
				}

				if strings.Contains(err.Error(), "Order could not immediately match against any resting orders") {
					retryInfo.count++
					retryInfo.lastErr = err.Error()
					if attempt+1 < maxAttempts {
						logger.Info("IOC did not immediately match; retrying", slog.Int("attempt", attempt+1), slog.Int("ioc-retries", retryInfo.count), slog.Int("max-attempts", maxAttempts), slog.String("error", err.Error()))
						continue
					}
				}

				if strings.Contains(err.Error(), "429") || strings.Contains(strings.ToLower(err.Error()), "rate limit") {
					logger.Debug("hit ratelimit, cooldown of 10s applied")
					// HL allows ~1 action per 10s when address-limited.
					e.applyCooldown(10 * time.Second)
				}
				logger.Warn("could not place order", slog.String("error", err.Error()), slog.Any("action", w.Action.Create))
				return fmt.Errorf("could not place order: %w", err)
			}

			lastStatus = &status
			success = true
			break
		}

		if !success {
			// IOC retries exhausted without success.
			err := fmt.Errorf("could not place order: %s", retryInfo.lastErr)
			logger.Warn("could not place order", slog.String("error", err.Error()), slog.Any("action", w.Action.Create))
			return err
		}

		if err := e.store.RecordHyperliquidOrderRequest(ctx, ident, w.Action.Create, w.BotEvent.RowID); err != nil {
			logger.Warn("could not add to store", slog.String("error", err.Error()))
		}
		executedAction = w.Action
		didSubmit = true
		if retryInfo.count > 0 {
			retryMeta = &iocRetryOrderId{count: retryInfo.count, lastError: retryInfo.lastErr}
		}

	case recomma.ActionCancel:
		logger.Info("Cancelling order", slog.Any("cancel", w.Action.Cancel))
		_, err := e.exchange.CancelByCloid(ctx, w.Action.Cancel.Coin, w.Action.Cancel.Cloid)
		if err != nil {
			// If HL rate limits (address-based or IP-based), apply a cooldown.
			if strings.Contains(err.Error(), "429") || strings.Contains(strings.ToLower(err.Error()), "rate limit") {
				logger.Debug("hit ratelimit, cooldown of 10s applied")
				// HL allows ~1 action per 10s when address-limited.
				e.applyCooldown(10 * time.Second)
			}
			logger.Warn("could not cancel order", slog.String("error", err.Error()), slog.Any("action", w.Action.Cancel))
			return fmt.Errorf("could not cancel order: %w", err)
		}
		if err := e.store.RecordHyperliquidCancel(ctx, ident, w.Action.Cancel, w.BotEvent.RowID); err != nil {
			logger.Warn("could not add to store", slog.String("error", err.Error()))
		}
		executedAction = w.Action
		didSubmit = true

	case recomma.ActionModify:
		order := e.setMarketPrice(ctx, wsClient, w.Action.Modify.Order)
		if constrainedPrice, err := e.constrainPrice(ctx, order.Coin, order.Price); err != nil {
			logger.Warn("could not constrain price", slog.String("error", err.Error()))
		} else {
			order.Price = constrainedPrice
		}
		w.Action.Modify.Order = order
		status, err := e.submitModify(ctx, logger, w, w.Action.Modify)
		if err != nil {
			return err
		}
		if status != nil {
			lastStatus = status
		}
		executedAction = w.Action
		didSubmit = true
	default:
		return nil
	}

	if didSubmit {
		requested := requestedOrderSize(executedAction)
		statusText, executed := orderStatusSummary(lastStatus)
		attrs := []any{
			slog.Any("action", executedAction),
			slog.Float64("requested_size", requested),
			slog.Float64("executed_size", executed),
		}
		if statusText != "" {
			attrs = append(attrs, slog.String("hl_status", statusText))
		}
		if retryMeta != nil {
			attrs = append(attrs, slog.Int("ioc-retries", retryMeta.count), slog.String("last-error", retryMeta.lastError))
			logger.Info("Order sent after IOC retries", attrs...)
		} else {
			logger.Info("Order sent", attrs...)
		}
	}

	return nil
}

func (e *HyperLiquidEmitter) constrainPrice(ctx context.Context, coin string, price float64) (float64, error) {
	constraints, err := e.constraints.Resolve(ctx, coin)
	if err != nil {
		return 0, fmt.Errorf("resolve constraints: %w", err)
	}

	return constraints.RoundPrice(price), nil
}

func (e *HyperLiquidEmitter) submitModify(
	ctx context.Context,
	logger *slog.Logger,
	w recomma.OrderWork,
	req hyperliquid.ModifyOrderRequest,
) (*hyperliquid.OrderStatus, error) {
	resp, err := e.exchange.ModifyOrder(ctx, req)
	var status *hyperliquid.OrderStatus
	if err == nil {
		status = &resp
	}
	if err != nil {
		errMsg := strings.ToLower(err.Error())
		// If HL rate limits (address-based or IP-based), apply a cooldown.
		if strings.Contains(err.Error(), "429") || strings.Contains(errMsg, "rate limit") {
			logger.Debug("hit ratelimit, cooldown of 10s applied")
			// HL allows ~1 action per 10s when address-limited.
			e.applyCooldown(10 * time.Second)
		}
		// We received an empty response, attempt to verify via the info client before failing.
		if strings.Contains(err.Error(), missingModifyResponseDataFragment) {
			status, err = e.confirmModifyViaStatus(ctx, w, req)
			if err == nil {
				logger.Info("modify verified via info fallback after empty response", slog.String("cloid", req.Cloid.Value))
			} else {
				return nil, fmt.Errorf("modify error was successful but returned an error: %w", err)
			}
		} else if strings.Contains(errMsg, cannotModifyFilledFragment) {
			cloid := ""
			if req.Cloid != nil {
				cloid = req.Cloid.Value
			}
			logger.Info("modify skipped because order already filled or canceled", slog.String("cloid", cloid))
			err = nil
		} else {
			logger.Warn("could not modify order", slog.String("error", err.Error()), slog.Any("action", req))
			return nil, fmt.Errorf("could not modify order: %w", err)
		}
	}
	if err := e.store.AppendHyperliquidModify(ctx, w.Identifier, req, w.BotEvent.RowID); err != nil {
		logger.Warn("could not add to store", slog.String("error", err.Error()))
	}
	return status, nil
}

func (e *HyperLiquidEmitter) confirmModifyViaStatus(
	ctx context.Context,
	w recomma.OrderWork,
	req hyperliquid.ModifyOrderRequest,
) (*hyperliquid.OrderStatus, error) {
	if e.statusClient == nil {
		return nil, fmt.Errorf("status client unavailable for venue %s", w.Identifier.Venue())
	}
	if req.Cloid == nil {
		return nil, errors.New("modify verification requires cloid")
	}

	result, err := e.statusClient.QueryOrderByCloid(ctx, req.Cloid.Value)
	if err != nil {
		return nil, fmt.Errorf("query order status: %w", err)
	}

	wsOrder, err := hl.OrderQueryResultToWsOrder(w.OrderId, result)
	if err != nil {
		return nil, fmt.Errorf("convert queried order: %w", err)
	}
	if wsOrder == nil {
		return nil, fmt.Errorf("order status unavailable for %s", req.Cloid.Value)
	}

	if !ordersMatch(wsOrder, &req.Order, req.Order) {
		return nil, fmt.Errorf("queried order does not match desired state")
	}

	res := &hyperliquid.OrderStatus{
		Resting: &hyperliquid.OrderStatusResting{
			Oid:    wsOrder.Order.Oid,
			Status: string(wsOrder.Status),
		},
	}
	if wsOrder.Order.Cloid != nil {
		res.Resting.ClientID = wsOrder.Order.Cloid
	}

	return res, nil
}

func requestedOrderSize(action recomma.Action) float64 {
	switch action.Type {
	case recomma.ActionCreate:
		return action.Create.Size
	case recomma.ActionModify:
		return action.Modify.Order.Size
	}
	return 0
}

func orderStatusSummary(status *hyperliquid.OrderStatus) (string, float64) {
	if status == nil {
		return "", 0
	}
	if status.Filled != nil {
		size, err := strconv.ParseFloat(status.Filled.TotalSz, 64)
		if err != nil {
			size = 0
		}
		return "filled", size
	}
	if status.Resting != nil {
		return status.Resting.Status, 0
	}
	if status.Error != nil {
		return "error", 0
	}
	return "", 0
}

func isLiveStatus(status *hyperliquid.WsOrder) bool {
	if status == nil {
		return false
	}

	switch status.Status {
	case hyperliquid.OrderStatusValueOpen:
		return true
	case hyperliquid.OrderStatusValue("live"):
		return true
	default:
		return false
	}
}

func ordersMatch(status *hyperliquid.WsOrder, latestSubmission *hyperliquid.CreateOrderRequest, desired hyperliquid.CreateOrderRequest) bool {
	if status == nil {
		return false
	}
	if latestSubmission == nil {
		return false
	}

	if !strings.EqualFold(status.Order.Coin, desired.Coin) {
		return false
	}

	side := strings.ToUpper(status.Order.Side)
	if desired.IsBuy {
		if side != "B" {
			return false
		}
	} else {
		if side != "S" {
			return false
		}
	}

	size, err := strconv.ParseFloat(status.Order.Sz, 64)
	if err != nil {
		return false
	}
	if !floatEquals(size, desired.Size) {
		return false
	}

	price, err := strconv.ParseFloat(status.Order.LimitPx, 64)
	if err != nil {
		return false
	}
	if !floatEquals(price, desired.Price) {
		return false
	}

	if latestSubmission.ReduceOnly != desired.ReduceOnly {
		return false
	}

	if !orderTypesMatch(latestSubmission.OrderType, desired.OrderType) {
		return false
	}

	return true
}

const floatEqualityTolerance = 1e-9

func floatEquals(a, b float64) bool {
	return math.Abs(a-b) <= floatEqualityTolerance
}

func orderTypesMatch(existing, desired hyperliquid.OrderType) bool {
	switch {
	case existing.Limit != nil || desired.Limit != nil:
		if existing.Limit == nil || desired.Limit == nil {
			return false
		}
		return existing.Limit.Tif == desired.Limit.Tif
	case existing.Trigger != nil || desired.Trigger != nil:
		if existing.Trigger == nil || desired.Trigger == nil {
			return false
		}
		if !floatEquals(existing.Trigger.TriggerPx, desired.Trigger.TriggerPx) {
			return false
		}
		if existing.Trigger.IsMarket != desired.Trigger.IsMarket {
			return false
		}
		if existing.Trigger.Tpsl != desired.Trigger.Tpsl {
			return false
		}
		return true
	default:
		return true
	}
}

// RoundHalfEven rounds a float ties-to-even to 8 decimals, required for
// Hyperliquid wire format that expects a float to not be more precise.
func RoundHalfEven(x float64) float64 {
	p := math.Pow(10, float64(8))
	y := x * p
	f, frac := math.Modf(y)
	absFrac := math.Abs(frac)

	switch {
	case absFrac < 0.5:
		return f / p
	case absFrac > 0.5:
		if y > 0 {
			return (f + 1) / p
		}
		return (f - 1) / p
	default: // exactly .5 or -.5
		// round to make the last kept digit even
		if int64(math.Abs(f))%2 == 0 {
			return f / p
		}
		if y > 0 {
			return (f + 1) / p
		}
		return (f - 1) / p
	}
}
