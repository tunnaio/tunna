import { describe, expect, test } from "bun:test";
import { combine, crc32c, encodeChecksum, parseChecksum } from "../src/crc32c.ts";
import { inputBytes, loadSpec, specVersion } from "./vectors.ts";

import type { Input } from "./vectors.ts";
type Single = { name: string; input: Input; crc32c_hex: string; wire: string };
type Combine = {
  name: string;
  parts: Input[];
  part_crc32c_hex: string[];
  part_lengths: number[];
  whole_crc32c_hex: string;
  wire: string;
};

const file = loadSpec<{ spec: string; single: Single[]; combine: Combine[] }>("vectors/crc32c.json");

const hex = (n: number) => (n >>> 0).toString(16).padStart(8, "0");

describe("spec/vectors/crc32c.json", () => {
  test("declares the current spec version", () => {
    expect(file.spec).toBe(specVersion());
  });

  test("check value for 123456789", () => {
    expect(hex(crc32c(new TextEncoder().encode("123456789")))).toBe("e3069283");
  });

  for (const c of file.single) {
    test(`single: ${c.name}`, () => {
      const crc = crc32c(inputBytes(c.input));
      expect(hex(crc)).toBe(c.crc32c_hex);
      expect(encodeChecksum(crc)).toBe(c.wire);
      expect(parseChecksum(c.wire)).toBe(crc);
    });

    test(`single, streamed one byte at a time: ${c.name}`, () => {
      const bytes = inputBytes(c.input);
      let crc = 0;
      for (let i = 0; i < bytes.length; i++) {
        crc = crc32c(bytes.subarray(i, i + 1), crc);
      }
      expect(hex(crc)).toBe(c.crc32c_hex);
    });
  }

  for (const c of file.combine) {
    test(`combine: ${c.name}`, () => {
      const parts = c.parts.map(inputBytes);
      const crcs = parts.map((p) => crc32c(p));
      crcs.forEach((crc, i) => expect(hex(crc)).toBe(c.part_crc32c_hex[i]!));
      parts.forEach((p, i) => expect(p.length).toBe(c.part_lengths[i]!));

      let whole = crcs[0]!;
      for (let i = 1; i < crcs.length; i++) {
        whole = combine(whole, crcs[i]!, c.part_lengths[i]!);
      }
      expect(hex(whole)).toBe(c.whole_crc32c_hex);
      expect(encodeChecksum(whole)).toBe(c.wire);
    });
  }

  test("parseChecksum rejects malformed values", () => {
    for (const bad of ["", "crc32c=", "crc32c=AAAA", "md5=AAAAAA==", "crc32c=AAAAAA==x", "CRC32C=AAAAAA=="]) {
      expect(() => parseChecksum(bad)).toThrow();
    }
  });
});
