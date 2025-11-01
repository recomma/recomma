package orderid

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"log"
	"strings"
)

// OrderId is to track an order across different states
type OrderId struct {
	BotID      uint32
	DealID     uint32
	BotEventID uint32
}

func (oid *OrderId) HexAsPointer() *string {
	hex := oid.Hex()
	return &hex
}

func (oid *OrderId) Hex() string {
	return "0x" + hex.EncodeToString(oid.AsHex())
}

// AsHex returns a 16 byte representation of the OrderId
// All are BigEndian encoded
// 4 bytes are the BotID uint32
// 4 bytes are the DealID uint32
// 4 bytes are the OrderID uint32
// 4 bytes are a CRC32 of the preceding bytes
// The time is stored with UTC
func (oid *OrderId) AsHex() []byte {
	out := make([]byte, 0, 16)

	out = binary.BigEndian.AppendUint32(out, uint32(oid.BotID))
	out = binary.BigEndian.AppendUint32(out, uint32(oid.DealID))

	out = binary.BigEndian.AppendUint32(out, uint32(oid.BotEventID))
	out = binary.BigEndian.AppendUint32(out, crc32.Checksum(out, crc32.IEEETable))

	return out
}

var ErrHexTooShort error = fmt.Errorf("hex data too short")
var ErrIncorrectChecksum error = fmt.Errorf("checksum does not match")

// FromHex returns a OrderId from the provided hex. If the CRC16 checksum
// does not pass an error is returned. The time is loaded with UTC
func FromHex(v []byte) (*OrderId, error) {
	if len(v) != 16 {
		log.Printf("too short: %s\n%X", v, v)
		return nil, ErrHexTooShort
	}

	if crc32.Checksum(v[0:12], crc32.IEEETable) != binary.BigEndian.Uint32(v[12:16]) {
		return nil, ErrIncorrectChecksum
	}

	oid := &OrderId{}
	oid.BotID = uint32(binary.BigEndian.Uint32(v[0:4]))
	oid.DealID = uint32(binary.BigEndian.Uint32(v[4:8]))
	oid.BotEventID = uint32(binary.BigEndian.Uint32(v[8:12]))

	return oid, nil
}

// FromHexString strips off a prepending 0x if present
func FromHexString(s string) (*OrderId, error) {
	s = strings.TrimPrefix(s, "0x")
	s = strings.ReplaceAll(s, " ", "")
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("could not decode: %s", err)
	}
	return FromHex(b)
}
