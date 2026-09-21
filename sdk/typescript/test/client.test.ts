import { describe, expect, test } from "bun:test";
import { Tunna, TunnaError, TransportError } from "../src/index.ts";
import { authorization } from "../src/sign.ts";

// The request pipeline with an injected fetch: URL, signing, error mapping.
// The wire itself is covered by the conformance test; this pins what the
// client does before and after the network.

const key = { id: "tk_test", secret: "test-secret-not-real" };
const now = 1_788_912_000;

type Seen = { url: string; init: RequestInit & { duplex?: string } };

function client(reply: (seen: Seen) => Response | Promise<Response>, options: { key?: typeof key; url?: string } = { key }) {
  const seen: Seen[] = [];
  const fetch = (async (input: string | URL | Request, init?: RequestInit) => {
    const s = { url: String(input), init: (init ?? {}) as Seen["init"] };
    seen.push(s);
    return reply(s);
  }) as typeof globalThis.fetch;
  const tunna = new Tunna({ url: options.url ?? "http://store.test", fetch, now: () => now, ...(options.key && { key: options.key }) });
  return { tunna, seen };
}

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json; charset=utf-8" } });

describe("request pipeline", () => {
  test("builds the URL with the canonical encoders", async () => {
    const { tunna, seen } = client(() => json(200, { buckets: [] }));
    await tunna.request({ method: "GET", path: ["photos", "år 2026/bild.jpg"], query: [["limit", "10"], ["prefix", "a/"]] });
    expect(seen[0]!.url).toBe("http://store.test/photos/%C3%A5r%202026%2Fbild.jpg?limit=10&prefix=a%2F");
  });

  test("tolerates a trailing slash on the base URL", async () => {
    const { tunna, seen } = client(() => json(200, {}), { key, url: "http://store.test/" });
    await tunna.request({ method: "GET", path: ["-", "health"] });
    expect(seen[0]!.url).toBe("http://store.test/-/health");
  });

  test("signs with the date and an Authorization the signer agrees with", async () => {
    const { tunna, seen } = client(() => json(200, {}));
    const call = {
      method: "PUT",
      path: ["photos", "a.txt"],
      headers: { "Content-Type": "text/plain", "X-Tunna-Checksum": "crc32c=AAAAAA==" },
      signedHeaders: ["X-Tunna-Checksum", "Content-Type"],
      body: "x",
    };
    await tunna.request(call);
    const h = new Headers(seen[0]!.init.headers);
    expect(h.get("X-Tunna-Date")).toBe(String(now));
    expect(h.get("Authorization")).toBe(await authorization(call, key, now));
    expect(h.get("Content-Type")).toBe("text/plain");
    expect(h.get("X-Tunna-Checksum")).toBe("crc32c=AAAAAA==");
    expect(seen[0]!.init.method).toBe("PUT");
    expect(seen[0]!.init.body).toBe("x");
  });

  test("sends anonymously without a key", async () => {
    const { tunna, seen } = client(() => json(200, {}), {});
    await tunna.request({ method: "GET", path: ["public-site", "index.html"] });
    const h = new Headers(seen[0]!.init.headers);
    expect(h.has("Authorization")).toBe(false);
    expect(h.has("X-Tunna-Date")).toBe(false);
  });

  test("asks for half duplex only when the body is a stream", async () => {
    const { tunna, seen } = client(() => json(200, {}));
    await tunna.request({ method: "PUT", path: ["b", "k"], body: new ReadableStream() });
    await tunna.request({ method: "PUT", path: ["b", "k"], body: "bytes" });
    expect(seen[0]!.init.duplex).toBe("half");
    expect(seen[1]!.init.duplex).toBeUndefined();
  });

  test("returns the response untouched on success", async () => {
    const { tunna } = client(() => new Response("raw bytes", { status: 201, headers: { ETag: '"x"' } }));
    const res = await tunna.request({ method: "PUT", path: ["b", "k"] });
    expect(res.status).toBe(201);
    expect(res.headers.get("ETag")).toBe('"x"');
    expect(await res.text()).toBe("raw bytes");
  });

  test("maps a wire error body to TunnaError", async () => {
    const { tunna } = client(() =>
      json(404, { error: { code: "bucket_not_found", message: "no bucket named nope", details: { bucket: "nope" } } }),
    );
    const err = await tunna.request({ method: "GET", path: ["-", "buckets", "nope"] }).catch((e) => e);
    expect(err).toBeInstanceOf(TunnaError);
    expect(err.name).toBe("TunnaError");
    expect(err.code).toBe("bucket_not_found");
    expect(err.status).toBe(404);
    expect(err.message).toBe("no bucket named nope");
    expect(err.details).toEqual({ bucket: "nope" });
  });

  test("an unknown error code is a TransportError, never a mislabelled TunnaError", async () => {
    const { tunna } = client(() => json(418, { error: { code: "teapot", message: "short and stout" } }));
    const err = await tunna.request({ method: "GET", path: ["-", "health"] }).catch((e) => e);
    expect(err).toBeInstanceOf(TransportError);
    expect(err.message).toContain("teapot");
    expect(err.message).toContain("418");
  });

  test("a non-JSON failure is a TransportError with the status", async () => {
    const { tunna } = client(() => new Response("<html>bad gateway</html>", { status: 502, headers: { "Content-Type": "text/html" } }));
    const err = await tunna.request({ method: "GET", path: ["-", "health"] }).catch((e) => e);
    expect(err).toBeInstanceOf(TransportError);
    expect(err.message).toContain("502");
  });

  test("a thrown fetch is a TransportError with the cause kept", async () => {
    const boom = new TypeError("connection refused");
    const { tunna } = client(() => {
      throw boom;
    });
    const err = await tunna.request({ method: "GET", path: ["-", "health"] }).catch((e) => e);
    expect(err).toBeInstanceOf(TransportError);
    expect(err.name).toBe("TransportError");
    expect(err.cause).toBe(boom);
    expect(err.message).toContain("GET http://store.test/-/health");
  });
});

describe("buckets", () => {
  test("list maps records and created_at to a Date", async () => {
    const { tunna, seen } = client(() =>
      json(200, { buckets: [{ name: "photos", public: false, created_at: 1_788_912_000 }, { name: "site", public: true, created_at: 1_788_912_001 }] }),
    );
    const buckets = await tunna.buckets.list();
    expect(seen[0]!.url).toBe("http://store.test/-/buckets");
    expect(buckets).toEqual([
      { name: "photos", public: false, createdAt: new Date(1_788_912_000_000) },
      { name: "site", public: true, createdAt: new Date(1_788_912_001_000) },
    ]);
  });

  test("create sends a JSON body with the public flag", async () => {
    const { tunna, seen } = client(() => json(201, { name: "photos", public: true, created_at: 1 }));
    const b = await tunna.buckets.create("photos", { public: true });
    expect(seen[0]!.init.method).toBe("PUT");
    expect(new Headers(seen[0]!.init.headers).get("Content-Type")).toBe("application/json");
    expect(JSON.parse(String(seen[0]!.init.body))).toEqual({ public: true });
    expect(b.public).toBe(true);
  });

  test("delete returns nothing on 204", async () => {
    const { tunna, seen } = client(() => new Response(null, { status: 204 }));
    await expect(tunna.buckets.delete("photos")).resolves.toBeUndefined();
    expect(seen[0]!.init.method).toBe("DELETE");
    expect(seen[0]!.url).toBe("http://store.test/-/buckets/photos");
  });
});

describe("errors without a body", () => {
  const bodiless = (status: number, code?: string) =>
    new Response(null, { status, headers: { "Content-Type": "application/json", ...(code && { "X-Tunna-Error": code }) } });

  test("a failed HEAD is a TunnaError with the code from X-Tunna-Error", async () => {
    const { tunna } = client(() => bodiless(404, "object_not_found"));
    const err = await tunna.objects.head("b", "missing").catch((e) => e);
    expect(err).toBeInstanceOf(TunnaError);
    expect(err.code).toBe("object_not_found");
    expect(err.status).toBe(404);
    expect(err.stage).toBe(6);
    expect(err.details).toBeUndefined();
    // No body means no server message; the SDK's own must still be readable.
    expect(err.message).toContain("object_not_found");
    expect(err.message).not.toContain("undefined");
  });

  test("a header code outside the contract is a TransportError, like a body code", async () => {
    const { tunna } = client(() => bodiless(404, "teapot"));
    const err = await tunna.objects.head("b", "missing").catch((e) => e);
    expect(err).toBeInstanceOf(TransportError);
    expect(err.message).toContain("teapot");
  });

  test("with a body, the body wins: it has the message and the details", async () => {
    const { tunna } = client(
      () =>
        new Response(JSON.stringify({ error: { code: "bucket_not_found", message: "no bucket named nope", details: { bucket: "nope" } } }), {
          status: 404,
          headers: { "Content-Type": "application/json", "X-Tunna-Error": "bucket_not_found" },
        }),
    );
    const err = await tunna.buckets.get("nope").catch((e) => e);
    expect(err).toBeInstanceOf(TunnaError);
    expect(err.message).toBe("no bucket named nope");
    expect(err.details).toEqual({ bucket: "nope" });
  });

  test("a failed HEAD from a server without the header names the status and says why there is no code", async () => {
    // Servers before 0.1.0-alpha.4 send no X-Tunna-Error. The message must
    // say so rather than "code undefined".
    const { tunna } = client(() => new Response(null, { status: 404, headers: { "Content-Type": "application/json" } }));
    const err = await tunna.objects.head("b", "missing").catch((e) => e);
    expect(err).toBeInstanceOf(TransportError);
    expect(err.message).toContain("404");
    expect(err.message).toContain("HEAD");
    expect(err.message).not.toContain("undefined");
  });
});

describe("construction", () => {
  test("an empty key id or secret is refused up front, naming the field", () => {
    // Otherwise the first request fails inside crypto.subtle with
    // "Zero-length key is not supported", which names nothing.
    expect(() => new Tunna({ url: "http://store.test", key: { id: "", secret: "s" } })).toThrow(/key\.id/);
    expect(() => new Tunna({ url: "http://store.test", key: { id: "tk_x", secret: "" } })).toThrow(/key\.secret/);
  });

  test("an empty url is refused", () => {
    expect(() => new Tunna({ url: "" })).toThrow(/url/);
  });
});

describe("buckets.patch", () => {
  test("sends only the public flag and maps the record back", async () => {
    const { tunna, seen } = client(() => json(200, { name: "photos", public: true, created_at: 1_788_912_000 }));
    const b = await tunna.buckets.patch("photos", { public: true });
    expect(seen[0]!.init.method).toBe("PATCH");
    expect(seen[0]!.url).toBe("http://store.test/-/buckets/photos");
    expect(new Headers(seen[0]!.init.headers).get("Content-Type")).toBe("application/json");
    expect(JSON.parse(String(seen[0]!.init.body))).toEqual({ public: true });
    expect(b).toEqual({ name: "photos", public: true, createdAt: new Date(1_788_912_000_000) });
  });

  test("a whole record passed back in still sends only public", async () => {
    // Types are open: a BucketRecord satisfies { public: boolean } through a
    // variable, which is exactly what a console does after buckets.get.
    const { tunna, seen } = client(() => json(200, { name: "photos", public: false, created_at: 1 }));
    const record = { name: "photos", public: false, createdAt: new Date(0) };
    await tunna.buckets.patch("photos", record);
    expect(JSON.parse(String(seen[0]!.init.body))).toEqual({ public: false });
  });
});

describe("objects.page", () => {
  const wire = (key: string) => ({ bucket: "photos", key, size: 1, content_type: "text/plain", checksum: "crc32c=AAAAAA==", created_at: 1_788_912_000 });

  test("returns one page and the cursor, without following it", async () => {
    const { tunna, seen } = client(() => json(200, { objects: [wire("a"), wire("b")], next: "b" }));
    const page = await tunna.objects.page("photos", { prefix: "a/", limit: 2 });
    expect(seen).toHaveLength(1);
    // encodeQuery sorts pairs by name, so limit comes before prefix.
    expect(seen[0]!.url).toBe("http://store.test/photos?limit=2&prefix=a%2F");
    expect(page.objects.map((o) => o.key)).toEqual(["a", "b"]);
    expect(page.objects[0]!.createdAt).toEqual(new Date(1_788_912_000_000));
    expect(page.next).toBe("b");
  });

  test("after resumes from a cursor, and the last page has no next", async () => {
    const { tunna, seen } = client(() => json(200, { objects: [wire("c")] }));
    const page = await tunna.objects.page("photos", { after: "b" });
    expect(seen[0]!.url).toBe("http://store.test/photos?after=b");
    expect(page.next).toBeUndefined();
    expect("next" in page).toBe(false);
  });
});

describe("AbortSignal", () => {
  test("a signal given to a call reaches fetch", async () => {
    const { tunna, seen } = client(() => json(200, { name: "photos", public: false, created_at: 1 }));
    const controller = new AbortController();
    await tunna.buckets.get("photos", { signal: controller.signal });
    expect(seen[0]!.init.signal).toBe(controller.signal);
  });

  test("every group takes one: objects, apiKeys, uploads", async () => {
    const { tunna, seen } = client((s) =>
      s.init.method === "DELETE" ? new Response(null, { status: 204 }) : json(200, { objects: [], keys: [] }),
    );
    const { signal } = new AbortController();
    await tunna.objects.delete("photos", "a.txt", { signal });
    await tunna.objects.page("photos", { signal });
    await tunna.apiKeys.list({ signal });
    await tunna.uploads.abort("up_1", { signal });
    expect(seen.map((s) => s.init.signal)).toEqual([signal, signal, signal, signal]);
  });

  test("list forwards its signal to every page it fetches", async () => {
    const wire = (key: string) => ({ bucket: "photos", key, size: 1, content_type: "text/plain", checksum: "crc32c=AAAAAA==", created_at: 1 });
    const { tunna, seen } = client((s) =>
      s.url.includes("after=") ? json(200, { objects: [wire("b")] }) : json(200, { objects: [wire("a")], next: "a" }),
    );
    const { signal } = new AbortController();
    const keys: string[] = [];
    for await (const o of tunna.objects.list("photos", { signal })) keys.push(o.key);
    expect(keys).toEqual(["a", "b"]);
    expect(seen.map((s) => s.init.signal)).toEqual([signal, signal]);
  });

  test("the signal never reaches a request body", async () => {
    // Methods that serialize their argument take the signal as a separate
    // parameter; JSON.stringify of a signal is {"signal":{}} on the wire.
    const record = { id: "tk_1", name: "ci", admin: true, scopes: {}, disabled: false, created_at: 1 };
    const { tunna, seen } = client(() => json(200, { ...record, secret: "s", public: true }));
    const { signal } = new AbortController();

    await tunna.apiKeys.create({ name: "ci", admin: true }, { signal });
    await tunna.apiKeys.patch("tk_1", { disabled: true }, { signal });
    await tunna.buckets.create("photos", { public: true, signal });
    await tunna.buckets.patch("photos", { public: true }, { signal });

    expect(seen.map((s) => JSON.parse(String(s.init.body)))).toEqual([
      { name: "ci", admin: true },
      { disabled: true },
      { public: true },
      { public: true },
    ]);
    expect(seen.map((s) => s.init.signal)).toEqual([signal, signal, signal, signal]);
  });

  test("an abort rejects with the abort reason, not a TransportError", async () => {
    // The caller cancelled; that is not a transport failure, and code that
    // checks err.name === "AbortError" must keep working.
    const controller = new AbortController();
    const { tunna } = client((s) => {
      controller.abort();
      s.init.signal!.throwIfAborted();
      return json(200, {});
    });
    const err = await tunna.buckets.get("photos", { signal: controller.signal }).catch((e) => e);
    expect(err).not.toBeInstanceOf(TransportError);
    expect(err.name).toBe("AbortError");
  });
});

describe("uploads.complete", () => {
  const object = { bucket: "b", key: "k", size: 1, content_type: "text/plain", checksum: "crc32c=AAAAAA==", created_at: 1 };

  test("checksums and the signal share the trailing options; only checksums reach the body", async () => {
    const { tunna, seen } = client(() => json(201, object));
    const { signal } = new AbortController();
    await tunna.uploads.complete("up_1", 2, { checksums: ["crc32c=AAAAAA==", "crc32c=AAAAAQ=="], signal });
    expect(JSON.parse(String(seen[0]!.init.body))).toEqual({ parts: 2, checksums: ["crc32c=AAAAAA==", "crc32c=AAAAAQ=="] });
    expect(seen[0]!.init.signal).toBe(signal);
  });

  test("without options the body is the part count alone", async () => {
    const { tunna, seen } = client(() => json(201, object));
    await tunna.uploads.complete("up_1", 2);
    expect(JSON.parse(String(seen[0]!.init.body))).toEqual({ parts: 2 });
  });
});

describe("TunnaError.stage", () => {
  test("carries the ladder stage of its code (ADR-0004)", async () => {
    const { tunna } = client(() => json(403, { error: { code: "forbidden", message: "no" } }));
    const err = await tunna.buckets.get("photos").catch((e) => e);
    expect(err).toBeInstanceOf(TunnaError);
    expect(err.stage).toBe(3);
  });
});

describe("health and version", () => {
  test("health resolves on 200 and sends no credentials", async () => {
    const { tunna, seen } = client(() => json(200, { status: "ok" }));
    await expect(tunna.health()).resolves.toBeUndefined();
    expect(seen[0]!.url).toBe("http://store.test/-/health");
    expect(new Headers(seen[0]!.init.headers).has("Authorization")).toBe(false);
  });

  test("version reports compatible when the server speaks this package's spec", async () => {
    const { SPEC_VERSION } = await import("../src/index.ts");
    const { tunna, seen } = client(() => json(200, { version: "0.1.0-alpha.3", spec: SPEC_VERSION }));
    expect(await tunna.version()).toEqual({ version: "0.1.0-alpha.3", spec: SPEC_VERSION, compatible: true });
    expect(seen[0]!.url).toBe("http://store.test/-/version");
    expect(new Headers(seen[0]!.init.headers).has("Authorization")).toBe(false);
  });

  test("version reports incompatible on another spec", async () => {
    const { tunna } = client(() => json(200, { version: "9.0.0", spec: "9.0.0" }));
    expect((await tunna.version()).compatible).toBe(false);
  });
});

describe("publicUrl", () => {
  test("presigned URLs use it; requests keep using url", async () => {
    // A server reached as http://tunna:8000 inside a network hands browsers
    // URLs on its public name. Host is not signed (ADR-0003), so the same
    // signature is valid on both.
    const seen: string[] = [];
    const fetch = (async (input: string | URL | Request) => {
      seen.push(String(input));
      return json(200, { buckets: [] });
    }) as typeof globalThis.fetch;
    const options = { key, fetch, now: () => now };
    const inside = new Tunna({ url: "http://tunna:8000", publicUrl: "https://store.example.com/", ...options });
    const plain = new Tunna({ url: "http://tunna:8000", ...options });

    const request = { method: "GET", bucket: "photos", key: "a b.jpg", expiresIn: 600 } as const;
    const url = await inside.presign(request);
    expect(url.startsWith("https://store.example.com/photos/a%20b.jpg?")).toBe(true);
    expect(new URL(url).search).toBe(new URL(await plain.presign(request)).search);

    await inside.buckets.list();
    expect(seen[0]).toBe("http://tunna:8000/-/buckets");
  });

  test("an empty publicUrl is refused, not silently turned into relative URLs", () => {
    expect(() => new Tunna({ url: "http://tunna:8000", publicUrl: "" })).toThrow(/publicUrl/);
  });
});

describe("apiKeys.self", () => {
  test("reads the caller's own record from /-/keys/self, as a scoped key", async () => {
    const { tunna, seen } = client(() =>
      json(200, { id: "tk_reader", name: "reader", admin: false, scopes: { photos: "read" }, disabled: false, created_at: 1_788_912_000 }),
    );
    const { signal } = new AbortController();
    const me = await tunna.apiKeys.self({ signal });
    expect(seen[0]!.init.method).toBe("GET");
    expect(seen[0]!.url).toBe("http://store.test/-/keys/self");
    expect(seen[0]!.init.signal).toBe(signal);
    expect(new Headers(seen[0]!.init.headers).has("Authorization")).toBe(true);
    expect(me).toEqual({ id: "tk_reader", name: "reader", admin: false, scopes: { photos: "read" }, disabled: false, createdAt: new Date(1_788_912_000_000) });
    // The union narrows on admin, so scopes is reachable only on a scoped key.
    if (!me.admin) expect(me.scopes.photos).toBe("read");
  });

  test("an admin record has no scopes", async () => {
    const { tunna } = client(() => json(200, { id: "tk_alice", name: "alice", admin: true, disabled: false, created_at: 1 }));
    const me = await tunna.apiKeys.self();
    expect(me.admin).toBe(true);
    expect("scopes" in me).toBe(false);
  });
});

describe("limits", () => {
  const wire = {
    object_size_max: 104_857_600,
    part_size_min: 5_242_880,
    part_size_max: 104_857_600,
    parts_max: 10_000,
    list_limit_max: 1000,
    key_length_max: 1024,
    presign_lifetime_max_seconds: 604_800,
    upload_expiry_seconds: 86_400,
    clock_skew_seconds: 900,
  };

  test("reads /-/limits unsigned and maps it to camelCase, sizes in bytes and durations in seconds", async () => {
    const { tunna, seen } = client(() => json(200, wire));
    const { signal } = new AbortController();
    const limits = await tunna.limits({ signal });
    expect(seen[0]!.url).toBe("http://store.test/-/limits");
    expect(seen[0]!.init.signal).toBe(signal);
    expect(new Headers(seen[0]!.init.headers).has("Authorization")).toBe(false);
    expect(limits).toEqual({
      objectSizeMax: 104_857_600,
      partSizeMin: 5_242_880,
      partSizeMax: 104_857_600,
      partsMax: 10_000,
      listLimitMax: 1000,
      keyLengthMax: 1024,
      presignLifetimeMaxSeconds: 604_800,
      uploadExpirySeconds: 86_400,
      clockSkewSeconds: 900,
    });
  });

  test("a field this package does not know is dropped, not an error", async () => {
    // wire.md 11.1: a later limit is an addition, so an older SDK must keep working.
    const { tunna } = client(() => json(200, { ...wire, copy_size_max: 1 }));
    const limits = await tunna.limits();
    expect("copySizeMax" in limits).toBe(false);
    expect("copy_size_max" in limits).toBe(false);
    expect(limits.partSizeMin).toBe(5_242_880);
  });
});

// ADR-0013. The backend half: presignRequest is the general form of presign.
describe("presignRequest", () => {
  test("signs any path, binding the given headers, on the public base", async () => {
    const { presignQuery } = await import("../src/sign.ts");
    const fetch = (async () => json(200, {})) as unknown as typeof globalThis.fetch;
    const tunna = new Tunna({ url: "http://tunna:8000", publicUrl: "https://store.example.com", key, fetch, now: () => now });
    const headers = { "X-Tunna-Checksum": "crc32c=AAAAAA==" };
    const url = await tunna.presignRequest({ method: "PUT", path: ["-", "uploads", "up_1", "parts", "3"], headers, expiresIn: 600 });

    const want = await presignQuery(
      { method: "PUT", path: ["-", "uploads", "up_1", "parts", "3"], headers, signedHeaders: ["X-Tunna-Checksum"] },
      key,
      now + 600,
    );
    expect(url).toBe(`https://store.example.com/-/uploads/up_1/parts/3?${want}`);
  });

  test("binds the query too, and encodes path segments once", async () => {
    const { tunna } = client(() => json(200, {}));
    const url = new URL(await tunna.presignRequest({ method: "GET", path: ["photos"], query: [["prefix", "år 2026/"]], expiresIn: 60 }));
    expect(url.pathname).toBe("/photos");
    expect(url.searchParams.get("prefix")).toBe("år 2026/");
    expect(url.searchParams.get("x-tunna-expires")).toBe(String(now + 60));
    expect(url.search.endsWith(`x-tunna-sig=${url.searchParams.get("x-tunna-sig")}`)).toBe(true);
  });

  test("presign is presignRequest for an object path", async () => {
    const { tunna } = client(() => json(200, {}));
    const a = await tunna.presign({ method: "GET", bucket: "photos", key: "a b.jpg", expiresIn: 600 });
    const b = await tunna.presignRequest({ method: "GET", path: ["photos", "a b.jpg"], expiresIn: 600 });
    expect(a).toBe(b);
  });

  test("refuses the routes the server refuses, so a backend finds out when it is written", async () => {
    const { tunna } = client(() => json(200, {}));
    const refused: [string, string[]][] = [
      ["POST", ["-", "uploads"]],
      ["PUT", ["-", "buckets", "photos"]],
      ["PATCH", ["-", "buckets", "photos"]],
      ["POST", ["-", "keys"]],
      ["PATCH", ["-", "keys", "tk_1"]],
    ];
    for (const [method, path] of refused) {
      const err = await tunna.presignRequest({ method, path, expiresIn: 60 }).catch((e) => e);
      expect(err, `${method} /${path.join("/")}`).toBeInstanceOf(TypeError);
      expect(err.message).toContain("header form");
    }
    // Same prefixes, no parameters in a body: allowed.
    const allowed: [string, string[]][] = [
      ["POST", ["-", "uploads", "up_1", "complete"]],
      ["POST", ["-", "keys", "tk_1", "rotate"]],
      ["DELETE", ["-", "buckets", "photos"]],
      ["GET", ["-", "keys"]],
    ];
    for (const [method, path] of allowed) {
      await expect(tunna.presignRequest({ method, path, expiresIn: 60 })).resolves.toContain("x-tunna-sig=");
    }
  });

  test("needs a key", async () => {
    const { tunna } = client(() => json(200, {}), {});
    await expect(tunna.presignRequest({ method: "GET", path: ["photos", "a.jpg"], expiresIn: 60 })).rejects.toBeInstanceOf(TypeError);
  });
});

// ADR-0013. The page half: no key, a function that asks the backend.
describe("presign provider", () => {
  type Asked = {
    request: { method: string; path: readonly string[]; query?: readonly (readonly [string, string])[]; headers?: Record<string, string> };
    signal: AbortSignal | undefined;
  };

  function page(reply: (seen: Seen) => Response | Promise<Response>, provide?: (a: Asked) => string | Promise<string>) {
    const seen: Seen[] = [];
    const asked: Asked[] = [];
    const fetch = (async (input: string | URL | Request, init?: RequestInit) => {
      const s = { url: String(input), init: (init ?? {}) as Seen["init"] };
      seen.push(s);
      return reply(s);
    }) as typeof globalThis.fetch;
    const tunna = new Tunna({
      url: "http://store.test",
      fetch,
      presign: async (request, options) => {
        const a = { request, signal: options.signal };
        asked.push(a);
        return provide ? provide(a) : `https://signed.test/${request.path.join("/")}?x-tunna-sig=made-by-the-backend`;
      },
    });
    return { tunna, seen, asked };
  }

  test("a key and a provider together is refused, by the types and at runtime", () => {
    // The directive pins the option type: if it ever accepts both, tsc fails
    // here on an unused directive. The throw is for plain JavaScript callers.
    // @ts-expect-error a key and a presign provider are exclusive
    expect(() => new Tunna({ url: "http://store.test", key, presign: async () => "x" })).toThrow(/presign/);
  });

  test("a call asks the provider for a URL and fetches exactly that URL, unsigned", async () => {
    const { tunna, seen, asked } = page(() => json(200, { name: "photos", public: false, created_at: 1 }));
    const { signal } = new AbortController();
    await tunna.buckets.get("photos", { signal });

    expect(asked).toHaveLength(1);
    expect(asked[0]!.request.method).toBe("GET");
    expect(asked[0]!.request.path).toEqual(["-", "buckets", "photos"]);
    expect(asked[0]!.signal).toBe(signal);

    expect(seen[0]!.url).toBe("https://signed.test/-/buckets/photos?x-tunna-sig=made-by-the-backend");
    const h = new Headers(seen[0]!.init.headers);
    expect(h.has("Authorization")).toBe(false);
    expect(h.has("X-Tunna-Date")).toBe(false);
    expect(seen[0]!.init.signal).toBe(signal);
  });

  test("the provider is given the query and the headers the pipeline would have signed, and they are still sent", async () => {
    const wire = { bucket: "photos", key: "a.txt", size: 1, content_type: "text/plain", checksum: "crc32c=AAAAAA==", created_at: 1 };
    const { tunna, seen, asked } = page((s) => (s.init.method === "PUT" ? json(201, wire) : json(200, { objects: [] })));

    await tunna.objects.put("photos", "a.txt", "x", { contentType: "text/plain", metadata: { title: "t" } });
    const bound = asked[0]!.request.headers ?? {};
    expect(Object.keys(bound).map((k) => k.toLowerCase()).sort()).toEqual(["content-type", "x-tunna-checksum", "x-tunna-meta-title"]);
    const sent = new Headers(seen[0]!.init.headers);
    expect(sent.get("Content-Type")).toBe("text/plain");
    expect(sent.get("X-Tunna-Meta-title")).toBe("t");
    expect(sent.get("X-Tunna-Checksum")).toBe(bound["X-Tunna-Checksum"] ?? null);

    await tunna.objects.page("photos", { prefix: "a/", limit: 2 });
    expect([...(asked[1]!.request.query ?? [])].sort()).toEqual([["limit", "2"], ["prefix", "a/"]]);
    // The URL is the provider's, verbatim: the query is inside it already.
    expect(seen[1]!.url).toBe("https://signed.test/photos?x-tunna-sig=made-by-the-backend");
  });

  test("health, version and limits never reach the provider", async () => {
    const { tunna, seen, asked } = page(() => json(200, { status: "ok", version: "v", spec: "s" }));
    await tunna.health();
    await tunna.version();
    expect(asked).toHaveLength(0);
    expect(seen.map((s) => s.url)).toEqual(["http://store.test/-/health", "http://store.test/-/version"]);
  });

  test("what the provider throws reaches the caller unchanged", async () => {
    // It is the application's own error (not signed in, not your file), not a
    // transport failure, and the upload helper must not retry it.
    const refusal = new Error("not your file");
    const { tunna, seen } = page(
      () => json(200, {}),
      () => {
        throw refusal;
      },
    );
    const err = await tunna.objects.delete("photos", "a.txt").catch((e) => e);
    expect(err).toBe(refusal);
    expect(seen).toHaveLength(0);
  });

  test("a server error through a provided URL is a TunnaError like any other", async () => {
    const { tunna } = page(() => json(403, { error: { code: "forbidden", message: "no" } }));
    const err = await tunna.objects.delete("photos", "a.txt").catch((e) => e);
    expect(err).toBeInstanceOf(TunnaError);
    expect(err.code).toBe("forbidden");
  });

  test("tunna.presign returns the provider URL, for an img src", async () => {
    const { tunna, asked } = page(() => json(200, {}));
    const url = await tunna.presign({ method: "GET", bucket: "photos", key: "a.jpg", expiresIn: 600 });
    expect(url).toBe("https://signed.test/photos/a.jpg?x-tunna-sig=made-by-the-backend");
    expect(asked[0]!.request.method).toBe("GET");
    expect(asked[0]!.request.path).toEqual(["photos", "a.jpg"]);
  });

  test("tunna.presign hands its signal to the provider: there it is a network call", async () => {
    const { tunna, asked } = page(() => json(200, {}));
    const { signal } = new AbortController();
    await tunna.presign({ method: "GET", bucket: "photos", key: "a.jpg", expiresIn: 600, signal });
    expect(asked[0]!.signal).toBe(signal);
    // The signal is for the call, not part of what gets signed.
    expect("signal" in asked[0]!.request).toBe(false);
  });

  test("tunna.presign with an aborted signal rejects in both modes, before anything happens", async () => {
    const controller = new AbortController();
    controller.abort();
    const options = { method: "GET", bucket: "photos", key: "a.jpg", expiresIn: 600, signal: controller.signal } as const;

    const { tunna: onPage, asked } = page(() => json(200, {}));
    const a = await onPage.presign(options).catch((e) => e);
    expect(a.name).toBe("AbortError");
    expect(asked).toHaveLength(0);

    // With a key it is a local HMAC, but the promise is the same one.
    const { tunna: onBackend } = client(() => json(200, {}));
    const b = await onBackend.presign(options).catch((e) => e);
    expect(b.name).toBe("AbortError");
  });
});

// Which requests presignRequest refuses. Tested through the public method, so
// the table inside client.ts is free to change shape.
describe("presignRequest guard", () => {
  const verdict = async (method: string, path: string[]) => {
    const { tunna } = client(() => json(200, {}));
    return tunna.presignRequest({ method, path, expiresIn: 60 }).then(
      () => "allowed",
      (e) => (e instanceof TypeError ? "refused" : `unexpected ${String(e)}`),
    );
  };

  const table: [string, string[], "allowed" | "refused", string][] = [
    // the five routes of ADR-0013
    ["POST", ["-", "uploads"], "refused", "initiate"],
    ["PUT", ["-", "buckets", "photos"], "refused", "bucket create"],
    ["PATCH", ["-", "buckets", "photos"], "refused", "bucket patch"],
    ["POST", ["-", "keys"], "refused", "key create"],
    ["PATCH", ["-", "keys", "tk_1"], "refused", "key patch"],
    // the method is compared without regard to case
    ["post", ["-", "uploads"], "refused", "lowercase method"],
    ["Patch", ["-", "keys", "tk_1"], "refused", "mixed case method"],
    // same paths, methods with no parameters in a body
    ["GET", ["-", "buckets", "photos"], "allowed", "bucket get"],
    ["DELETE", ["-", "buckets", "photos"], "allowed", "bucket delete"],
    ["GET", ["-", "keys", "tk_1"], "allowed", "key get"],
    ["DELETE", ["-", "keys", "tk_1"], "allowed", "key delete"],
    ["GET", ["-", "buckets"], "allowed", "bucket list"],
    ["GET", ["-", "keys"], "allowed", "key list"],
    // longer paths under the same prefixes
    ["POST", ["-", "uploads", "up_1", "complete"], "allowed", "complete has a body but stays presignable"],
    ["PUT", ["-", "uploads", "up_1", "parts", "3"], "allowed", "part"],
    ["GET", ["-", "uploads", "up_1"], "allowed", "session query"],
    ["DELETE", ["-", "uploads", "up_1"], "allowed", "abort"],
    ["POST", ["-", "keys", "tk_1", "rotate"], "allowed", "rotate"],
    ["GET", ["-", "keys", "self"], "allowed", "self"],
    // object routes, including buckets whose names look like the control plane
    ["PUT", ["photos", "a.jpg"], "allowed", "object put"],
    ["PUT", ["uploads", "a.jpg"], "allowed", "a bucket named uploads"],
    ["POST", ["keys"], "allowed", "a bucket named keys is not /-/keys"],
    ["PATCH", ["buckets", "photos"], "allowed", "a bucket named buckets"],
    ["PUT", ["photos", "-/buckets/x"], "allowed", "a key that spells a control path is one segment"],
    ["GET", ["photos"], "allowed", "listing"],
  ];

  for (const [method, path, want, why] of table) {
    test(`${method} /${path.join("/")} is ${want}: ${why}`, async () => {
      expect(await verdict(method, path)).toBe(want);
    });
  }

  test("agrees with the conformance cases: refuses what the server answers presign_not_allowed, allows every presigned step that succeeds", async () => {
    // The server's list and this package's list are two copies of one rule.
    // The spec's cases are the authority on both, so read the verdicts there.
    const { readdirSync } = await import("node:fs");
    const { loadSpec } = await import("./vectors.ts");
    type Step = { request: { method: string; path: string[]; auth?: unknown }; expect: { status?: number; error?: string } };
    const dir = new URL("../../../spec/conformance/cases/", import.meta.url);
    let refused = 0;
    let allowed = 0;
    for (const file of readdirSync(dir).filter((f) => f.endsWith(".json"))) {
      const cf = loadSpec<{ cases: { name: string; steps: Step[] }[] }>(`conformance/cases/${file}`);
      for (const c of cf.cases) {
        for (const s of c.steps) {
          const a = s.request.auth;
          if (typeof a !== "object" || a === null || !("presign" in a)) continue;
          // A captured placeholder stands for one path segment; any value does.
          const path = s.request.path.map((seg) => seg.replace(/\{\{\w+\}\}/g, "x"));
          const at = `${c.name}: ${s.request.method} /${path.join("/")}`;
          if (s.expect.error === "presign_not_allowed") {
            expect(await verdict(s.request.method, path), at).toBe("refused");
            refused++;
          } else if (s.expect.status !== undefined && s.expect.status < 300) {
            expect(await verdict(s.request.method, path), at).toBe("allowed");
            allowed++;
          }
        }
      }
    }
    // Guard against the loop silently matching nothing.
    expect(refused).toBeGreaterThanOrEqual(5);
    expect(allowed).toBeGreaterThanOrEqual(8);
  });
});
