package storage

import (
	"context"
	"testing"
	"time"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/orderid"
	hyperliquid "github.com/sonirico/go-hyperliquid"
	"github.com/stretchr/testify/require"
)

func TestStorageListLatestHyperliquidSafetyStatuses(t *testing.T) {
	safetyType := string(tc.MarketOrderDealOrderTypeSafety)
	coin := "ETH"
	quote := "USDT"

	normalizeOrderId := func(t *testing.T, oid orderid.OrderId) orderid.OrderId {
		t.Helper()
		normalized, err := orderid.FromHexString(oid.Hex())
		require.NoError(t, err)
		return *normalized
	}

	// NB: orderSize is NOT the same as the size of the order! it is the Nth order for OrderPosition!!!!
	type order struct {
		position  int
		ordersize int
		volume    int
	}

	insertSafetyEvent := func(t *testing.T, store *Storage, oid orderid.OrderId, created time.Time, o order) orderid.OrderId {
		t.Helper()

		botevent := tc.BotEvent{
			CreatedAt:     created,
			Action:        tc.BotEventActionExecute,
			Coin:          coin,
			Type:          tc.BUY,
			Status:        tc.Filled,
			Price:         1000 + float64(o.position),
			Size:          float64(o.volume),
			OrderType:     tc.MarketOrderDealOrderTypeSafety,
			OrderSize:     o.ordersize,
			OrderPosition: o.position,
			QuoteVolume:   float64(o.volume),
			QuoteCurrency: quote,
			IsMarket:      true,
			Text:          "test safety order",
		}

		if _, err := store.RecordThreeCommasBotEvent(context.Background(), oid, botevent); err != nil {
			t.Fatalf("RecordThreeCommasBotEvent: %v", err)
		}

		return normalizeOrderId(t, oid)
	}

	recordStatus := func(t *testing.T, store *Storage, oid orderid.OrderId, status hyperliquid.WsOrder) {
		t.Helper()
		if err := store.RecordHyperliquidStatus(context.Background(), oid, status); err != nil {
			t.Fatalf("RecordHyperliquidStatus: %v", err)
		}
	}

	cases := []struct {
		name       string
		dealID     uint32
		setup      func(t *testing.T, store *Storage) []HyperliquidSafetyStatus
		wantFilled bool
		wantErr    string
	}{
		{
			name:   "all-safety-orders-filled",
			dealID: 9001,
			setup: func(t *testing.T, store *Storage) []HyperliquidSafetyStatus {
				base := time.Date(2024, time.March, 10, 15, 0, 0, 0, time.UTC)
				botID := uint32(42)

				oid1 := orderid.OrderId{
					BotID:      botID,
					DealID:     9001,
					BotEventID: 1,
				}
				oid2 := orderid.OrderId{
					BotID:      botID,
					DealID:     9001,
					BotEventID: 2,
				}

				normalized1 := insertSafetyEvent(t, store, oid1, base, order{
					position:  1,
					ordersize: 2,
					volume:    2,
				})
				normalized2 := insertSafetyEvent(t, store, oid2, base.Add(24*time.Hour), order{
					position:  2,
					ordersize: 2,
					volume:    1,
				})

				status1Live := hyperliquid.WsOrder{
					Order: hyperliquid.WsBasicOrder{
						Coin:      coin,
						Side:      "B",
						LimitPx:   "2500.0",
						Sz:        "2",
						Oid:       111,
						Timestamp: base.Add(10 * time.Second).UnixMilli(),
						OrigSz:    "2",
						Cloid:     oid1.HexAsPointer(),
					},
					Status:          hyperliquid.OrderStatusValueOpen,
					StatusTimestamp: base.Add(10 * time.Second).UnixMilli(),
				}
				status1Filled := status1Live
				status1Filled.Status = hyperliquid.OrderStatusValueFilled
				status1Filled.StatusTimestamp = base.Add(20 * time.Second).UnixMilli()

				recordStatus(t, store, oid1, status1Live)
				recordStatus(t, store, oid1, status1Filled)

				status2Live := hyperliquid.WsOrder{
					Order: hyperliquid.WsBasicOrder{
						Coin:      coin,
						Side:      "B",
						LimitPx:   "2500.0",
						Sz:        "1",
						Oid:       222,
						Timestamp: base.Add(12 * time.Second).UnixMilli(),
						OrigSz:    "1",
						Cloid:     oid2.HexAsPointer(),
					},
					Status:          hyperliquid.OrderStatusValueOpen,
					StatusTimestamp: base.Add(12 * time.Second).UnixMilli(),
				}
				status2Filled := status2Live
				status2Filled.Status = hyperliquid.OrderStatusValueFilled
				status2Filled.StatusTimestamp = base.Add(22 * time.Second).UnixMilli()

				recordStatus(t, store, oid2, status2Live)
				recordStatus(t, store, oid2, status2Filled)

				return []HyperliquidSafetyStatus{
					{
						OrderId:       normalized1,
						BotID:         normalized1.BotID,
						DealID:        normalized1.DealID,
						OrderType:     safetyType,
						OrderPosition: 1,
						OrderSize:     2,
						HLStatus:      hyperliquid.OrderStatusValueFilled,
						HLEventTime:   time.UnixMilli(status1Filled.StatusTimestamp).UTC(),
					},
					{
						OrderId:       normalized2,
						BotID:         normalized2.BotID,
						DealID:        normalized2.DealID,
						OrderType:     safetyType,
						OrderPosition: 2,
						OrderSize:     2,
						HLStatus:      hyperliquid.OrderStatusValueFilled,
						HLEventTime:   time.UnixMilli(status2Filled.StatusTimestamp).UTC(),
					},
				}
			},
			wantFilled: true,
		},
		{
			name:       "partial-fill-still-open",
			dealID:     9002,
			wantFilled: false,
			setup: func(t *testing.T, store *Storage) []HyperliquidSafetyStatus {
				base := time.Date(2024, time.March, 10, 16, 0, 0, 0, time.UTC)
				botID := uint32(43)

				oid1 := orderid.OrderId{
					BotID:      botID,
					DealID:     9002,
					BotEventID: 3,
				}
				oid2 := orderid.OrderId{
					BotID:      botID,
					DealID:     9002,
					BotEventID: 4,
				}

				normalized1 := insertSafetyEvent(t, store, oid1, base, order{
					position:  1,
					ordersize: 2,
					volume:    3,
				})
				normalized2 := insertSafetyEvent(t, store, oid2, base.Add(24*time.Hour), order{
					position:  2,
					ordersize: 2,
					volume:    4,
				})

				status1Live := hyperliquid.WsOrder{
					Order: hyperliquid.WsBasicOrder{
						Coin:      coin,
						Side:      "B",
						LimitPx:   "2550.0",
						Sz:        "3",
						Oid:       333,
						Timestamp: base.Add(8 * time.Second).UnixMilli(),
						OrigSz:    "3",
						Cloid:     oid1.HexAsPointer(),
					},
					Status:          hyperliquid.OrderStatusValueOpen,
					StatusTimestamp: base.Add(8 * time.Second).UnixMilli(),
				}
				status1Filled := status1Live
				status1Filled.Status = hyperliquid.OrderStatusValueFilled
				status1Filled.StatusTimestamp = base.Add(18 * time.Second).UnixMilli()

				recordStatus(t, store, oid1, status1Live)
				recordStatus(t, store, oid1, status1Filled)

				status2Live := hyperliquid.WsOrder{
					Order: hyperliquid.WsBasicOrder{
						Coin:      coin,
						Side:      "B",
						LimitPx:   "2550.0",
						Sz:        "4",
						Oid:       444,
						Timestamp: base.Add(9 * time.Second).UnixMilli(),
						OrigSz:    "4",
						Cloid:     oid2.HexAsPointer(),
					},
					Status:          hyperliquid.OrderStatusValueOpen,
					StatusTimestamp: base.Add(9 * time.Second).UnixMilli(),
				}

				recordStatus(t, store, oid2, status2Live)

				return []HyperliquidSafetyStatus{
					{
						OrderId:       normalized1,
						BotID:         normalized1.BotID,
						DealID:        normalized1.DealID,
						OrderType:     safetyType,
						OrderPosition: 1,
						OrderSize:     2,
						HLStatus:      hyperliquid.OrderStatusValueFilled,
						HLEventTime:   time.UnixMilli(status1Filled.StatusTimestamp).UTC(),
					},
					{
						OrderId:       normalized2,
						BotID:         normalized2.BotID,
						DealID:        normalized2.DealID,
						OrderType:     safetyType,
						OrderPosition: 2,
						OrderSize:     2,
						HLStatus:      hyperliquid.OrderStatusValueOpen,
						HLEventTime:   time.UnixMilli(status2Live.StatusTimestamp).UTC(),
					},
				}
			},
		},
		{
			name:       "no-safety-statuses",
			dealID:     9003,
			wantFilled: false,
			wantErr:    "no statuses available for deal 9003",
			setup: func(t *testing.T, store *Storage) []HyperliquidSafetyStatus {
				base := time.Date(2024, time.March, 10, 17, 0, 0, 0, time.UTC)

				otherDeal := uint32(9100)
				otherMD := orderid.OrderId{
					BotID:      50,
					DealID:     otherDeal,
					BotEventID: 1,
				}
				insertSafetyEvent(t, store, otherMD, base, order{
					position:  1,
					ordersize: 1,
					volume:    1,
				})
				recordStatus(t, store, otherMD, hyperliquid.WsOrder{
					Order: hyperliquid.WsBasicOrder{
						Coin:      coin,
						Side:      "B",
						LimitPx:   "2600.0",
						Sz:        "1",
						Oid:       500,
						Timestamp: base.Add(5 * time.Second).UnixMilli(),
						OrigSz:    "1",
						Cloid:     otherMD.HexAsPointer(),
					},
					Status:          hyperliquid.OrderStatusValueFilled,
					StatusTimestamp: base.Add(5 * time.Second).UnixMilli(),
				})

				oidTakeProfit := orderid.OrderId{
					BotID:      51,
					DealID:     9003,
					BotEventID: 2,
				}
				created := base
				takeProfit := tc.BotEvent{
					CreatedAt:     created,
					Action:        tc.BotEventActionExecute,
					Coin:          coin,
					Type:          tc.BUY,
					Status:        tc.Filled,
					Price:         2600.0,
					Size:          1.0,
					OrderType:     tc.MarketOrderDealOrderTypeTakeProfit,
					OrderSize:     1,
					OrderPosition: 1,
					QuoteVolume:   1.0,
					QuoteCurrency: quote,
					IsMarket:      true,
					Text:          "test take profit",
				}
				if _, err := store.RecordThreeCommasBotEvent(context.Background(), oidTakeProfit, takeProfit); err != nil {
					t.Fatalf("RecordThreeCommasBotEvent take profit: %v", err)
				}

				recordStatus(t, store, oidTakeProfit, hyperliquid.WsOrder{
					Order: hyperliquid.WsBasicOrder{
						Coin:      coin,
						Side:      "B",
						LimitPx:   "2600.0",
						Sz:        "1",
						Oid:       501,
						Timestamp: base.Add(6 * time.Second).UnixMilli(),
						OrigSz:    "1",
						Cloid:     oidTakeProfit.HexAsPointer(),
					},
					Status:          hyperliquid.OrderStatusValueFilled,
					StatusTimestamp: base.Add(6 * time.Second).UnixMilli(),
				})

				return []HyperliquidSafetyStatus{}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// logger := slog.Default()
			store := newTestStorageWithLogger(t, nil)
			want := tc.setup(t, store)

			got, err := store.ListLatestHyperliquidSafetyStatuses(context.Background(), tc.dealID)
			require.NoError(t, err)
			require.Equal(t, len(want), len(got), "unexpected number of safety statuses")

			for i := range want {
				require.Equal(t, want[i].OrderId, got[i].OrderId, "order id mismatch at index %d", i)
				require.Equal(t, want[i].BotID, got[i].BotID, "bot id mismatch at index %d", i)
				require.Equal(t, want[i].DealID, got[i].DealID, "deal id mismatch at index %d", i)
				require.Equal(t, want[i].OrderType, got[i].OrderType, "order type mismatch at index %d", i)
				require.Equal(t, want[i].OrderPosition, got[i].OrderPosition, "order position mismatch at index %d", i)
				// if diff := cmp.Diff(want, got); diff != "" {
				// 	t.Errorf("not equal\nwant: %v\ngot:  %v", want, got)
				// 	t.Fatalf("want:\n%+v\ngot:\n%+v", want, got)
				// }
				require.Equal(t, want[i].OrderSize, got[i].OrderSize, "order size mismatch at index %d", i)
				require.Equal(t, want[i].HLStatus, got[i].HLStatus, "hl status mismatch at index %d", i)
				require.Equal(t, want[i].HLEventTime, got[i].HLEventTime, "event time mismatch at index %d", i)
				require.False(t, got[i].HLStatusRecorded.IsZero(), "expected recorded time at index %d", i)
			}

			filled, err := store.DealSafetiesFilled(context.Background(), tc.dealID)
			if tc.wantFilled {
				require.NoError(t, err)
				require.True(t, filled, "expect deal safeties to be filled")
			} else {
				if tc.wantErr != "" {
					require.ErrorContains(t, err, tc.wantErr)
				}
				require.False(t, filled, "expect deal safeties to not be filled")
			}

		})
	}
}
