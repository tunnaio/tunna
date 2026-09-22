// The anonymous control-plane reads: what the server is and what it allows.

/** GET /-/version as it is on the wire (spec/wire.md 4). */
export interface WireServerVersion {
  version: string;
  spec: string;
}

/** The server's build and the spec it implements; compatible is whether that spec equals this package's SPEC_VERSION. */
export interface ServerVersion {
  version: string;
  spec: string;
  compatible: boolean;
}

/** GET /-/limits as it is on the wire (spec/wire.md 11.1). */
export interface WireServerLimits {
  object_size_max: number;
  part_size_min: number;
  part_size_max: number;
  parts_max: number;
  list_limit_max: number;
  key_length_max: number;
  presign_lifetime_max_seconds: number;
  upload_expiry_seconds: number;
  clock_skew_seconds: number;
}

/** This deployment's limits: sizes in bytes, durations in seconds. They are what the server enforces, so an operator's change shows here. */
export interface ServerLimits {
  objectSizeMax: number;
  partSizeMin: number;
  partSizeMax: number;
  partsMax: number;
  listLimitMax: number;
  keyLengthMax: number;
  presignLifetimeMaxSeconds: number;
  uploadExpirySeconds: number;
  clockSkewSeconds: number;
}
