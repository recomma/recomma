package hl

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	hyperliquid "github.com/sonirico/go-hyperliquid"
	"github.com/stretchr/testify/require"
	"github.com/terwey/recomma/metadata"

	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/recorder"
)

var record = true

func scrubHLJSON(body string) string {
	var m map[string]any
	dec := json.NewDecoder(strings.NewReader(body))
	dec.UseNumber() // keep numeric fidelity
	if err := dec.Decode(&m); err != nil {
		return body // not JSON; leave as-is
	}
	delete(m, "nonce")
	if sig, ok := m["signature"].(map[string]any); ok {
		delete(sig, "r")
		delete(sig, "s")
		delete(sig, "v")
		if len(sig) == 0 {
			delete(m, "signature")
		} else {
			m["signature"] = sig
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return string(b)
}

func hyperliquidJSONMatcher() recorder.MatcherFunc {
	def := cassette.NewDefaultMatcher(
		cassette.WithIgnoreHeaders("Authorization", "Apikey", "Signature"),
	)

	return func(req *http.Request, rec cassette.Request) bool {
		// Quick method/URL gate
		if req.Method != rec.Method || req.URL.String() != rec.URL {
			return false
		}

		// Ignore auth-ish headers (from the recorded request)
		rec.Headers.Del("Authorization")
		rec.Headers.Del("Apikey")
		rec.Headers.Del("Signature")

		// If JSON, compare normalized bodies
		if strings.Contains(rec.Headers.Get("Content-Type"), "application/json") {
			rb, _ := io.ReadAll(req.Body)
			defer func() { req.Body = io.NopCloser(bytes.NewReader(rb)) }()
			a := scrubHLJSON(string(rb))
			b := scrubHLJSON(rec.Body)
			return a == b
		}

		// Fallback to the library’s default matcher
		return def(req, rec)
	}
}

func defaultRecorderOpts(record bool) []recorder.Option {
	opts := []recorder.Option{
		recorder.WithHook(func(i *cassette.Interaction) error {
			i.Request.Headers.Del("Authorization")
			i.Request.Headers.Del("Apikey")
			i.Request.Headers.Del("Signature")

			if strings.Contains(i.Request.Headers.Get("Content-Type"), "application/json") && i.Request.Body != "" {
				i.Request.Body = scrubHLJSON(i.Request.Body)
			}

			return nil
		}, recorder.AfterCaptureHook),
		recorder.WithMatcher(hyperliquidJSONMatcher()),
		recorder.WithSkipRequestLatency(true),
	}

	if record {
		opts = append(opts,
			recorder.WithMode(recorder.ModeReplayWithNewEpisodes),
			recorder.WithRealTransport(http.DefaultTransport),
		)
	} else {
		opts = append(opts, recorder.WithMode(recorder.ModeReplayOnly))
	}

	return opts
}

func initRecorder(t *testing.T, record bool, cassetteName string) {
	opts := defaultRecorderOpts(record)

	base := strings.ReplaceAll(t.Name(), "/", "_")
	cassette := filepath.Join("testdata", func() string {
		if cassetteName != "" {
			return cassetteName
		}
		return base
	}())

	orig := http.DefaultTransport

	r, err := recorder.New(cassette, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// restore default
		http.DefaultTransport = orig
		// Make sure recorder is stopped once done with it.
		if err := r.Stop(); err != nil {
			t.Error(err)
		}
	})

	http.DefaultTransport = r
}

func TestOrders(t *testing.T) {
	type tc struct {
		name         string
		cassetteName string
		config       ClientConfig
		md           metadata.Metadata
		order        hyperliquid.CreateOrderRequest
		result       hyperliquid.OrderStatus
		wantErr      string
		record       bool
	}

	cases := []tc{
		{
			name:         "invalid auth",
			cassetteName: "Orders",
			config:       ClientConfig{Key: "0x38d55ff1195c57b9dbc8a72c93119500f1fcd47a33f98149faa18d2fc37932fa"},
			// if the Order is not a proper request it won't even hit the key check
			order: hyperliquid.CreateOrderRequest{
				Coin:  "DOGE",
				IsBuy: true,
				Size:  55,
				Price: 0.22330,
				OrderType: hyperliquid.OrderType{
					Limit: &hyperliquid.LimitOrderType{
						Tif: hyperliquid.TifGtc,
					},
				},
			},
			wantErr: "failed to create order: User or API Wallet",
			record:  false,
		},
		{
			name:         "Create Order below 10$",
			cassetteName: "Orders",
			config:       config,
			order: hyperliquid.CreateOrderRequest{
				Coin:  "DOGE",
				IsBuy: true,
				Size:  25,
				Price: 0.22330,
				OrderType: hyperliquid.OrderType{
					Limit: &hyperliquid.LimitOrderType{
						Tif: hyperliquid.TifGtc,
					},
				},
			},
			wantErr: "Order must have minimum value of $10.",
			record:  false,
		},
		{
			name:         "Order above 10$",
			cassetteName: "Orders",
			config:       config,
			order: hyperliquid.CreateOrderRequest{
				Coin:  "DOGE",
				IsBuy: true,
				Size:  45,
				Price: 0.12330, // set it low so it never gets executed
				OrderType: hyperliquid.OrderType{
					Limit: &hyperliquid.LimitOrderType{
						Tif: hyperliquid.TifGtc,
					},
				},
			},
			result: hyperliquid.OrderStatus{
				Resting: &hyperliquid.OrderStatusResting{
					Oid: 37522216543,
				},
			},
			record: false,
		},
		{
			name:         "Order above with cloid",
			cassetteName: "Orders",
			config:       config,
			order: hyperliquid.CreateOrderRequest{
				Coin:  "DOGE",
				IsBuy: true,
				Size:  45,
				Price: 0.12330, // set it low so it never gets executed
				OrderType: hyperliquid.OrderType{
					Limit: &hyperliquid.LimitOrderType{
						Tif: hyperliquid.TifGtc,
					},
				},
				ClientOrderID: func() *string {
					md := metadata.Metadata{}
					return md.HexAsPointer()
				}(),
			},
			result: hyperliquid.OrderStatus{
				Resting: &hyperliquid.OrderStatusResting{
					Oid:      37522336860,
					ClientID: strAsPtr("0x06c60000000000000000000000003f5a"),
				},
			},
			record: false,
		},
		{
			name:         "DOGE buy",
			cassetteName: "replicate",
			config:       config,
			order: hyperliquid.CreateOrderRequest{
				Coin:       "DOGE",
				IsBuy:      true,
				Price:      0.21367,
				Size:       7,
				ReduceOnly: false,
				OrderType: hyperliquid.OrderType{
					Limit: &hyperliquid.LimitOrderType{
						Tif: hyperliquid.TifGtc,
					},
				},
				ClientOrderID: strAsPtr("0x4f6c00fa836b8d608f365c1c8a34f64f"),
			},
			record: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(tt *testing.T) {
			// we don't care about errors here
			initRecorder(tt, tc.record, tc.cassetteName)
			exchange, err := NewExchange(tc.config)
			require.NoError(tt, err, "client should not produce error")

			payload, _ := json.Marshal(tc.order)
			tt.Logf("req: \n%s\n", payload)

			res, err := exchange.Order(tc.order, nil)
			tt.Logf("res: %v", res)
			tt.Logf("err: %v", err)
			if tc.wantErr != "" {
				require.Error(tt, err)
				require.Contains(tt, err.Error(), tc.wantErr)
				return
			} else {
				require.NoError(tt, err)
			}

			if err == nil {
				if diff := cmp.Diff(res, tc.result); diff != "" {
					tt.Errorf("not equal\nwant: %v\ngot:  %v", tc.result, res)
				}
			}
		})
	}
}

func strAsPtr(s string) *string {
	return &s
}
