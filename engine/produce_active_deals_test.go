package engine

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/require"
	tc "github.com/terwey/3commas-sdk-go/threecommas"
)

// fakeQueue implements Queue by collecting added keys.
type fakeQueue struct{ added []WorkKey }

func (q *fakeQueue) Add(item WorkKey) { q.added = append(q.added, item) }

// fakeClient implements ThreeCommasAPI for ProduceActiveDeals tests.
type fakeClient struct {
	listBotsResp  []tc.Bot
	listBotsErr   error
	dealsByBot    map[int][]tc.Deal
	dealsErrByBot map[int]error
}

func (f *fakeClient) ListBots(ctx context.Context, _ ...tc.ListBotsParamsOption) ([]tc.Bot, error) {
	return f.listBotsResp, f.listBotsErr
}

func (f *fakeClient) GetListOfDeals(ctx context.Context, opts ...tc.ListDealsParamsOption) ([]tc.Deal, error) {
	// Parse the bot_id from options exactly like the SDK would.
	p := tc.ListDealsParamsFromOptions(opts...)
	var botID int
	if p.BotId != nil {
		botID = *p.BotId
	}
	if err := f.dealsErrByBot[botID]; err != nil {
		return nil, err
	}
	ds := f.dealsByBot[botID]
	out := make([]tc.Deal, len(ds))
	copy(out, ds)
	return out, nil
}

// Unused by ProduceActiveDeals; required by interface.
func (f *fakeClient) GetMarketOrdersForDeal(ctx context.Context, id tc.DealPathId) ([]tc.MarketOrder, error) {
	return nil, nil
}

// helper to compare WorkKey slices ignoring order
var sortWK = cmpopts.SortSlices(func(a, b WorkKey) bool {
	if a.BotID != b.BotID {
		return a.BotID < b.BotID
	}
	return a.DealID < b.DealID
})

func TestProduceActiveDeals_TableDriven(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		listBotsResp  []tc.Bot
		listBotsErr   error
		dealsByBot    map[int][]tc.Deal
		dealsErrByBot map[int]error
		wantKeys      []WorkKey
		wantCachedIDs []int
		wantErr       bool
	}{
		{
			name:         "two bots, mixed deals",
			listBotsResp: []tc.Bot{{Id: 1}, {Id: 2}},
			dealsByBot: map[int][]tc.Deal{
				1: {{Id: 101, BotId: 1}, {Id: 102, BotId: 1}},
				2: {{Id: 201, BotId: 2}},
			},
			wantKeys: []WorkKey{
				{DealID: 101, BotID: 1},
				{DealID: 102, BotID: 1},
				{DealID: 201, BotID: 2},
			},
			wantCachedIDs: []int{101, 102, 201},
		},
		{
			name:        "list bots error surfaces",
			listBotsErr: errors.New("boom"),
			wantErr:     true,
		},
		{
			name:         "per-bot deals error continues for others",
			listBotsResp: []tc.Bot{{Id: 1}, {Id: 2}},
			dealsByBot: map[int][]tc.Deal{
				2: {{Id: 2001, BotId: 2}},
			},
			dealsErrByBot: map[int]error{
				1: errors.New("rate limited"),
			},
			wantKeys:      []WorkKey{{DealID: 2001, BotID: 2}},
			wantCachedIDs: []int{2001},
		},
		{
			name:          "no bots => no work, no error",
			listBotsResp:  nil,
			wantKeys:      nil,
			wantCachedIDs: nil,
		},
		{
			name:         "bot with no deals",
			listBotsResp: []tc.Bot{{Id: 9}},
			dealsByBot: map[int][]tc.Deal{
				9: {},
			},
			wantKeys:      nil,
			wantCachedIDs: nil,
		},
	}

	for _, tcse := range tests {
		tcse := tcse
		t.Run(tcse.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			client := &fakeClient{
				listBotsResp: tcse.listBotsResp,
				listBotsErr:  tcse.listBotsErr,
				dealsByBot:   tcse.dealsByBot,
				dealsErrByBot: func() map[int]error {
					if tcse.dealsErrByBot == nil {
						return map[int]error{}
					}
					m := make(map[int]error, len(tcse.dealsErrByBot))
					for k, v := range tcse.dealsErrByBot {
						m[k] = v
					}
					return m
				}(),
			}
			q := &fakeQueue{}
			em := &fakeEmitter{}
			e := NewEngine(client, nil, em)

			err := e.ProduceActiveDeals(ctx, q)
			if tcse.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			got := append([]WorkKey(nil), q.added...)
			require.True(t, cmp.Equal(tcse.wantKeys, got, sortWK),
				"diff (-want +got):\n%s", cmp.Diff(tcse.wantKeys, got, sortWK))

			for _, id := range tcse.wantCachedIDs {
				_, ok := e.dealCache.Load(id)
				require.Truef(t, ok, "deal %d not cached", id)
			}

			// Optional: verify no unexpected IDs cached.
			if tcse.wantCachedIDs != nil {
				var cached []int
				e.dealCache.Range(func(k, _ any) bool {
					cached = append(cached, k.(int))
					return true
				})
				sort.Ints(cached)
				wantIDs := append([]int(nil), tcse.wantCachedIDs...)
				sort.Ints(wantIDs)
				require.True(t, cmp.Equal(wantIDs, cached),
					"cached IDs diff (-want +got):\n%s", cmp.Diff(wantIDs, cached))
			}
		})
	}
}
