// Runs under plain Node against the built ESM output, with no Bun and no
// TypeScript: proves the package uses platform APIs only (ADR-0010). Signs
// the first header-form vector and compares.
import { readFileSync } from "node:fs";
import { sign, SPEC_VERSION } from "../dist/index.mjs";

const vectors = JSON.parse(readFileSync(new URL("../../../spec/vectors/signing.json", import.meta.url), "utf8"));
const c = vectors.cases.find((x) => x.mode === "header");
const got = await sign.authorization(
  { method: c.request.method, path: c.request.path, query: c.request.query, headers: c.request.headers, signedHeaders: c.request.signed_headers },
  c.key,
  c.time,
);
if (got !== c.expected.authorization) {
  console.error(`esm smoke: ${c.name}\n got  ${got}\n want ${c.expected.authorization}`);
  process.exit(1);
}
if (SPEC_VERSION !== vectors.spec) {
  console.error(`esm smoke: SPEC_VERSION ${SPEC_VERSION} != vectors ${vectors.spec}`);
  process.exit(1);
}
console.log(`esm smoke ok under node ${process.version}: ${c.name}`);
