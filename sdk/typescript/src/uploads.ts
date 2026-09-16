import type { Tunna } from "./client.ts";
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

export interface UploadPart {
  part: number;
  size: number;
  checksum: string;
}

export interface UploadCreateOptions {
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

export class Uploads {
  readonly #client: Tunna;

  constructor(client: Tunna) {
    this.#client = client;
  }

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
      }),
      headers: { "Content-Type": "application/json" },
    });
    const body: WireUploadSession = await res.json();
    return toUploadSession(body);
  }

  async putPart(
    id: string,
    n: number,
    bytes: Uint8Array<ArrayBuffer>,
  ): Promise<UploadPart> {
    const checksum = encodeChecksum(crc32c(bytes));
    const res = await this.#client.request({
      method: "PUT",
      path: ["-", "uploads", id, "parts", n.toString()],
      headers: { "X-Tunna-Checksum": checksum },
      signedHeaders: ["X-Tunna-Checksum"],
      body: bytes,
    });
    const body: WireUploadPart = await res.json();
    return toUploadPart(body);
  }

  async get(id: string): Promise<UploadSession> {
    const res = await this.#client.request({
      method: "GET",
      path: ["-", "uploads", id],
    });
    const body: WireUploadSession = await res.json();
    return toUploadSession(body);
  }

  async complete(
    id: string,
    parts: number,
    checksums?: string[],
  ): Promise<ObjectRecord> {
    const res = await this.#client.request({
      method: "POST",
      path: ["-", "uploads", id, "complete"],
      body: JSON.stringify({
        parts,
        checksums,
      }),
      headers: { "Content-Type": "application/json" },
    });
    const body: WireObject = await res.json();
    return toObject(body);
  }

  async abort(id: string): Promise<void> {
    await this.#client.request({
      method: "DELETE",
      path: ["-", "uploads", id],
    });
  }
}
