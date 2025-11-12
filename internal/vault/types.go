package vault

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/recomma/recomma/recomma"
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
	// THREECOMMASAPIKEY Public API key for the 3Commas integration.
	THREECOMMASAPIKEY string

	// THREECOMMASPRIVATEKEY Private API key for the 3Commas integration.
	THREECOMMASPRIVATEKEY string

	// THREECOMMASPLANTIER Subscription tier governing 3Commas rate limits.
	THREECOMMASPLANTIER string

	Venues []VenueSecret
}

// VenueSecret captures the credentials required to interact with a venue.
type VenueSecret struct {
	ID          string
	Type        string
	DisplayName string
	Wallet      string
	PrivateKey  string
	APIURL      string
	Flags       map[string]interface{}
	Primary     bool
}

type wireData struct {
	ThreeCommasAPIKey   string          `json:"THREECOMMAS_API_KEY"`
	ThreeCommasPrivate  string          `json:"THREECOMMAS_PRIVATE_KEY"`
	ThreeCommasPlanTier string          `json:"THREECOMMAS_PLAN_TIER"`
	Venues              json.RawMessage `json:"venues"`
}

type wireVenue struct {
	ID          string                 `json:"id"`
	Type        string                 `json:"type"`
	DisplayName string                 `json:"display_name"`
	Wallet      string                 `json:"wallet"`
	PrivateKey  string                 `json:"private_key"`
	APIURL      string                 `json:"api_url"`
	Flags       map[string]interface{} `json:"flags"`
	Primary     bool                   `json:"is_primary"`
}

// UnmarshalJSON decodes the multi-venue payload format.
func (d *Data) UnmarshalJSON(raw []byte) error {
	var payload wireData
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}

	venues, err := parseWireVenues(payload.Venues)
	if err != nil {
		return err
	}

	d.THREECOMMASAPIKEY = payload.ThreeCommasAPIKey
	d.THREECOMMASPRIVATEKEY = payload.ThreeCommasPrivate
	d.THREECOMMASPLANTIER = strings.ToLower(strings.TrimSpace(payload.ThreeCommasPlanTier))
	d.Venues = venues
	return nil
}

func parseWireVenues(raw json.RawMessage) ([]VenueSecret, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	switch raw[0] {
	case '{':
		var mapped map[string]wireVenue
		if err := json.Unmarshal(raw, &mapped); err != nil {
			return nil, err
		}
		venues := make([]VenueSecret, 0, len(mapped))
		for key, venue := range mapped {
			v := convertWireVenue(venue)
			if v.ID == "" {
				v.ID = strings.TrimSpace(key)
			}
			venues = append(venues, v)
		}
		sort.SliceStable(venues, func(i, j int) bool {
			return venues[i].ID < venues[j].ID
		})
		return venues, nil
	case '[':
		var list []wireVenue
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, err
		}
		venues := make([]VenueSecret, 0, len(list))
		for _, venue := range list {
			venues = append(venues, convertWireVenue(venue))
		}
		return venues, nil
	default:
		return nil, fmt.Errorf("unsupported venues payload format")
	}
}

func convertWireVenue(venue wireVenue) VenueSecret {
	clonedFlags := copyFlags(venue.Flags)
	return VenueSecret{
		ID:          strings.TrimSpace(venue.ID),
		Type:        strings.TrimSpace(venue.Type),
		DisplayName: strings.TrimSpace(venue.DisplayName),
		Wallet:      strings.TrimSpace(venue.Wallet),
		PrivateKey:  strings.TrimSpace(venue.PrivateKey),
		APIURL:      strings.TrimSpace(venue.APIURL),
		Flags:       clonedFlags,
		Primary:     venue.Primary,
	}
}


func copyFlags(src map[string]interface{}) map[string]interface{} {
	if len(src) == 0 {
		if src == nil {
			return nil
		}
		return map[string]interface{}{}
	}
	dst := make(map[string]interface{}, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// Clone creates a deep copy of the secret payload data.
func (d Data) Clone() Data {
	cloned := Data{
		THREECOMMASAPIKEY:     d.THREECOMMASAPIKEY,
		THREECOMMASPRIVATEKEY: d.THREECOMMASPRIVATEKEY,
		THREECOMMASPLANTIER:   d.THREECOMMASPLANTIER,
	}

	if len(d.Venues) > 0 {
		cloned.Venues = make([]VenueSecret, len(d.Venues))
		for i := range d.Venues {
			cloned.Venues[i] = d.Venues[i].Clone()
		}
	}

	return cloned
}

// Clone returns a deep copy of the VenueSecret.
func (v VenueSecret) Clone() VenueSecret {
	cloned := v
	cloned.Flags = copyFlags(v.Flags)
	return cloned
}

// PrimaryVenueByType returns the first primary venue matching the requested type.
func (d Data) PrimaryVenueByType(venueType string) (VenueSecret, bool) {
	for _, venue := range d.Venues {
		if !venue.Primary {
			continue
		}
		if venueType == "" || strings.EqualFold(venue.Type, venueType) {
			return venue.Clone(), true
		}
	}
	return VenueSecret{}, false
>>>>>>> spec/tc-rate-limit
}

// Clone returns a deep copy of the Secrets instance.
func (s Secrets) Clone() Secrets {
	cloned := Secrets{
		Secrets:    s.Secrets.Clone(),
		Raw:        cloneBytes(s.Raw),
		ReceivedAt: s.ReceivedAt,
	}
	return cloned
}

// Validate checks that the Data payload meets all requirements for vault storage.
func (d Data) Validate() error {
	if strings.TrimSpace(d.THREECOMMASAPIKEY) == "" {
		return errors.New("invalid payload: missing required field 'secrets.THREECOMMAS_API_KEY'")
	}

	if strings.TrimSpace(d.THREECOMMASPRIVATEKEY) == "" {
		return errors.New("invalid payload: missing required field 'secrets.THREECOMMAS_PRIVATE_KEY'")
	}

	if strings.TrimSpace(d.THREECOMMASPLANTIER) == "" {
		return errors.New("invalid payload: missing required field 'secrets.THREECOMMAS_PLAN_TIER'")
	}
	if _, err := recomma.ParseThreeCommasPlanTier(d.THREECOMMASPLANTIER); err != nil {
		return fmt.Errorf("invalid payload: %w", err)
	}

	// Venues are optional - if present, validate them
	if len(d.Venues) == 0 {
		return nil
	}

	// Track venue IDs for uniqueness check
	seenIDs := make(map[string]bool)
	primaryCount := 0

	for _, venue := range d.Venues {
		id := strings.TrimSpace(venue.ID)
		if id == "" {
			return errors.New("invalid payload: venue missing id")
		}

		if seenIDs[id] {
			return fmt.Errorf("invalid payload: duplicate venue ID '%s'", id)
		}
		seenIDs[id] = true

		if strings.TrimSpace(venue.Type) == "" {
			return fmt.Errorf("invalid payload: venue '%s' missing type", id)
		}

		wallet := strings.TrimSpace(venue.Wallet)
		if wallet == "" {
			return fmt.Errorf("invalid payload: venue '%s' missing wallet", id)
		}

		// Validate Ethereum address format (40 hex chars, with or without 0x prefix)
		cleanWallet := strings.TrimPrefix(wallet, "0x")
		if len(cleanWallet) != 40 {
			return fmt.Errorf("invalid payload: venue '%s' has invalid wallet address (expected 40 hex chars)", id)
		}
		for _, c := range cleanWallet {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return fmt.Errorf("invalid payload: venue '%s' has invalid wallet address (must be hex)", id)
			}
		}

		privateKey := strings.TrimSpace(venue.PrivateKey)
		if privateKey == "" {
			return fmt.Errorf("invalid payload: venue '%s' missing private_key", id)
		}

		// Validate private key format (64 hex chars, with or without 0x prefix)
		cleanKey := strings.TrimPrefix(privateKey, "0x")
		if len(cleanKey) != 64 {
			return fmt.Errorf("invalid payload: venue '%s' has invalid private_key (expected 64 hex chars)", id)
		}
		for _, c := range cleanKey {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return fmt.Errorf("invalid payload: venue '%s' has invalid private_key (must be hex)", id)
			}
		}

		if venue.Primary {
			primaryCount++
		}
	}

	if primaryCount == 0 {
		return errors.New("invalid payload: no primary venue found")
	}

	if primaryCount > 1 {
		return errors.New("invalid payload: multiple primary venues (only one allowed)")
	}

	return nil
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
