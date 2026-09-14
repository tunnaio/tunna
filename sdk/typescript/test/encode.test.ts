import { describe, expect, test } from "bun:test";
import { encodePath, encodeQuery, encodeSegment } from "../src/encode.ts";
import { loadSpec, specVersion } from "./vectors.ts";

type Case =
  | { name: string; kind: "segment"; input: string; expected: string; note?: string }
  | { name: string; kind: "path"; input: string[]; expected: string; note?: string }
  | { name: string; kind: "query"; input: [string, string][]; expected: string; note?: string };

const file = loadSpec<{ spec: string; cases: Case[] }>("vectors/encoding.json");

describe("spec/vectors/encoding.json", () => {
  test("declares the current spec version", () => {
    expect(file.spec).toBe(specVersion());
  });

  for (const c of file.cases) {
    test(`${c.kind}: ${c.name}`, () => {
      switch (c.kind) {
        case "segment":
          expect(encodeSegment(c.input)).toBe(c.expected);
          break;
        case "path":
          expect(encodePath(c.input)).toBe(c.expected);
          break;
        case "query":
          expect(encodeQuery(c.input)).toBe(c.expected);
          break;
      }
    });
  }
});
