package sig_test

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash/crc32"
	"os"
	"strconv"
	"testing"

	"github.com/tunnaio/tunna/sig"
)

// spec/vectors/crc32c.json is the oracle for the wire form and for
// combination. The single cases check EncodeCRC32C and ParseChecksum against
// the standard library's own CRC of the input; the combine cases check
// CombineCRC32C over per-part CRCs against the CRC of the concatenation.

type crcInput struct {
	Text  *string `json:"text"`
	Hex   *string `json:"hex"`
	Bytes *struct {
		Seed   int64 `json:"seed"`
		Length int64 `json:"length"`
	} `json:"bytes"`
}

type crcVectors struct {
	Spec   string `json:"spec"`
	Single []struct {
		Name   string   `json:"name"`
		Input  crcInput `json:"input"`
		CRCHex string   `json:"crc32c_hex"`
		Wire   string   `json:"wire"`
	} `json:"single"`
	Combine []struct {
		Name     string     `json:"name"`
		Parts    []crcInput `json:"parts"`
		PartCRCs []string   `json:"part_crc32c_hex"`
		PartLens []int64    `json:"part_lengths"`
		WholeHex string     `json:"whole_crc32c_hex"`
		Wire     string     `json:"wire"`
	} `json:"combine"`
}

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

func loadCRCVectors(t *testing.T) crcVectors {
	t.Helper()
	raw, err := os.ReadFile("../spec/vectors/crc32c.json")
	if err != nil {
		t.Fatalf("reading vectors: %v", err)
	}
	var v crcVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("parsing vectors: %v", err)
	}
	return v
}

func bytesOf(t *testing.T, in crcInput) []byte {
	t.Helper()
	switch {
	case in.Text != nil:
		return []byte(*in.Text)
	case in.Hex != nil:
		b, err := hex.DecodeString(*in.Hex)
		if err != nil {
			t.Fatal(err)
		}
		return b
	case in.Bytes != nil:
		return generate(in.Bytes.Seed, in.Bytes.Length)
	}
	t.Fatal("input has no form")
	return nil
}

// generate is the sha256-counter generator from spec/conformance/fixtures.json.
func generate(seed, length int64) []byte {
	out := make([]byte, 0, length)
	var block [16]byte
	binary.BigEndian.PutUint64(block[:8], uint64(seed))
	for counter := int64(0); int64(len(out)) < length; counter++ {
		binary.BigEndian.PutUint64(block[8:], uint64(counter))
		sum := sha256.Sum256(block[:])
		out = append(out, sum[:]...)
	}
	return out[:length]
}

func parseHex32(t *testing.T, s string) uint32 {
	t.Helper()
	n, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return uint32(n)
}

func TestChecksumWireFormVectors(t *testing.T) {
	v := loadCRCVectors(t)
	if len(v.Single) == 0 {
		t.Fatal("no single cases")
	}
	for _, c := range v.Single {
		t.Run(c.Name, func(t *testing.T) {
			crc := crc32.Checksum(bytesOf(t, c.Input), castagnoli)
			if got := parseHex32(t, c.CRCHex); got != crc {
				t.Fatalf("vector's crc %08x disagrees with the standard library %08x; the vector file is wrong", got, crc)
			}
			if got := sig.EncodeCRC32C(crc); got != c.Wire {
				t.Errorf("EncodeCRC32C(%08x) = %q, want %q", crc, got, c.Wire)
			}
			back, err := sig.ParseChecksum(c.Wire)
			if err != nil {
				t.Fatalf("ParseChecksum(%q): %v", c.Wire, err)
			}
			if back != crc {
				t.Errorf("ParseChecksum(%q) = %08x, want %08x", c.Wire, back, crc)
			}
		})
	}
}

func TestParseChecksumRejectsMalformed(t *testing.T) {
	for _, bad := range []string{
		"",
		"4waSgw==",          // no prefix
		"crc32c=",           // empty value
		"crc32c=4waSgw",     // missing padding
		"crc32c=4waSgw==x",  // trailing junk
		"crc32c=AAAAAAAA==", // 6 bytes, not 4
		"sha256=4waSgw==",   // other algorithm
		"CRC32C=4waSgw==",   // prefix is case-sensitive
		"crc32c=4waS gw==",  // whitespace inside
		"crc32c=!!!!!!==",   // not base64
	} {
		if _, err := sig.ParseChecksum(bad); err == nil {
			t.Errorf("ParseChecksum(%q) accepted; want an error", bad)
		}
	}
}

func TestCombineCRC32CVectors(t *testing.T) {
	v := loadCRCVectors(t)
	if len(v.Combine) == 0 {
		t.Fatal("no combine cases")
	}
	for _, c := range v.Combine {
		t.Run(c.Name, func(t *testing.T) {
			// Sanity: the per-part CRCs in the file match the standard library.
			for i, p := range c.Parts {
				if got := crc32.Checksum(bytesOf(t, p), castagnoli); got != parseHex32(t, c.PartCRCs[i]) {
					t.Fatalf("part %d: vector crc %s disagrees with the standard library %08x", i, c.PartCRCs[i], got)
				}
			}
			acc := parseHex32(t, c.PartCRCs[0])
			for i := 1; i < len(c.PartCRCs); i++ {
				acc = sig.CombineCRC32C(acc, parseHex32(t, c.PartCRCs[i]), c.PartLens[i])
			}
			want := parseHex32(t, c.WholeHex)
			if acc != want {
				t.Errorf("combined = %08x, want %08x (the CRC of the concatenated bytes)", acc, want)
			}
			if got := sig.EncodeCRC32C(acc); got != c.Wire {
				t.Errorf("wire form of combined = %q, want %q", got, c.Wire)
			}
		})
	}
}

func TestCombineCRC32CEdges(t *testing.T) {
	a := crc32.Checksum([]byte("abc"), castagnoli)
	if got := sig.CombineCRC32C(a, 0, 0); got != a {
		t.Errorf("combining with an empty second range changed the CRC: %08x != %08x", got, a)
	}
	if got := sig.CombineCRC32C(0, a, 3); got != a {
		t.Errorf("combining an empty first range did not yield the second's CRC: %08x != %08x", got, a)
	}
}
