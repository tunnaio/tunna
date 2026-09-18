// Generated from spec/errors.json and spec/VERSION by scripts/gen-errors.ts.
// Do not edit; run `bun run gen`.

/** The wire contract version this package implements; compare with GET /-/version. */
export const SPEC_VERSION = "0.1.0-draft";

/** Every error code the server can answer with (spec/wire.md 9). */
export type ErrorCode =
  | "malformed_request"
  | "unknown_route"
  | "method_not_allowed"
  | "unauthenticated"
  | "unknown_key"
  | "bad_signature"
  | "missing_signed_header"
  | "clock_skew"
  | "presign_expired"
  | "presign_too_long"
  | "forbidden"
  | "invalid_bucket_name"
  | "invalid_key"
  | "invalid_parameter"
  | "invalid_part_size"
  | "invalid_part_number"
  | "session_not_active"
  | "body_too_large"
  | "body_length_mismatch"
  | "checksum_mismatch"
  | "bucket_not_found"
  | "object_not_found"
  | "upload_not_found"
  | "key_not_found"
  | "bucket_exists"
  | "bucket_not_empty"
  | "upload_incomplete"
  | "part_size_mismatch"
  | "internal"
  | "unavailable";

/** HTTP status for each code. */
export const ERROR_STATUS: Readonly<Record<ErrorCode, number>> = {
  malformed_request: 400,
  unknown_route: 404,
  method_not_allowed: 405,
  unauthenticated: 401,
  unknown_key: 401,
  bad_signature: 401,
  missing_signed_header: 401,
  clock_skew: 401,
  presign_expired: 401,
  presign_too_long: 401,
  forbidden: 403,
  invalid_bucket_name: 422,
  invalid_key: 422,
  invalid_parameter: 422,
  invalid_part_size: 422,
  invalid_part_number: 422,
  session_not_active: 422,
  body_too_large: 413,
  body_length_mismatch: 422,
  checksum_mismatch: 422,
  bucket_not_found: 404,
  object_not_found: 404,
  upload_not_found: 404,
  key_not_found: 404,
  bucket_exists: 409,
  bucket_not_empty: 409,
  upload_incomplete: 409,
  part_size_mismatch: 409,
  internal: 500,
  unavailable: 503,
};

/** A stage of the request ladder (ADR-0004): 1 syntax, 2 authentication, 3 authorization, 4 validation, 5 body, 6 state. */
export type ErrorStage = 1 | 2 | 3 | 4 | 5 | 6;

/** The spec's name for a stage. */
export type ErrorStageName = "syntax" | "authentication" | "authorization" | "validation" | "body" | "state";

/** The name of each stage, for messages and grouping. */
export const ERROR_STAGE_NAME: Readonly<Record<ErrorStage, ErrorStageName>> = {
  1: "syntax",
  2: "authentication",
  3: "authorization",
  4: "validation",
  5: "body",
  6: "state",
};

/** The ladder stage each code is answered at. */
export const ERROR_STAGE: Readonly<Record<ErrorCode, ErrorStage>> = {
  malformed_request: 1,
  unknown_route: 1,
  method_not_allowed: 1,
  unauthenticated: 2,
  unknown_key: 2,
  bad_signature: 2,
  missing_signed_header: 2,
  clock_skew: 2,
  presign_expired: 2,
  presign_too_long: 2,
  forbidden: 3,
  invalid_bucket_name: 4,
  invalid_key: 4,
  invalid_parameter: 4,
  invalid_part_size: 4,
  invalid_part_number: 4,
  session_not_active: 4,
  body_too_large: 5,
  body_length_mismatch: 5,
  checksum_mismatch: 5,
  bucket_not_found: 6,
  object_not_found: 6,
  upload_not_found: 6,
  key_not_found: 6,
  bucket_exists: 6,
  bucket_not_empty: 6,
  upload_incomplete: 6,
  part_size_mismatch: 6,
  internal: 6,
  unavailable: 6,
};

/** Type guard for a string off the wire. */
export function isErrorCode(s: string): s is ErrorCode {
  return Object.hasOwn(ERROR_STATUS, s);
}
