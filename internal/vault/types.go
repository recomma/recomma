package vault

import (
	"encoding/json"
	"time"
)

// State represents the lifecycle state of the vault controller.
type State string

const (
	StateSetupRequired State = "setup_required"
	StateSealed        State = "sealed"
	StateUnsealed      State = "unsealed"
)

// User represents the single logical account permitted to unlock the vault.
type User struct {
	ID        int64
	Username  string
	CreatedAt time.Time
}

// Payload is the encrypted secret bundle stored for the user.
type Payload struct {
	ID             int64
	UserID         int64
	Version        string
	Ciphertext     []byte
	Nonce          []byte
	AssociatedData []byte
	PRFParams      json.RawMessage
	UpdatedAt      time.Time
}

// Credential stores a serialized WebAuthn credential.
type Credential struct {
	ID           int64
	UserID       int64
	CredentialID []byte
	Credential   json.RawMessage
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// PayloadInput captures the parameters required to persist an encrypted payload.
type PayloadInput struct {
	UserID         int64
	Version        string
	Ciphertext     []byte
	Nonce          []byte
	AssociatedData []byte
	PRFParams      json.RawMessage
	UpdatedAt      time.Time
}

// CredentialInput captures the parameters required to persist a WebAuthn credential.
type CredentialInput struct {
	UserID       int64
	CredentialID []byte
	Credential   json.RawMessage
}

// Secrets contains the decrypted payload supplied during the unseal flow.
type Secrets struct {
	Secrets    Data
	Raw        json.RawMessage
	ReceivedAt time.Time
}

type Data struct {
	// HYPERLIQUIDPRIVATEKEY Hyperliquid private key corresponding to the wallet.
	HYPERLIQUIDPRIVATEKEY string

	// HYPERLIQUIDWALLET Hyperliquid wallet address.
	HYPERLIQUIDWALLET string

	HYPERLIQUIDURL string

	// THREECOMMASAPIKEY Public API key for the 3Commas integration.
	THREECOMMASAPIKEY string

	// THREECOMMASPRIVATEKEY Private API key for the 3Commas integration.
	THREECOMMASPRIVATEKEY string

	// THREECOMMASPLANTIER Plan tier for 3Commas rate limiting (starter, pro, expert).
	THREECOMMASPLANTIER string
}

// Clone returns a deep copy of the Secrets instance.
func (s Secrets) Clone() Secrets {
	cloned := Secrets{
		Secrets:    s.Secrets,
		Raw:        cloneBytes(s.Raw),
		ReceivedAt: s.ReceivedAt,
	}
	return cloned
}

// ControllerStatus summarises the runtime state exposed by the controller.
type ControllerStatus struct {
	State            State
	User             *User
	SealedAt         *time.Time
	UnsealedAt       *time.Time
	SessionExpiresAt *time.Time
}

func cloneBytes(src []byte) []byte {
	if len(src) == 0 {
		if src == nil {
			return nil
		}
		return []byte{}
	}
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}
