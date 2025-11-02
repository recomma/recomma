package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/oapi-codegen/nullable"
	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/orderid"
	"github.com/recomma/recomma/recomma"
	hyperliquid "github.com/sonirico/go-hyperliquid"
	"github.com/stretchr/testify/require"
)

func newTestStorage(t *testing.T) *Storage {
	t.Helper()

	return newTestStorageWithLogger(t, nil)
}

func newTestStorageWithLogger(t *testing.T, logger *slog.Logger) *Storage {
	t.Helper()

	store, err := New(":memory:", WithLogger(logger))
	if err != nil {
		t.Fatalf("open sqlite storage: %v", err)
	}

	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("close sqlite storage: %v", err)
		}
	})

	return store
}

func TestStorageThreeCommasRoundTrip(t *testing.T) {
	base := time.Date(2024, time.January, 1, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name     string
		oid      orderid.OrderId
		botevent tc.BotEvent
	}{
		{
			name: "basic-roundtrip",
			oid: orderid.OrderId{
				BotID:      42,
				DealID:     7,
				BotEventID: 3,
			},
			botevent: tc.BotEvent{
				CreatedAt:        base,
				Action:           tc.BotEventActionExecute,
				Coin:             "DOGE",
				Type:             tc.MarketOrderOrderType(tc.BUY),
				Status:           tc.MarketOrderStatusString(tc.Filled),
				Price:            123.45,
				Size:             110.0,
				OrderType:        tc.MarketOrderDealOrderTypeTakeProfit,
				OrderSize:        9,
				OrderPosition:    8,
				QuoteVolume:      25.0654404,
				QuoteCurrency:    "USDT",
				IsMarket:         true,
				Profit:           5.67,
				ProfitCurrency:   "USDT",
				ProfitUSD:        5.67,
				ProfitPercentage: 2.5,
				Text:             "Averaging order (8 out of 9) executed. Price: market Size: 25.0654404 USDT (110.0 DOGE)",
			},
		},
		{
			name: "different-metadata",
			oid: orderid.OrderId{
				BotID:      99,
				DealID:     1001,
				BotEventID: 222,
			},
			botevent: tc.BotEvent{
				CreatedAt:        base,
				Action:           tc.BotEventActionExecute,
				Coin:             "DOGE",
				Type:             tc.MarketOrderOrderType(tc.BUY),
				Status:           tc.MarketOrderStatusString(tc.Filled),
				Price:            123.45,
				Size:             110.0,
				OrderType:        tc.MarketOrderDealOrderTypeTakeProfit,
				OrderSize:        9,
				OrderPosition:    8,
				QuoteVolume:      25.0654404,
				QuoteCurrency:    "USDT",
				IsMarket:         true,
				Profit:           5.67,
				ProfitCurrency:   "USDT",
				ProfitUSD:        5.67,
				ProfitPercentage: 2.5,
				Text:             "Averaging order (8 out of 9) executed. Price: market Size: 25.0654404 USDT (110.0 DOGE)",
			},
		},
	}

	for _, tcases := range cases {
		t.Run(tcases.name, func(t *testing.T) {
			store := newTestStorage(t)
			ctx := context.Background()

			has, err := store.HasOrderId(ctx, tcases.oid)
			if err != nil {
				t.Fatalf("HasOrderId before insert: %v", err)
			}
			if has {
				t.Fatalf("expected HasOrderId to be false before insert")
			}

			inserted, err := store.RecordThreeCommasBotEvent(ctx, tcases.oid, tcases.botevent)
			if err != nil {
				t.Fatalf("RecordThreeCommasOrder: %v", err)
			}
			if inserted == 0 {
				t.Fatalf("RecordThreeCommasOrder: expected insert to be new")
			}

			has, err = store.HasOrderId(ctx, tcases.oid)
			if err != nil {
				t.Fatalf("HasOrderId after insert: %v", err)
			}
			if !has {
				t.Fatalf("expected HasOrderId to be true after insert")
			}

			botevent, err := store.LoadThreeCommasBotEvent(ctx, tcases.oid)
			if err != nil {
				t.Fatalf("LoadThreeCommas: %v", err)
			}
			if botevent == nil {
				t.Fatalf("expected LoadThreeCommas to return payload")
			}

			if diff := cmp.Diff(tcases.botevent, *botevent); diff != "" {
				t.Fatalf("mismatch after roundtrip (-want +got):\n%s", diff)
			}

			// ensure the same OrderId can be queried again
			botevent, err = store.LoadThreeCommasBotEvent(ctx, tcases.oid)
			if err != nil || botevent == nil {
				t.Fatalf("LoadThreeCommas second read failed: %v", err)
			}
		})
	}
}

func TestStorageHyperliquidRoundTrip(t *testing.T) {
	store := newTestStorage(t)
	ctx := context.Background()

	oid := orderid.OrderId{
		BotID:      7,
		DealID:     77,
		BotEventID: 777,
	}

	cloid := oid.Hex()
	req1 := hyperliquid.CreateOrderRequest{
		Coin:          "ETH",
		IsBuy:         true,
		Price:         2425.75,
		Size:          1.5,
		ReduceOnly:    false,
		OrderType:     hyperliquid.OrderType{Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc}},
		ClientOrderID: oid.HexAsPointer(),
	}

	action, found, err := store.LoadHyperliquidSubmission(ctx, DefaultHyperliquidIdentifier(oid))
	if err != nil {
		t.Fatalf("LoadHyperliquidSubmission empty: %v", err)
	}
	if found {
		t.Fatalf("expected no submission for fresh orderid")
	}
	if action.Type != recomma.ActionNone {
		t.Fatalf("expected ActionNone for empty submission, got %v", action.Type)
	}
	if action.Create != nil || action.Modify != nil || action.Cancel != nil {
		t.Fatalf("expected all payloads nil for empty submission, got %#v", action)
	}

	// we are not testing events here, so we just set a fake event row id
	if err := store.RecordHyperliquidOrderRequest(ctx, DefaultHyperliquidIdentifier(oid), req1, 123456789); err != nil {
		t.Fatalf("RecordHyperliquidOrderRequest: %v", err)
	}

	action, found, err = store.LoadHyperliquidSubmission(ctx, DefaultHyperliquidIdentifier(oid))
	if err != nil {
		t.Fatalf("LoadHyperliquidSubmission after create: %v", err)
	}
	if !found {
		t.Fatalf("expected submission after create")
	}
	if action.Type != recomma.ActionCreate {
		t.Fatalf("expected ActionCreate, got %v", action.Type)
	}
	if action.Create == nil {
		t.Fatalf("expected create payload after create")
	}
	if diff := cmp.Diff(req1, *action.Create); diff != "" {
		t.Fatalf("create payload mismatch (-want +got):\n%s", diff)
	}
	if action.Modify != nil || action.Cancel != nil {
		t.Fatalf("unexpected extra payloads after create")
	}

	modify1 := hyperliquid.ModifyOrderRequest{
		Oid: cloid,
		Order: hyperliquid.CreateOrderRequest{
			Coin:          req1.Coin,
			IsBuy:         req1.IsBuy,
			Price:         2430.10,
			Size:          1.25,
			ReduceOnly:    req1.ReduceOnly,
			OrderType:     req1.OrderType,
			ClientOrderID: oid.HexAsPointer(),
		},
	}

	if err := store.AppendHyperliquidModify(ctx, DefaultHyperliquidIdentifier(oid), modify1, 123456789); err != nil {
		t.Fatalf("AppendHyperliquidModify first: %v", err)
	}

	action, found, err = store.LoadHyperliquidSubmission(ctx, DefaultHyperliquidIdentifier(oid))
	if err != nil {
		t.Fatalf("LoadHyperliquidSubmission after first modify: %v", err)
	}
	if !found {
		t.Fatalf("expected submission after first modify")
	}
	if action.Type != recomma.ActionModify {
		t.Fatalf("expected ActionModify after first modify, got %v", action.Type)
	}
	if action.Modify == nil {
		t.Fatalf("expected modify payload after first modify")
	}

	normalize := func(d hyperliquid.ModifyOrderRequest) hyperliquid.ModifyOrderRequest {
		var out hyperliquid.ModifyOrderRequest
		raw, err := json.Marshal(d)
		if err != nil {
			t.Fatalf("marshal deal: %v", err)
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("unmarshal deal: %v", err)
		}
		return out
	}

	normalized := normalize(modify1)

	if diff := cmp.Diff(normalized, *action.Modify); diff != "" {
		t.Fatalf("modify payload mismatch (-want +got):\n%s", diff)
	}
	if action.Create == nil {
		t.Fatalf("expected create payload to remain after modify")
	}

	modify2 := modify1
	modify2.Order.Price = 2435.55
	modify2.Order.Size = 1.1

	if err := store.AppendHyperliquidModify(ctx, DefaultHyperliquidIdentifier(oid), modify2, 123456789); err != nil {
		t.Fatalf("AppendHyperliquidModify second: %v", err)
	}

	action, found, err = store.LoadHyperliquidSubmission(ctx, DefaultHyperliquidIdentifier(oid))
	if err != nil {
		t.Fatalf("LoadHyperliquidSubmission after second modify: %v", err)
	}
	if !found {
		t.Fatalf("expected submission after second modify")
	}
	if action.Type != recomma.ActionModify {
		t.Fatalf("expected ActionModify after second modify, got %v", action.Type)
	}
	if action.Modify == nil {
		t.Fatalf("expected modify payload after second modify")
	}

	normalized2 := normalize(modify2)

	if diff := cmp.Diff(normalized2, *action.Modify); diff != "" {
		t.Fatalf("latest modify payload mismatch (-want +got):\n%s", diff)
	}

	cancelReq := hyperliquid.CancelOrderRequestByCloid{Coin: req1.Coin, Cloid: cloid}
	if err := store.RecordHyperliquidCancel(ctx, DefaultHyperliquidIdentifier(oid), cancelReq, 123456789); err != nil {
		t.Fatalf("RecordHyperliquidCancel: %v", err)
	}

	action, found, err = store.LoadHyperliquidSubmission(ctx, DefaultHyperliquidIdentifier(oid))
	if err != nil {
		t.Fatalf("LoadHyperliquidSubmission after cancel: %v", err)
	}
	if !found {
		t.Fatalf("expected submission after cancel")
	}
	if action.Type != recomma.ActionCancel {
		t.Fatalf("expected ActionCancel after cancel, got %v", action.Type)
	}
	if action.Cancel == nil {
		t.Fatalf("expected cancel payload after cancel")
	}
	if diff := cmp.Diff(cancelReq, *action.Cancel); diff != "" {
		t.Fatalf("cancel payload mismatch (-want +got):\n%s", diff)
	}
	if action.Modify == nil {
		t.Fatalf("expected last modify to remain available after cancel")
	}
	if diff := cmp.Diff(normalize(modify2), *action.Modify); diff != "" {
		t.Fatalf("modify payload changed after cancel (-want +got):\n%s", diff)
	}
	if action.Create == nil {
		t.Fatalf("expected create payload to persist after cancel")
	}
	if diff := cmp.Diff(req1, *action.Create); diff != "" {
		t.Fatalf("create payload changed after cancel (-want +got):\n%s", diff)
	}

	reqOnly, foundReq, err := store.LoadHyperliquidRequest(ctx, DefaultHyperliquidIdentifier(oid))
	if err != nil {
		t.Fatalf("LoadHyperliquidRequest helper: %v", err)
	}
	if !foundReq || reqOnly == nil {
		t.Fatalf("expected helper to return create payload")
	}
	if diff := cmp.Diff(req1, *reqOnly); diff != "" {
		t.Fatalf("helper create payload mismatch (-want +got):\n%s", diff)
	}

	statusOnly, foundStatus, err := store.LoadHyperliquidStatus(ctx, DefaultHyperliquidIdentifier(oid))
	if err != nil {
		t.Fatalf("LoadHyperliquidStatus before websocket insert: %v", err)
	}
	if foundStatus || statusOnly != nil {
		t.Fatalf("expected no websocket status before inserts")
	}

	status1 := hyperliquid.WsOrder{
		Order: hyperliquid.WsBasicOrder{
			Coin:      req1.Coin,
			Side:      "B",
			LimitPx:   "2425.75",
			Sz:        "1.500",
			Oid:       90001,
			Timestamp: 1700000000,
			OrigSz:    "1.500",
			Cloid:     &cloid,
		},
		Status:          "live",
		StatusTimestamp: 1700000050,
	}

	if err := store.RecordHyperliquidStatus(ctx, DefaultHyperliquidIdentifier(oid), status1); err != nil {
		t.Fatalf("RecordHyperliquidStatus first: %v", err)
	}

	statusOnly, foundStatus, err = store.LoadHyperliquidStatus(ctx, DefaultHyperliquidIdentifier(oid))
	if err != nil {
		t.Fatalf("LoadHyperliquidStatus after first insert: %v", err)
	}
	if !foundStatus || statusOnly == nil {
		t.Fatalf("expected websocket status after first insert")
	}
	if diff := cmp.Diff(status1, *statusOnly); diff != "" {
		t.Fatalf("websocket status mismatch (-want +got):\n%s", diff)
	}

	status2 := hyperliquid.WsOrder{
		Order: hyperliquid.WsBasicOrder{
			Coin:      req1.Coin,
			Side:      "B",
			LimitPx:   "2435.55",
			Sz:        "1.100",
			Oid:       90001,
			Timestamp: 1700000300,
			OrigSz:    "1.500",
			Cloid:     &cloid,
		},
		Status:          "canceled",
		StatusTimestamp: 1700000350,
	}

	if err := store.RecordHyperliquidStatus(ctx, DefaultHyperliquidIdentifier(oid), status2); err != nil {
		t.Fatalf("RecordHyperliquidStatus second: %v", err)
	}

	statusOnly, foundStatus, err = store.LoadHyperliquidStatus(ctx, DefaultHyperliquidIdentifier(oid))
	if err != nil {
		t.Fatalf("LoadHyperliquidStatus after second insert: %v", err)
	}
	if !foundStatus || statusOnly == nil {
		t.Fatalf("expected websocket status after second insert")
	}
	if diff := cmp.Diff(status2, *statusOnly); diff != "" {
		t.Fatalf("latest websocket status mismatch (-want +got):\n%s", diff)
	}

	statuses, err := store.ListHyperliquidStatuses(ctx, DefaultHyperliquidIdentifier(oid))
	if err != nil {
		t.Fatalf("ListHyperliquidStatuses: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("expected 2 websocket statuses, got %d", len(statuses))
	}
	if diff := cmp.Diff(status1, statuses[0]); diff != "" {
		t.Fatalf("first websocket status mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(status2, statuses[1]); diff != "" {
		t.Fatalf("second websocket status mismatch (-want +got):\n%s", diff)
	}

	action, found, err = store.LoadHyperliquidSubmission(ctx, DefaultHyperliquidIdentifier(oid))
	if err != nil {
		t.Fatalf("LoadHyperliquidSubmission final: %v", err)
	}
	if !found {
		t.Fatalf("expected submission to persist at end")
	}
	if action.Type != recomma.ActionCancel {
		t.Fatalf("expected ActionCancel at end, got %v", action.Type)
	}
	if diff := cmp.Diff(cancelReq, *action.Cancel); diff != "" {
		t.Fatalf("cancel payload changed at end (-want +got):\n%s", diff)
	}
}

func TestStorageListHyperliquidOrderIds(t *testing.T) {
	store := newTestStorage(t)
	ctx := context.Background()

	oid1 := orderid.OrderId{BotID: 1, DealID: 2, BotEventID: 3}
	oid2 := orderid.OrderId{BotID: 4, DealID: 5, BotEventID: 6}

	req := hyperliquid.CreateOrderRequest{
		Coin:          "ETH",
		IsBuy:         true,
		Price:         100,
		Size:          1,
		OrderType:     hyperliquid.OrderType{Limit: &hyperliquid.LimitOrderType{Tif: hyperliquid.TifGtc}},
		ClientOrderID: oid1.HexAsPointer(),
	}

	require.NoError(t, store.RecordHyperliquidOrderRequest(ctx, DefaultHyperliquidIdentifier(oid1), req, 0))

	req2 := req
	req2.ClientOrderID = oid2.HexAsPointer()
	require.NoError(t, store.RecordHyperliquidOrderRequest(ctx, DefaultHyperliquidIdentifier(oid2), req2, 0))

	// Re-insert oid1 with modify to ensure we don't duplicate entries.
	modify := hyperliquid.ModifyOrderRequest{
		Oid:   oid1.Hex(),
		Order: req,
	}
	require.NoError(t, store.AppendHyperliquidModify(ctx, DefaultHyperliquidIdentifier(oid1), modify, 0))

	list, err := store.ListHyperliquidOrderIds(ctx)
	require.NoError(t, err)

	require.Len(t, list, 2)

	hexes := []string{list[0].Hex(), list[1].Hex()}
	expected := []string{oid1.Hex(), oid2.Hex()}
	sort.Strings(hexes)
	sort.Strings(expected)
	require.Equal(t, expected, hexes)

	for _, ident := range list {
		require.Equal(t, string(defaultHyperliquidVenueID), ident.Venue())
		require.Equal(t, defaultHyperliquidWallet, ident.Wallet)
	}
}

func ptr[T any](v T) *T {
	return &v
}

func TestStorageBotOperations(t *testing.T) {
	createdA := time.Date(2024, time.April, 10, 7, 45, 0, 0, time.UTC)
	updatedA := createdA.Add(5 * time.Minute)
	createdB := time.Date(2023, time.December, 5, 15, 30, 0, 0, time.UTC)
	updatedB := createdB.Add(45 * time.Minute)

	syncA := time.Date(2024, time.April, 10, 9, 15, 0, 0, time.FixedZone("UTC+2", 2*60*60))
	touchA := time.Date(2024, time.April, 10, 5, 30, 0, 0, time.FixedZone("UTC-4", -4*60*60))
	syncB := time.Date(2023, time.December, 5, 20, 0, 0, 0, time.UTC)
	upsertSyncB := time.Date(2023, time.December, 6, 8, 45, 0, 0, time.FixedZone("UTC+9", 9*60*60))
	touchB := time.Date(2023, time.December, 6, 9, 15, 0, 0, time.FixedZone("UTC-5", -5*60*60))

	botA := tc.Bot{
		AccountId:                   501,
		AccountName:                 "primary-account",
		ActiveDealsBtcProfit:        "0.0025",
		ActiveDealsCount:            1,
		ActiveDealsUsdProfit:        "120.00",
		ActiveSafetyOrdersCount:     ptr(1),
		AllowedDealsOnSamePair:      ptr(0),
		BaseOrderVolume:             ptr("100"),
		BaseOrderVolumeType:         ptr(tc.BotBaseOrderVolumeTypeQuoteCurrency),
		BtcFundsLockedInActiveDeals: "0.004",
		CloseDealsTimeout:           ptr("3600"),
		Cooldown:                    ptr("120"),
		CreatedAt:                   createdA,
		Deletable:                   true,
		FinishedDealsCount:          "3",
		FinishedDealsProfitUsd:      "98.5",
		FundsLockedInActiveDeals:    "500",
		Id:                          9001,
		IsEnabled:                   true,
		MaxActiveDeals:              ptr(2),
		Pairs:                       []string{"BTC_USDT"},
		ProfitCurrency:              ptr(tc.BotProfitCurrencyQuoteCurrency),
		ReinvestedVolumeUsd:         nullable.NewNullableWithValue(float32(12.25)),
		SafetyOrderVolumeType:       ptr(tc.BotSafetyOrderVolumeTypeQuoteCurrency),
		Strategy:                    ptr(tc.BotStrategyLong),
		UpdatedAt:                   updatedA,
	}

	botB := tc.Bot{
		AccountId:                   777,
		AccountName:                 "scout-bot",
		ActiveDealsBtcProfit:        "0.100",
		ActiveDealsCount:            0,
		ActiveDealsUsdProfit:        "0",
		ActiveSafetyOrdersCount:     ptr(3),
		BaseOrderVolume:             ptr("75"),
		BtcFundsLockedInActiveDeals: "0",
		CreatedAt:                   createdB,
		Deletable:                   false,
		FinishedDealsCount:          "6",
		FinishedDealsProfitUsd:      "340.0",
		FundsLockedInActiveDeals:    "1100",
		Id:                          42,
		IsEnabled:                   true,
		MaxActiveDeals:              ptr(4),
		MinProfitPercentage:         ptr("1.5"),
		Pairs:                       []string{"ETH_USDT", "SOL_USDT"},
		ProfitCurrency:              ptr(tc.BotProfitCurrencyBaseCurrency),
		ReinvestedVolumeUsd:         nullable.NewNullableWithValue(float32(3.75)),
		SafetyOrderVolumeType:       ptr(tc.BotSafetyOrderVolumeTypeBaseCurrency),
		StartOrderType:              ptr(tc.BotStartOrderTypeMarket),
		Strategy:                    ptr(tc.BotStrategyShort),
		UpdatedAt:                   updatedB,
	}

	botBUpdated := botB
	botBUpdated.AccountName = "scout-bot-v2"
	botBUpdated.ActiveDealsBtcProfit = "0.750"
	botBUpdated.ActiveDealsCount = 5
	botBUpdated.ActiveDealsUsdProfit = "480.00"
	botBUpdated.ActiveSafetyOrdersCount = ptr(5)
	botBUpdated.BaseOrderVolume = ptr("150")
	botBUpdated.BtcFundsLockedInActiveDeals = "0.025"
	botBUpdated.Cooldown = ptr("600")
	botBUpdated.Deletable = true
	botBUpdated.FinishedDealsCount = "10"
	botBUpdated.FinishedDealsProfitUsd = "1337.0"
	botBUpdated.FundsLockedInActiveDeals = "2500"
	botBUpdated.IsEnabled = false
	botBUpdated.MaxActiveDeals = ptr(6)
	botBUpdated.MinProfitPercentage = ptr("2.5")
	botBUpdated.Pairs = []string{"ETH_USDT"}
	botBUpdated.ProfitCurrency = ptr(tc.BotProfitCurrencyQuoteCurrency)
	botBUpdated.ReinvestedVolumeUsd = nullable.NewNullableWithValue(float32(10.5))
	botBUpdated.SafetyOrderVolumeType = ptr(tc.BotSafetyOrderVolumeTypeQuoteCurrency)
	botBUpdated.StartOrderType = ptr(tc.BotStartOrderTypeLimit)
	botBUpdated.Strategy = ptr(tc.BotStrategyLong)
	botBUpdated.UpdatedAt = botB.UpdatedAt.Add(3 * time.Hour)

	cases := []struct {
		name        string
		bot         tc.Bot
		initialSync time.Time
		upsert      *tc.Bot
		upsertSync  time.Time
		touchSync   time.Time
	}{
		{
			name:        "single-record-touch",
			bot:         botA,
			initialSync: syncA,
			touchSync:   touchA,
		},
		{
			name:        "upsert-replaces-payload",
			bot:         botB,
			initialSync: syncB,
			upsert:      &botBUpdated,
			upsertSync:  upsertSyncB,
			touchSync:   touchB,
		},
	}

	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			store := newTestStorage(t)
			ctx := context.Background()

			gotBot, gotSync, found, err := store.LoadBot(ctx, scenario.bot.Id)
			if err != nil {
				t.Fatalf("LoadBot before data: %v", err)
			}
			if found {
				t.Fatalf("expected LoadBot to report not found before record")
			}
			if gotBot != nil {
				t.Fatalf("expected nil bot before record, got %#v", gotBot)
			}
			if !gotSync.IsZero() {
				t.Fatalf("expected zero sync time before record, got %s", gotSync)
			}

			if err := store.RecordBot(ctx, scenario.bot, scenario.initialSync); err != nil {
				t.Fatalf("RecordBot initial: %v", err)
			}

			expected := scenario.bot

			gotBot, gotSync, found, err = store.LoadBot(ctx, scenario.bot.Id)
			if err != nil {
				t.Fatalf("LoadBot after initial record: %v", err)
			}
			if !found || gotBot == nil {
				t.Fatalf("expected bot to be found after initial record")
			}
			if diff := cmp.Diff(expected, *gotBot); diff != "" {
				t.Fatalf("bot mismatch after initial record (-want +got):\n%s", diff)
			}
			if want := scenario.initialSync.UTC(); !gotSync.Equal(want) {
				t.Fatalf("syncedAt mismatch after initial record: want %s got %s", want, gotSync)
			}

			if scenario.upsert != nil {
				if err := store.RecordBot(ctx, *scenario.upsert, scenario.upsertSync); err != nil {
					t.Fatalf("RecordBot upsert: %v", err)
				}
				expected = *scenario.upsert

				gotBot, gotSync, found, err = store.LoadBot(ctx, scenario.bot.Id)
				if err != nil {
					t.Fatalf("LoadBot after upsert: %v", err)
				}
				if !found || gotBot == nil {
					t.Fatalf("expected bot to be found after upsert")
				}
				if diff := cmp.Diff(expected, *gotBot); diff != "" {
					t.Fatalf("bot mismatch after upsert (-want +got):\n%s", diff)
				}
				if want := scenario.upsertSync.UTC(); !gotSync.Equal(want) {
					t.Fatalf("syncedAt mismatch after upsert: want %s got %s", want, gotSync)
				}
			}

			if !scenario.touchSync.IsZero() {
				if err := store.TouchBot(ctx, scenario.bot.Id, scenario.touchSync); err != nil {
					t.Fatalf("TouchBot: %v", err)
				}

				gotBot, gotSync, found, err = store.LoadBot(ctx, scenario.bot.Id)
				if err != nil {
					t.Fatalf("LoadBot after touch: %v", err)
				}
				if !found || gotBot == nil {
					t.Fatalf("expected bot to be found after touch")
				}
				if diff := cmp.Diff(expected, *gotBot); diff != "" {
					t.Fatalf("bot payload changed after touch (-want +got):\n%s", diff)
				}
				if want := scenario.touchSync.UTC(); !gotSync.Equal(want) {
					t.Fatalf("syncedAt mismatch after touch: want %s got %s", want, gotSync)
				}
			}
		})
	}
}

func TestStorageThreeCommasDealRoundTrip(t *testing.T) {
	store := newTestStorage(t)
	ctx := context.Background()

	base := time.Date(2024, time.April, 4, 12, 0, 0, 0, time.UTC)

	if _, found, err := store.LoadThreeCommasDeal(ctx, 101); err != nil {
		t.Fatalf("LoadThreeCommasDeal empty: %v", err)
	} else if found {
		t.Fatalf("expected no deal before insert")
	}

	deal := tc.Deal{
		Id:                          101,
		BotId:                       505,
		AccountId:                   12,
		AccountName:                 "demo-account",
		BotName:                     "scalper",
		Pair:                        "BTC_USDT",
		Status:                      tc.DealStatus("active"),
		Type:                        "deal",
		ActualProfitPercentage:      "0.00",
		FinalProfit:                 "15.5",
		FinalProfitPercentage:       "3.5",
		CreatedAt:                   base,
		UpdatedAt:                   base.Add(5 * time.Minute),
		TakeProfitPrice:             "75000",
		TrailingEnabled:             true,
		SafetyOrderVolumeType:       "quote_currency",
		MartingaleVolumeCoefficient: "1.5",
	}

	if err := store.RecordThreeCommasDeal(ctx, deal); err != nil {
		t.Fatalf("RecordThreeCommasDeal: %v", err)
	}

	got, found, err := store.LoadThreeCommasDeal(ctx, deal.Id)
	if err != nil {
		t.Fatalf("LoadThreeCommasDeal after insert: %v", err)
	}
	if !found {
		t.Fatalf("expected LoadThreeCommasDeal to find record")
	}

	normalize := func(d tc.Deal) tc.Deal {
		var out tc.Deal
		raw, err := json.Marshal(d)
		if err != nil {
			t.Fatalf("marshal deal: %v", err)
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("unmarshal deal: %v", err)
		}
		return out
	}

	want := normalize(deal)
	got2 := normalize(*got)
	if diff := cmp.Diff(want, got2); diff != "" {
		t.Fatalf("deal mismatch:\n%s", diff)
	}
}

func TestStorageThreeCommasDealUpsert(t *testing.T) {
	store := newTestStorage(t)
	ctx := context.Background()

	base := time.Date(2024, time.May, 5, 9, 30, 0, 0, time.UTC)

	original := tc.Deal{
		Id:                    202,
		BotId:                 777,
		BotName:               "initial",
		Pair:                  "ETH_USDT",
		Status:                tc.DealStatus("active"),
		Type:                  "deal",
		FinalProfit:           "5.0",
		FinalProfitPercentage: "1.0",
		CreatedAt:             base,
		UpdatedAt:             base,
	}

	updated := original
	updated.BotName = "updated-name"
	updated.FinalProfit = "7.5"
	updated.FinalProfitPercentage = "1.4"
	updated.UpdatedAt = base.Add(30 * time.Minute)

	if err := store.RecordThreeCommasDeal(ctx, original); err != nil {
		t.Fatalf("initial RecordThreeCommasDeal: %v", err)
	}
	if err := store.RecordThreeCommasDeal(ctx, updated); err != nil {
		t.Fatalf("updated RecordThreeCommasDeal: %v", err)
	}

	got, found, err := store.LoadThreeCommasDeal(ctx, updated.Id)
	if err != nil {
		t.Fatalf("LoadThreeCommasDeal after update: %v", err)
	}
	if !found {
		t.Fatalf("expected LoadThreeCommasDeal to find record")
	}

	normalize := func(d tc.Deal) tc.Deal {
		var out tc.Deal
		raw, err := json.Marshal(d)
		if err != nil {
			t.Fatalf("marshal deal: %v", err)
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("unmarshal deal: %v", err)
		}
		return out
	}

	want := normalize(updated)
	got2 := normalize(*got)
	if diff := cmp.Diff(want, got2); diff != "" {
		t.Fatalf("deal mismatch:\n%s", diff)
	}
}

func TestStorageListEventsForOrder(t *testing.T) {
	store := newTestStorage(t)
	ctx := context.Background()

	base := time.Date(2024, time.July, 18, 14, 30, 0, 0, time.UTC)
	botID := uint32(501)
	dealID := uint32(1601)
	botEventID := uint32(9)

	mk := func(offset time.Duration, price float64, label string) (orderid.OrderId, tc.BotEvent) {
		ts := base.Add(offset)
		return orderid.OrderId{
				BotID:      botID,
				DealID:     dealID,
				BotEventID: botEventID,
			}, tc.BotEvent{
				CreatedAt:        ts,
				Action:           tc.BotEventActionExecute,
				Coin:             "DOGE",
				Type:             tc.MarketOrderOrderType(tc.BUY),
				Status:           tc.MarketOrderStatusString(tc.Filled),
				Price:            price,
				Size:             110.0,
				OrderType:        tc.MarketOrderDealOrderTypeTakeProfit,
				OrderSize:        9,
				OrderPosition:    8,
				QuoteVolume:      25.0654404,
				QuoteCurrency:    "USDT",
				IsMarket:         true,
				Profit:           5.67,
				ProfitCurrency:   "USDT",
				ProfitUSD:        5.67,
				ProfitPercentage: 2.5,
				Text:             label,
			}
	}

	oid1, evt1 := mk(0, 123.00, "initial revision")
	oid2, evt2 := mk(10*time.Minute, 123.45, "second revision")
	oid3, evt3 := mk(25*time.Minute, 124.10, "final revision")

	for _, rec := range []struct {
		oid orderid.OrderId
		evt tc.BotEvent
	}{
		{oid: oid2, evt: evt2},
		{oid: oid1, evt: evt1},
		{oid: oid3, evt: evt3},
	} {
		inserted, err := store.RecordThreeCommasBotEvent(ctx, rec.oid, rec.evt)
		if err != nil {
			t.Fatalf("RecordThreeCommasBotEvent %q: %v", rec.evt.Text, err)
		}
		if inserted == 0 {
			t.Fatalf("RecordThreeCommasBotEvent %q: expected insert to be new", rec.evt.Text)
		}
	}

	otherOid := orderid.OrderId{
		BotID:      botID,
		DealID:     dealID + 1,
		BotEventID: botEventID,
	}
	otherEvt := tc.BotEvent{
		CreatedAt: base,
		Action:    tc.BotEventActionExecute,
		Coin:      "DOGE",
	}
	if inserted, err := store.RecordThreeCommasBotEvent(ctx, otherOid, otherEvt); err != nil {
		t.Fatalf("RecordThreeCommasBotEvent (other): %v", err)
	} else if inserted == 0 {
		t.Fatalf("RecordThreeCommasBotEvent (other): expected insert to be new")
	}

	events, err := store.ListEventsForOrder(ctx, botID, dealID, botEventID)
	if err != nil {
		t.Fatalf("ListEventsForOrder: %v", err)
	}

	want := []tc.BotEvent{evt1, evt2, evt3}
	if diff := cmp.Diff(want, recomma.ToThreeCommasBotEvent(events)); diff != "" {
		t.Fatalf("events mismatch (-want +got):\n%s", diff)
	}
}

func TestRecordThreeCommasBotEventDuplicateReturnsPreviousInsertID(t *testing.T) {
	store := newTestStorage(t)
	ctx := context.Background()

	base := time.Date(2024, time.February, 2, 9, 30, 0, 0, time.UTC)
	oid := orderid.OrderId{
		BotID:      123,
		DealID:     456,
		BotEventID: 789,
	}
	event := tc.BotEvent{
		CreatedAt:     base,
		Action:        tc.BotEventActionPlace,
		Coin:          "DOGE",
		Type:          tc.BUY,
		Status:        "Active",
		Price:         0.25,
		Size:          10,
		OrderType:     tc.MarketOrderDealOrderTypeBase,
		OrderSize:     1,
		OrderPosition: 0,
		QuoteVolume:   2.5,
		QuoteCurrency: "USDT",
		IsMarket:      false,
		Text:          "test duplicate insert",
	}

	first, err := store.RecordThreeCommasBotEvent(ctx, oid, event)
	require.NoError(t, err, "first insert failed")
	t.Logf("first id: %d", first)
	require.NotZero(t, first, "first insert unexpectedly returned zero rowid")

	second, err := store.RecordThreeCommasBotEvent(ctx, oid, event)
	require.ErrorIs(t, err, sql.ErrNoRows, "no rows expected")
	require.Zero(t, second, "second insert returned non-zero row id")
	require.NotEqual(t, first, second, "expected unique ids")
}

func TestLoadTakeProfitForDeal(t *testing.T) {
	store := newTestStorage(t)
	ctx := context.Background()

	t.Run("missing deal returns nil oid and event", func(t *testing.T) {
		oid, event, err := store.LoadTakeProfitForDeal(ctx, 999)
		require.Error(t, err)
		require.Nil(t, oid)
		require.Nil(t, event)
	})

	t.Run("returns latest take profit event", func(t *testing.T) {
		base := time.Date(2024, time.January, 15, 8, 0, 0, 0, time.UTC)
		oid := orderid.OrderId{
			BotID:      321,
			DealID:     654,
			BotEventID: 987,
		}
		evt := tc.BotEvent{
			CreatedAt:     base,
			Action:        tc.BotEventActionExecute,
			Coin:          "ETH",
			OrderType:     tc.MarketOrderDealOrderTypeTakeProfit,
			OrderSize:     1,
			OrderPosition: 0,
			Text:          "take profit execution",
		}

		inserted, err := store.RecordThreeCommasBotEvent(ctx, oid, evt)
		require.NoError(t, err)
		require.NotZero(t, inserted)

		gotOid, gotEvent, err := store.LoadTakeProfitForDeal(ctx, oid.DealID)
		require.NoError(t, err)
		require.NotNil(t, gotOid)
		require.NotNil(t, gotEvent)

		require.Equal(t, oid.BotID, gotOid.BotID)
		require.Equal(t, oid.DealID, gotOid.DealID)
		require.Equal(t, oid.BotEventID, gotOid.BotEventID)
		require.Equal(t, evt.Coin, gotEvent.Coin)
		require.Equal(t, evt.Action, gotEvent.Action)
		require.Equal(t, tc.MarketOrderDealOrderTypeTakeProfit, gotEvent.OrderType)
		require.NotNil(t, gotEvent.CreatedAt)
		require.WithinDuration(t, base, gotEvent.CreatedAt, 0)
	})
}
