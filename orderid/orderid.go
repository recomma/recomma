package orderid

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"log"
	"strings"
)

// OrderId tracks an incoming 3commas order across different states.
type OrderId struct {
	BotID      uint32
	DealID     uint32
	BotEventID uint32
}

func (id *OrderId) HexAsPointer() *string {
	hex := id.Hex()
	return &hex
}

func (id *OrderId) Hex() string {
	return "0x" + hex.EncodeToString(id.AsHex())
}

// AsHex returns a 16 byte representation of the order identifier.
// All components are BigEndian encoded as:
// 4 bytes for the BotID uint32
// 4 bytes for the DealID uint32
// 4 bytes for the OrderID uint32 (ThreeCommas bot event id)
// 4 bytes for a CRC32 of the preceding bytes
func (id *OrderId) AsHex() []byte {
	out := make([]byte, 0, 16)

	out = binary.BigEndian.AppendUint32(out, uint32(id.BotID))
	out = binary.BigEndian.AppendUint32(out, uint32(id.DealID))

	out = binary.BigEndian.AppendUint32(out, uint32(id.BotEventID))
	out = binary.BigEndian.AppendUint32(out, crc32.Checksum(out, crc32.IEEETable))

	return out
}

var ErrHexTooShort error = fmt.Errorf("hex data too short")
var ErrIncorrectChecksum error = fmt.Errorf("checksum does not match")

// FromHex returns an OrderId from the provided hex. If the CRC32 checksum
// does not pass an error is returned. The time is loaded with UTC.
func FromHex(v []byte) (*OrderId, error) {
	if len(v) != 16 {
		log.Printf("too short: %s\n%X", v, v)
		return nil, ErrHexTooShort
	}

	if crc32.Checksum(v[0:12], crc32.IEEETable) != binary.BigEndian.Uint32(v[12:16]) {
		return nil, ErrIncorrectChecksum
	}

	id := &OrderId{}
	id.BotID = uint32(binary.BigEndian.Uint32(v[0:4]))
	id.DealID = uint32(binary.BigEndian.Uint32(v[4:8]))
	id.BotEventID = uint32(binary.BigEndian.Uint32(v[8:12]))

	return id, nil
}

// FromHexString strips off a prepending 0x if present.
func FromHexString(s string) (*OrderId, error) {
	s = strings.TrimPrefix(s, "0x")
	s = strings.ReplaceAll(s, " ", "")
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("could not decode: %s", err)
	}
	return FromHex(b)
}
