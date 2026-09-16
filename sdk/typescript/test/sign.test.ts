import { describe, expect, test } from "bun:test";
import {
  authorization,
  canonical,
  presignQuery,
  signature,
  type Key,
  type SigningRequest,
} from "../src/sign.ts";
import { loadSpec, specVersion } from "./vectors.ts";

type Case = {
  name: string;
  note?: string;
  mode: "header" | "presign";
  key: Key;
  request: {
    method: string;
    path: string[];
    query?: [string, string][];
    headers?: Record<string, string>;
    signed_headers?: string[];
  };
  time: number;
  expected: {
    canonical: string;
    signature: string;
    authorization?: string;
    query?: string;
  };
};

const file = loadSpec<{ spec: string; cases: Case[] }>("vectors/signing.json");

function toRequest(r: Case["request"]): SigningRequest {
  const req: SigningRequest = { method: r.method, path: r.path };
  if (r.query) req.query = r.query;
  if (r.headers) req.headers = r.headers;
  if (r.signed_headers) req.signedHeaders = r.signed_headers;
  return req;
}

describe("spec/vectors/signing.json", () => {
  test("declares the current spec version", () => {
    expect(file.spec).toBe(specVersion());
  });

  for (const c of file.cases) {
    const req = toRequest(c.request);

    test(`${c.name}: canonical`, () => {
      expect(canonical(req, c.key, c.time, c.mode)).toBe(c.expected.canonical);
    });

    test(`${c.name}: signature`, async () => {
      expect(await signature(c.key.secret, c.expected.canonical)).toBe(
        c.expected.signature,
      );
    });

    // Captured outside the closures: TypeScript's narrowing of an optional
    // field does not carry into a callback, so the callback would see
    // string | undefined again.
    const wantAuthorization = c.expected.authorization;
    if (wantAuthorization !== undefined) {
      test(`${c.name}: authorization header`, async () => {
        expect(await authorization(req, c.key, c.time)).toBe(wantAuthorization);
      });
    }

    const wantQuery = c.expected.query;
    if (wantQuery !== undefined) {
      test(`${c.name}: presigned query`, async () => {
        expect(await presignQuery(req, c.key, c.time)).toBe(wantQuery);
      });
    }
  }
});
