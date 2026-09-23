import { ApiKeys } from "./api/api-keys.ts";
import { Buckets } from "./api/buckets.ts";
import { Objects, type ObjectRecord } from "./api/objects.ts";
import type {
  ServerLimits,
  ServerVersion,
  WireServerLimits,
  WireServerVersion,
} from "./api/server.ts";
import {
  Uploads,
  type UploadCreateOptions,
  type UploadSession,
} from "./api/uploads.ts";
import { isErrorCode, SPEC_VERSION } from "./errors.generated.ts";
import { TransportError, TunnaError } from "./errors.ts";
import {
  assertPresignAllowed,
  type PresignableRequest,
  type PresignOptions,
  type PresignProvider,
  type PresignRequestOptions,
} from "./presign.ts";
import type { CallOptions, Fetch, Now, UploadProgress } from "./types.ts";
import { encodePath, encodeQuery } from "./wire/encode.ts";
import {
  authorization,
  HEADER_DATE,
  presignQuery,
  type Key,
  type SigningRequest,
} from "./wire/sign.ts";

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
  onUploadProgress?: UploadProgress;
}

/** How requests are authorized: signed with a key, presigned by a provider, or neither (an anonymous client). Never both. */
export type TunnaAuthOptions =
  | { key: Key; presign?: never }
  | { key?: never; presign: PresignProvider }
  | { key?: undefined; presign?: undefined };

/** The options every client takes, whatever authorizes its requests. */
export interface TunnaBaseOptions {
  url: string;
  publicUrl?: string;
  fetch?: Fetch;
  now?: Now;
}

/** Client options. With a key the client signs; with presign it asks a provider for each URL and holds no secret (a browser page); with neither it is anonymous: public reads only. publicUrl is the base presigned URLs are built on, for when browsers reach the server under another name than this client does; default url. */
export type TunnaOptions = TunnaBaseOptions & TunnaAuthOptions;

/** Options for upload: part size (default 8 MiB), parts in flight (default 8; each worker holds up to two parts), and a progress callback in bytes. Aborting the signal stops the parts in flight and aborts the session on the server. session is an upload session that already exists: required when the client has no key, since only a key can initiate one; its part size wins over partSize, and bucket and key are then the session's. An empty source is a single PUT and needs no session, so a given one is aborted rather than left to expire. */
export interface UploadOptions extends CallOptions {
  session?: UploadSession;
  partSize?: number;
  concurrency?: number;
  contentType?: string;
  metadata?: Record<string, string>;
  onProgress?: (sent: number, total: number) => void;
}

// boundHeaders is what a provider is told to bind: the headers the pipeline
// would have signed, with their values.
function boundHeaders(call: Call): Record<string, string> | undefined {
  if (!call.signedHeaders?.length || !call.headers) return undefined;
  const out: Record<string, string> = {};
  for (const name of call.signedHeaders) {
    const value = call.headers[name];
    if (value !== undefined) out[name] = value;
  }
  return out;
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
  readonly #presign: PresignProvider | undefined;

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
    if (options.key && options.presign)
      throw new TypeError(
        "TunnaOptions: give a key or a presign provider, not both",
      );

    this.#base = options.url.replace(/\/+$/, "");
    this.#publicUrl = (options.publicUrl ?? options.url).replace(/\/+$/, "");
    this.#key = options.key;
    this.#presign = options.presign;
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
    if (!this.#key && !options?.session) {
      throw new TypeError(
        "upload needs options.session when the client has no key: create the session on your backend (uploads.create) and pass it here",
      );
    }

    const callOpts: CallOptions = {
      ...(options?.signal && { signal: options.signal }),
    };
    const size = source instanceof Blob ? source.size : source.byteLength;
    if (size === 0) {
      try {
        return await this.objects.put(bucket, key, source, options);
      } finally {
        try {
          // An empty source needs no session; one handed in would sit unused
          // until it expires, and blocks deleting the bucket meanwhile.
          if (options?.session) {
            await this.uploads.abort(options.session.id);
          }
        } catch {}
      }
    }

    const {
      partSize: wantedPartSize = 8 << 20,
      contentType,
      concurrency = 8,
      metadata,
      onProgress,
    } = options ?? {};

    let session: UploadSession;
    if (options?.session) {
      session = options.session;
    } else {
      const sessionOptions: UploadCreateOptions = {
        partSize: wantedPartSize,
        ...callOpts,
      };
      if (contentType) sessionOptions.contentType = contentType;
      if (metadata) sessionOptions.metadata = metadata;
      session = await this.uploads.create(bucket, key, sessionOptions);
    }

    const partSize = session.partSize;
    const count = Math.ceil(size / partSize);

    let next = 1;
    let done = 0;
    let inFlight = new Map<number, number>();
    let reported = 0;
    const checksums = new Array<string>(count);

    type Prepared = { n: number; bytes: Uint8Array<ArrayBuffer> };

    function report() {
      let current = done;
      for (const loaded of inFlight.values()) current += loaded;
      if (current > reported) {
        reported = current;
        onProgress?.(reported, size);
      }
    }

    // prepare claims the next part number and reads its bytes. Each worker
    // keeps one part in flight and one being prepared, so the network never
    // waits on a disk read; that is the whole of the read-ahead.
    async function prepare(): Promise<Prepared | undefined> {
      const n = next++;
      if (n > count) return undefined;
      const start = (n - 1) * partSize;
      const end = Math.min(n * partSize, size);
      const bytes =
        source instanceof Blob
          ? new Uint8Array(await source.slice(start, end).arrayBuffer())
          : source.subarray(start, end);
      return { n, bytes };
    }

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
            // A retry starts its count over. Nothing reads the entry between
            // a failure and the retry today (no await in between), so this is
            // the invariant, not a fix: an entry is the current attempt's count.
            inFlight.set(n, 0);
            const part = await this.uploads.putPart(session.id, n, bytes, {
              signal,
              ...(options?.onProgress && {
                onProgress: (loaded) => {
                  inFlight.set(n, loaded);
                  report();
                },
              }),
            });
            inFlight.delete(n);
            done += bytes.byteLength;
            report();
            checksums[n - 1] = part.checksum;
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

  /** A presigned URL for one object request, valid for expiresIn seconds. With a key it is signed here; with a provider it is asked for, which is a network call, hence the signal. The URL is a credential: do not log it. */
  async presign(options: PresignOptions): Promise<string> {
    options.signal?.throwIfAborted();

    if (this.#presign) {
      const asked: PresignableRequest = {
        method: options.method,
        path: [options.bucket, options.key],
      };
      if (options.headers) asked.headers = options.headers;
      return this.#presign(
        asked,
        options.signal ? { signal: options.signal } : {},
      );
    }

    const { bucket, key, signal: _signal, ...rest } = options;
    return this.presignRequest({ ...rest, path: [bucket, key] });
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

  /** The server's limits, for choosing a valid partSize or presign lifetime without hardcoding a default. Sent unsigned; fields a newer server adds are dropped. */
  async limits(options?: CallOptions): Promise<ServerLimits> {
    const res = await this.request({
      method: "GET",
      path: ["-", "limits"],
      anonymous: true,
      ...(options?.signal && { signal: options.signal }),
    });
    const body: WireServerLimits = await res.json();
    return {
      clockSkewSeconds: body.clock_skew_seconds,
      keyLengthMax: body.key_length_max,
      listLimitMax: body.list_limit_max,
      objectSizeMax: body.object_size_max,
      partSizeMax: body.part_size_max,
      partSizeMin: body.part_size_min,
      partsMax: body.parts_max,
      presignLifetimeMaxSeconds: body.presign_lifetime_max_seconds,
      uploadExpirySeconds: body.upload_expiry_seconds,
    };
  }

  /** A presigned URL for any request the server accepts in presigned form (spec/wire.md 3.4): the backend half of a presign provider. Needs a key. Refuses the routes that take their parameters from a body, which a URL cannot bind. The URL is a credential: do not log it. */
  async presignRequest(options: PresignRequestOptions): Promise<string> {
    if (!this.#key) {
      throw new TypeError("presign requires authentication");
    }
    assertPresignAllowed(options.method, options.path);

    const expires = this.#now() + options.expiresIn;
    const req: SigningRequest = {
      method: options.method,
      path: options.path,
    };
    if (options.headers) {
      req.headers = options.headers;
      req.signedHeaders = Object.keys(options.headers);
    }
    if (options.query) {
      req.query = options.query;
    }

    return (
      this.#publicUrl +
      encodePath(options.path) +
      "?" +
      (await presignQuery(req, this.#key, expires))
    );
  }

  /** @internal */
  async request(call: Call): Promise<Response> {
    const provided = this.#presign !== undefined && !call.anonymous;
    let url: string;
    if (provided) {
      const checksum = call.headers?.["X-Tunna-Checksum"];
      const checksumQuery: [string, string][] = [];
      if (checksum) {
        checksumQuery.push(["checksum", checksum]);
      }

      const asked: PresignableRequest = {
        method: call.method,
        path: call.path,
      };
      if (call.query) {
        asked.query = [...call.query, ...checksumQuery];
      } else if (checksumQuery.length > 0) {
        asked.query = checksumQuery;
      }
      const bound = boundHeaders(call);
      if (bound) delete bound["X-Tunna-Checksum"];
      if (bound && Object.keys(bound).length > 0) {
        asked.headers = bound;
      }
      url = await this.#presign(
        asked,
        call.signal ? { signal: call.signal } : {},
      );
    } else {
      url = this.#base + encodePath(call.path);
      if (call.query) {
        url += `?${encodeQuery(call.query)}`;
      }
    }

    const headers = new Headers(call.headers);
    if (provided) {
      headers.delete("X-Tunna-Checksum");
    }
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
        ...(call.onUploadProgress && {
          onUploadProgress: call.onUploadProgress,
        }),
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

    if (call.method === "HEAD" || res.body === null) {
      const code = res.headers.get("X-Tunna-Error");
      if (code === null) {
        throw new TransportError(
          `${call.method} ${url}: ${res.status}; no error body and no X-Tunna-Error header (server older than 0.1.0-alpha.4?), repeat as GET for the code`,
        );
      }
      if (!isErrorCode(code)) {
        throw new TransportError(
          `unexpected error code ${code} (${res.status})`,
        );
      }
      throw new TunnaError(code, res.status, `${call.method} ${url}: ${code}`);
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
