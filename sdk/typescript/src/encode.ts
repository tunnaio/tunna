// Percent-encoding for the canonical request (spec/wire.md 3.2, ADR-0003):
// encode once, RFC 3986 unreserved set, applied to decoded values. The
// vector file spec/vectors/encoding.json is the authority for every edge.

/** Encodes one path segment: every byte outside A-Z a-z 0-9 - . _ ~ as %XX, uppercase hex, UTF-8. */
export function encodeSegment(segment: string): string {
  throw new Error("not implemented");
}

/** Joins encoded segments with "/" and a leading "/"; no segments is "/". */
export function encodePath(segments: readonly string[]): string {
  throw new Error("not implemented");
}

/** Sorts pairs by name then value, encodes both sides as segments, joins with "=" and "&"; no pairs is "". */
export function encodeQuery(pairs: readonly (readonly [string, string])[]): string {
  throw new Error("not implemented");
}
