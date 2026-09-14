import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { outputPath, render } from "../scripts/gen-errors.ts";
import { ERROR_STAGE, ERROR_STATUS, SPEC_VERSION, isErrorCode } from "../src/errors.generated.ts";
import { loadSpec, specVersion } from "./vectors.ts";

describe("src/errors.generated.ts", () => {
  test("is current; run `bun run gen` if this fails", () => {
    expect(readFileSync(outputPath, "utf8")).toBe(render());
  });

  test("matches spec/errors.json", () => {
    const table = loadSpec<{ errors: { code: string; status: number; stage: number }[] }>("errors.json");
    expect(Object.keys(ERROR_STATUS).length).toBe(table.errors.length);
    for (const e of table.errors) {
      expect(isErrorCode(e.code)).toBe(true);
      if (isErrorCode(e.code)) {
        expect(ERROR_STATUS[e.code]).toBe(e.status);
        expect(ERROR_STAGE[e.code]).toBe(e.stage);
      }
    }
    expect(isErrorCode("not_a_code")).toBe(false);
  });

  test("carries the spec version", () => {
    expect(SPEC_VERSION).toBe(specVersion());
  });
});
