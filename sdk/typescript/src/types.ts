// The types every part of the client shares.

/** Per-call options every method accepts. An aborted call rejects with the signal's reason (an AbortError by default), never a TransportError. */
export interface CallOptions {
  signal?: AbortSignal;
}

export type Fetch = (
  input: string | URL | Request,
  init?: RequestInit,
) => Promise<Response>;

export type Now = () => number;
