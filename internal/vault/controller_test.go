package vault

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestControllerUnsealRequiresPrimaryVenue(t *testing.T) {
	ctrl := NewController(StateSealed)
	secrets := Secrets{
		Secrets: Data{
			THREECOMMASAPIKEY:     "key",
			THREECOMMASPRIVATEKEY: "secret",
			Venues: []VenueSecret{{
				ID:         "hyperliquid:test",
				Type:       "hyperliquid",
				Wallet:     "wallet",
				PrivateKey: "pk",
			}},
		},
	}

	err := ctrl.Unseal(secrets, nil)
	require.ErrorIs(t, err, ErrPrimaryVenueRequired)
}

func TestControllerUnsealAcceptsPrimaryVenue(t *testing.T) {
	ctrl := NewController(StateSealed)
	secrets := Secrets{
		Secrets: Data{
			THREECOMMASAPIKEY:     "key",
			THREECOMMASPRIVATEKEY: "secret",
			Venues: []VenueSecret{{
				ID:         "hyperliquid:test",
				Type:       "hyperliquid",
				Wallet:     "wallet",
				PrivateKey: "pk",
				Primary:    true,
			}},
		},
	}

	expiry := time.Now().Add(time.Hour)
	require.NoError(t, ctrl.Unseal(secrets, &expiry))
	require.Equal(t, StateUnsealed, ctrl.State())
}
