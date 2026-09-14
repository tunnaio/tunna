// Public surface of the tunna client (ADR-0010). The client itself comes
// after the primitives; until then the package exports the primitives and
// the generated spec constants.

export { SPEC_VERSION, ERROR_STATUS, ERROR_STAGE, isErrorCode } from "./errors.generated.ts";
export type { ErrorCode } from "./errors.generated.ts";

export * as encode from "./encode.ts";
export * as sign from "./sign.ts";
export * as crc32c from "./crc32c.ts";
export type { Key, Request, Mode } from "./sign.ts";
