// CRC-32C and its wire form "crc32c=<base64>" (spec/wire.md 8). Bitwise
// operators are signed in JavaScript, so results go through ">>> 0".
// spec/vectors/crc32c.json is the authority.

const POLY = 0x82f63b78;

const TABLE = new Uint32Array(256);
for (let n = 0; n < 256; n++) {
  let c = n;
  for (let k = 0; k < 8; k++) {
    c = c & 1 ? (c >>> 1) ^ POLY : c >>> 1;
  }
  TABLE[n] = c >>> 0;
}

/** CRC-32C of data, continuing from a previous value when given. */
export function crc32c(data: Uint8Array, previous = 0): number {
  let crc = ~previous >>> 0;
  for (const b of data) {
    crc = TABLE[(crc ^ b) & 0xff]! ^ (crc >>> 8);
  }
  return ~crc >>> 0;
}

const PREFIX = "crc32c=";

/** The wire form: "crc32c=" plus base64 of the big-endian 4-byte value. */
export function encodeChecksum(crc: number): string {
  const bytes = new Uint8Array(4);
  new DataView(bytes.buffer).setUint32(0, crc >>> 0);
  return PREFIX + btoa(String.fromCharCode(...bytes));
}

/** Parses the wire form; throws on anything but "crc32c=" and exactly four bytes of base64. */
export function parseChecksum(wire: string): number {
  const rest = wire.startsWith(PREFIX) ? wire.slice(PREFIX.length) : "";
  let decoded = "";
  if (rest.length === 8 && rest.endsWith("==")) {
    try {
      decoded = atob(rest);
    } catch {
      decoded = "";
    }
  }
  if (decoded.length !== 4) {
    throw new Error(`malformed checksum: ${JSON.stringify(wire)}`);
  }
  const bytes = Uint8Array.from(decoded, (ch) => ch.charCodeAt(0));
  return new DataView(bytes.buffer).getUint32(0);
}

// combine is zlib's crc32_combine, transcribed from sig/combine.go, which
// explains the GF(2) matrix method.

function gf2Times(m: Uint32Array, v: number): number {
  let sum = 0;
  for (let i = 0; v !== 0; i++, v >>>= 1) {
    if (v & 1) sum ^= m[i]!;
  }
  return sum >>> 0;
}

function gf2Square(sq: Uint32Array, m: Uint32Array): void {
  for (let n = 0; n < 32; n++) {
    sq[n] = gf2Times(m, m[n]!);
  }
}

/** The CRC of two byte strings concatenated, given their CRCs and the second one's length. */
export function combine(crc1: number, crc2: number, length2: number): number {
  if (length2 <= 0) return crc1 >>> 0;

  const even = new Uint32Array(32);
  const odd = new Uint32Array(32);

  odd[0] = POLY;
  let row = 1;
  for (let n = 1; n < 32; n++) {
    odd[n] = row;
    row = (row << 1) >>> 0;
  }
  gf2Square(even, odd);
  gf2Square(odd, even);

  let a = crc1 >>> 0;
  let len = length2;
  for (;;) {
    gf2Square(even, odd);
    if (len & 1) a = gf2Times(even, a);
    len = Math.floor(len / 2);
    if (len === 0) break;
    gf2Square(odd, even);
    if (len & 1) a = gf2Times(odd, a);
    len = Math.floor(len / 2);
    if (len === 0) break;
  }
  return (a ^ crc2) >>> 0;
}
