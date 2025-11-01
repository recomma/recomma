package orderid

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
)

func mustHex(s string) []byte {
	s = strings.TrimPrefix(s, "0x")
	s = strings.ReplaceAll(s, " ", "")
	out, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return out
}

func TestAsHex_(t *testing.T) {
	tests := getTests()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if tc.wantPanic && r == nil {
					t.Fatalf("expected panic, got none")
				}
				if !tc.wantPanic && r != nil {
					t.Fatalf("unexpected panic: %v", r)
				}
			}()

			got := tc.oid.AsHex()
			if tc.wantPanic {
				return
			}

			want := mustHex(tc.hex)
			if !bytes.Equal(want, got) {
				t.Fatalf("not equal\nwant: % X\ngot:  % X", want, got)
			}
		})
	}
}

func TestFromHex(t *testing.T) {
	tests := getTests()

	for _, tc := range tests {
		t.Run(tc.name, func(tt *testing.T) {
			// no point in testing the from empty
			if tc.name == "Empty" {
				return
			}
			// we can reuse the existing tests just in reverse
			oid, err := FromHex(mustHex(tc.hex))
			if tc.wantErrorFromHex != nil {
				require.Error(tt, err, tc.wantErrorFromHex)
				require.Nil(tt, oid)
				return
			}

			require.NoError(tt, err)

			if oid != nil {

				if diff := cmp.Diff(*oid, tc.oid); diff != "" {
					tt.Errorf("not equal\nwant: %v\ngot:  %v", tc.oid, *oid)
				}
			}
		})
	}
}

func TestFromHexString(t *testing.T) {
	tests := getTests()

	for _, tc := range tests {
		t.Run(tc.name, func(tt *testing.T) {
			if tc.name == "Empty" {
				return
			}
			// we can reuse the existing tests just in reverse
			oid, err := FromHexString(tc.hex)
			if tc.wantErrorFromHex != nil {
				require.Error(tt, err, tc.wantErrorFromHex)
				require.Nil(tt, oid)
				return
			}

			require.NoError(tt, err)

			if oid != nil {

				if diff := cmp.Diff(*oid, tc.oid); diff != "" {
					tt.Errorf("not equal\nwant: %v\ngot:  %v", tc.oid, *oid)
				}
			}
		})
	}
}

type hexData struct {
	name             string
	oid              OrderId
	hex              string
	wantPanic        bool
	wantErrorFromHex error
}

func getTests() []hexData {
	return []hexData{
		{
			name: "Empty",
			oid:  OrderId{},
			hex:  "00000000 00000000 00000000 7B D5 C6 6F",
		},
		{
			name: "small values sample",
			oid: OrderId{
				BotID:      1,
				DealID:     2,
				BotEventID: 3,
			},
			hex: "00000001 00000002 00000003 8F 67 D0 F6",
		},
		{
			name: "boundary",
			oid: OrderId{
				BotID:      0x01020304,
				DealID:     0xAABBCCDD,
				BotEventID: 3735928559, // 0xDEADBEEF within uint32
			},
			hex: "01020304 AABBCCDD DEADBEEF 5D AF 20 B1",
		},
		{
			name: "received back from API",
			oid: OrderId{
				BotID:      16541235,
				DealID:     2381631392,
				BotEventID: 568275668,
			},
			hex: "0x00fc66338df4cfa021df32d4094afe38",
		},
	}
}
