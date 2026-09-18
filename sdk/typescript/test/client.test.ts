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
  test("a failed HEAD names the status and says why there is no code", async () => {
    // HEAD answers carry the error's Content-Type but no body, so the code
    // cannot be read; the message must say so rather than "code undefined".
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
