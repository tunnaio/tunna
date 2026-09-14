// CRC-32C (Castagnoli) and its wire form (spec/wire.md 8, ADR-0006):
// "crc32c=" plus base64 of the 4-byte big-endian value. combine lets a
// client compute a whole-object checksum from per-part checksums without
// re-reading the parts. spec/vectors/crc32c.json is the authority.

/** CRC-32C of data, continuing from a previous value when given (0 to start). */
export function crc32c(data: Uint8Array, previous = 0): number {
  throw new Error("not implemented");
}

/** The CRC of the concatenation of two byte strings, given their CRCs and the second one's length. */
export function combine(crc1: number, crc2: number, length2: number): number {
  throw new Error("not implemented");
}

/** The wire form: "crc32c=" + base64 of the big-endian 4-byte value. */
export function encodeChecksum(crc: number): string {
  throw new Error("not implemented");
}

/** Parses the wire form back to a number; throws on a malformed value. */
export function parseChecksum(wire: string): number {
  throw new Error("not implemented");
}
