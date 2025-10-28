package filltracker

import (
	"context"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/recomma/recomma/metadata"
	"github.com/recomma/recomma/recomma"
	"github.com/recomma/recomma/storage"
	"github.com/sonirico/go-hyperliquid"
	tc "github.com/terwey/3commas-sdk-go/threecommas"
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
func (s *Service) UpdateStatus(ctx context.Context, md metadata.Metadata, status hyperliquid.WsOrder) error {
	event, err := s.store.LoadThreeCommasBotEvent(ctx, md)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state := s.ensureOrderLocked(md)
	if event != nil {
		clone := *event
		state.applyEvent(&clone)
	}
	state.applyStatus(status)

	deal := s.ensureDealLocked(md)
	deal.orders[md.Hex()] = state
	deal.lastUpdate = time.Now().UTC()
	deal.recompute()

	s.logger.Debug("updated order status",
		slog.Uint64("deal_id", uint64(deal.dealID)),
		slog.String("md", md.Hex()),
		slog.String("status", string(status.Status)),
		slog.Float64("filled_qty", state.filledQty),
		slog.Float64("remaining_qty", state.remainingQty),
	)

	return nil
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

// CancelCompletedTakeProfits cancels open reduce-only orders for deals where every buy-side order filled.
func (s *Service) CancelCompletedTakeProfits(ctx context.Context, submitter recomma.Emitter) {
	for _, dealID := range s.listDealIDs() {
		snapshot, ok := s.Snapshot(dealID)
		if !ok {
			continue
		}
		if !snapshot.AllBuysFilled {
			continue
		}

		tp := snapshot.ActiveTakeProfit
		if tp == nil {
			continue
		}

		cancel := hyperliquid.CancelOrderRequestByCloid{
			Coin:  snapshot.Currency,
			Cloid: tp.Metadata.Hex(),
		}
		work := recomma.OrderWork{
			MD: tp.Metadata,
			Action: recomma.Action{
				Type:   recomma.ActionCancel,
				Cancel: &cancel,
				Reason: "deal fully averaged; refreshing take profit",
			},
		}

		cancelCtx, cancelFn := context.WithTimeout(ctx, 30*time.Second)
		err := submitter.Emit(cancelCtx, work)
		cancelFn()

		fields := []any{
			slog.Uint64("deal_id", uint64(snapshot.DealID)),
			slog.Uint64("bot_id", uint64(snapshot.BotID)),
			slog.String("cloid", tp.Metadata.Hex()),
			slog.Float64("qty", tp.RemainingQty),
		}
		if err != nil {
			s.logger.Warn("cancel take profit failed", append(fields, slog.String("error", err.Error()))...)
			continue
		}
		s.logger.Info("cancelled take profit", append(fields, slog.Float64("net_qty", snapshot.Position.NetQty))...)
	}
}

func (s *Service) reloadDeal(ctx context.Context, dealID uint32) error {
	mds, err := s.store.ListMetadataForDeal(ctx, dealID)
	if err != nil {
		return err
	}
	if len(mds) == 0 {
		return nil
	}

	for _, md := range mds {
		event, err := s.store.LoadThreeCommasBotEvent(ctx, md)
		if err != nil {
			return err
		}
		status, found, err := s.store.LoadHyperliquidStatus(ctx, md)
		if err != nil {
			return err
		}

		s.mu.Lock()
		state := s.ensureOrderLocked(md)
		if event != nil {
			clone := *event
			state.applyEvent(&clone)
		}
		if found && status != nil {
			state.applyStatus(*status)
		} else {
			state.inferFromEvent()
		}

		deal := s.ensureDealLocked(md)
		deal.orders[md.Hex()] = state
		deal.lastUpdate = time.Now().UTC()
		deal.recompute()
		s.mu.Unlock()
	}

	return nil
}

func (s *Service) ensureOrderLocked(md metadata.Metadata) *orderState {
	key := md.Hex()
	state, ok := s.orders[key]
	if !ok {
		state = &orderState{
			metadata: md,
		}
		s.orders[key] = state
	}
	return state
}

func (s *Service) ensureDealLocked(md metadata.Metadata) *dealState {
	deal, ok := s.deals[md.DealID]
	if !ok {
		deal = &dealState{
			botID:      md.BotID,
			dealID:     md.DealID,
			orders:     make(map[string]*orderState),
			lastUpdate: time.Now().UTC(),
		}
		s.deals[md.DealID] = deal
	}
	if deal.botID == 0 {
		deal.botID = md.BotID
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
	metadata metadata.Metadata

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
	o.status = status.Status
	if status.StatusTimestamp > 0 {
		o.statusObserved = time.UnixMilli(status.StatusTimestamp).UTC()
	}
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
		Metadata:       o.metadata,
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
	Metadata metadata.Metadata
	Coin     string

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
