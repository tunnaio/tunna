// Percent-encoding for the canonical request (spec/wire.md 3.2): encode
// once, RFC 3986 unreserved set, bytewise on UTF-8. Not encodeURIComponent,
// which leaves ! ' ( ) * alone. spec/vectors/encoding.json is the authority.

const UNRESERVED = new Uint8Array(256);
for (const range of ["AZ", "az", "09"]) {
  for (let b = range.charCodeAt(0); b <= range.charCodeAt(1); b++) {
    UNRESERVED[b] = 1;
  }
}
for (const ch of "-._~") UNRESERVED[ch.charCodeAt(0)] = 1;

const HEX = Array.from(
  { length: 256 },
  (_, b) => "%" + b.toString(16).toUpperCase().padStart(2, "0"),
);

/** Encodes one segment, or one query name or value: every byte outside A-Z a-z 0-9 - . _ ~ as %XX. */
export function encodeSegment(segment: string): string {
  let out = "";
  for (const b of new TextEncoder().encode(segment)) {
    out += UNRESERVED[b] ? String.fromCharCode(b) : HEX[b];
  }
  return out;
}

/** The canonical path: encoded segments joined with "/" and a leading "/"; no segments is "/". */
export function encodePath(segments: readonly string[]): string {
  const encoded = segments.map((s) => encodeSegment(s));
  return `/${encoded.join("/")}`;
}

type Param = { value: string; name: string };

/** The canonical query: pairs encoded, sorted bytewise by name then value, joined as name=value with "&"; no pairs is "". */
export function encodeQuery(
  pairs: readonly (readonly [string, string])[],
): string {
  const encoded: Param[] = [];
  for (const [name, value] of pairs) {
    encoded.push({ name: encodeSegment(name), value: encodeSegment(value) });
  }
  encoded.sort((a, b) => {
    if (a.name !== b.name) {
      return a.name > b.name ? 1 : -1;
    }
    if (a.value !== b.value) {
      return a.value > b.value ? 1 : -1;
    }
    return 0;
  });

  return encoded.map((p) => `${p.name}=${p.value}`).join("&");
}
