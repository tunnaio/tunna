import type { Tunna } from "../client.ts";
import type { CallOptions } from "../types.ts";

interface WireBucket {
  name: string;
  public: boolean;
  created_at: number;
}

/** A bucket as the server reports it. */
export interface BucketRecord {
  name: string;
  public: boolean;
  createdAt: Date;
}

function toBucket(b: WireBucket): BucketRecord {
  return {
    name: b.name,
    public: b.public,
    createdAt: new Date(b.created_at * 1000),
  };
}

/** The bucket routes (spec/wire.md 4.1). Create and delete need an admin key; list shows what the key may read. */
export class Buckets {
  readonly #client: Tunna;

  constructor(client: Tunna) {
    this.#client = client;
  }

  /** Every bucket the key may read, in name order. */
  async list(options?: CallOptions): Promise<BucketRecord[]> {
    const res = await this.#client.request({
      method: "GET",
      path: ["-", "buckets"],
      ...(options?.signal && { signal: options.signal }),
    });
    const body: { buckets: WireBucket[] } = await res.json();
    return body.buckets.map(toBucket);
  }

  /** One bucket; bucket_not_found when there is none. */
  async get(name: string, options?: CallOptions): Promise<BucketRecord> {
    const res = await this.#client.request({
      method: "GET",
      path: ["-", "buckets", name],
      ...(options?.signal && { signal: options.signal }),
    });
    return toBucket(await res.json());
  }

  /** Creates a bucket; bucket_exists when the name is taken. */
  async create(
    name: string,
    options?: { public?: boolean } & CallOptions,
  ): Promise<BucketRecord> {
    const res = await this.#client.request({
      method: "PUT",
      path: ["-", "buckets", name],
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ public: options?.public ?? false }),
      ...(options?.signal && { signal: options.signal }),
    });
    return toBucket(await res.json());
  }

  /** Changes a bucket's settings (public) and returns the updated record; bucket_not_found when there is none. Admin keys only. */
  async patch(
    name: string,
    changes: { public: boolean },
    options?: CallOptions,
  ): Promise<BucketRecord> {
    const res = await this.#client.request({
      method: "PATCH",
      path: ["-", "buckets", name],
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        public: changes.public,
      }),
      ...(options?.signal && { signal: options.signal }),
    });
    return toBucket(await res.json());
  }

  /** Deletes an empty bucket; bucket_not_empty while it holds objects or active uploads. */
  async delete(name: string, options?: CallOptions): Promise<void> {
    await this.#client.request({
      method: "DELETE",
      path: ["-", "buckets", name],
      ...(options?.signal && { signal: options.signal }),
    });
  }
}
