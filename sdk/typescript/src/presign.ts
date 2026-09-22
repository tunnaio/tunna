// Presigned URLs: the types of both halves of a presign provider (ADR-0013)
// and the rule for which requests may be presigned at all.

import type { CallOptions } from "./types.ts";

/** What the pipeline would otherwise sign: the provider's backend passes it to presignRequest. */
export interface PresignableRequest {
  method: string;
  path: readonly string[];
  query?: readonly (readonly [string, string])[];
  headers?: Record<string, string>; // the headers to bind, with their values
}

/** How a client without a key gets its URLs: asked once per request, it returns a presigned URL, typically from the application's backend (presignRequest there). What it throws reaches the caller unchanged. ADR-0013. */
export type PresignProvider = (
  request: PresignableRequest,
  options: CallOptions,
) => Promise<string>;

/** presignRequest options: the request, and how long the URL lives, in seconds. */
export interface PresignRequestOptions extends PresignableRequest {
  expiresIn: number;
}

/** Options for presign; headers given here must be sent by whoever uses the URL. With a provider, expiresIn is not passed on: how long a URL lives is the backend's decision. */
export interface PresignOptions extends CallOptions {
  method: "GET" | "HEAD" | "PUT" | "DELETE";
  bucket: string;
  key: string;
  expiresIn: number;
  headers?: Record<string, string>;
}

type Rule = readonly [method: string, path: readonly string[]];

// The routes that take their parameters from a JSON body, which a presigned
// URL cannot bind; the server refuses them too (ADR-0013).
const HEADER_FORM_ONLY: readonly Rule[] = [
  ["POST", ["-", "uploads"]],
  ["PUT", ["-", "buckets", "*"]],
  ["PATCH", ["-", "buckets", "*"]],
  ["POST", ["-", "keys"]],
  ["PATCH", ["-", "keys", "*"]],
];

function matchesPattern(
  path: readonly string[],
  pattern: readonly string[],
): boolean {
  return (
    path.length === pattern.length &&
    pattern.every((seg, i) => seg === "*" || seg === path[i])
  );
}

function presignAllowed(method: string, path: readonly string[]): boolean {
  const m = method.toUpperCase();
  return !HEADER_FORM_ONLY.some(
    ([ruleMethod, pattern]) =>
      (ruleMethod === "*" || ruleMethod === m) && matchesPattern(path, pattern),
  );
}

/** Throws a TypeError for a request the server would answer presign_not_allowed, so a backend finds out when it is written. */
export function assertPresignAllowed(
  method: string,
  path: readonly string[],
): void {
  if (!presignAllowed(method, path)) {
    throw new TypeError(
      `Presigning is not supported for ${method.toUpperCase()} /${path.join("/")}; use the header form instead`,
    );
  }
}
