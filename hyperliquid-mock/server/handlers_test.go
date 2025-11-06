package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHandleInfo_Meta tests the /info endpoint with type "meta"
func TestHandleInfo_Meta(t *testing.T) {
	handler := NewHandler()

	// Create request
	reqBody := InfoRequest{
		Type: "meta",
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/info", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	// Execute request
	handler.HandleInfo(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
		t.Logf("Response body: %s", w.Body.String())
		return
	}

	// Parse response
	var response Meta
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v\nBody: %s", err, w.Body.String())
	}

	// Validate response structure
	if len(response.Universe) == 0 {
		t.Error("Expected universe to have assets")
	}

	// Check for expected assets
	foundBTC := false
	foundETH := false
	for _, asset := range response.Universe {
		if asset.Name == "BTC" {
			foundBTC = true
			if asset.SzDecimals != 5 {
				t.Errorf("BTC: expected szDecimals=5, got %d", asset.SzDecimals)
			}
			if asset.MaxLeverage == 0 {
				t.Error("BTC: expected MaxLeverage > 0")
			}
		}
		if asset.Name == "ETH" {
			foundETH = true
			if asset.SzDecimals != 4 {
				t.Errorf("ETH: expected szDecimals=4, got %d", asset.SzDecimals)
			}
		}
	}

	if !foundBTC {
		t.Error("Expected to find BTC in universe")
	}
	if !foundETH {
		t.Error("Expected to find ETH in universe")
	}

	// Check margin tables
	if len(response.MarginTables) == 0 {
		t.Error("Expected margin tables to exist")
	}

	for _, mt := range response.MarginTables {
		if len(mt.MarginTiers) == 0 {
			t.Errorf("MarginTable %d: expected margin tiers", mt.ID)
		}
	}

	t.Logf("✓ Meta response: %d assets, %d margin tables", len(response.Universe), len(response.MarginTables))
}

// TestHandleInfo_SpotMeta tests the /info endpoint with type "spotMeta"
func TestHandleInfo_SpotMeta(t *testing.T) {
	handler := NewHandler()

	// Create request
	reqBody := InfoRequest{
		Type: "spotMeta",
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/info", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	// Execute request
	handler.HandleInfo(w, req)

	// Check response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
		t.Logf("Response body: %s", w.Body.String())
		return
	}

	// Parse response
	var response SpotMeta
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v\nBody: %s", err, w.Body.String())
	}

	// Validate response structure
	if len(response.Tokens) == 0 {
		t.Error("Expected tokens to exist")
	}

	if len(response.Universe) == 0 {
		t.Error("Expected universe to have trading pairs")
	}

	// Check for expected tokens
	foundUSDC := false
	for _, token := range response.Tokens {
		if token.Name == "USDC" {
			foundUSDC = true
			if token.SzDecimals != 6 {
				t.Errorf("USDC: expected szDecimals=6, got %d", token.SzDecimals)
			}
		}
	}

	if !foundUSDC {
		t.Error("Expected to find USDC in tokens")
	}

	t.Logf("✓ SpotMeta response: %d tokens, %d trading pairs", len(response.Tokens), len(response.Universe))
}

// TestHandleInfo_MetaAndAssetCtxs tests the existing metaAndAssetCtxs endpoint
func TestHandleInfo_MetaAndAssetCtxs(t *testing.T) {
	handler := NewHandler()

	reqBody := InfoRequest{
		Type: "metaAndAssetCtxs",
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/info", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleInfo(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
		t.Logf("Response body: %s", w.Body.String())
		return
	}

	var response MetaAndAssetCtxs
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v\nBody: %s", err, w.Body.String())
	}

	if len(response.Universe) == 0 {
		t.Error("Expected universe to have assets")
	}

	if len(response.AssetCtxs) == 0 {
		t.Error("Expected assetCtxs to exist")
	}

	t.Logf("✓ MetaAndAssetCtxs response: %d assets, %d contexts", len(response.Universe), len(response.AssetCtxs))
}

// TestHandleInfo_UnknownType tests that unknown info types return 400
func TestHandleInfo_UnknownType(t *testing.T) {
	handler := NewHandler()

	reqBody := InfoRequest{
		Type: "nonExistentType",
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/info", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.HandleInfo(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for unknown type, got %d", w.Code)
	}
}
