import type { Tunna } from "./client.ts";

export type Access = "read" | "write";

interface WireApiKey {
  id: string;
  name: string;
  admin: boolean;
  scopes?: Record<string, Access>;
  disabled: boolean;
  created_at: number;
}

export interface AdminApiKey {
  id: string;
  name: string;
  admin: true;
  disabled: boolean;
  createdAt: Date;
}

export interface ScopedApiKey {
  id: string;
  name: string;
  admin: false;
  scopes: Record<string, Access>;
  disabled: boolean;
  createdAt: Date;
}

export type ApiKey = AdminApiKey | ScopedApiKey;

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

export type ApiKeyPatchOptions = Partial<
  ApiKeyCreateOptions & { disabled: boolean }
>;

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

export class ApiKeys {
  readonly #client: Tunna;

  constructor(client: Tunna) {
    this.#client = client;
  }

  async list(): Promise<ApiKey[]> {
    const res = await this.#client.request({
      method: "GET",
      path: ["-", "keys"],
    });
    const body: { keys: WireApiKey[] } = await res.json();
    return body.keys.map(toApiKey);
  }

  async get(id: string): Promise<ApiKey> {
    const res = await this.#client.request({
      method: "GET",
      path: ["-", "keys", id],
    });
    const body: WireApiKey = await res.json();
    return toApiKey(body);
  }

  async create(options: ApiKeyCreateOptions): Promise<WithSecret<ApiKey>> {
    const res = await this.#client.request({
      method: "POST",
      path: ["-", "keys"],
      body: JSON.stringify(options),
      headers: { "Content-Type": "application/json" },
    });
    return withSecret(res);
  }

  async patch(id: string, options: ApiKeyPatchOptions): Promise<ApiKey> {
    const res = await this.#client.request({
      method: "PATCH",
      path: ["-", "keys", id],
      body: JSON.stringify(options),
      headers: { "Content-Type": "application/json" },
    });
    const body: WireApiKey = await res.json();
    return toApiKey(body);
  }

  async rotate(id: string): Promise<WithSecret<ApiKey>> {
    const res = await this.#client.request({
      method: "POST",
      path: ["-", "keys", id, "rotate"],
    });
    return withSecret(res);
  }

  async delete(id: string): Promise<void> {
    await this.#client.request({
      method: "DELETE",
      path: ["-", "keys", id],
    });
  }
}
