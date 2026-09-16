import type { Tunna } from "./client.ts";
import { crc32c, encodeChecksum } from "./crc32c.ts";
import { TransportError, TunnaError } from "./errors.ts";

interface WireObject {
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

export interface ObjectRecord {
  bucket: string;
  key: string;
  size: number;
  contentType: string;
  checksum: string;
  createdAt: Date;
  metadata: Record<string, string>;
}

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

export interface ObjectPutOptions {
  contentType?: string;
  metadata?: Record<string, string>;
}

export interface ObjectGetOptions {
  range?: { start: number; end?: number };
}

export interface ObjectListOptions {
  prefix?: string;
  limit?: number;
}

export interface ObjectInfo {
  size: number;
  contentType: string;
  etag: string;
  checksum: string;
  lastModified: Date;
  metadata: Record<string, string>;
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

function toObject(o: WireObject): ObjectRecord {
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

export class Objects {
  readonly #client: Tunna;

  constructor(client: Tunna) {
    this.#client = client;
  }

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
    });
    const record: WireObject = await res.json();
    return toObject(record);
  }

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

  async head(bucket: string, key: string): Promise<ObjectInfo> {
    const headers: Record<string, string> = {};
    const res = await this.#client.request({
      method: "HEAD",
      path: [bucket, key],
      headers,
    });

    return parseHeaders(res.headers);
  }

  async delete(bucket: string, key: string): Promise<void> {
    await this.#client.request({
      method: "DELETE",
      path: [bucket, key],
    });
  }

  async *list(
    bucket: string,
    options?: ObjectListOptions,
  ): AsyncGenerator<ObjectRecord, void, unknown> {
    const query: [string, string][] = [];
    if (options?.prefix !== undefined) {
      query.push(["prefix", options.prefix]);
    }
    if (options?.limit !== undefined) {
      query.push(["limit", options.limit.toString()]);
    }

    let next: string | undefined = undefined;
    do {
      const page = next ? [...query, ["after", next] as const] : query;
      const res = await this.#client.request({
        method: "GET",
        path: [bucket],
        query: page,
      });
      const body: WireListObjects = await res.json();
      for (const obj of body.objects) {
        yield toObject(obj);
      }
      next = body.next;
    } while (next);
  }
}
