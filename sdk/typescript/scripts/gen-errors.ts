// Generates src/errors.generated.ts from spec/errors.json and spec/VERSION,
// so the error-code union and the spec version are never typed by hand
// (ADR-0010: nothing specified twice). Run with `bun run gen`; the test in
// test/errors.test.ts fails when the committed output is stale.

import { readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const here = fileURLToPath(new URL(".", import.meta.url));
const specDir = new URL("../../../spec/", import.meta.url);

type ErrorTable = {
  spec: string;
  stages: { stage: number; name: string }[];
  errors: { code: string; status: number; stage: number; description: string }[];
};

export function render(): string {
  const table = JSON.parse(readFileSync(new URL("errors.json", specDir), "utf8")) as ErrorTable;
  const version = readFileSync(new URL("VERSION", specDir), "utf8").trim();

  const lines: string[] = [];
  lines.push("// Generated from spec/errors.json and spec/VERSION by scripts/gen-errors.ts.");
  lines.push("// Do not edit; run `bun run gen`.");
  lines.push("");
  lines.push("/** The wire contract version this package implements; compare with GET /-/version. */");
  lines.push(`export const SPEC_VERSION = ${JSON.stringify(version)};`);
  lines.push("");
  lines.push("/** Every error code the server can answer with (spec/wire.md 9). */");
  lines.push("export type ErrorCode =");
  for (const e of table.errors) {
    lines.push(`  | ${JSON.stringify(e.code)}`);
  }
  lines[lines.length - 1] += ";";
  lines.push("");
  lines.push("/** HTTP status for each code. */");
  lines.push("export const ERROR_STATUS: Readonly<Record<ErrorCode, number>> = {");
  for (const e of table.errors) {
    lines.push(`  ${e.code}: ${e.status},`);
  }
  lines.push("};");
  lines.push("");
  const stageList = table.stages.map((s) => `${s.stage} ${s.name}`).join(", ");
  lines.push(`/** A stage of the request ladder (ADR-0004): ${stageList}. */`);
  lines.push(`export type ErrorStage = ${table.stages.map((s) => s.stage).join(" | ")};`);
  lines.push("");
  lines.push("/** The spec's name for a stage. */");
  lines.push(`export type ErrorStageName = ${table.stages.map((s) => JSON.stringify(s.name)).join(" | ")};`);
  lines.push("");
  lines.push("/** The name of each stage, for messages and grouping. */");
  lines.push("export const ERROR_STAGE_NAME: Readonly<Record<ErrorStage, ErrorStageName>> = {");
  for (const s of table.stages) {
    lines.push(`  ${s.stage}: ${JSON.stringify(s.name)},`);
  }
  lines.push("};");
  lines.push("");
  lines.push("/** The ladder stage each code is answered at. */");
  lines.push("export const ERROR_STAGE: Readonly<Record<ErrorCode, ErrorStage>> = {");
  for (const e of table.errors) {
    if (!table.stages.some((s) => s.stage === e.stage)) {
      throw new Error(`spec/errors.json: ${e.code} names stage ${e.stage}, which is not in stages`);
    }
    lines.push(`  ${e.code}: ${e.stage},`);
  }
  lines.push("};");
  lines.push("");
  lines.push("/** Type guard for a string off the wire. */");
  lines.push("export function isErrorCode(s: string): s is ErrorCode {");
  lines.push("  return Object.hasOwn(ERROR_STATUS, s);");
  lines.push("}");
  lines.push("");
  return lines.join("\n");
}

export const outputPath = new URL("../src/errors.generated.ts", import.meta.url);

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  writeFileSync(outputPath, render());
  console.log(`wrote ${fileURLToPath(outputPath)} from ${here}`);
}
