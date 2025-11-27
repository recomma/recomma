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
	orders map[recomma.OrderIdentifier]*orderState
	deals  map[uint32]*dealState
}

// New creates a new fill tracker service.
func New(store *storage.Storage, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		store:  store,
		logger: logger.WithGroup("filltracker"),
		orders: make(map[recomma.OrderIdentifier]*orderState),
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
func (s *Service) UpdateStatus(ctx context.Context, ident recomma.OrderIdentifier, status hyperliquid.WsOrder) error {
	event, err := s.store.LoadThreeCommasBotEvent(ctx, ident.OrderId)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state := s.ensureOrderLocked(ident)
	if event != nil {
		clone := *event
		state.applyEvent(&clone)
	}
	state.applyStatus(status)

	deal := s.ensureDealLocked(ident)
	deal.orders[ident] = state
	deal.lastUpdate = time.Now().UTC()
	deal.recompute()

	s.logger.Debug("updated order status",
		slog.Uint64("deal_id", uint64(deal.dealID)),
		slog.String("venue", ident.Venue()),
		slog.String("wallet", ident.Wallet),
		slog.String("orderid", ident.Hex()),
		slog.String("status", string(status.Status)),
		slog.Float64("filled_qty", state.filledQty),
		slog.Float64("remaining_qty", state.remainingQty),
	)

	return nil
}

// ApplyScaledOrder updates the cached state with a scaled order size/price.
func (s *Service) ApplyScaledOrder(ident recomma.OrderIdentifier, size, price float64) {
	if s == nil {
		return
	}
	if size <= 0 && price <= 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state := s.ensureOrderLocked(ident)
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

	deal := s.ensureDealLocked(ident)
	deal.orders[ident] = state
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

		// Calculate per-venue positions from filled orders
		venuePositions := s.calculateVenuePositions(snapshot)

		// Map existing active take-profits by venue+wallet (not full identifier)
		activeTPs := make(map[venueKey]OrderSnapshot)
		for _, tp := range snapshot.ActiveTakeProfits {
			key := venueKey{venue: tp.Identifier.VenueID, wallet: tp.Identifier.Wallet}
			activeTPs[key] = tp
		}

		// Reconcile each venue independently
		processedVenues := make(map[venueKey]bool)
		for ident, venueNetQty := range venuePositions {
			key := venueKey{venue: ident.VenueID, wallet: ident.Wallet}
			processedVenues[key] = true

			if venueNetQty <= floatTolerance {
				// Position flat for this venue; cancel TP if it exists
				if tp, exists := activeTPs[key]; exists {
					s.cancelTakeProfitBySnapshot(ctx, submitter, snapshot, tp)
				}
				continue
			}

			// Check if this venue has an active take-profit
			if tp, exists := activeTPs[key]; exists {
				// Reconcile existing take-profit to match venue's net position
				s.reconcileActiveTakeProfitBySnapshot(ctx, submitter, snapshot, tp, venueNetQty)
			} else {
				// Create take-profit for this venue
				// Build the correct identifier with venue+wallet and TP's OrderId (not base order's)
				tpIdent := s.buildTakeProfitIdentifier(ctx, snapshot, ident.VenueID, ident.Wallet)
				s.ensureTakeProfit(ctx, submitter, snapshot, venueNetQty, tpIdent, nil, false)
			}
		}

		// Cancel any TPs for venues that have no position (not in venuePositions map)
		for key, tp := range activeTPs {
			if !processedVenues[key] {
				s.cancelTakeProfitBySnapshot(ctx, submitter, snapshot, tp)
			}
		}
	}
}

// buildTakeProfitIdentifier constructs an identifier for a TP at a specific venue.
// It looks up the TP's OrderId and combines it with the target venue+wallet.
// Returns nil if no TP metadata exists (caller should pass nil to ensureTakeProfit).
func (s *Service) buildTakeProfitIdentifier(
	ctx context.Context,
	snapshot DealSnapshot,
	targetVenue recomma.VenueID,
	targetWallet string,
) *recomma.OrderIdentifier {
	// Try to find a TP OrderId from the snapshot first
	for _, order := range snapshot.Orders {
		if order.ReduceOnly && order.Event != nil {
			// Found a TP order; use its OrderId with our target venue+wallet
			ident := recomma.NewOrderIdentifier(targetVenue, targetWallet, order.OrderId)
			return &ident
		}
	}

	// Try loading from storage
	storedOrderId, _, err := s.store.LoadTakeProfitForDeal(ctx, snapshot.DealID)
	if err != nil {
		// No TP metadata available; return nil to let ensureTakeProfit handle lookup
		return nil
	}

	ident := recomma.NewOrderIdentifier(targetVenue, targetWallet, *storedOrderId)
	return &ident
}

// venueKey uniquely identifies a venue+wallet combination for position tracking.
type venueKey struct {
	venue  recomma.VenueID
	wallet string
}

// calculateVenuePositions computes the net position per venue from filled orders.
// Returns a map keyed by venue+wallet (not including OrderId) to properly aggregate
// all orders for the same venue. Includes filled reduce-only orders (take-profits that
// have executed) but bases calculation on FilledQty to represent actual position changes.
func (s *Service) calculateVenuePositions(snapshot DealSnapshot) map[recomma.OrderIdentifier]float64 {
	// First, aggregate by venue+wallet only
	venuePositions := make(map[venueKey]float64)
	venueIdentifiers := make(map[venueKey]recomma.OrderIdentifier)

	for _, order := range snapshot.Orders {
		key := venueKey{venue: order.Identifier.VenueID, wallet: order.Identifier.Wallet}

		// Track one identifier per venue for result map (use any order's identifier)
		if _, exists := venueIdentifiers[key]; !exists {
			venueIdentifiers[key] = order.Identifier
		}

		// Use FilledQty to compute actual position changes (includes filled TPs)
		if order.Side == "B" || strings.EqualFold(order.Side, "BUY") {
			venuePositions[key] += order.FilledQty
		} else {
			venuePositions[key] -= order.FilledQty
		}
	}

	// Convert to map keyed by OrderIdentifier (using representative identifier per venue)
	result := make(map[recomma.OrderIdentifier]float64)
	for key, netQty := range venuePositions {
		result[venueIdentifiers[key]] = netQty
	}

	return result
}

func (s *Service) cancelTakeProfitBySnapshot(
	ctx context.Context,
	submitter recomma.Emitter,
	snapshot DealSnapshot,
	tp OrderSnapshot,
) {
	ident := tp.Identifier
	oid := tp.OrderId
	cancel := hyperliquid.CancelOrderRequestByCloid{
		Coin:  snapshot.Currency,
		Cloid: oid.Hex(),
	}

	work := recomma.OrderWork{
		Identifier: ident,
		OrderId:    oid,
		Action: recomma.Action{
			Type:   recomma.ActionCancel,
			Cancel: cancel,
			Reason: "position closed; cancel stale take profit",
		},
	}

	if s.emitOrderWork(ctx, submitter, work, "cancelled take profit for flat position", snapshot, tp.RemainingQty) {
		s.markOrderCancelled(ident)
	}
}

func (s *Service) reconcileActiveTakeProfitBySnapshot(
	ctx context.Context,
	submitter recomma.Emitter,
	snapshot DealSnapshot,
	tp OrderSnapshot,
	desiredQty float64,
) bool {
	if tp.ReduceOnly && floatsEqual(tp.RemainingQty, desiredQty) {
		return true
	}

	ident := tp.Identifier
	oid := tp.OrderId
	evt := cloneEvent(tp.Event)
	if evt == nil {
		evt = cloneEvent(snapshot.LastTakeProfitEvent)
	}

	if evt != nil {
		modify := adapter.ToModifyOrderRequest(snapshot.Currency, recomma.BotEvent{BotEvent: *evt}, oid)
		modify.Order.Size = desiredQty
		modify.Order.ReduceOnly = true

		if s.shouldSkipSubmission(ctx, ident, modify.Order.Size, modify.Order.ReduceOnly) {
			s.logger.Debug("skip take profit modify: already submitted", slog.Uint64("deal_id", uint64(snapshot.DealID)), slog.String("cloid", oid.Hex()))
			return true
		}

		work := recomma.OrderWork{
			Identifier: ident,
			OrderId:    oid,
			Action: recomma.Action{
				Type:   recomma.ActionModify,
				Modify: modify,
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
		Identifier: ident,
		OrderId:    oid,
		Action: recomma.Action{
			Type:   recomma.ActionCancel,
			Cancel: cancel,
			Reason: "stale take profit metadata; cancel before recreation",
		},
	}
	if !s.emitOrderWork(ctx, submitter, cancelWork, "cancelled take profit", snapshot, tp.RemainingQty) {
		return true
	}

	s.markOrderCancelled(ident)
	s.ensureTakeProfit(ctx, submitter, snapshot, desiredQty, &ident, snapshot.LastTakeProfitEvent, true)
	return true
}

func (s *Service) markOrderCancelled(ident recomma.OrderIdentifier) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, ok := s.orders[ident]
	if !ok {
		return
	}

	now := time.Now().UTC()
	state.remainingQty = 0
	if state.originalQty > 0 && state.filledQty < state.originalQty {
		state.filledQty = state.originalQty
	}
	state.status = hyperliquid.OrderStatusValueCanceled
	state.statusObserved = now
	state.lastUpdate = now

	if deal, ok := s.deals[ident.OrderId.DealID]; ok {
		deal.orders[ident] = state
		deal.lastUpdate = now
		deal.recompute()
	}
}

func (s *Service) ensureTakeProfit(
	ctx context.Context,
	submitter recomma.Emitter,
	snapshot DealSnapshot,
	desiredQty float64,
	preferredIdent *recomma.OrderIdentifier,
	preferredEvent *tc.BotEvent,
	force bool,
) {
	ident, evt, ok := s.lookupTakeProfitContext(ctx, snapshot, preferredIdent, preferredEvent)
	if !ok {
		s.logger.Warn("take profit reconciliation skipped: no metadata", slog.Uint64("deal_id", uint64(snapshot.DealID)))
		return
	}

	oid := ident.OrderId
	create := adapter.ToCreateOrderRequest(snapshot.Currency, recomma.BotEvent{BotEvent: *evt}, oid)
	create.Size = desiredQty
	create.ReduceOnly = true

	if !force && s.shouldSkipSubmission(ctx, ident, create.Size, create.ReduceOnly) {
		s.logger.Debug("skip take profit create: already submitted", slog.Uint64("deal_id", uint64(snapshot.DealID)), slog.String("cloid", oid.Hex()))
		return
	}

	work := recomma.OrderWork{
		Identifier: ident,
		OrderId:    oid,
		Action: recomma.Action{
			Type:   recomma.ActionCreate,
			Create: create,
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
	if work.Identifier == (recomma.OrderIdentifier{}) {
		s.logger.Warn("order work missing identifier", slog.Uint64("deal_id", uint64(snapshot.DealID)), slog.Uint64("bot_id", uint64(snapshot.BotID)), slog.String("cloid", work.OrderId.Hex()))
		return false
	}

	emitCtx, cancelFn := context.WithTimeout(ctx, 30*time.Second)
	err := submitter.Emit(emitCtx, work)
	cancelFn()

	fields := []any{
		slog.Uint64("deal_id", uint64(snapshot.DealID)),
		slog.Uint64("bot_id", uint64(snapshot.BotID)),
		slog.String("venue", work.Identifier.Venue()),
		slog.String("wallet", work.Identifier.Wallet),
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

func (s *Service) shouldSkipSubmission(ctx context.Context, ident recomma.OrderIdentifier, desiredSize float64, requireReduceOnly bool) bool {
	action, found, err := s.store.LoadHyperliquidSubmission(ctx, ident)
	if err != nil {
		s.logger.Warn("load hyperliquid submission failed", slog.String("cloid", ident.Hex()), slog.String("error", err.Error()))
		return false
	}
	if !found {
		return false
	}

	if !submissionMatchesDesired(action, desiredSize, requireReduceOnly) {
		return false
	}

	status, haveStatus, err := s.store.LoadHyperliquidStatus(ctx, ident)
	if err != nil {
		s.logger.Warn("load hyperliquid status failed", slog.String("cloid", ident.Hex()), slog.String("error", err.Error()))
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
		if requireReduceOnly && !action.Create.ReduceOnly {
			return false
		}
		return floatsEqual(action.Create.Size, desiredSize)
	case recomma.ActionModify:
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

func isCanceledHyperliquidStatus(status hyperliquid.OrderStatusValue) bool {
	if status == hyperliquid.OrderStatusValueCanceled {
		return true
	}
	if status == "" {
		return false
	}
	lower := strings.ToLower(string(status))
	return strings.HasSuffix(lower, "canceled") || strings.HasSuffix(lower, "cancelled")
}

func (s *Service) lookupTakeProfitContext(
	ctx context.Context,
	snapshot DealSnapshot,
	preferredIdent *recomma.OrderIdentifier,
	preferredEvent *tc.BotEvent,
) (recomma.OrderIdentifier, *tc.BotEvent, bool) {
	var ident *recomma.OrderIdentifier
	if preferredIdent != nil {
		clone := *preferredIdent
		ident = &clone
	}

	evt := cloneEvent(preferredEvent)
	if evt == nil {
		evt = cloneEvent(snapshot.LastTakeProfitEvent)
	}

	if ident == nil && evt != nil {
		for _, order := range snapshot.Orders {
			if !order.ReduceOnly || order.Event == nil {
				continue
			}
			if eventsMatch(order.Event, evt) {
				clone := order.Identifier
				ident = &clone
				break
			}
		}
	}

	needLoadIdent := ident == nil
	needLoadEvent := evt == nil
	if needLoadIdent || needLoadEvent {
		storedOrderId, storedEvt, err := s.store.LoadTakeProfitForDeal(ctx, snapshot.DealID)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				s.logger.Warn("load take profit metadata failed", slog.Uint64("deal_id", uint64(snapshot.DealID)), slog.String("error", err.Error()))
			}
			return recomma.OrderIdentifier{}, nil, false
		}
		if needLoadIdent {
			storedIdents, err := s.store.ListSubmissionIdentifiersForOrder(ctx, *storedOrderId)
			if err != nil {
				s.logger.Warn("load submission identifiers", slog.Uint64("deal_id", uint64(snapshot.DealID)), slog.String("error", err.Error()))
			}
			if len(storedIdents) > 0 {
				clone := storedIdents[0]
				ident = &clone
			} else {
				s.logger.Warn("take profit reconciliation missing submission identifier", slog.Uint64("deal_id", uint64(snapshot.DealID)), slog.String("orderid", storedOrderId.Hex()))
				return recomma.OrderIdentifier{}, nil, false
			}
		}
		if needLoadEvent {
			evt = cloneEvent(storedEvt)
		}
	}

	if ident == nil || evt == nil {
		return recomma.OrderIdentifier{}, nil, false
	}

	return *ident, evt, true
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

		identifiers, err := s.store.ListSubmissionIdentifiersForOrder(ctx, oid)
		if err != nil {
			s.logger.Warn("load submission identifiers", slog.Uint64("deal_id", uint64(oid.DealID)), slog.String("error", err.Error()))
		}
		if len(identifiers) == 0 {
			s.logger.Warn("reload deal missing submission identifiers", slog.Uint64("deal_id", uint64(oid.DealID)), slog.String("orderid", oid.Hex()))
			continue
		}

		scaledOrders, err := s.store.ListScaledOrdersByOrderId(ctx, oid)
		if err != nil {
			s.logger.Warn("load scaled orders", slog.Uint64("deal_id", uint64(oid.DealID)), slog.String("error", err.Error()))
		}

		for _, ident := range identifiers {
			status, found, err := s.store.LoadHyperliquidStatus(ctx, ident)
			if err != nil {
				return err
			}

			// Find the most recent scaled order that matches this identifier's venue
			var latestScaled *storage.ScaledOrderAudit
			if len(scaledOrders) > 0 {
				for i := len(scaledOrders) - 1; i >= 0; i-- {
					audit := &scaledOrders[i]
					// Match by venue_id and wallet
					if audit.VenueID == string(ident.VenueID) && audit.Wallet == ident.Wallet {
						copy := scaledOrders[i]
						latestScaled = &copy
						break
					}
				}
			}

			s.mu.Lock()
			state := s.ensureOrderLocked(ident)
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

			deal := s.ensureDealLocked(ident)
			deal.orders[ident] = state
			deal.lastUpdate = time.Now().UTC()
			deal.recompute()
			s.mu.Unlock()
		}
	}

	return nil
}

func (s *Service) ensureOrderLocked(ident recomma.OrderIdentifier) *orderState {
	state, ok := s.orders[ident]
	if !ok {
		state = &orderState{
			identifier: ident,
		}
		s.orders[ident] = state
	} else {
		state.identifier = ident
	}
	return state
}

func (s *Service) ensureDealLocked(ident recomma.OrderIdentifier) *dealState {
	deal, ok := s.deals[ident.OrderId.DealID]
	if !ok {
		deal = &dealState{
			botID:      ident.OrderId.BotID,
			dealID:     ident.OrderId.DealID,
			orders:     make(map[recomma.OrderIdentifier]*orderState),
			lastUpdate: time.Now().UTC(),
		}
		s.deals[ident.OrderId.DealID] = deal
	}
	if deal.botID == 0 {
		deal.botID = ident.OrderId.BotID
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

// CleanupStaleDealsstale removes completed/inactive deals from memory to prevent unbounded growth.
// A deal is considered stale if:
// - All orders are either filled or cancelled
// - No updates in the last staleDuration period
func (s *Service) CleanupStaleDeals(staleDuration time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	staleDeals := make([]uint32, 0)

	for dealID, deal := range s.deals {
		if now.Sub(deal.lastUpdate) < staleDuration {
			continue // Deal was recently updated
		}

		// Check if all orders are complete (filled or cancelled)
		allComplete := true
		for _, order := range deal.orders {
			if order.isActive() {
				allComplete = false
				break
			}
		}

		if allComplete {
			staleDeals = append(staleDeals, dealID)
		}
	}

	// Remove stale deals and their associated orders
	for _, dealID := range staleDeals {
		deal := s.deals[dealID]
		for ident := range deal.orders {
			delete(s.orders, ident)
		}
		delete(s.deals, dealID)
	}

	if len(staleDeals) > 0 {
		s.logger.Info("cleaned up stale deals", slog.Int("count", len(staleDeals)))
	}

	return len(staleDeals)
}

type orderState struct {
	identifier recomma.OrderIdentifier

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
	cancelled := isCanceledHyperliquidStatus(status.Status)

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

	var filled float64
	if o.originalQty > 0 {
		filled = math.Max(0, o.originalQty-o.remainingQty)
	}

	if cancelled {
		o.remainingQty = 0
	}

	if o.originalQty > 0 {
		o.filledQty = filled
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
	if isCanceledHyperliquidStatus(o.status) || o.status == hyperliquid.OrderStatusValueFilled {
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
		Identifier:     o.identifier,
		OrderId:        o.identifier.OrderId,
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
	orders   map[recomma.OrderIdentifier]*orderState

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
	var activeTPs []OrderSnapshot
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
				copy := snap
				activeTPs = append(activeTPs, copy)
			}
		}

		if state.reduceOnly && state.event != nil {
			if latestTPEvent == nil || state.event.CreatedAt.After(latestTPEvent.CreatedAt) {
				clone := *state.event
				latestTPEvent = &clone
			}
		}
	}

	out.ActiveTakeProfits = activeTPs
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

	ActiveTakeProfits   []OrderSnapshot
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
	Identifier recomma.OrderIdentifier
	OrderId    orderid.OrderId
	Coin       string

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
