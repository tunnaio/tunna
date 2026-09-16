import { describe, expect, test } from "bun:test";
import { combine, crc32c, encodeChecksum, parseChecksum } from "../src/crc32c.ts";
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
};

function fakeServer(opts: { failPart?: { n: number; times: number; how: "throw" | "422" } } = {}) {
  const state: Fake = { parts: new Map(), attempts: new Map(), inFlight: 0, maxInFlight: 0, aborted: false, completed: undefined, puts: [] };
  const json = (status: number, body: unknown) =>
    new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

  const fetch = (async (input: string | URL | Request, init?: RequestInit) => {
    const url = new URL(String(input));
    const path = url.pathname;
    const method = init?.method ?? "GET";

    if (method === "POST" && path === "/-/uploads") {
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
      await new Promise((r) => setTimeout(r, 5));
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
  return { tunna, state };
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
