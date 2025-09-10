package metadata

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
	"time"

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

			got := tc.md.AsHex()
			if tc.wantPanic {
				return
			}
			if !bytes.Equal(mustHex(tc.hex), got) {
				t.Fatalf("not equal\nwant: % X\ngot:  % X", tc.hex, got)
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
			md, err := FromHex(mustHex(tc.hex))
			if tc.wantErrorFromHex != nil {
				require.Error(tt, err, tc.wantErrorFromHex)
				require.Nil(tt, md)
				return
			}

			require.NoError(tt, err)

			if md != nil {

				if diff := cmp.Diff(*md, tc.md); diff != "" {
					tt.Errorf("not equal\nwant: %v\ngot:  %v", tc.md, *md)
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
			md, err := FromHexString(tc.hex)
			if tc.wantErrorFromHex != nil {
				require.Error(tt, err, tc.wantErrorFromHex)
				require.Nil(tt, md)
				return
			}

			require.NoError(tt, err)

			if md != nil {

				if diff := cmp.Diff(*md, tc.md); diff != "" {
					tt.Errorf("not equal\nwant: %v\ngot:  %v", tc.md, *md)
				}
			}
		})
	}
}

type hexData struct {
	name             string
	md               Metadata
	hex              string
	wantPanic        bool
	wantErrorFromHex error
}

func getTests() []hexData {
	epoch := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	return []hexData{
		{
			name: "Empty",
			md:   Metadata{},
			hex:  "06 C6 00 00 00 00 00 00 00 00 00 00 00 00 3F 5A",
		},
		{
			name: "epoch day 0, all zeros",
			md: Metadata{
				CreatedAt: epoch,
				BotID:     0,
				DealID:    0,
				OrderID:   0,
			},
			hex: "0000 00000000 00000000 00000000 FE 54",
		},
		{
			name: "small values sample",
			md: Metadata{
				CreatedAt: epoch.Add(24 * time.Hour), // 1970-01-02 -> day=1
				BotID:     1,
				DealID:    2,
				OrderID:   3,
			},
			hex: "0001 00000001 00000002 00000003 2E 62",
		},
		{
			name: "max uint16 day boundary",
			md: Metadata{
				CreatedAt: epoch.AddDate(0, 0, 65535), // day=65535 -> 0xFFFF
				BotID:     0x01020304,
				DealID:    0xAABBCCDD,
				OrderID:   3735928559, // 0xDEADBEEF within uint32
			},
			hex: "FFFF 01020304 AABBCCDD DEADBEEF 7D 2C",
		},
		// {
		// 	name: "invalid order id panics",
		// 	md: Metadata{
		// 		CreatedAt: epoch,
		// 		BotID:     123,
		// 		DealID:    456,
		// 		OrderID:   "not-a-number",
		// 	},
		// 	wantPanic:        true,
		// 	wantErrorFromHex: HexTooShort,
		// },
		{
			name: "received back from API",
			md: Metadata{
				CreatedAt: time.Date(2025, 8, 11, 0, 0, 0, 0, time.UTC),
				BotID:     1,
				DealID:    1,
				OrderID:   1,
			},
			hex: "0x4f57000000010000000100000001f620",
		},
	}
}
