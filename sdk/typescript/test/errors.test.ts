import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { outputPath, render } from "../scripts/gen-errors.ts";
import { ERROR_STAGE, ERROR_STAGE_NAME, ERROR_STATUS, SPEC_VERSION, isErrorCode, type ErrorStage } from "../src/errors.generated.ts";
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
        expect<number>(ERROR_STAGE[e.code]).toBe(e.stage);
      }
    }
    expect(isErrorCode("not_a_code")).toBe(false);
  });

  test("stages are the literal union from the spec's stage list", () => {
    const table = loadSpec<{ stages: { stage: number; name: string }[] }>("errors.json");
    // Compile-time half: a stage outside the union must not typecheck.
    const six: ErrorStage = 6;
    // @ts-expect-error 7 is not a stage
    const seven: ErrorStage = 7;
    void six, seven;
    // Runtime half: every stage in the table is one the spec lists.
    const listed = new Set(table.stages.map((s) => s.stage));
    for (const stage of Object.values(ERROR_STAGE)) expect(listed.has(stage)).toBe(true);
    // And every stage has the spec's name, nothing more, nothing less.
    expect<Record<number, string>>(ERROR_STAGE_NAME).toEqual(Object.fromEntries(table.stages.map((s) => [s.stage, s.name])));
  });

  test("carries the spec version", () => {
    expect(SPEC_VERSION).toBe(specVersion());
  });
});
