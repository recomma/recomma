package recomma

import (
	"context"
	"errors"
	"time"

	tc "github.com/recomma/3commas-sdk-go/threecommas"
	"github.com/recomma/recomma/orderid"
	"github.com/sonirico/go-hyperliquid"
)

// VenueID identifies the upstream venue that processed an order submission.
//
// It intentionally uses a string alias so identifiers remain comparable when
// embedded into other structs (e.g., recomma.OrderWork).
type VenueID string

// OrderIdentifier uniquely identifies a submission across venues and wallets.
// It bundles the metadata fingerprint with the target venue information so
// storage, emitters, and SSE payloads can disambiguate submissions that share
// the same metadata but target different wallets.
type OrderIdentifier struct {
	VenueID VenueID
	Wallet  string
	OrderId orderid.OrderId
}

// NewOrderIdentifier constructs an identifier for the given venue, wallet, and
// metadata fingerprint.
func NewOrderIdentifier(venue VenueID, wallet string, oid orderid.OrderId) OrderIdentifier {
	return OrderIdentifier{
		VenueID: venue,
		Wallet:  wallet,
		OrderId: oid,
	}
}

// Venue returns the venue identifier as a string. Callers that need the raw
// VenueID may access the field directly, but most SQL bindings expect a string.
func (oi OrderIdentifier) Venue() string {
	return string(oi.VenueID)
}

// Hex returns the metadata fingerprint in hex form.
func (oi OrderIdentifier) Hex() string {
	return oi.OrderId.Hex()
}

type OrderWork struct {
	Identifier OrderIdentifier
	OrderId    orderid.OrderId
	Action     Action
	BotEvent   BotEvent
}

type Emitter interface {
	Emit(ctx context.Context, w OrderWork) error
}

var ErrOrderAlreadySatisfied = errors.New("recomma: order already satisfies desired state")

type ActionType int

const (
	ActionNone ActionType = iota
	ActionCreate
	ActionModify
	ActionCancel
)

func (t ActionType) String() string {
	switch t {
	case ActionCreate:
		return "create"
	case ActionModify:
		return "modify"
	case ActionCancel:
		return "cancel"
	default:
		return "none"
	}
}

type Action struct {
	Type   ActionType
	Create hyperliquid.CreateOrderRequest
	Modify hyperliquid.ModifyOrderRequest
	Cancel hyperliquid.CancelOrderRequestByCloid
	Reason string // optional human hint for ActionNone
}

type BotEvent struct {
	RowID int64
	tc.BotEvent
}

type BotEventLog struct {
	RowID      int64
	BotEvent   tc.BotEvent
	OrderId    orderid.OrderId
	BotID      int64
	DealID     int64
	BoteventID int64
	CreatedAt  time.Time
	ObservedAt time.Time
}

func ToThreeCommasBotEvent(in []BotEvent) []tc.BotEvent {
	var out []tc.BotEvent
	for _, be := range in {
		out = append(out, be.BotEvent)
	}
	return out
}
