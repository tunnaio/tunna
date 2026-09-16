export {
  SPEC_VERSION,
  ERROR_STATUS,
  ERROR_STAGE,
  isErrorCode,
} from "./errors.generated.ts";
export type { ErrorCode } from "./errors.generated.ts";

export * as encode from "./encode.ts";
export * as sign from "./sign.ts";
export * as crc32c from "./crc32c.ts";
export type { Key, Request, Mode } from "./sign.ts";
export { Tunna } from "./client.ts";
export type { TunnaOptions } from "./client.ts";
export { TunnaError, TransportError } from "./errors.ts";
export type { BucketRecord as Bucket } from "./buckets.ts";
