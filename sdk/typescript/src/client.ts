import { ApiKeys } from "./api-keys.ts";
import { Buckets } from "./buckets.ts";
import { encodePath, encodeQuery } from "./encode.ts";
import { isErrorCode } from "./errors.generated.ts";
import { TransportError, TunnaError } from "./errors.ts";
import { Objects, type ObjectRecord } from "./objects.ts";
import {
  authorization,
  HEADER_DATE,
  presignQuery,
  type Key,
  type SigningRequest,
} from "./sign.ts";
import { Uploads, type UploadCreateOptions } from "./uploads.ts";

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

export interface UploadOptions {
  partSize?: number;
  concurrency?: number;
  contentType?: string;
  metadata?: Record<string, string>;
  onProgress?: (sent: number, total: number) => void;
}

export interface PresignOptions {
  method: "GET" | "HEAD" | "PUT" | "DELETE";
  bucket: string;
  key: string;
  expiresIn: number;
  headers?: Record<string, string>;
}

export class Tunna {
  readonly objects: Objects;
  readonly buckets: Buckets;
  readonly apiKeys: ApiKeys;
  readonly uploads: Uploads;

  readonly #base: string;
  readonly #key: Key | undefined;
  readonly #now: Now;
  readonly #fetch: Fetch;

  constructor(options: TunnaOptions) {
    this.#base = options.url.replace(/\/+$/, "");
    this.#key = options.key;
    this.#fetch = options.fetch ?? defaultFetch.bind(globalThis);
    this.#now = options.now ?? defaultNow;
    this.objects = new Objects(this);
    this.buckets = new Buckets(this);
    this.apiKeys = new ApiKeys(this);
    this.uploads = new Uploads(this);
  }

  async upload(
    bucket: string,
    key: string,
    source: Blob | Uint8Array<ArrayBuffer>,
    options?: UploadOptions,
  ): Promise<ObjectRecord> {
    const size = source instanceof Blob ? source.size : source.byteLength;
    if (size === 0) {
      return await this.objects.put(bucket, key, source, options);
    }

    const {
      partSize = 8 << 20, // 8mb
      contentType,
      concurrency = 4,
      metadata,
      onProgress,
    } = options ?? {};

    const count = Math.ceil(size / partSize);
    const sessionOptions: UploadCreateOptions = {
      partSize,
    };
    if (contentType) sessionOptions.contentType = contentType;
    if (metadata) sessionOptions.metadata = metadata;
    const session = await this.uploads.create(bucket, key, sessionOptions);

    let next = 1;
    let sent = 0;
    const checksums = new Array<string>(count);

    async function slice(start: number, end: number) {
      if (source instanceof Blob) {
        return new Uint8Array(await source.slice(start, end).arrayBuffer());
      }
      return source.subarray(start, end);
    }

    const abortController = new AbortController();
    const worker = async (): Promise<void> => {
      for (;;) {
        if (abortController.signal.aborted) {
          return;
        }
        const n = next++;
        if (n > count) return;
        const start = (n - 1) * partSize;
        const end = Math.min(n * partSize, size);
        const bytes = await slice(start, end);
        let tries = 1;
        while (true) {
          try {
            const part = await this.uploads.putPart(session.id, n, bytes);
            checksums[n - 1] = part.checksum;
            sent += end - start;
            onProgress?.(sent, size);
            break;
          } catch (err) {
            if (tries <= 2 && err instanceof TransportError) {
              tries++;
              continue;
            }
            abortController.abort();
            throw err;
          }
        }
      }
    };

    try {
      await Promise.all(
        Array.from({ length: Math.min(concurrency, count) }, () => worker()),
      );

      return await this.uploads.complete(session.id, count, checksums);
    } catch (err) {
      try {
        await this.uploads.abort(session.id);
      } catch {}
      throw err;
    }
  }

  async presign(options: PresignOptions): Promise<string> {
    if (!this.#key) {
      throw new TypeError("presign requires authentication");
    }

    const expires = this.#now() + options.expiresIn;
    const req: SigningRequest = {
      method: options.method,
      path: [options.bucket, options.key],
    };
    if (options.headers) {
      req.headers = options.headers;
      req.signedHeaders = Object.keys(options.headers);
    }

    return (
      this.#base +
      encodePath([options.bucket, options.key]) +
      "?" +
      (await presignQuery(req, this.#key, expires))
    );
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
