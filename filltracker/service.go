package filltracker

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/adapter"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
	"github.com/recomma/recomma/storage"
	"github.com/sonirico/go-hyperliquid"
)

const floatTolerance = 1e-6

// Service keeps track of the executed and outstanding orders for a deal.
type Service struct {
	store  *storage.Storage
	logger *slog.Logger

	mu     sync.RWMutex
	orders map[string]*orderState // keyed by metadata hex
	deals  map[uint32]*dealState  // keyed by deal id
}

// New creates a new fill tracker service.
func New(store *storage.Storage, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		store:  store,
		logger: logger.WithGroup("filltracker"),
		orders: make(map[string]*orderState),
		deals:  make(map[uint32]*dealState),
	}
}

// Rebuild loads fill state for all deals from storage. Call once during startup.
func (s *Service) Rebuild(ctx context.Context) error {
	start := time.Now()

	dealIDs, err := s.store.ListDealIDs(ctx)
	if err != nil {
		return err
	}

	var loaded int
	for _, rawID := range dealIDs {
		dealID := uint32(rawID)
		if err := s.reloadDeal(ctx, dealID); err != nil {
			s.logger.Warn("rebuild: skipping deal", slog.Uint64("deal_id", uint64(dealID)), slog.String("error", err.Error()))
			continue
		}
		loaded++
	}

	s.logger.Info("rebuild completed", slog.Int("deals", loaded), slog.Duration("elapsed", time.Since(start)))
	return nil
}

// UpdateStatus ingests a fresh Hyperliquid status update for a metadata fingerprint.
func (s *Service) UpdateStatus(ctx context.Context, oid orderid.OrderId, status hyperliquid.WsOrder) error {
	event, err := s.store.LoadThreeCommasBotEvent(ctx, oid)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state := s.ensureOrderLocked(oid)
	if event != nil {
		clone := *event
		state.applyEvent(&clone)
	}
	state.applyStatus(status)

	deal := s.ensureDealLocked(oid)
	deal.orders[oid.Hex()] = state
	deal.lastUpdate = time.Now().UTC()
	deal.recompute()

	s.logger.Debug("updated order status",
		slog.Uint64("deal_id", uint64(deal.dealID)),
		slog.String("orderid", oid.Hex()),
		slog.String("status", string(status.Status)),
		slog.Float64("filled_qty", state.filledQty),
		slog.Float64("remaining_qty", state.remainingQty),
	)

	return nil
}

// ApplyScaledOrder updates the cached state with a scaled order size/price.
func (s *Service) ApplyScaledOrder(oid orderid.OrderId, size, price float64) {
	if s == nil {
		return
	}
	if size <= 0 && price <= 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state := s.ensureOrderLocked(oid)
	if size > 0 {
		state.originalQty = size
		if state.filledQty > size {
			state.filledQty = size
		}
		remaining := size - state.filledQty
		if remaining < 0 {
			remaining = 0
		}
		state.remainingQty = remaining
	}
	if price > 0 {
		state.price = price
		if state.limitPrice == 0 {
			state.limitPrice = price
		}
	}
	state.lastUpdate = time.Now().UTC()

	deal := s.ensureDealLocked(oid)
	deal.orders[oid.Hex()] = state
	deal.lastUpdate = time.Now().UTC()
	deal.recompute()
}

// Snapshot returns a copy of the tracked deal state. ok=false if unknown.
func (s *Service) Snapshot(dealID uint32) (DealSnapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	deal, ok := s.deals[dealID]
	if !ok {
		return DealSnapshot{}, false
	}
	return deal.snapshot(), true
}

// ReconcileTakeProfits ensures a reduce-only order exists that matches the current net position.
func (s *Service) ReconcileTakeProfits(ctx context.Context, submitter recomma.Emitter) {
	for _, dealID := range s.listDealIDs() {
		snapshot, ok := s.Snapshot(dealID)
		if !ok {
			continue
		}
		if !snapshot.AllBuysFilled {
			continue
		}

		desiredQty := snapshot.Position.NetQty
		if desiredQty <= floatTolerance {
			if snapshot.ActiveTakeProfit != nil {
				s.cancelTakeProfit(ctx, submitter, snapshot)
			}
			continue
		}

		if snapshot.ActiveTakeProfit != nil {
			if s.reconcileActiveTakeProfit(ctx, submitter, snapshot, desiredQty) {
				continue
			}
		}

		s.ensureTakeProfit(ctx, submitter, snapshot, desiredQty, nil, nil, false)
	}
}

func (s *Service) cancelTakeProfit(
	ctx context.Context,
	submitter recomma.Emitter,
	snapshot DealSnapshot,
) {
	tp := snapshot.ActiveTakeProfit
	if tp == nil {
		return
	}

	oid := tp.OrderId
	cancel := hyperliquid.CancelOrderRequestByCloid{
		Coin:  snapshot.Currency,
		Cloid: oid.Hex(),
	}

	work := recomma.OrderWork{
		OrderId: oid,
		Action: recomma.Action{
			Type:   recomma.ActionCancel,
			Cancel: &cancel,
			Reason: "position closed; cancel stale take profit",
		},
	}

	s.emitOrderWork(ctx, submitter, work, "cancelled take profit for flat position", snapshot, tp.RemainingQty)
}

func (s *Service) reconcileActiveTakeProfit(
	ctx context.Context,
	submitter recomma.Emitter,
	snapshot DealSnapshot,
	desiredQty float64,
) bool {
	tp := snapshot.ActiveTakeProfit
	if tp == nil {
		return false
	}

	if tp.ReduceOnly && floatsEqual(tp.RemainingQty, desiredQty) {
		return true
	}

	oid := tp.OrderId
	evt := cloneEvent(tp.Event)
	if evt == nil {
		evt = cloneEvent(snapshot.LastTakeProfitEvent)
	}

	if evt != nil {
		modify := adapter.ToModifyOrderRequest(snapshot.Currency, recomma.BotEvent{BotEvent: *evt}, oid)
		modify.Order.Size = desiredQty
		modify.Order.ReduceOnly = true

		if s.shouldSkipSubmission(ctx, oid, modify.Order.Size, modify.Order.ReduceOnly) {
			s.logger.Debug("skip take profit modify: already submitted", slog.Uint64("deal_id", uint64(snapshot.DealID)), slog.String("cloid", oid.Hex()))
			return true
		}

		work := recomma.OrderWork{
			OrderId: oid,
			Action: recomma.Action{
				Type:   recomma.ActionModify,
				Modify: &modify,
				Reason: "resize take profit to match net position",
			},
			BotEvent: recomma.BotEvent{BotEvent: *evt},
		}

		s.emitOrderWork(ctx, submitter, work, "modified take profit", snapshot, desiredQty)
		return true
	}

	// Unable to build a modify request; cancel and recreate using fallback OrderId.
	cancel := hyperliquid.CancelOrderRequestByCloid{
		Coin:  snapshot.Currency,
		Cloid: oid.Hex(),
	}
	cancelWork := recomma.OrderWork{
		OrderId: oid,
		Action: recomma.Action{
			Type:   recomma.ActionCancel,
			Cancel: &cancel,
			Reason: "stale take profit metadata; cancel before recreation",
		},
	}
	if !s.emitOrderWork(ctx, submitter, cancelWork, "cancelled take profit", snapshot, tp.RemainingQty) {
		return true
	}

	s.ensureTakeProfit(ctx, submitter, snapshot, desiredQty, &oid, snapshot.LastTakeProfitEvent, true)
	return true
}

func (s *Service) ensureTakeProfit(
	ctx context.Context,
	submitter recomma.Emitter,
	snapshot DealSnapshot,
	desiredQty float64,
	preferredOid *orderid.OrderId,
	preferredEvent *tc.BotEvent,
	force bool,
) {
	oid, evt, ok := s.lookupTakeProfitContext(ctx, snapshot, preferredOid, preferredEvent)
	if !ok {
		s.logger.Warn("take profit reconciliation skipped: no metadata", slog.Uint64("deal_id", uint64(snapshot.DealID)))
		return
	}

	create := adapter.ToCreateOrderRequest(snapshot.Currency, recomma.BotEvent{BotEvent: *evt}, oid)
	create.Size = desiredQty
	create.ReduceOnly = true

	if !force && s.shouldSkipSubmission(ctx, oid, create.Size, create.ReduceOnly) {
		s.logger.Debug("skip take profit create: already submitted", slog.Uint64("deal_id", uint64(snapshot.DealID)), slog.String("cloid", oid.Hex()))
		return
	}

	work := recomma.OrderWork{
		OrderId: oid,
		Action: recomma.Action{
			Type:   recomma.ActionCreate,
			Create: &create,
			Reason: "recreate missing take profit",
		},
		BotEvent: recomma.BotEvent{BotEvent: *evt},
	}

	s.emitOrderWork(ctx, submitter, work, "recreated take profit", snapshot, desiredQty)
}

func (s *Service) emitOrderWork(
	ctx context.Context,
	submitter recomma.Emitter,
	work recomma.OrderWork,
	logMsg string,
	snapshot DealSnapshot,
	qty float64,
) bool {
	emitCtx, cancelFn := context.WithTimeout(ctx, 30*time.Second)
	err := submitter.Emit(emitCtx, work)
	cancelFn()

	fields := []any{
		slog.Uint64("deal_id", uint64(snapshot.DealID)),
		slog.Uint64("bot_id", uint64(snapshot.BotID)),
		slog.String("cloid", work.OrderId.Hex()),
		slog.Float64("net_qty", snapshot.Position.NetQty),
		slog.Float64("target_qty", qty),
	}

	performed := true
	if err != nil && errors.Is(err, recomma.ErrOrderAlreadySatisfied) {
		performed = false
		err = nil
	}

	if err != nil {
		s.logger.Warn(logMsg+" failed", append(fields, slog.String("error", err.Error()))...)
		return false
	}

	if !performed {
		s.logger.Debug(logMsg+" skipped", append(fields, slog.String("reason", "order already satisfied"))...)
		return false
	}

	s.logger.Info(logMsg, fields...)
	return true
}

func (s *Service) shouldSkipSubmission(ctx context.Context, oid orderid.OrderId, desiredSize float64, requireReduceOnly bool) bool {
	action, found, err := s.store.LoadHyperliquidSubmission(ctx, storage.DefaultHyperliquidIdentifier(oid))
	if err != nil {
		s.logger.Warn("load hyperliquid submission failed", slog.String("cloid", oid.Hex()), slog.String("error", err.Error()))
		return false
	}
	if !found {
		return false
	}

	if !submissionMatchesDesired(action, desiredSize, requireReduceOnly) {
		return false
	}

	status, haveStatus, err := s.store.LoadHyperliquidStatus(ctx, storage.DefaultHyperliquidIdentifier(oid))
	if err != nil {
		s.logger.Warn("load hyperliquid status failed", slog.String("cloid", oid.Hex()), slog.String("error", err.Error()))
		return false
	}
	if !haveStatus || !isLiveHyperliquidStatus(status) {
		return false
	}

	return true
}

func submissionMatchesDesired(action recomma.Action, desiredSize float64, requireReduceOnly bool) bool {
	switch action.Type {
	case recomma.ActionCreate:
		if action.Create == nil {
			return false
		}
		if requireReduceOnly && !action.Create.ReduceOnly {
			return false
		}
		return floatsEqual(action.Create.Size, desiredSize)
	case recomma.ActionModify:
		if action.Modify == nil {
			return false
		}
		if requireReduceOnly && !action.Modify.Order.ReduceOnly {
			return false
		}
		return floatsEqual(action.Modify.Order.Size, desiredSize)
	default:
		return false
	}
}

func isLiveHyperliquidStatus(status *hyperliquid.WsOrder) bool {
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

func (s *Service) lookupTakeProfitContext(
	ctx context.Context,
	snapshot DealSnapshot,
	preferredOid *orderid.OrderId,
	preferredEvent *tc.BotEvent,
) (orderid.OrderId, *tc.BotEvent, bool) {
	var oid *orderid.OrderId
	if preferredOid != nil {
		clone := *preferredOid
		oid = &clone
	}

	evt := cloneEvent(preferredEvent)
	if evt == nil {
		evt = cloneEvent(snapshot.LastTakeProfitEvent)
	}

	if oid == nil && evt != nil {
		for _, order := range snapshot.Orders {
			if !order.ReduceOnly || order.Event == nil {
				continue
			}
			if eventsMatch(order.Event, evt) {
				clone := order.OrderId
				oid = &clone
				break
			}
		}
	}

	needLoadOrderId := oid == nil
	needLoadEvent := evt == nil
	if needLoadOrderId || needLoadEvent {
		storedOrderId, storedEvt, err := s.store.LoadTakeProfitForDeal(ctx, snapshot.DealID)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				s.logger.Warn("load take profit metadata failed", slog.Uint64("deal_id", uint64(snapshot.DealID)), slog.String("error", err.Error()))
			}
			return orderid.OrderId{}, nil, false
		}
		if needLoadOrderId {
			clone := *storedOrderId
			oid = &clone
		}
		if needLoadEvent {
			evt = cloneEvent(storedEvt)
		}
	}

	if oid == nil || evt == nil {
		return orderid.OrderId{}, nil, false
	}

	return *oid, evt, true
}

func cloneEvent(evt *tc.BotEvent) *tc.BotEvent {
	if evt == nil {
		return nil
	}
	clone := *evt
	return &clone
}

func eventsMatch(a, b *tc.BotEvent) bool {
	if a == nil || b == nil {
		return false
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return false
	}
	return a.Fingerprint() == b.Fingerprint()
}

func floatsEqual(a, b float64) bool {
	return math.Abs(a-b) <= floatTolerance
}

func (s *Service) reloadDeal(ctx context.Context, dealID uint32) error {
	oids, err := s.store.ListOrderIdsForDeal(ctx, dealID)
	if err != nil {
		return err
	}
	if len(oids) == 0 {
		return nil
	}

	for _, oid := range oids {
		event, err := s.store.LoadThreeCommasBotEvent(ctx, oid)
		if err != nil {
			return err
		}
		status, found, err := s.store.LoadHyperliquidStatus(ctx, storage.DefaultHyperliquidIdentifier(oid))
		if err != nil {
			return err
		}

		scaledOrders, err := s.store.ListScaledOrdersByOrderId(ctx, oid)
		if err != nil {
			s.logger.Warn("load scaled orders", slog.Uint64("deal_id", uint64(oid.DealID)), slog.String("error", err.Error()))
		}
		var latestScaled *storage.ScaledOrderAudit
		if err == nil && len(scaledOrders) > 0 {
			copy := scaledOrders[len(scaledOrders)-1]
			latestScaled = &copy
		}

		s.mu.Lock()
		state := s.ensureOrderLocked(oid)
		if event != nil {
			clone := *event
			state.applyEvent(&clone)
		}
		if found && status != nil {
			state.applyStatus(*status)
		} else {
			state.inferFromEvent()
		}
		if latestScaled != nil {
			if latestScaled.ScaledSize > 0 {
				state.originalQty = latestScaled.ScaledSize
				if state.filledQty > state.originalQty {
					state.filledQty = state.originalQty
				}
				remaining := state.originalQty - state.filledQty
				if remaining < 0 {
					remaining = 0
				}
				state.remainingQty = remaining
			}
		}

		deal := s.ensureDealLocked(oid)
		deal.orders[oid.Hex()] = state
		deal.lastUpdate = time.Now().UTC()
		deal.recompute()
		s.mu.Unlock()
	}

	return nil
}

func (s *Service) ensureOrderLocked(oid orderid.OrderId) *orderState {
	key := oid.Hex()
	state, ok := s.orders[key]
	if !ok {
		state = &orderState{
			oid: oid,
		}
		s.orders[key] = state
	}
	return state
}

func (s *Service) ensureDealLocked(oid orderid.OrderId) *dealState {
	deal, ok := s.deals[oid.DealID]
	if !ok {
		deal = &dealState{
			botID:      oid.BotID,
			dealID:     oid.DealID,
			orders:     make(map[string]*orderState),
			lastUpdate: time.Now().UTC(),
		}
		s.deals[oid.DealID] = deal
	}
	if deal.botID == 0 {
		deal.botID = oid.BotID
	}
	return deal
}

func (s *Service) listDealIDs() []uint32 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]uint32, 0, len(s.deals))
	for id := range s.deals {
		out = append(out, id)
	}
	return out
}

type orderState struct {
	oid orderid.OrderId

	event *tc.BotEvent

	coin       string
	orderType  string
	side       string
	reduceOnly bool

	originalQty  float64
	remainingQty float64
	filledQty    float64
	price        float64
	limitPrice   float64

	status         hyperliquid.OrderStatusValue
	statusObserved time.Time
	lastUpdate     time.Time
}

func (o *orderState) applyEvent(evt *tc.BotEvent) {
	if evt == nil {
		return
	}
	clone := *evt
	o.event = &clone
	o.coin = evt.Coin
	o.orderType = string(evt.OrderType)
	o.side = string(evt.Type)
	if evt.Size > 0 && o.originalQty == 0 {
		o.originalQty = evt.Size
	}
	if evt.Price > 0 {
		o.price = evt.Price
	}
	if evt.OrderType == tc.MarketOrderDealOrderTypeTakeProfit {
		o.reduceOnly = true
	}
}

func (o *orderState) applyStatus(status hyperliquid.WsOrder) {
	if status.StatusTimestamp > 0 {
		observed := time.UnixMilli(status.StatusTimestamp).UTC()
		if !o.statusObserved.IsZero() && observed.Before(o.statusObserved) {
			return
		}
		o.statusObserved = observed
	}

	o.status = status.Status
	o.lastUpdate = time.Now().UTC()

	if status.Order.Coin != "" {
		o.coin = status.Order.Coin
	}
	if status.Order.Side != "" {
		o.side = status.Order.Side
	}
	if status.Order.LimitPx != "" {
		if px, err := parseFloat(status.Order.LimitPx); err == nil {
			o.limitPrice = px
		}
	}
	if status.Order.OrigSz != "" {
		if qty, err := parseFloat(status.Order.OrigSz); err == nil {
			o.originalQty = qty
		}
	}
	if status.Order.Sz != "" {
		if qty, err := parseFloat(status.Order.Sz); err == nil {
			o.remainingQty = qty
		}
	}

	if o.originalQty > 0 {
		o.filledQty = math.Max(0, o.originalQty-o.remainingQty)
	}
}

func (o *orderState) inferFromEvent() {
	if o.event == nil {
		return
	}
	if o.event.Size > 0 && o.originalQty == 0 {
		o.originalQty = o.event.Size
	}
	if o.event.Status == tc.Filled || o.event.Status == tc.Finished {
		o.filledQty = o.event.Size
		o.remainingQty = 0
	}
	if o.event.CreatedAt.UnixMilli() > 0 {
		o.statusObserved = o.event.CreatedAt.UTC()
	}
}

func (o *orderState) isBuy() bool {
	if o.event != nil {
		return o.event.Type == tc.BUY
	}
	return strings.EqualFold(o.side, "B") || strings.EqualFold(o.side, "BUY")
}

func (o *orderState) isActive() bool {
	if o.status == hyperliquid.OrderStatusValueCanceled || o.status == hyperliquid.OrderStatusValueFilled {
		return false
	}
	if o.remainingQty <= floatTolerance {
		return false
	}
	return true
}

func (o *orderState) isFilled() bool {
	if o.status == hyperliquid.OrderStatusValueFilled {
		return true
	}
	if o.originalQty == 0 {
		return false
	}
	return math.Abs(o.filledQty-o.originalQty) <= floatTolerance || o.remainingQty <= floatTolerance
}

func (o *orderState) fillPrice() float64 {
	if o.limitPrice > 0 {
		return o.limitPrice
	}
	if o.price > 0 {
		return o.price
	}
	return 0
}

func (o *orderState) snapshot() OrderSnapshot {
	var eventCopy *tc.BotEvent
	if o.event != nil {
		clone := *o.event
		eventCopy = &clone
	}

	return OrderSnapshot{
		OrderId:        o.oid,
		Coin:           o.coin,
		OrderType:      o.orderType,
		Side:           o.side,
		ReduceOnly:     o.reduceOnly,
		OriginalQty:    o.originalQty,
		RemainingQty:   o.remainingQty,
		FilledQty:      o.filledQty,
		LimitPrice:     o.limitPrice,
		ReferencePrice: o.price,
		Status:         o.status,
		StatusTime:     o.statusObserved,
		LastUpdate:     o.lastUpdate,
		Event:          eventCopy,
	}
}

type dealState struct {
	botID  uint32
	dealID uint32

	currency string
	orders   map[string]*orderState

	position   dealPosition
	lastUpdate time.Time
}

func (d *dealState) recompute() {
	var currency string
	var buyQty, buyValue, sellQty float64

	for _, order := range d.orders {
		if currency == "" && order.coin != "" {
			currency = order.coin
		}
		filled := order.filledQty
		if filled <= 0 {
			continue
		}

		if order.isBuy() {
			buyQty += filled
			price := order.fillPrice()
			if price > 0 {
				buyValue += price * filled
			}
		} else {
			sellQty += filled
		}
	}

	net := buyQty - sellQty
	d.position.TotalBuyQty = buyQty
	d.position.TotalBuyValue = buyValue
	d.position.TotalSellQty = sellQty
	d.position.NetQty = net
	if buyQty > 0 {
		d.position.AverageEntry = buyValue / buyQty
	} else {
		d.position.AverageEntry = 0
	}
	if currency != "" {
		d.currency = currency
	}
}

func (d *dealState) snapshot() DealSnapshot {
	out := DealSnapshot{
		DealID:    d.dealID,
		BotID:     d.botID,
		Currency:  d.currency,
		Position:  DealPosition(d.position),
		Orders:    make([]OrderSnapshot, 0, len(d.orders)),
		UpdatedAt: d.lastUpdate,
	}

	allBuysFilled := true
	var outstandingBuyQty float64
	var outstandingSellQty float64
	var activeTP *OrderSnapshot
	var latestTPEvent *tc.BotEvent

	for _, state := range d.orders {
		snap := state.snapshot()
		out.Orders = append(out.Orders, snap)

		if state.isBuy() {
			if state.isActive() {
				outstandingBuyQty += state.remainingQty
				allBuysFilled = false
				out.OpenBuys = append(out.OpenBuys, snap)
			} else if !state.isFilled() {
				allBuysFilled = false
			}
		} else {
			outstandingSellQty += state.remainingQty
			if state.reduceOnly && state.isActive() {
				if activeTP == nil || snap.StatusTime.After(activeTP.StatusTime) {
					copy := snap
					activeTP = &copy
				}
			}
		}

		if state.reduceOnly && state.event != nil {
			if latestTPEvent == nil || state.event.CreatedAt.After(latestTPEvent.CreatedAt) {
				clone := *state.event
				latestTPEvent = &clone
			}
		}
	}

	out.ActiveTakeProfit = activeTP
	out.LastTakeProfitEvent = latestTPEvent
	out.AllBuysFilled = allBuysFilled
	out.OutstandingBuyQty = outstandingBuyQty
	out.OutstandingSellQty = outstandingSellQty

	return out
}

type dealPosition struct {
	TotalBuyQty   float64
	TotalBuyValue float64
	TotalSellQty  float64
	NetQty        float64
	AverageEntry  float64
}

// DealSnapshot captures the computed state for a deal.
type DealSnapshot struct {
	DealID   uint32
	BotID    uint32
	Currency string

	Position DealPosition

	Orders []OrderSnapshot

	ActiveTakeProfit    *OrderSnapshot
	LastTakeProfitEvent *tc.BotEvent

	OpenBuys []OrderSnapshot

	AllBuysFilled      bool
	OutstandingBuyQty  float64
	OutstandingSellQty float64

	UpdatedAt time.Time
}

// DealPosition summarises executable size information for a deal.
type DealPosition struct {
	TotalBuyQty   float64
	TotalBuyValue float64
	TotalSellQty  float64
	NetQty        float64
	AverageEntry  float64
}

// OrderSnapshot describes a single order tracked by the fill tracker.
type OrderSnapshot struct {
	OrderId orderid.OrderId
	Coin    string

	OrderType string
	Side      string

	ReduceOnly bool

	OriginalQty  float64
	RemainingQty float64
	FilledQty    float64

	LimitPrice     float64
	ReferencePrice float64

	Status     hyperliquid.OrderStatusValue
	StatusTime time.Time
	LastUpdate time.Time

	Event *tc.BotEvent
}

func parseFloat(in string) (float64, error) {
	return strconv.ParseFloat(in, 64)
}
