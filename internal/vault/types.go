package vault

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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
	// THREECOMMASAPIKEY Public API key for the 3Commas integration.
	THREECOMMASAPIKEY string

	// THREECOMMASPRIVATEKEY Private API key for the 3Commas integration.
	THREECOMMASPRIVATEKEY string

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
	ThreeCommasAPIKey    string                 `json:"THREECOMMAS_API_KEY"`
	ThreeCommasPrivate   string                 `json:"THREECOMMAS_PRIVATE_KEY"`
	Venues               json.RawMessage        `json:"venues"`
	LegacyWallet         string                 `json:"HYPERLIQUID_WALLET"`
	LegacyPrivateKey     string                 `json:"HYPERLIQUID_PRIVATE_KEY"`
	LegacyURL            string                 `json:"HYPERLIQUID_URL"`
	LegacyVenueID        string                 `json:"HYPERLIQUID_VENUE_ID"`
	LegacyVenueType      string                 `json:"HYPERLIQUID_VENUE_TYPE"`
	LegacyVenueName      string                 `json:"HYPERLIQUID_VENUE_NAME"`
	LegacyVenues         map[string]legacyVenue `json:"HYPERLIQUID_VENUES"`
	LegacyVenuesFallback []legacyVenueWithID    `json:"hyperliquid_venues"`
}

type legacyVenue struct {
	Wallet     string `json:"wallet"`
	PrivateKey string `json:"private_key"`
	APIURL     string `json:"api_url"`
	Primary    bool   `json:"primary"`
	Display    string `json:"display_name"`
}

type legacyVenueWithID struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Wallet     string `json:"wallet"`
	PrivateKey string `json:"private_key"`
	APIURL     string `json:"api_url"`
	Primary    bool   `json:"primary"`
	Display    string `json:"display_name"`
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

// UnmarshalJSON decodes both the new multi-venue format and legacy single venue payloads.
func (d *Data) UnmarshalJSON(raw []byte) error {
	var payload wireData
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}

	venues, err := decodeVenues(payload)
	if err != nil {
		return err
	}

	d.THREECOMMASAPIKEY = payload.ThreeCommasAPIKey
	d.THREECOMMASPRIVATEKEY = payload.ThreeCommasPrivate
	d.Venues = venues
	return nil
}

func decodeVenues(payload wireData) ([]VenueSecret, error) {
	venues := make([]VenueSecret, 0)
	if len(payload.Venues) > 0 && string(payload.Venues) != "null" {
		parsed, err := parseWireVenues(payload.Venues)
		if err != nil {
			return nil, err
		}
		venues = append(venues, parsed...)
	}

	if len(venues) > 0 {
		return venues, nil
	}

	legacy := decodeLegacyVenues(payload)
	if len(legacy) == 0 {
		return venues, nil
	}

	return legacy, nil
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

func decodeLegacyVenues(payload wireData) []VenueSecret {
	const (
		defaultVenueID   = "hyperliquid:default"
		defaultVenueType = "hyperliquid"
	)

	build := func(id, venueType, display, wallet, key, url string, primary bool) VenueSecret {
		return VenueSecret{
			ID:          strings.TrimSpace(id),
			Type:        strings.TrimSpace(venueType),
			DisplayName: strings.TrimSpace(display),
			Wallet:      strings.TrimSpace(wallet),
			PrivateKey:  strings.TrimSpace(key),
			APIURL:      strings.TrimSpace(url),
			Primary:     primary,
		}
	}

	venues := make([]VenueSecret, 0)
	for id, v := range payload.LegacyVenues {
		venueID := id
		if venueID == "" {
			venueID = defaultVenueID
		}
		venues = append(venues, build(venueID, defaultVenueType, v.Display, v.Wallet, v.PrivateKey, v.APIURL, v.Primary))
	}
	if len(venues) > 0 {
		sort.SliceStable(venues, func(i, j int) bool {
			return venues[i].ID < venues[j].ID
		})
		return venues
	}

	if len(payload.LegacyVenuesFallback) > 0 {
		for _, v := range payload.LegacyVenuesFallback {
			venueID := v.ID
			if venueID == "" {
				venueID = defaultVenueID
			}
			venueType := v.Type
			if venueType == "" {
				venueType = defaultVenueType
			}
			venues = append(venues, build(venueID, venueType, v.Display, v.Wallet, v.PrivateKey, v.APIURL, v.Primary))
		}
		return venues
	}

	wallet := strings.TrimSpace(payload.LegacyWallet)
	privateKey := strings.TrimSpace(payload.LegacyPrivateKey)
	if wallet == "" && privateKey == "" {
		return venues
	}

	venueID := strings.TrimSpace(payload.LegacyVenueID)
	if venueID == "" {
		venueID = defaultVenueID
	}
	venueType := strings.TrimSpace(payload.LegacyVenueType)
	if venueType == "" {
		venueType = defaultVenueType
	}
	display := strings.TrimSpace(payload.LegacyVenueName)
	venues = append(venues, build(venueID, venueType, display, wallet, privateKey, payload.LegacyURL, true))
	return venues
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
