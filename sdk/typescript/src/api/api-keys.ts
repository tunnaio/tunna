import type { Tunna } from "../client.ts";
import type { CallOptions } from "../types.ts";

/** What a scoped key may do in a bucket; write includes read (ADR-0008). */
export type Access = "read" | "write";

interface WireApiKey {
  id: string;
  name: string;
  admin: boolean;
  scopes?: Record<string, Access>;
  disabled: boolean;
  created_at: number;
}

/** An admin key: every route, every bucket, no scopes. */
export interface AdminApiKey {
  id: string;
  name: string;
  admin: true;
  disabled: boolean;
  createdAt: Date;
}

/** A scoped key: access per bucket name, or "*" for every bucket. */
export interface ScopedApiKey {
  id: string;
  name: string;
  admin: false;
  scopes: Record<string, Access>;
  disabled: boolean;
  createdAt: Date;
}

/** A key record; narrow on admin to reach scopes. Never carries the secret. */
export type ApiKey = AdminApiKey | ScopedApiKey;

/** A new key: admin, or scoped with at least one scope; the two are exclusive by type. */
export type ApiKeyCreateOptions =
  | {
      name: string;
      admin: true;
    }
  | {
      name: string;
      admin?: false;
      scopes: Record<string, Access>;
    };

/** Fields to change; only those given are sent. Making a scoped key admin must send scopes: {} in the same patch. */
export type ApiKeyPatchOptions = Partial<
  ApiKeyCreateOptions & { disabled: boolean }
>;

/** A record plus its secret, which the server shows only on create and rotate. */
export type WithSecret<T> = T & { secret: string };

function toApiKey(k: WireApiKey): ApiKey {
  const base = {
    id: k.id,
    name: k.name,
    disabled: k.disabled,
    createdAt: new Date(k.created_at * 1000),
  };

  if (k.admin) {
    return {
      admin: true,
      ...base,
    };
  }

  return {
    admin: false,
    scopes: k.scopes ?? {},
    ...base,
  };
}

export async function withSecret(res: Response): Promise<WithSecret<ApiKey>> {
  const { secret, ...body }: WithSecret<WireApiKey> = await res.json();
  const key = toApiKey(body);
  return {
    ...key,
    secret,
  };
}

/** The key management routes (spec/wire.md 4.2); every one needs an admin key. */
export class ApiKeys {
  readonly #client: Tunna;

  constructor(client: Tunna) {
    this.#client = client;
  }

  /** Every key, by id. */
  async list(options?: CallOptions): Promise<ApiKey[]> {
    const res = await this.#client.request({
      method: "GET",
      path: ["-", "keys"],
      ...(options?.signal && { signal: options.signal }),
    });
    const body: { keys: WireApiKey[] } = await res.json();
    return body.keys.map(toApiKey);
  }

  /** One key; key_not_found when there is none. */
  async get(id: string, options?: CallOptions): Promise<ApiKey> {
    const res = await this.#client.request({
      method: "GET",
      path: ["-", "keys", id],
      ...(options?.signal && { signal: options.signal }),
    });
    const body: WireApiKey = await res.json();
    return toApiKey(body);
  }

  /** Creates a key; the returned secret is shown this once. */
  async create(
    key: ApiKeyCreateOptions,
    options?: CallOptions,
  ): Promise<WithSecret<ApiKey>> {
    const res = await this.#client.request({
      method: "POST",
      path: ["-", "keys"],
      body: JSON.stringify(key),
      headers: { "Content-Type": "application/json" },
      ...(options?.signal && { signal: options.signal }),
    });
    return withSecret(res);
  }

  /** Changes the given fields; takes effect on the key's next request. */
  async patch(
    id: string,
    changes: ApiKeyPatchOptions,
    options?: CallOptions,
  ): Promise<ApiKey> {
    const res = await this.#client.request({
      method: "PATCH",
      path: ["-", "keys", id],
      body: JSON.stringify(changes),
      headers: { "Content-Type": "application/json" },
      ...(options?.signal && { signal: options.signal }),
    });
    const body: WireApiKey = await res.json();
    return toApiKey(body);
  }

  /** Replaces the secret; the old one stops verifying at once, presigned URLs included. */
  async rotate(id: string, options?: CallOptions): Promise<WithSecret<ApiKey>> {
    const res = await this.#client.request({
      method: "POST",
      path: ["-", "keys", id, "rotate"],
      ...(options?.signal && { signal: options.signal }),
    });
    return withSecret(res);
  }

  /** Deletes a key. The server does not stop you deleting your own or the last admin. */
  async delete(id: string, options?: CallOptions): Promise<void> {
    await this.#client.request({
      method: "DELETE",
      path: ["-", "keys", id],
      ...(options?.signal && { signal: options.signal }),
    });
  }

  /** The record of the key this client signs with: whether it is admin, and a scoped key's buckets. Works for any key, unlike the rest of this group. */
  async self(options?: CallOptions): Promise<ApiKey> {
    const res = await this.#client.request({
      method: "GET",
      path: ["-", "keys", "self"],
      ...(options?.signal && { signal: options.signal }),
    });
    const body: WireApiKey = await res.json();
    return toApiKey(body);
  }
}
