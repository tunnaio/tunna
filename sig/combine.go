package sig

// CRC32C combination (ADR-0006). A CRC is linear over GF(2), so the CRC of
// A followed by B equals the CRC of A shifted by len(B) zero bytes, XORed
// with the CRC of B. Shifting by n zero bits is a 32x32 GF(2) matrix raised
// to the n-th power, computed by squaring, so the cost is log2(n) matrix
// operations however long B is. This is zlib's crc32_combine, transcribed.
// The vectors in spec/vectors/crc32c.json, whose expected values come from
// hashing the concatenated bytes directly, are the oracle.

// crc32cPoly is the Castagnoli polynomial in reflected form.
const crc32cPoly uint32 = 0x82f63b78

// gf2Times multiplies the bit vector v by the matrix m over GF(2). Columns
// of m are the images of the unit vectors; the product is the XOR of the
// columns selected by the set bits of v.
func gf2Times(m *[32]uint32, v uint32) uint32 {
	var sum uint32
	for i := 0; v != 0; i, v = i+1, v>>1 {
		if v&1 != 0 {
			sum ^= m[i]
		}
	}
	return sum
}

// gf2Square sets sq to m*m.
func gf2Square(sq, m *[32]uint32) {
	for n := range sq {
		sq[n] = gf2Times(m, m[n])
	}
}

// CombineCRC32C returns the CRC32C of two byte ranges concatenated, given
// each range's CRC and the length of the second. Used at complete to fold
// per-part checksums into the object's, in part order: start with part 1's
// CRC and fold each following part in with its length.
func CombineCRC32C(a, b uint32, lenB int64) uint32 {
	if lenB <= 0 {
		return a
	}

	var even, odd [32]uint32

	// odd: the operator for one zero bit. For a reflected CRC that is the
	// polynomial in column 0 and a shifted identity in the rest.
	odd[0] = crc32cPoly
	row := uint32(1)
	for n := 1; n < 32; n++ {
		odd[n] = row
		row <<= 1
	}
	gf2Square(&even, &odd) // two zero bits
	gf2Square(&odd, &even) // four zero bits

	// Each iteration below handles one bit of lenB and doubles the shift,
	// starting at eight bits, one byte, so the first bit examined is the
	// lowest bit of the byte count. The two buffers alternate because a
	// square needs a source and a destination that are not the same array.
	for {
		gf2Square(&even, &odd)
		if lenB&1 != 0 {
			a = gf2Times(&even, a)
		}
		lenB >>= 1
		if lenB == 0 {
			break
		}
		gf2Square(&odd, &even)
		if lenB&1 != 0 {
			a = gf2Times(&odd, a)
		}
		lenB >>= 1
		if lenB == 0 {
			break
		}
	}
	return a ^ b
}
