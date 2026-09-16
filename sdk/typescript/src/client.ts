import { Buckets } from "./buckets.ts";
import { encodePath, encodeQuery } from "./encode.ts";
import { isErrorCode } from "./errors.generated.ts";
import { TransportError, TunnaError } from "./errors.ts";
import { authorization, HEADER_DATE, type Key } from "./sign.ts";

type Fetch = (
  input: string | URL | Request,
  init?: RequestInit,
) => Promise<Response>;
type Now = () => number;

function defaultNow() {
  return Math.floor(Date.now() / 1000);
}

const defaultFetch = fetch;

interface Call {
  method: string;
  path: readonly string[];
  query?: readonly (readonly [string, string])[];
  headers?: Record<string, string>;
  signedHeaders?: readonly string[];
  body?: BodyInit | null;
}

export interface TunnaOptions {
  url: string;
  key?: Key;
  fetch?: Fetch;
  now?: Now;
}

export class Tunna {
  readonly buckets: Buckets;

  readonly #base: string;
  readonly #key: Key | undefined;
  readonly #now: Now;
  readonly #fetch: Fetch;

  constructor(options: TunnaOptions) {
    this.#base = options.url.replace(/\/+$/, "");
    this.#key = options.key;
    this.#fetch = options.fetch ?? defaultFetch.bind(globalThis);
    this.#now = options.now ?? defaultNow;
    this.buckets = new Buckets(this);
  }

  /** @internal */
  async request(call: Call): Promise<Response> {
    let url = this.#base + encodePath(call.path);
    if (call.query) {
      url += `?${encodeQuery(call.query)}`;
    }
    const headers = new Headers(call.headers);
    if (this.#key) {
      const now = this.#now();
      headers.set(HEADER_DATE, now.toString());
      headers.set("Authorization", await authorization(call, this.#key, now));
    }
    let res: Response;
    try {
      res = await this.#fetch(url, {
        method: call.method,
        body: call.body ?? null,
        headers,
        ...(call.body instanceof ReadableStream ? { duplex: "half" } : {}),
      });
    } catch (err) {
      throw new TransportError(`failed to fetch ${call.method} ${url}`, {
        cause: err,
      });
    }
    if (res.ok) {
      return res;
    }

    if (res.headers.get("Content-Type")?.includes("application/json")) {
      const body = await res.json();
      const code = body?.error?.code;
      if (!isErrorCode(code)) {
        throw new TransportError(
          `unexpected error code ${String(code)} (${res.status})`,
        );
      }
      throw new TunnaError(
        code,
        res.status,
        body.error.message,
        body.error.details,
      );
    }
    throw new TransportError(`unexpected ${res.status} response`);
  }
}
