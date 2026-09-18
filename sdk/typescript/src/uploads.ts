import type { CallOptions, Tunna } from "./client.ts";
import { crc32c, encodeChecksum } from "./crc32c.ts";
import { toObject, type ObjectRecord, type WireObject } from "./objects.ts";

interface WireUploadSession {
  id: string;
  bucket: string;
  key: string;
  part_size: number;
  content_type: string;
  created_at: number;
  expires_at: number;
  parts?: number[];
}

interface WireUploadPart {
  part: number;
  size: number;
  checksum: string;
}

/** An upload session; parts lists the received part numbers, [] from create. */
export interface UploadSession {
  id: string;
  bucket: string;
  key: string;
  partSize: number;
  contentType: string;
  createdAt: Date;
  expiresAt: Date;
  parts: number[];
}

/** The server's record of one received part. */
export interface UploadPart {
  part: number;
  size: number;
  checksum: string;
}

/** Session options; partSize is fixed for the session and bounded by the server (default 5 MiB to 100 MiB). */
export interface UploadCreateOptions extends CallOptions {
  partSize: number;
  contentType?: string;
  metadata?: Record<string, string>;
}

function toUploadSession(s: WireUploadSession): UploadSession {
  return {
    id: s.id,
    bucket: s.bucket,
    key: s.key,
    partSize: s.part_size,
    contentType: s.content_type,
    createdAt: new Date(s.created_at * 1000),
    expiresAt: new Date(s.expires_at * 1000),
    parts: s.parts ?? [],
  };
}

function toUploadPart(s: WireUploadPart): UploadPart {
  return {
    part: s.part,
    size: s.size,
    checksum: s.checksum,
  };
}

/** The raw upload routes (spec/wire.md 6, ADR-0001); tunna.upload drives them for you. */
export class Uploads {
  readonly #client: Tunna;

  constructor(client: Tunna) {
    this.#client = client;
  }

  /** Initiates a session for one key; it expires unless completed or aborted. */
  async create(
    bucket: string,
    key: string,
    options: UploadCreateOptions,
  ): Promise<UploadSession> {
    const res = await this.#client.request({
      method: "POST",
      path: ["-", "uploads"],
      body: JSON.stringify({
        bucket,
        key,
        part_size: options.partSize,
        content_type: options.contentType ?? "application/octet-stream",
        metadata: options.metadata,
        ...(options?.signal && { signal: options.signal }),
      }),
      headers: { "Content-Type": "application/json" },
    });
    const body: WireUploadSession = await res.json();
    return toUploadSession(body);
  }

  /** Sends part n (from 1) with a signed checksum; parts may go in any order and a resend replaces. */
  async putPart(
    id: string,
    n: number,
    bytes: Uint8Array<ArrayBuffer>,
    options?: CallOptions,
  ): Promise<UploadPart> {
    const checksum = encodeChecksum(crc32c(bytes));
    const res = await this.#client.request({
      method: "PUT",
      path: ["-", "uploads", id, "parts", n.toString()],
      headers: { "X-Tunna-Checksum": checksum },
      signedHeaders: ["X-Tunna-Checksum"],
      body: bytes,
      ...(options?.signal && { signal: options.signal }),
    });
    const body: WireUploadPart = await res.json();
    return toUploadPart(body);
  }

  /** The session with its received parts; resume is get, then send what is missing. */
  async get(id: string, options?: CallOptions): Promise<UploadSession> {
    const res = await this.#client.request({
      method: "GET",
      path: ["-", "uploads", id],
      ...(options?.signal && { signal: options.signal }),
    });
    const body: WireUploadSession = await res.json();
    return toUploadSession(body);
  }

  /** Completes by part count, optionally verifying each part's checksum; returns the object. */
  async complete(
    id: string,
    parts: number,
    options?: { checksums?: string[] } & CallOptions,
  ): Promise<ObjectRecord> {
    const res = await this.#client.request({
      method: "POST",
      path: ["-", "uploads", id, "complete"],
      body: JSON.stringify({
        parts,
        ...(options?.checksums && { checksums: options.checksums }),
      }),
      headers: { "Content-Type": "application/json" },
      ...(options?.signal && { signal: options.signal }),
    });
    const body: WireObject = await res.json();
    return toObject(body);
  }

  /** Discards the session and its bytes. */
  async abort(id: string, options?: CallOptions): Promise<void> {
    await this.#client.request({
      method: "DELETE",
      path: ["-", "uploads", id],
      ...(options?.signal && { signal: options.signal }),
    });
  }
}
