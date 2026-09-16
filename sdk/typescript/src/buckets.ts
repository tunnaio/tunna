import type { Tunna } from "./client.ts";

interface WireBucket {
  name: string;
  public: boolean;
  created_at: number;
}

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

export class Buckets {
  readonly #client: Tunna;

  constructor(client: Tunna) {
    this.#client = client;
  }

  async list(): Promise<BucketRecord[]> {
    const res = await this.#client.request({
      method: "GET",
      path: ["-", "buckets"],
    });
    const body: { buckets: WireBucket[] } = await res.json();
    return body.buckets.map(toBucket);
  }

  async get(name: string): Promise<BucketRecord> {
    const res = await this.#client.request({
      method: "GET",
      path: ["-", "buckets", name],
    });
    return toBucket(await res.json());
  }

  async create(
    name: string,
    options?: { public?: boolean },
  ): Promise<BucketRecord> {
    const res = await this.#client.request({
      method: "PUT",
      path: ["-", "buckets", name],
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ public: options?.public ?? false }),
    });
    return toBucket(await res.json());
  }

  async delete(name: string): Promise<void> {
    await this.#client.request({
      method: "DELETE",
      path: ["-", "buckets", name],
    });
  }
}
