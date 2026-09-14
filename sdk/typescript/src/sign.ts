// The TUNNA1 request signature (spec/wire.md 3): HMAC-SHA256 over the
// seven-line canonical request, in header and presigned form. Async because
// HMAC comes from crypto.subtle. spec/vectors/signing.json is the authority.

import { encodePath, encodeQuery } from "./encode.ts";

/** A key id and its secret. */
export interface Key {
  id: string;
  secret: string;
}

/** The parts of a request the signature binds. Path, query and headers are decoded; the signer encodes. */
export interface Request {
  method: string;
  path: readonly string[];
  query?: readonly (readonly [string, string])[];
  headers?: Readonly<Record<string, string>>;
  /** Names of headers to bind, any case. */
  signedHeaders?: readonly string[];
}

export type Mode = "header" | "presign";

export const SCHEME = "TUNNA1";
export const ALGORITHM = "TUNNA1-HMAC-SHA256";
export const PARAM_KEY = "x-tunna-key";
export const PARAM_EXPIRES = "x-tunna-expires";
export const PARAM_SIG = "x-tunna-sig";
export const PARAM_HEADERS = "x-tunna-headers";
export const HEADER_DATE = "X-Tunna-Date";

function signedHeaderNames(req: Request): string[] {
  return (req.signedHeaders ?? []).map((h) => h.toLowerCase()).sort();
}

function headerValue(headers: Readonly<Record<string, string>> | undefined, name: string): string | undefined {
  if (!headers) return undefined;
  for (const [k, v] of Object.entries(headers)) {
    if (k.toLowerCase() === name) return v;
  }
  return undefined;
}

/** The caller's query with the three presign parameters set, replacing any of the same name. */
function presignPairs(req: Request, key: Key, expires: number): (readonly [string, string])[] {
  const own = new Set([PARAM_KEY, PARAM_EXPIRES, PARAM_HEADERS]);
  const pairs = (req.query ?? []).filter(([name]) => !own.has(name));
  pairs.push([PARAM_KEY, key.id], [PARAM_EXPIRES, String(expires)], [PARAM_HEADERS, signedHeaderNames(req).join(";")]);
  return pairs;
}

function collapse(value: string): string {
  return value.trim().split(/\s+/).join(" ");
}

/** The canonical request; time is the timestamp in header mode, the expiry in presign mode. */
export function canonical(req: Request, key: Key, time: number, mode: Mode): string {
  const names = signedHeaderNames(req);

  const block: string[] = [];
  for (const name of names) {
    const value = headerValue(req.headers, name);
    if (value === undefined) continue;
    block.push(`${name}:${collapse(value)}`);
  }

  const query = mode === "header" ? encodeQuery(req.query ?? []) : encodeQuery(presignPairs(req, key, time));

  return [SCHEME, req.method, encodePath(req.path), query, names.join(";"), block.join("\n"), String(time)].join("\n");
}

/** Lowercase hex HMAC-SHA256 of the canonical string. */
export async function signature(secret: string, canonicalString: string): Promise<string> {
  const enc = new TextEncoder();
  const cryptoKey = await crypto.subtle.importKey("raw", enc.encode(secret), { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  const mac = await crypto.subtle.sign("HMAC", cryptoKey, enc.encode(canonicalString));
  return Array.from(new Uint8Array(mac), (b) => b.toString(16).padStart(2, "0")).join("");
}

/** The Authorization header value at the given Unix time. */
export async function authorization(req: Request, key: Key, time: number): Promise<string> {
  const sig = await signature(key.secret, canonical(req, key, time, "header"));
  return `${ALGORITHM} key=${key.id}, headers=${signedHeaderNames(req).join(";")}, sig=${sig}`;
}

/** The presigned query string, expiring at the given Unix time; x-tunna-sig is last, outside the sort. */
export async function presignQuery(req: Request, key: Key, expires: number): Promise<string> {
  const sig = await signature(key.secret, canonical(req, key, expires, "presign"));
  return `${encodeQuery(presignPairs(req, key, expires))}&${PARAM_SIG}=${sig}`;
}
