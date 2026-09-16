import type { ErrorCode } from "./errors.generated.ts";

/** The server answered with an error body: code from spec/errors.json, its status, message and details. */
export class TunnaError extends Error {
  readonly code: ErrorCode;
  readonly status: number;
  readonly details: Readonly<Record<string, unknown>> | undefined;

  constructor(
    code: ErrorCode,
    status: number,
    message: string,
    details?: Readonly<Record<string, unknown>>,
    options?: { cause?: unknown },
  ) {
    super(message, options);
    this.name = "TunnaError";
    this.code = code;
    this.status = status;
    this.details = details;
  }
}

/** No answer from the server, or one that is not the contract: a failed fetch (see cause), a proxy page, an unknown code. */
export class TransportError extends Error {
  constructor(message: string, options?: { cause?: unknown }) {
    super(message, options);
    this.name = "TransportError";
  }
}
