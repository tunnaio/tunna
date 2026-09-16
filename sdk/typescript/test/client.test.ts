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
