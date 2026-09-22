import { describe, expect, test } from "bun:test";
import { combine, crc32c, encodeChecksum, parseChecksum } from "../src/wire/crc32c.ts";
import { Tunna, TunnaError } from "../src/index.ts";
import { generate } from "./vectors.ts";

// The upload helper against a fake server behind an injected fetch: part
// slicing, the concurrency bound, retry on transport failure, abort on a
// server error, and the empty-source fallback to a single PUT.

const key = { id: "tk_test", secret: "test-secret-not-real" };
const partSize = 5 << 20;

type Fake = {
  parts: Map<number, Uint8Array>;
  attempts: Map<number, number>;
  inFlight: number;
  maxInFlight: number;
  aborted: boolean;
  completed: { parts: number; checksums?: string[] } | undefined;
  puts: string[];
  created: number; // sessions initiated
  abortAttempts: number;
};

function fakeServer(opts: { failPart?: { n: number; times: number; how: "throw" | "422" }; holdMs?: number; failAbort?: boolean } = {}) {
  const state: Fake = { parts: new Map(), attempts: new Map(), inFlight: 0, maxInFlight: 0, aborted: false, completed: undefined, puts: [], created: 0, abortAttempts: 0 };
  const json = (status: number, body: unknown) =>
    new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

  const fetch = (async (input: string | URL | Request, init?: RequestInit) => {
    const url = new URL(String(input));
    const path = url.pathname;
    const method = init?.method ?? "GET";
    init?.signal?.throwIfAborted(); // a real fetch refuses an aborted signal

    if (method === "POST" && path === "/-/uploads") {
      state.created++;
      const body = JSON.parse(String(init?.body));
      return json(201, { id: "up_1", bucket: body.bucket, key: body.key, part_size: body.part_size, content_type: body.content_type, created_at: 1, expires_at: 2 });
    }
    const part = /^\/-\/uploads\/up_1\/parts\/(\d+)$/.exec(path);
    if (method === "PUT" && part) {
      const n = Number(part[1]);
      state.attempts.set(n, (state.attempts.get(n) ?? 0) + 1);
      const fail = opts.failPart;
      if (fail && fail.n === n && state.attempts.get(n)! <= fail.times) {
        if (fail.how === "throw") throw new TypeError("socket hung up");
        return json(422, { error: { code: "checksum_mismatch", message: "nope" } });
      }
      state.inFlight++;
      state.maxInFlight = Math.max(state.maxInFlight, state.inFlight);
      await new Promise((r) => setTimeout(r, opts.holdMs ?? 5));
      state.inFlight--;
      const bytes = new Uint8Array(init?.body as ArrayBuffer | Uint8Array);
      state.parts.set(n, bytes);
      const checksum = encodeChecksum(crc32c(bytes));
      expect(new Headers(init?.headers).get("X-Tunna-Checksum")).toBe(checksum);
      return json(200, { part: n, size: bytes.length, checksum });
    }
    if (method === "POST" && path === "/-/uploads/up_1/complete") {
      state.completed = JSON.parse(String(init?.body));
      const all = new Uint8Array([...state.parts.keys()].sort((a, b) => a - b).flatMap((n) => [...state.parts.get(n)!]));
      return json(201, { bucket: "b", key: "k", size: all.length, content_type: "application/octet-stream", checksum: encodeChecksum(crc32c(all)), created_at: 3 });
    }
    if (method === "DELETE" && path === "/-/uploads/up_1") {
      state.abortAttempts++;
      if (opts.failAbort) return json(500, { error: { code: "internal", message: "abort failed" } });
      state.aborted = true;
      return new Response(null, { status: 204 });
    }
    if (method === "PUT") {
      state.puts.push(path);
      return json(201, { bucket: "b", key: "k", size: 0, content_type: "application/octet-stream", checksum: encodeChecksum(0), created_at: 3 });
    }
    return json(404, { error: { code: "unknown_route", message: "no such route" } });
  }) as typeof globalThis.fetch;

  const tunna = new Tunna({ url: "http://store.test", key, fetch, now: () => 1_788_912_000 });
  return { tunna, state, fetch };
}

describe("upload", () => {
  test("slices into numbered parts, sends each with its checksum, completes by count", async () => {
    const { tunna, state } = fakeServer();
    const data = generate(1, 2 * partSize + 1024);
    const progress: number[] = [];

    const obj = await tunna.upload("b", "k", data, { partSize, concurrency: 2, onProgress: (sent) => progress.push(sent) });

    expect([...state.parts.keys()].sort()).toEqual([1, 2, 3]);
    expect(state.parts.get(1)!.length).toBe(partSize);
    expect(state.parts.get(3)!.length).toBe(1024);
    expect(state.completed?.parts).toBe(3);
    expect(state.completed?.checksums).toHaveLength(3);
    expect(progress.at(-1)).toBe(data.length);
    expect(obj.size).toBe(data.length);

    // The whole-object checksum folds from the part checksums (ADR-0006).
    let whole = parseChecksum(state.completed!.checksums![0]!);
    whole = combine(whole, parseChecksum(state.completed!.checksums![1]!), partSize);
    whole = combine(whole, parseChecksum(state.completed!.checksums![2]!), 1024);
    expect(encodeChecksum(whole)).toBe(obj.checksum);
    expect(obj.checksum).toBe(encodeChecksum(crc32c(data)));
  });

  test("accepts a Blob and slices it lazily", async () => {
    const { tunna, state } = fakeServer();
    const data = generate(2, partSize + 7);
    const obj = await tunna.upload("b", "k", new Blob([data]), { partSize });
    expect(state.parts.get(2)!.length).toBe(7);
    expect(obj.checksum).toBe(encodeChecksum(crc32c(data)));
  });

  test("keeps at most concurrency parts in flight", async () => {
    const { tunna, state } = fakeServer();
    await tunna.upload("b", "k", generate(3, 6 * partSize), { partSize, concurrency: 2 });
    expect(state.maxInFlight).toBe(2);
    expect(state.parts.size).toBe(6);
  });

  test("retries a part on a transport failure", async () => {
    const { tunna, state } = fakeServer({ failPart: { n: 2, times: 1, how: "throw" } });
    await tunna.upload("b", "k", generate(4, 3 * partSize), { partSize, concurrency: 1 });
    expect(state.attempts.get(2)).toBe(2);
    expect(state.parts.size).toBe(3);
    expect(state.aborted).toBe(false);
  });

  test("aborts the session and rethrows on a server error", async () => {
    const { tunna, state } = fakeServer({ failPart: { n: 2, times: 99, how: "422" } });
    const err = await tunna.upload("b", "k", generate(5, 3 * partSize), { partSize, concurrency: 1 }).catch((e) => e);
    expect(err).toBeInstanceOf(TunnaError);
    expect(err.code).toBe("checksum_mismatch");
    expect(state.attempts.get(2)).toBe(1);
    expect(state.aborted).toBe(true);
    expect(state.completed).toBeUndefined();
  });

  test("an empty source is a single PUT, not a session", async () => {
    const { tunna, state } = fakeServer();
    await tunna.upload("b", "k", new Uint8Array(0), { partSize });
    expect(state.puts).toEqual(["/b/k"]);
    expect(state.parts.size).toBe(0);
  });
});

// A Blob whose slices take a fixed time to read, standing in for a disk,
// and which reports whether a read happened while a part was in flight.
class SlowBlob extends Blob {
  readonly readMs: number;
  readonly inFlight: () => number;
  reading = 0;
  maxReading = 0;
  overlapped = false;
  constructor(parts: BlobPart[], readMs: number, inFlight: () => number) {
    super(parts);
    this.readMs = readMs;
    this.inFlight = inFlight;
  }
  override slice(start?: number, end?: number, contentType?: string): Blob {
    const piece = super.slice(start, end, contentType);
    const self = this;
    const real = piece.arrayBuffer.bind(piece);
    piece.arrayBuffer = async () => {
      self.reading++;
      self.maxReading = Math.max(self.maxReading, self.reading);
      // Sampled at both ends of the read: read-ahead starts the read before
      // the previous part is on the wire, so the overlap shows at the end.
      if (self.inFlight() > 0) self.overlapped = true;
      await new Promise((r) => setTimeout(r, self.readMs));
      if (self.inFlight() > 0) self.overlapped = true;
      self.reading--;
      return real();
    };
    return piece;
  }
}

describe("upload read-ahead", () => {
  const small = 1 << 20;

  test("reads the next part while the current one is in flight", async () => {
    // One worker: in lockstep a read never overlaps a part in flight; with
    // read-ahead the read of part n+1 starts while part n is still held.
    const { tunna, state } = fakeServer({ holdMs: 60 });
    const blob = new SlowBlob([generate(6, 4 * small)], 20, () => state.inFlight);
    await tunna.upload("b", "k", blob, { partSize: small, concurrency: 1 });
    expect(state.parts.size).toBe(4);
    expect(blob.overlapped).toBe(true);
  });

  test("holds at most one prepared part per worker", async () => {
    const { tunna, state } = fakeServer({ holdMs: 30 });
    const blob = new SlowBlob([generate(7, 8 * small)], 30, () => state.inFlight);
    await tunna.upload("b", "k", blob, { partSize: small, concurrency: 2 });
    expect(blob.maxReading).toBeLessThanOrEqual(2);
  });
});

describe("upload with an AbortSignal", () => {
  test("stops without retrying, aborts the session, and rejects with the abort reason", async () => {
    // The cleanup DELETE must go out without the caller's signal, or the
    // fake refuses it like a real fetch would and the session is left behind.
    const { tunna, state } = fakeServer({ holdMs: 20 });
    const controller = new AbortController();

    const err = await tunna
      .upload("b", "k", generate(9, 6 * partSize), {
        partSize,
        concurrency: 2,
        signal: controller.signal,
        onProgress: (sent) => {
          if (sent >= 2 * partSize) controller.abort();
        },
      })
      .catch((e) => e);

    expect(err.name).toBe("AbortError");
    expect(state.completed).toBeUndefined();
    expect(state.aborted).toBe(true);
    expect(state.parts.size).toBeLessThan(6);
    for (const tries of state.attempts.values()) expect(tries).toBe(1);
  });
});

// ADR-0013: a page has no key, so its backend initiates the session and the
// page uploads into it, asking the provider for each URL.
describe("upload into a session the backend created", () => {
  const session = {
    id: "up_1",
    bucket: "b",
    key: "k",
    partSize,
    contentType: "application/octet-stream",
    createdAt: new Date(1000),
    expiresAt: new Date(2000),
    parts: [],
  };

  function pageClient(opts: Parameters<typeof fakeServer>[0] = {}) {
    const { fetch, state } = fakeServer(opts);
    const asked: { method: string; path: readonly string[]; headers?: Record<string, string> }[] = [];
    const tunna = new Tunna({
      url: "http://store.test",
      fetch,
      presign: async (request) => {
        asked.push(request);
        return `http://store.test/${request.path.join("/")}?x-tunna-sig=made-by-the-backend`;
      },
    });
    return { tunna, state, asked };
  }

  test("skips the create, takes the part size from the session, and asks the provider for every part and the complete", async () => {
    const { tunna, state, asked } = pageClient();
    const data = generate(21, 2 * partSize + 1024);

    // partSize here is ignored: the session already fixed it.
    const obj = await tunna.upload("b", "k", data, { session, partSize: 64 << 20, concurrency: 2 });

    expect(state.created).toBe(0);
    expect([...state.parts.keys()].sort()).toEqual([1, 2, 3]);
    expect(state.parts.get(3)!.length).toBe(1024);
    expect(state.completed?.parts).toBe(3);
    expect(obj.checksum).toBe(encodeChecksum(crc32c(data)));

    const paths = asked.map((a) => `${a.method} /${a.path.join("/")}`).sort();
    expect(paths).toEqual([
      "POST /-/uploads/up_1/complete",
      "PUT /-/uploads/up_1/parts/1",
      "PUT /-/uploads/up_1/parts/2",
      "PUT /-/uploads/up_1/parts/3",
    ]);
    // Each part URL binds that part's checksum, as the header form signs it.
    const part = asked.find((a) => a.path.at(-1) === "3")!;
    expect(part.headers?.["X-Tunna-Checksum"]).toBe(encodeChecksum(crc32c(data.subarray(2 * partSize))));
  });

  test("without a key and without a session it is refused before anything is sent", async () => {
    const { tunna, state, asked } = pageClient();
    const err = await tunna.upload("b", "k", generate(22, partSize + 1)).catch((e) => e);
    expect(err).toBeInstanceOf(TypeError);
    expect(err.message).toContain("session");
    expect(asked).toHaveLength(0);
    expect(state.created).toBe(0);
  });

  test("a failed part aborts the session through the provider too", async () => {
    const { tunna, state, asked } = pageClient({ failPart: { n: 2, times: 9, how: "422" } });
    const err = await tunna.upload("b", "k", generate(23, 3 * partSize), { session, concurrency: 1 }).catch((e) => e);
    expect(err).toBeInstanceOf(TunnaError);
    expect(state.aborted).toBe(true);
    expect(asked.some((a) => a.method === "DELETE" && a.path.join("/") === "-/uploads/up_1")).toBe(true);
  });

  test("a client with a key may be handed a session as well, and then does not create one", async () => {
    // The same option is what resuming after a reload will need.
    const { tunna, state } = fakeServer();
    await tunna.upload("b", "k", generate(24, partSize + 7), { session, concurrency: 2 });
    expect(state.created).toBe(0);
    expect(state.completed?.parts).toBe(2);
  });
});

// An empty source is a single PUT, so a session handed in would be left
// unused until it expires, blocking the bucket's deletion meanwhile.
describe("upload of an empty source into a given session", () => {
  const session = { id: "up_1", bucket: "b", key: "k", partSize, contentType: "application/octet-stream", createdAt: new Date(1000), expiresAt: new Date(2000), parts: [] };

  test("puts the object, then aborts the session it did not use", async () => {
    const { tunna, state } = fakeServer();
    const obj = await tunna.upload("b", "k", new Uint8Array(0), { session });
    expect(state.puts).toEqual(["/b/k"]);
    expect(state.parts.size).toBe(0);
    expect(state.completed).toBeUndefined();
    expect(state.aborted).toBe(true);
    expect(obj.size).toBe(0);
  });

  test("the abort is best effort: if it fails, the upload still succeeded", async () => {
    const { tunna, state } = fakeServer({ failAbort: true });
    const obj = await tunna.upload("b", "k", new Uint8Array(0), { session });
    expect(state.abortAttempts).toBe(1);
    expect(state.puts).toEqual(["/b/k"]);
    expect(obj.size).toBe(0);
  });

  test("if the put fails there is nothing to clean up differently: the session is still aborted, and the put's error is the one thrown", async () => {
    const { fetch, state } = fakeServer();
    const failingPut = (async (input: string | URL | Request, init?: RequestInit) => {
      if ((init?.method ?? "GET") === "PUT" && new URL(String(input)).pathname === "/b/k") {
        return new Response(JSON.stringify({ error: { code: "forbidden", message: "no" } }), { status: 403, headers: { "Content-Type": "application/json" } });
      }
      return fetch(input, init);
    }) as typeof globalThis.fetch;
    const tunna = new Tunna({ url: "http://store.test", key, fetch: failingPut });
    const err = await tunna.upload("b", "k", new Uint8Array(0), { session }).catch((e) => e);
    expect(err).toBeInstanceOf(TunnaError);
    expect(err.code).toBe("forbidden");
    expect(state.aborted).toBe(true);
  });

  test("without a session there is nothing to abort", async () => {
    const { tunna, state } = fakeServer();
    await tunna.upload("b", "k", new Uint8Array(0));
    expect(state.abortAttempts).toBe(0);
    expect(state.puts).toEqual(["/b/k"]);
  });
});
