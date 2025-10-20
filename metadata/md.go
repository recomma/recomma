package metadata

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"log"
	"strings"
)

// Metadata is to track an order across different states
type Metadata struct {
	BotID      uint32
	DealID     uint32
	BotEventID uint32
}

func (md *Metadata) String() string {
	return md.Hex()
}

func (md *Metadata) HexAsPointer() *string {
	hex := md.Hex()
	return &hex
}

func (md *Metadata) Hex() string {
	return "0x" + hex.EncodeToString(md.AsHex())
}

// AsHex returns a 16 byte representation of the metadata
// All are BigEndian encoded
// 4 bytes are the BotID uint32
// 4 bytes are the DealID uint32
// 4 bytes are the OrderID uint32
// 4 bytes are a CRC32 of the preceding bytes
// The time is stored with UTC
func (md *Metadata) AsHex() []byte {
	out := make([]byte, 0, 16)

	out = binary.BigEndian.AppendUint32(out, uint32(md.BotID))
	out = binary.BigEndian.AppendUint32(out, uint32(md.DealID))

	out = binary.BigEndian.AppendUint32(out, uint32(md.BotEventID))
	out = binary.BigEndian.AppendUint32(out, crc32.Checksum(out, crc32.IEEETable))

	return out
}

var ErrHexTooShort error = fmt.Errorf("hex data too short")
var ErrIncorrectChecksum error = fmt.Errorf("checksum does not match")

// FromHex returns a Metadata from the provided hex. If the CRC16 checksum
// does not pass an error is returned. The time is loaded with UTC
func FromHex(v []byte) (*Metadata, error) {
	if len(v) != 16 {
		log.Printf("too short: %s\n%X", v, v)
		return nil, ErrHexTooShort
	}

	if crc32.Checksum(v[0:12], crc32.IEEETable) != binary.BigEndian.Uint32(v[12:16]) {
		return nil, ErrIncorrectChecksum
	}

	md := &Metadata{}
	md.BotID = uint32(binary.BigEndian.Uint32(v[0:4]))
	md.DealID = uint32(binary.BigEndian.Uint32(v[4:8]))
	md.BotEventID = uint32(binary.BigEndian.Uint32(v[8:12]))

	return md, nil
}

// FromHexString strips off a prepending 0x if present
func FromHexString(s string) (*Metadata, error) {
	s = strings.TrimPrefix(s, "0x")
	s = strings.ReplaceAll(s, " ", "")
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("could not decode: %s", err)
	}
	return FromHex(b)
}
