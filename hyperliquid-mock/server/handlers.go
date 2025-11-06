package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"
)

// Handler manages HTTP requests for the mock server
type Handler struct {
	state *State
}

// NewHandler creates a new request handler
func NewHandler() *Handler {
	return &Handler{
		state: NewState(),
	}
}

// HandleExchange handles POST /exchange requests
func (h *Handler) HandleExchange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ExchangeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("Failed to decode exchange request: %v", err)
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	log.Printf("Exchange request: %+v", req)

	// Parse the action to determine the operation type
	actionMap, ok := req.Action.(map[string]interface{})
	if !ok {
		http.Error(w, "Invalid action format", http.StatusBadRequest)
		return
	}

	// Determine action type
	var response ExchangeResponse
	if order, ok := actionMap["type"].(string); ok {
		switch order {
		case "order":
			response = h.handleOrder(actionMap)
		case "cancel":
			response = h.handleCancel(actionMap)
		case "batchModify":
			response = h.handleBatchModify(actionMap)
		default:
			response = ExchangeResponse{Status: "ok", Response: &ExchangeActionData{Type: "default"}}
		}
	} else {
		// Try to detect action type from the structure
		if _, hasOrders := actionMap["orders"]; hasOrders {
			response = h.handleOrder(actionMap)
		} else if _, hasCancels := actionMap["cancels"]; hasCancels {
			response = h.handleCancel(actionMap)
		} else if _, hasModifies := actionMap["modifies"]; hasModifies {
			response = h.handleBatchModify(actionMap)
		} else {
			response = ExchangeResponse{Status: "ok", Response: &ExchangeActionData{Type: "default"}}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleOrder processes order creation/modification
func (h *Handler) handleOrder(action map[string]interface{}) ExchangeResponse {
	// Extract order details
	var statuses []OrderStatusResponse

	// Check if it's a single order or batch
	if orders, ok := action["orders"].([]interface{}); ok {
		for _, o := range orders {
			orderMap, _ := o.(map[string]interface{})
			status := h.processOrder(orderMap)
			statuses = append(statuses, status)
		}
	} else {
		// Single order
		status := h.processOrder(action)
		statuses = append(statuses, status)
	}

	return ExchangeResponse{
		Status: "ok",
		Response: &ExchangeActionData{
			Type: "order",
			Data: map[string]interface{}{
				"statuses": statuses,
			},
		},
	}
}

// processOrder creates or modifies a single order
func (h *Handler) processOrder(orderMap map[string]interface{}) OrderStatusResponse {
	coin, _ := orderMap["coin"].(string)
	isBuy, _ := orderMap["is_buy"].(bool)
	sz, _ := orderMap["sz"].(float64)
	limitPx, _ := orderMap["limit_px"].(float64)
	cloid, _ := orderMap["cloid"].(string)

	side := "B"
	if !isBuy {
		side = "A"
	}

	szStr := fmt.Sprintf("%.8g", sz)
	pxStr := fmt.Sprintf("%.8g", limitPx)

	// Check if this is a modification (cloid exists)
	if cloid != "" {
		if oid, ok := h.state.ModifyOrder(cloid, pxStr, szStr); ok {
			return OrderStatusResponse{
				Resting: &RestingStatus{Oid: oid},
			}
		}
	}

	// Create new order
	if cloid == "" {
		cloid = fmt.Sprintf("mock-%d", time.Now().UnixNano())
	}

	oid := h.state.CreateOrder(cloid, coin, side, pxStr, szStr)

	return OrderStatusResponse{
		Resting: &RestingStatus{Oid: oid},
	}
}

// handleCancel processes order cancellation
func (h *Handler) handleCancel(action map[string]interface{}) ExchangeResponse {
	var statuses []OrderStatusResponse

	if cancels, ok := action["cancels"].([]interface{}); ok {
		for _, c := range cancels {
			cancelMap, _ := c.(map[string]interface{})
			cloid, _ := cancelMap["cloid"].(string)

			if cloid != "" {
				h.state.CancelOrder(cloid)
				statuses = append(statuses, OrderStatusResponse{})
			}
		}
	} else {
		// Single cancel
		cloid, _ := action["cloid"].(string)
		if cloid != "" {
			h.state.CancelOrder(cloid)
			statuses = append(statuses, OrderStatusResponse{})
		}
	}

	return ExchangeResponse{
		Status: "ok",
		Response: &ExchangeActionData{
			Type: "default",
			Data: map[string]interface{}{
				"statuses": statuses,
			},
		},
	}
}

// handleBatchModify processes batch order modifications
func (h *Handler) handleBatchModify(action map[string]interface{}) ExchangeResponse {
	var statuses []OrderStatusResponse

	if modifies, ok := action["modifies"].([]interface{}); ok {
		for _, m := range modifies {
			modifyMap, _ := m.(map[string]interface{})
			orderMap, _ := modifyMap["order"].(map[string]interface{})
			cloid, _ := modifyMap["cloid"].(string)

			if cloid != "" && orderMap != nil {
				sz, _ := orderMap["sz"].(float64)
				limitPx, _ := orderMap["limit_px"].(float64)

				szStr := fmt.Sprintf("%.8g", sz)
				pxStr := fmt.Sprintf("%.8g", limitPx)

				if oid, ok := h.state.ModifyOrder(cloid, pxStr, szStr); ok {
					statuses = append(statuses, OrderStatusResponse{
						Resting: &RestingStatus{Oid: oid},
					})
				}
			}
		}
	}

	return ExchangeResponse{
		Status: "ok",
		Response: &ExchangeActionData{
			Type: "default",
			Data: map[string]interface{}{
				"statuses": statuses,
			},
		},
	}
}

// HandleInfo handles POST /info requests
func (h *Handler) HandleInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req InfoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("Failed to decode info request: %v", err)
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	log.Printf("Info request: %+v", req)

	var response interface{}

	switch req.Type {
	case "orderStatus":
		response = h.handleOrderStatus(req)
	case "metaAndAssetCtxs":
		response = h.handleMetaAndAssetCtxs()
	case "spotMetaAndAssetCtxs":
		response = h.handleSpotMetaAndAssetCtxs()
	case "meta":
		response = h.handleMeta()
	case "spotMeta":
		response = h.handleSpotMeta()
	default:
		http.Error(w, "Unknown info type", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	// Log the JSON response for debugging
	jsonBytes, _ := json.MarshalIndent(response, "", "  ")
	log.Printf("Sending %s response:\n%s", req.Type, string(jsonBytes))

	json.NewEncoder(w).Encode(response)
}

// handleOrderStatus queries order status by cloid or oid
func (h *Handler) handleOrderStatus(req InfoRequest) OrderQueryResult {
	var order *OrderDetail
	var exists bool

	if req.Oid != nil {
		order, exists = h.state.GetOrderByOid(*req.Oid)
	} else if req.User != "" {
		// In a real implementation, we'd filter by user
		// For the mock, we just return unknown
		return OrderQueryResult{Status: "unknown_cloid"}
	}

	if !exists || order == nil {
		return OrderQueryResult{Status: "unknown_cloid"}
	}

	return OrderQueryResult{
		Status: "success",
		Order:  order,
	}
}

// handleMetaAndAssetCtxs returns mock perpetual futures metadata
func (h *Handler) handleMetaAndAssetCtxs() MetaAndAssetCtxs {
	return MetaAndAssetCtxs{
		Universe: []MetaUniverse{
			{Name: "BTC", SzDecimals: 5},
			{Name: "ETH", SzDecimals: 4},
			{Name: "SOL", SzDecimals: 1},
			{Name: "ARB", SzDecimals: 0},
		},
		AssetCtxs: []AssetCtx{
			{
				Funding:      "0.0001",
				OpenInterest: "1000000",
				PrevDayPx:    "50000",
				DayNtlVlm:    "100000000",
				Premium:      "0.0005",
				OraclePx:     "50123.45",
				MarkPx:       "50125.00",
				MidPx:        "50124.00",
			},
			{
				Funding:      "0.00015",
				OpenInterest: "500000",
				PrevDayPx:    "3000",
				DayNtlVlm:    "50000000",
				Premium:      "0.0003",
				OraclePx:     "3012.34",
				MarkPx:       "3013.00",
				MidPx:        "3012.50",
			},
			{
				Funding:      "0.0002",
				OpenInterest: "100000",
				PrevDayPx:    "100",
				DayNtlVlm:    "10000000",
				Premium:      "0.0002",
				OraclePx:     "101.23",
				MarkPx:       "101.25",
				MidPx:        "101.24",
			},
			{
				Funding:      "0.0001",
				OpenInterest: "50000",
				PrevDayPx:    "1.5",
				DayNtlVlm:    "5000000",
				Premium:      "0.0001",
				OraclePx:     "1.51",
				MarkPx:       "1.52",
				MidPx:        "1.515",
			},
		},
	}
}

// handleSpotMetaAndAssetCtxs returns mock spot trading metadata
func (h *Handler) handleSpotMetaAndAssetCtxs() SpotMetaAndAssetCtxs {
	return SpotMetaAndAssetCtxs{
		Tokens: []SpotToken{
			{Name: "USDC", SzDecimals: 6, WeiDecimals: 6, Index: 0, TokenId: "0x1", IsCanonical: true},
			{Name: "BTC", SzDecimals: 8, WeiDecimals: 8, Index: 1, TokenId: "0x2", IsCanonical: true},
			{Name: "ETH", SzDecimals: 18, WeiDecimals: 18, Index: 2, TokenId: "0x3", IsCanonical: true},
		},
		Universe: []SpotUniverse{
			{Tokens: []int{1, 0}, Name: "BTC/USDC", Index: 0},
			{Tokens: []int{2, 0}, Name: "ETH/USDC", Index: 1},
		},
	}
}

// handleMeta returns mock perpetual futures metadata (simpler format than metaAndAssetCtxs)
func (h *Handler) handleMeta() Meta {
	return Meta{
		Universe: []AssetInfo{
			{Name: "BTC", SzDecimals: 5, MaxLeverage: 50, MarginTableId: 1},
			{Name: "ETH", SzDecimals: 4, MaxLeverage: 50, MarginTableId: 1},
			{Name: "SOL", SzDecimals: 1, MaxLeverage: 20, MarginTableId: 2},
			{Name: "ARB", SzDecimals: 0, MaxLeverage: 20, MarginTableId: 2},
		},
		// MarginTables is an array of tuples: [[id, {description, marginTiers}], ...]
		MarginTables: [][]interface{}{
			{1, map[string]interface{}{
				"description": "Standard",
				"marginTiers": []map[string]interface{}{
					{"lowerBound": "0.0", "maxLeverage": 50},
					{"lowerBound": "100000.0", "maxLeverage": 25},
					{"lowerBound": "500000.0", "maxLeverage": 10},
				},
			}},
			{2, map[string]interface{}{
				"description": "Alt Coins",
				"marginTiers": []map[string]interface{}{
					{"lowerBound": "0.0", "maxLeverage": 20},
					{"lowerBound": "50000.0", "maxLeverage": 10},
					{"lowerBound": "200000.0", "maxLeverage": 5},
				},
			}},
		},
	}
}

// handleSpotMeta returns mock spot trading metadata (same structure as spotMetaAndAssetCtxs)
func (h *Handler) handleSpotMeta() SpotMeta {
	return SpotMeta{
		Tokens: []SpotToken{
			{Name: "USDC", SzDecimals: 6, WeiDecimals: 6, Index: 0, TokenId: "0x1", IsCanonical: true},
			{Name: "BTC", SzDecimals: 8, WeiDecimals: 8, Index: 1, TokenId: "0x2", IsCanonical: true},
			{Name: "ETH", SzDecimals: 18, WeiDecimals: 18, Index: 2, TokenId: "0x3", IsCanonical: true},
		},
		Universe: []SpotUniverse{
			{Tokens: []int{1, 0}, Name: "BTC/USDC", Index: 0},
			{Tokens: []int{2, 0}, Name: "ETH/USDC", Index: 1},
		},
	}
}

// HandleHealth handles GET /health for health checks
func (h *Handler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "healthy",
		"time":   strconv.FormatInt(time.Now().Unix(), 10),
	})
}
