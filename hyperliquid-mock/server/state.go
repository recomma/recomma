package server

import (
	"sync"
	"sync/atomic"
	"time"
)

// State manages the mock server's in-memory order state
type State struct {
	mu        sync.RWMutex
	orders    map[string]*OrderDetail // cloid -> OrderDetail
	nextOid   int64
}

// NewState creates a new state manager
func NewState() *State {
	return &State{
		orders:  make(map[string]*OrderDetail),
		nextOid: 1000000, // Start with a high OID to look realistic
	}
}

// CreateOrder adds a new order to the state
func (s *State) CreateOrder(cloid string, coin string, side string, limitPx string, sz string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	oid := atomic.AddInt64(&s.nextOid, 1)
	now := time.Now().UnixMilli()

	order := &OrderDetail{
		Order: OrderInfo{
			Coin:      coin,
			Side:      side,
			LimitPx:   limitPx,
			Sz:        sz,
			Oid:       oid,
			Timestamp: now,
			OrigSz:    sz,
			Cloid:     &cloid,
		},
		Status:          "open",
		StatusTimestamp: now,
	}

	s.orders[cloid] = order
	return oid
}

// ModifyOrder updates an existing order
func (s *State) ModifyOrder(cloid string, limitPx string, sz string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	order, exists := s.orders[cloid]
	if !exists {
		return 0, false
	}

	order.Order.LimitPx = limitPx
	order.Order.Sz = sz
	order.StatusTimestamp = time.Now().UnixMilli()

	return order.Order.Oid, true
}

// CancelOrder marks an order as canceled
func (s *State) CancelOrder(cloid string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	order, exists := s.orders[cloid]
	if !exists {
		return false
	}

	order.Status = "canceled"
	order.StatusTimestamp = time.Now().UnixMilli()

	return true
}

// GetOrder retrieves an order by cloid
func (s *State) GetOrder(cloid string) (*OrderDetail, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	order, exists := s.orders[cloid]
	if !exists {
		return nil, false
	}

	// Return a copy to avoid race conditions
	orderCopy := *order
	return &orderCopy, true
}

// GetOrderByOid retrieves an order by OID
func (s *State) GetOrderByOid(oid int64) (*OrderDetail, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, order := range s.orders {
		if order.Order.Oid == oid {
			orderCopy := *order
			return &orderCopy, true
		}
	}

	return nil, false
}
