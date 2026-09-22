export {
  SPEC_VERSION,
  ERROR_STATUS,
  ERROR_STAGE,
  ERROR_STAGE_NAME,
  isErrorCode,
} from "./errors.generated.ts";
export type {
  ErrorCode,
  ErrorStage,
  ErrorStageName,
} from "./errors.generated.ts";

export * as encode from "./wire/encode.ts";
export * as sign from "./wire/sign.ts";
export * as crc32c from "./wire/crc32c.ts";
export type { Key, SigningRequest, Mode } from "./wire/sign.ts";
export { Tunna } from "./client.ts";
export type {
  TunnaOptions,
  TunnaBaseOptions,
  TunnaAuthOptions,
  UploadOptions,
} from "./client.ts";
export type { CallOptions } from "./types.ts";
export type {
  PresignOptions,
  PresignProvider,
  PresignRequestOptions,
  PresignableRequest,
} from "./presign.ts";
export type { ServerVersion, ServerLimits } from "./api/server.ts";
export { TunnaError, TransportError } from "./errors.ts";
export type { BucketRecord } from "./api/buckets.ts";
export { withSecret } from "./api/api-keys.ts";
export type {
  Access,
  ApiKey,
  AdminApiKey,
  ScopedApiKey,
  ApiKeyCreateOptions,
  ApiKeyPatchOptions,
  WithSecret,
} from "./api/api-keys.ts";
export type {
  ObjectRecord,
  ObjectInfo,
  ObjectResponse,
  ObjectPutOptions,
  ObjectGetOptions,
  ObjectListOptions,
  ObjectPage,
  ObjectPageOptions,
} from "./api/objects.ts";
export type {
  UploadSession,
  UploadPart,
  UploadCreateOptions,
} from "./api/uploads.ts";
