import { ApiKeys } from "./api-keys.ts";
import { Buckets } from "./buckets.ts";
import { encodePath, encodeQuery } from "./encode.ts";
import { isErrorCode, SPEC_VERSION } from "./errors.generated.ts";
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
  anonymous?: boolean; // send unsigned even when the client has a key
  signal?: AbortSignal;
}

/** Client options. Without a key the client is anonymous: public reads only. publicUrl is the base presigned URLs are built on, for when browsers reach the server under another name than this client does; default url. */
export interface TunnaOptions {
  url: string;
  publicUrl?: string;
  key?: Key;
  fetch?: Fetch;
  now?: Now;
}

/** Options for upload: part size (default 8 MiB), parts in flight (default 8; each worker holds up to two parts), and a progress callback in bytes. Aborting the signal stops the parts in flight and aborts the session on the server. */
export interface UploadOptions extends CallOptions {
  partSize?: number;
  concurrency?: number;
  contentType?: string;
  metadata?: Record<string, string>;
  onProgress?: (sent: number, total: number) => void;
}

/** Options for presign; headers given here must be sent by whoever uses the URL. */
export interface PresignOptions {
  method: "GET" | "HEAD" | "PUT" | "DELETE";
  bucket: string;
  key: string;
  expiresIn: number;
  headers?: Record<string, string>;
}

/** GET /-/version as it is on the wire (spec/wire.md 4). */
interface WireServerVersion {
  version: string;
  spec: string;
}

/** The server's build and the spec it implements; compatible is whether that spec equals this package's SPEC_VERSION. */
export interface ServerVersion {
  version: string;
  spec: string;
  compatible: boolean;
}

/** Per-call options every method accepts. An aborted call rejects with the signal's reason (an AbortError by default), never a TransportError. */
export interface CallOptions {
  signal?: AbortSignal;
}

/** A tunna client: one base URL and one key, with the routes grouped as buckets, objects, apiKeys and uploads. */
export class Tunna {
  readonly objects: Objects;
  readonly buckets: Buckets;
  readonly apiKeys: ApiKeys;
  readonly uploads: Uploads;

  readonly #base: string;
  readonly #publicUrl: string;
  readonly #key: Key | undefined;
  readonly #now: Now;
  readonly #fetch: Fetch;

  constructor(options: TunnaOptions) {
    if (!options.url) throw new TypeError("TunnaOptions.url must not be empty");
    if (options.publicUrl === "")
      throw new TypeError("TunnaOptions.publicUrl must not be empty");
    if (options.key) {
      if (!options.key.id)
        throw new TypeError("TunnaOptions.key.id must not be empty");
      if (!options.key.secret)
        throw new TypeError("TunnaOptions.key.secret must not be empty");
    }

    this.#base = options.url.replace(/\/+$/, "");
    this.#publicUrl = (options.publicUrl ?? options.url).replace(/\/+$/, "");
    this.#key = options.key;
    this.#fetch = options.fetch ?? defaultFetch.bind(globalThis);
    this.#now = options.now ?? defaultNow;
    this.objects = new Objects(this);
    this.buckets = new Buckets(this);
    this.apiKeys = new ApiKeys(this);
    this.uploads = new Uploads(this);
  }

  /** Uploads a Blob or byte array as concurrent numbered parts (ADR-0001) and returns the object; an empty source is a single PUT. */
  async upload(
    bucket: string,
    key: string,
    source: Blob | Uint8Array<ArrayBuffer>,
    options?: UploadOptions,
  ): Promise<ObjectRecord> {
    const callOpts: CallOptions = {
      ...(options?.signal && { signal: options.signal }),
    };
    const size = source instanceof Blob ? source.size : source.byteLength;
    if (size === 0) {
      return await this.objects.put(bucket, key, source, options);
    }

    const {
      partSize = 8 << 20, // 8mb
      contentType,
      concurrency = 8,
      metadata,
      onProgress,
    } = options ?? {};

    const count = Math.ceil(size / partSize);
    const sessionOptions: UploadCreateOptions = {
      partSize,
      ...callOpts,
    };
    if (contentType) sessionOptions.contentType = contentType;
    if (metadata) sessionOptions.metadata = metadata;
    const session = await this.uploads.create(bucket, key, sessionOptions);

    let next = 1;
    let sent = 0;
    const checksums = new Array<string>(count);

    type Prepared = { n: number; bytes: Uint8Array<ArrayBuffer> };

    // prepare claims the next part number and reads its bytes. Each worker
    // keeps one part in flight and one being prepared, so the network never
    // waits on a disk read; that is the whole of the read-ahead.
    const prepare = async (): Promise<Prepared | undefined> => {
      const n = next++;
      if (n > count) return undefined;
      const start = (n - 1) * partSize;
      const end = Math.min(n * partSize, size);
      const bytes =
        source instanceof Blob
          ? new Uint8Array(await source.slice(start, end).arrayBuffer())
          : source.subarray(start, end);
      return { n, bytes };
    };

    const abortController = new AbortController();
    const signal = callOpts.signal
      ? AbortSignal.any([abortController.signal, callOpts.signal])
      : abortController.signal;
    const worker = async (): Promise<void> => {
      let current = await prepare();
      while (current !== undefined) {
        if (abortController.signal.aborted) return;
        const ahead = prepare();
        const { n, bytes } = current;
        let tries = 1;
        for (;;) {
          try {
            const part = await this.uploads.putPart(session.id, n, bytes, {
              signal,
            });
            checksums[n - 1] = part.checksum;
            sent += bytes.byteLength;
            onProgress?.(sent, size);
            break;
          } catch (err) {
            if (tries <= 2 && err instanceof TransportError) {
              tries++;
              continue;
            }
            abortController.abort();
            ahead.catch(() => {});
            throw err;
          }
        }
        current = await ahead;
      }
    };

    try {
      await Promise.all(
        Array.from({ length: Math.min(concurrency, count) }, () => worker()),
      );

      return await this.uploads.complete(session.id, count, {
        checksums,
        ...callOpts,
      });
    } catch (err) {
      try {
        await this.uploads.abort(session.id);
      } catch {}
      throw err;
    }
  }

  /** A presigned URL for one request, valid for expiresIn seconds. The URL is a credential: do not log it. */
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
      this.#publicUrl +
      encodePath([options.bucket, options.key]) +
      "?" +
      (await presignQuery(req, this.#key, expires))
    );
  }

  /** Resolves when the server answers GET /-/health. Sent unsigned, so it tells whether the server is up, not whether the key is good. */
  async health(options?: CallOptions): Promise<void> {
    await this.request({
      method: "GET",
      path: ["-", "health"],
      anonymous: true,
      ...(options?.signal && { signal: options.signal }),
    });
  }

  /** The server's version and spec, and whether this package speaks that spec. Sent unsigned. */
  async version(options?: CallOptions): Promise<ServerVersion> {
    const res = await this.request({
      method: "GET",
      path: ["-", "version"],
      anonymous: true,
      ...(options?.signal && { signal: options.signal }),
    });
    const body: WireServerVersion = await res.json();
    return {
      version: body.version,
      spec: body.spec,
      compatible: body.spec === SPEC_VERSION,
    };
  }

  /** @internal */
  async request(call: Call): Promise<Response> {
    let url = this.#base + encodePath(call.path);
    if (call.query) {
      url += `?${encodeQuery(call.query)}`;
    }
    const headers = new Headers(call.headers);
    if (this.#key && !call.anonymous) {
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
        ...(call.signal && { signal: call.signal }),
      });
    } catch (err) {
      if (call.signal?.aborted) throw err;
      throw new TransportError(`failed to fetch ${call.method} ${url}`, {
        cause: err,
      });
    }
    if (res.ok) {
      return res;
    }

    if (res.headers.get("Content-Type")?.includes("application/json")) {
      if (call.method === "HEAD" || res.body === null) {
        throw new TransportError(
          `${call.method} ${url}: ${res.status}; HEAD answers carry no error body, repeat as GET for the code`,
        );
      }

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
