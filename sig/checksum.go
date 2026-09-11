package sig

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
)

// checksumPrefix names the one algorithm the wire carries (ADR-0006). The
// prefix exists so a second algorithm could be added without a new header.
const checksumPrefix = "crc32c="

// ErrMalformedChecksum is returned by ParseChecksum for anything that is not
// the wire form.
var ErrMalformedChecksum = errors.New("sig: malformed checksum")

// EncodeCRC32C renders a CRC32C in the wire form, "crc32c=<base64>": the
// four-byte big-endian value in standard base64 with padding.
func EncodeCRC32C(crc uint32) string {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], crc)
	return checksumPrefix + base64.StdEncoding.EncodeToString(b[:])
}

// ParseChecksum reads the wire form back. It returns ErrMalformedChecksum
// for anything that is not "crc32c=" followed by exactly four bytes of
// standard base64. The prefix is case-sensitive.
func ParseChecksum(s string) (uint32, error) {
	rest, ok := strings.CutPrefix(s, checksumPrefix)
	if !ok {
		return 0, ErrMalformedChecksum
	}
	b, err := base64.StdEncoding.DecodeString(rest)
	if err != nil || len(b) != 4 {
		return 0, ErrMalformedChecksum
	}
	return binary.BigEndian.Uint32(b), nil
}
