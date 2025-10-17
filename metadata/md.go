package metadata

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/howeyc/crc16"
)

type Metadata struct {
	CreatedAt  time.Time
	BotID      uint32
	DealID     uint32
	BotEventID uint32
}

func (md *Metadata) String() string {
	// return fmt.Sprintf("%s::%d::%d::%s", md.CreatedAt.Format("2006-01-02"), md.BotID, md.DealID, md.OrderID)
	// return fmt.Sprintf("%s::%s", md.CreatedAt.Format("2006-01-02"), md.Hex())
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
// 2 bytes are the days since epoch uint16
// 4 bytes are the BotID uint32
// 4 bytes are the DealID uint32
// 4 bytes are the OrderID uint32
// 2 bytes are a CRC16 of the preceding bytes
// The time is stored with UTC
func (md *Metadata) AsHex() []byte {
	out := make([]byte, 0, 16)

	// unix returns number of seconds since epoch
	// we can divide this by the amount of seconds in a day
	// this should fit in an uint16
	d := md.CreatedAt.UTC().Unix() / 86400
	// log.Printf("days since epoch: %d", d)
	out = binary.BigEndian.AppendUint16(out, uint16(d))

	out = binary.BigEndian.AppendUint32(out, uint32(md.BotID))
	out = binary.BigEndian.AppendUint32(out, uint32(md.DealID))

	// var oid uint64
	// var err error
	// if md.OrderID != "" {
	// 	// we know OrderID is actually an uint32 too
	// 	oid, err = strconv.ParseUint(md.OrderID, 10, 32)
	// 	if err != nil {
	// 		panic("orderid cannot be parsed as uint32")
	// 	}
	// }

	out = binary.BigEndian.AppendUint32(out, uint32(md.BotEventID))
	// out = binary.BigEndian.AppendUint32(out, uint32(oid))

	out = binary.BigEndian.AppendUint16(out, crc16.Checksum(out, crc16.IBMTable))

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

	if crc16.Checksum(v[0:14], crc16.IBMTable) != binary.BigEndian.Uint16(v[14:16]) {
		return nil, ErrIncorrectChecksum
	}

	md := &Metadata{}
	days := binary.BigEndian.Uint16(v[0:2])
	md.CreatedAt = time.Unix(int64(days)*86400, 0).UTC()

	md.BotID = uint32(binary.BigEndian.Uint32(v[2:6]))
	md.DealID = uint32(binary.BigEndian.Uint32(v[6:10]))
	md.BotEventID = uint32(binary.BigEndian.Uint32(v[10:14]))

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
