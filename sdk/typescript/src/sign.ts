// The TUNNA1 request signature (spec/wire.md 3, ADR-0003): HMAC-SHA256
// over a seven-line canonical request, in header form (timestamp, skew
// window) and presigned form (absolute expiry, query parameters). The
// vector file spec/vectors/signing.json is the authority.
//
// HMAC comes from crypto.subtle, so every signing function is asynchronous.

/** A key id and its secret. The secret never leaves the process. */
export interface Key {
  id: string;
  secret: string;
}

/** The parts of a request that the signature binds. */
export interface Request {
  method: string;
  /** Decoded path segments; the signer encodes them. */
  path: readonly string[];
  /** Decoded query pairs, order irrelevant; the signer sorts and encodes. */
  query?: readonly (readonly [string, string])[];
  /** Header values by name, any case; only those listed in signedHeaders are bound. */
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

/** The seven-line canonical request; time is the timestamp (header) or the expiry (presign). */
export function canonical(req: Request, key: Key, time: number, mode: Mode): string {
  throw new Error("not implemented");
}

/** Lowercase hex HMAC-SHA256 of the canonical string under the secret. */
export async function signature(secret: string, canonicalString: string): Promise<string> {
  throw new Error("not implemented");
}

/** The Authorization header value for header form, at the given Unix time. */
export async function authorization(req: Request, key: Key, time: number): Promise<string> {
  throw new Error("not implemented");
}

/** The full query string for presigned form, expiring at the given Unix time; x-tunna-sig last. */
export async function presignQuery(req: Request, key: Key, expires: number): Promise<string> {
  throw new Error("not implemented");
}
