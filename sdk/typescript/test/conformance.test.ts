// Replays spec/conformance/cases/*.json against cmd/tunna-fixtures with the
// SDK's own encoder and signer (ADR-0010). A port of the Go runner in
// internal/httpapi/conformance_test.go: same file shapes, same checks.
//
// One difference the platform forces: fetch cannot send Expect:
// 100-continue, so expect_continue steps send the body eagerly. The server
// answers the same either way; only the bytes on the wire differ.

import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import { createHash } from "node:crypto";
import { readdirSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { encodePath, encodeQuery } from "../src/encode.ts";
import { SPEC_VERSION } from "../src/errors.generated.ts";
import { authorization, HEADER_DATE, presignQuery, type Key, type Request as SigRequest } from "../src/sign.ts";
import { FixtureServer } from "./fixture-server.ts";
import { generate, loadSpec, specVersion } from "./vectors.ts";

// --- spec file shapes (subset of the schemas, enough to run) ---

type Pair = [string, string];
type Gen = { seed: number; length: number };

type Auth =
  | "none"
  | { key: string; signed_headers?: string[]; time_offset_seconds?: number; corrupt_signature?: boolean }
  | { presign: { key: string; expires_in_seconds: number; signed_headers?: string[] } };

type Body = { json?: unknown; text?: string; hex?: string; bytes?: Gen };

type Expect = {
  status?: number;
  error?: string;
  headers?: Record<string, string | { present: boolean }>;
  json?: unknown;
  json_present?: string[];
  json_absent?: string[];
  body_sha256?: string;
  body_length?: number;
  body_empty?: boolean;
  body_bytes?: Gen;
};

type Step = {
  name?: string;
  request: { method: string; path: string[]; query?: Pair[]; headers?: Record<string, string>; auth?: Auth; body?: Body; expect_continue?: boolean };
  expect: Expect;
  capture?: Record<string, string>;
};

type Case = { name: string; description?: string; fixtures?: string[]; steps: Step[] };
type CaseFile = { spec: string; cases: Case[] };

type Fixtures = {
  keys: Record<string, { id: string; secret: string }>;
  buckets: Record<string, unknown>;
};

const fx = loadSpec<Fixtures>("conformance/fixtures.json");
const statusOf = new Map(loadSpec<{ errors: { code: string; status: number }[] }>("errors.json").errors.map((e) => [e.code, e.status]));
const casesDir = new URL("../../../spec/conformance/cases/", import.meta.url);

function fixtureKey(name: string): Key {
  const k = fx.keys[name];
  if (!k) throw new Error(`no key fixture ${name}`);
  return { id: k.id, secret: k.secret };
}

function unsupported(c: Case): string | undefined {
  for (const name of c.fixtures ?? []) {
    if (!(name in fx.keys) && !(name in fx.buckets)) return `unknown fixture ${name}`;
  }
  return undefined;
}

// --- the runner ---

let server: FixtureServer;

beforeAll(async () => {
  server = await FixtureServer.start();
});

afterAll(() => {
  server?.stop();
});

test("the fixture server implements this package's spec version", async () => {
  const res = await fetch(`${server.url}/-/version`);
  const body = (await res.json()) as { spec: string };
  expect(body.spec).toBe(SPEC_VERSION);
});

for (const file of readdirSync(casesDir).filter((f) => f.endsWith(".json")).sort()) {
  const cf = loadSpec<CaseFile>(`conformance/cases/${file}`);

  describe(file, () => {
    test("declares the current spec version", () => {
      expect(cf.spec).toBe(specVersion());
    });

    for (const c of cf.cases) {
      const reason = unsupported(c);
      const run = reason ? test.skip : test;
      run(c.name, async () => {
        // A fresh server per case: no case can poison another, and a case
        // that mutates a fixture is still a valid case.
        await server.reset();
        await runCase(c);
      });
    }
  });
}

async function runCase(c: Case): Promise<void> {
  const captured = new Map<string, string>();
  const sub = (s: string) => substitute(s, captured);

  for (const [i, s] of c.steps.entries()) {
    const name = s.name ?? `step-${i + 1}`;
    const at = (msg: string) => `${c.name} / ${name}: ${msg}`;

    // Decoded inputs, after substitution.
    const segments = s.request.path.map(sub);
    const query: Pair[] = (s.request.query ?? []).map(([k, v]) => [sub(k), sub(v)]);
    const headers: Record<string, string> = {};
    for (const [k, v] of Object.entries(s.request.headers ?? {})) headers[k] = sub(v);

    // Body
    let body: Uint8Array<ArrayBuffer> | string | undefined;
    if (s.request.body) {
      const b = s.request.body;
      if (b.json !== undefined) {
        body = sub(JSON.stringify(b.json));
        if (!Object.keys(headers).some((h) => h.toLowerCase() === "content-type")) headers["Content-Type"] = "application/json";
      } else if (b.text !== undefined) {
        body = sub(b.text);
      } else if (b.hex !== undefined) {
        body = Uint8Array.from(Buffer.from(b.hex, "hex"));
      } else if (b.bytes !== undefined) {
        body = generate(b.bytes.seed, b.bytes.length);
      }
    }

    const now = Math.floor(Date.now() / 1000);
    const auth = s.request.auth ?? "none";
    const sigReq: SigRequest = { method: s.request.method, path: segments, query, headers };
    let url = server.url + encodePath(segments);

    if (auth !== "none" && "presign" in auth) {
      const p = auth.presign;
      if (p.signed_headers) sigReq.signedHeaders = p.signed_headers;
      url += "?" + (await presignQuery(sigReq, fixtureKey(p.key), now + p.expires_in_seconds));
    } else if (query.length > 0) {
      url += "?" + encodeQuery(query);
    }

    if (auth !== "none" && "key" in auth) {
      if (auth.signed_headers) sigReq.signedHeaders = auth.signed_headers;
      const ts = now + (auth.time_offset_seconds ?? 0);
      let authz = await authorization(sigReq, fixtureKey(auth.key), ts);
      if (auth.corrupt_signature) authz = corruptLastHex(authz);
      headers[HEADER_DATE] = String(ts);
      headers["Authorization"] = authz;
    }

    const res = await fetch(url, { method: s.request.method, headers, body: body ?? null });
    const respBody = new Uint8Array(await res.arrayBuffer());

    check(at, s.expect, res, respBody, sub);

    for (const [key, from] of Object.entries(s.capture ?? {})) {
      captured.set(key, capture(from, res, respBody));
    }
  }
}

function check(at: (m: string) => string, e: Expect, res: Response, body: Uint8Array, sub: (s: string) => string): void {
  const text = () => new TextDecoder().decode(body);
  let wantStatus = e.status;

  if (e.error !== undefined) {
    const st = statusOf.get(e.error);
    if (st === undefined) throw new Error(at(`expects error ${e.error} which is not in errors.json`));
    wantStatus ??= st;
    const eb = parseJSON(body);
    expect(eb, at(`error body is not JSON: ${text()}`)).not.toBeUndefined();
    expect((eb as { error?: { code?: string } })?.error?.code, at("error code")).toBe(e.error);
  }
  if (wantStatus !== undefined) {
    expect(res.status, at(`status; body: ${text()}`)).toBe(wantStatus);
  }

  for (const [h, want] of Object.entries(e.headers ?? {})) {
    if (typeof want === "string") {
      expect(res.headers.get(h) ?? "", at(`header ${h}`)).toBe(want);
    } else {
      expect(res.headers.has(h), at(`header ${h} present`)).toBe(want.present);
    }
  }

  if (e.json !== undefined || e.json_present?.length || e.json_absent?.length) {
    const got = parseJSON(body);
    expect(got, at(`body is not JSON: ${text()}`)).not.toBeUndefined();
    if (e.json !== undefined) {
      // Captures apply inside the expectation too.
      const want = JSON.parse(sub(JSON.stringify(e.json)));
      const diff = subset(want, got, "");
      if (diff) throw new Error(at(`${diff}\nbody: ${text()}`));
    }
    for (const ptr of e.json_present ?? []) {
      if (pointer(got, ptr) === undefined) throw new Error(at(`${ptr} should be present\nbody: ${text()}`));
    }
    for (const ptr of e.json_absent ?? []) {
      if (pointer(got, ptr) !== undefined) throw new Error(at(`${ptr} should be absent`));
    }
  }

  if (e.body_sha256 !== undefined) {
    expect(createHash("sha256").update(body).digest("hex"), at("body sha256")).toBe(e.body_sha256);
  }
  if (e.body_length !== undefined) {
    expect(body.length, at("body length")).toBe(e.body_length);
  }
  if (e.body_empty !== undefined) {
    expect(body.length === 0, at("body empty")).toBe(e.body_empty);
  }
  if (e.body_bytes !== undefined) {
    expect(Buffer.from(body).equals(generate(e.body_bytes.seed, e.body_bytes.length)), at("body equals generated bytes")).toBe(true);
  }
}

// --- helpers ---

function parseJSON(body: Uint8Array): unknown {
  try {
    return JSON.parse(new TextDecoder().decode(body));
  } catch {
    return undefined;
  }
}

function substitute(s: string, vars: Map<string, string>): string {
  for (const [k, v] of vars) s = s.replaceAll(`{{${k}}}`, v);
  return s;
}

function corruptLastHex(s: string): string {
  return s.slice(0, -1) + (s.endsWith("0") ? "1" : "0");
}

/** The first place where want is not a subset of got, or "" when it is. */
function subset(want: unknown, got: unknown, at: string): string {
  const here = at || "/";
  if (Array.isArray(want)) {
    if (!Array.isArray(got)) return `${here}: want array, got ${typeof got}`;
    if (got.length !== want.length) return `${here}: array length ${got.length}, want ${want.length}`;
    for (let i = 0; i < want.length; i++) {
      const d = subset(want[i], got[i], `${at}/${i}`);
      if (d) return d;
    }
    return "";
  }
  if (want !== null && typeof want === "object") {
    if (got === null || typeof got !== "object" || Array.isArray(got)) return `${here}: want object, got ${typeof got}`;
    for (const [k, wv] of Object.entries(want)) {
      if (!(k in got)) return `${at}/${k}: missing`;
      const d = subset(wv, (got as Record<string, unknown>)[k], `${at}/${k}`);
      if (d) return d;
    }
    return "";
  }
  return Object.is(want, got) ? "" : `${here}: got ${JSON.stringify(got)}, want ${JSON.stringify(want)}`;
}

/** RFC 6901 JSON pointer; undefined when it does not resolve. */
function pointer(v: unknown, ptr: string): unknown {
  if (ptr === "") return v;
  if (!ptr.startsWith("/")) return undefined;
  let cur = v;
  for (const raw of ptr.slice(1).split("/")) {
    const tok = raw.replaceAll("~1", "/").replaceAll("~0", "~");
    if (Array.isArray(cur)) {
      const i = Number(tok);
      if (!Number.isInteger(i) || i < 0 || i >= cur.length) return undefined;
      cur = cur[i];
    } else if (cur !== null && typeof cur === "object") {
      if (!(tok in cur)) return undefined;
      cur = (cur as Record<string, unknown>)[tok];
    } else {
      return undefined;
    }
  }
  return cur;
}

function capture(from: string, res: Response, body: Uint8Array): string {
  if (from.startsWith("header:")) return res.headers.get(from.slice("header:".length)) ?? "";
  const v = parseJSON(body);
  if (v === undefined) throw new Error(`capture ${from}: body is not JSON`);
  const val = pointer(v, from);
  if (val === undefined) throw new Error(`capture ${from}: not found`);
  return typeof val === "string" ? val : JSON.stringify(val);
}
