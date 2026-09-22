import type { Tunna } from "../client.ts";
import type { CallOptions, UploadProgress } from "../types.ts";
import { crc32c, encodeChecksum } from "../wire/crc32c.ts";
import { TransportError } from "../errors.ts";

/** The object record as it is on the wire (spec/wire.md 5.3). */
export interface WireObject {
  bucket: string;
  key: string;
  size: number;
  content_type: string;
  checksum: string;
  created_at: number;
  metadata?: Record<string, string>;
}

interface WireListObjects {
  objects: WireObject[];
  next?: string;
}

/** An object's metadata; metadata is {} when the object has none. */
export interface ObjectRecord {
  bucket: string;
  key: string;
  size: number;
  contentType: string;
  checksum: string;
  createdAt: Date;
  metadata: Record<string, string>;
}

/** A GET result: the parsed headers, the body stream, and the Response for callers who want json() or arrayBuffer(). */
export interface ObjectResponse {
  response: Response;
  body: ReadableStream<Uint8Array<ArrayBuffer>>;
  size: number;
  contentType: string;
  etag: string;
  checksum: string;
  lastModified: Date;
  metadata: Record<string, string>;
}

/** Content type (default application/octet-stream) and user metadata for a PUT. */
export interface ObjectPutOptions extends CallOptions {
  contentType?: string;
  metadata?: Record<string, string>;
  onProgress?: UploadProgress;
}

/** A byte range for GET: start inclusive, end inclusive when given. */
export interface ObjectGetOptions extends CallOptions {
  range?: { start: number; end?: number };
}

/** Listing filters: keys starting with prefix. limit is the page size (server default 1000), not a total: list still yields every object. */
export interface ObjectListOptions extends CallOptions {
  prefix?: string;
  limit?: number;
}

/** The listing filters plus a cursor: after is the next of the previous page. */
export interface ObjectPageOptions extends ObjectListOptions {
  after?: string;
}

/** What a GET or HEAD reports in headers; size is the whole object's even for a range. */
export interface ObjectInfo {
  size: number;
  contentType: string;
  etag: string;
  checksum: string;
  lastModified: Date;
  metadata: Record<string, string>;
}

/** One page of a listing, in key order; next is present only when there are more objects. */
export interface ObjectPage {
  objects: ObjectRecord[];
  next?: string;
}

function parseHeaders(headers: Headers): ObjectInfo {
  const contentRange = headers.get("Content-Range");
  const size = contentRange
    ? Number(contentRange.slice(contentRange.lastIndexOf("/") + 1))
    : Number(requiredHeader(headers, "Content-Length"));
  const contentType = requiredHeader(headers, "Content-Type");
  const etag = requiredHeader(headers, "ETag");
  const checksum = requiredHeader(headers, "X-Tunna-Checksum");
  const lastModified = new Date(requiredHeader(headers, "Last-Modified"));

  const prefix = "x-tunna-meta-";
  const metadata: Record<string, string> = {};
  for (const [k, v] of headers) {
    if (!k.startsWith(prefix)) continue;
    metadata[k.slice(prefix.length)] = v;
  }

  return {
    size,
    checksum,
    contentType,
    etag,
    lastModified,
    metadata,
  };
}

function requiredHeader(headers: Headers, name: string): string {
  const v = headers.get(name);
  if (v === null) throw new TransportError(`response is missing ${name}`);
  return v;
}

export function toObject(o: WireObject): ObjectRecord {
  return {
    bucket: o.bucket,
    key: o.key,
    size: o.size,
    contentType: o.content_type,
    checksum: o.checksum,
    createdAt: new Date(o.created_at * 1000),
    metadata: o.metadata ?? {},
  };
}

async function toBytes(
  body: Uint8Array | ArrayBuffer | Blob | string,
): Promise<Uint8Array<ArrayBuffer>> {
  if (typeof body === "string") {
    return new TextEncoder().encode(body);
  }
  if (body instanceof Blob) {
    return new Uint8Array(await body.arrayBuffer());
  }
  if (body instanceof ArrayBuffer) {
    return new Uint8Array(body);
  }
  return body.buffer instanceof ArrayBuffer
    ? (body as Uint8Array<ArrayBuffer>)
    : new Uint8Array(body);
}

function computeChecksum(data: Uint8Array) {
  return encodeChecksum(crc32c(data));
}

/** The object routes (spec/wire.md 5 and 7). Reads on a public bucket need no key. */
export class Objects {
  readonly #client: Tunna;

  constructor(client: Tunna) {
    this.#client = client;
  }

  /** Stores the body as one request with a signed CRC32C; a stream is refused, use upload for those. Replaces an existing key. */
  async put(
    bucket: string,
    key: string,
    body: Uint8Array | Blob | string | ReadableStream<Uint8Array>,
    options?: ObjectPutOptions,
  ): Promise<ObjectRecord> {
    const isStream = body instanceof ReadableStream;
    if (isStream) {
      throw new TypeError("streams are not yet supported");
    }

    const headers: Record<string, string> = {
      "Content-Type": options?.contentType ?? "application/octet-stream",
    };
    if (options?.metadata) {
      for (const [k, v] of Object.entries(options.metadata)) {
        headers[`X-Tunna-Meta-${k}`] = v;
      }
    }

    const bytes = await toBytes(body);
    const checksum = computeChecksum(bytes);
    headers["X-Tunna-Checksum"] = checksum;

    const res = await this.#client.request({
      method: "PUT",
      path: [bucket, key],
      body: bytes,
      headers,
      signedHeaders: Object.keys(headers),
      ...(options?.signal && { signal: options.signal }),
      ...(options?.onProgress && { onUploadProgress: options.onProgress }),
    });
    const record: WireObject = await res.json();
    return toObject(record);
  }

  /** Fetches an object, optionally a byte range; the body is a stream the caller consumes. */
  async get(
    bucket: string,
    key: string,
    options?: ObjectGetOptions,
  ): Promise<ObjectResponse> {
    const headers: Record<string, string> = {};
    if (options?.range) {
      let val = `bytes=${options.range.start}-`;
      if (options.range.end !== undefined) {
        val += options.range.end;
      }
      headers["Range"] = val;
    }
    const res = await this.#client.request({
      method: "GET",
      path: [bucket, key],
      headers,
      ...(options?.signal && { signal: options.signal }),
    });

    if (!res.body) {
      throw new TransportError("response has no body");
    }

    return {
      response: res,
      body: res.body,
      ...parseHeaders(res.headers),
    };
  }

  /** The object's headers without its body. A failure is a TunnaError like any other: the code comes from the X-Tunna-Error header, since a HEAD answer has no body (so no server message or details). */
  async head(
    bucket: string,
    key: string,
    options?: CallOptions,
  ): Promise<ObjectInfo> {
    const headers: Record<string, string> = {};
    const res = await this.#client.request({
      method: "HEAD",
      path: [bucket, key],
      headers,
      ...(options?.signal && { signal: options.signal }),
    });

    return parseHeaders(res.headers);
  }

  /** Deletes an object; object_not_found when there is none. */
  async delete(
    bucket: string,
    key: string,
    options?: CallOptions,
  ): Promise<void> {
    await this.#client.request({
      method: "DELETE",
      path: [bucket, key],
      ...(options?.signal && { signal: options.signal }),
    });
  }

  /** Iterates every object in key order, following pages; use with for await. */
  async *list(
    bucket: string,
    options?: ObjectListOptions,
  ): AsyncGenerator<ObjectRecord, void, unknown> {
    const base: ObjectPageOptions = {};
    if (options?.prefix !== undefined) {
      base.prefix = options.prefix;
    }
    if (options?.limit !== undefined) {
      base.limit = options.limit;
    }
    if (options?.signal !== undefined) {
      base.signal = options.signal;
    }

    let after: string | undefined = undefined;
    do {
      const page = await this.page(
        bucket,
        after === undefined ? base : { ...base, after },
      );
      yield* page.objects;
      after = page.next;
    } while (after);
  }

  /** One page of at most limit objects, as one request; pass next back as after for the following page. For UIs that page by hand. */
  async page(bucket: string, options?: ObjectPageOptions): Promise<ObjectPage> {
    const query: [string, string][] = [];
    if (options?.prefix !== undefined) {
      query.push(["prefix", options.prefix]);
    }
    if (options?.limit !== undefined) {
      query.push(["limit", options.limit.toString()]);
    }
    if (options?.after !== undefined) {
      query.push(["after", options.after]);
    }

    const res = await this.#client.request({
      method: "GET",
      path: [bucket],
      query,
      ...(options?.signal && { signal: options.signal }),
    });
    const body: WireListObjects = await res.json();
    const page: ObjectPage = { objects: body.objects.map(toObject) };
    if (body.next !== undefined) {
      page.next = body.next;
    }
    return page;
  }
}
