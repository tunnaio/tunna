// Runs under plain Node against the built CommonJS output (ADR-0010):
// proves the require() entry works. Signs the first presigned vector.
const { readFileSync } = require("node:fs");
const path = require("node:path");
const { sign, SPEC_VERSION } = require("../dist/index.cjs");

const vectors = JSON.parse(readFileSync(path.join(__dirname, "..", "..", "..", "spec", "vectors", "signing.json"), "utf8"));
const c = vectors.cases.find((x) => x.mode === "presign");

sign
  .presignQuery(
    { method: c.request.method, path: c.request.path, query: c.request.query, headers: c.request.headers, signedHeaders: c.request.signed_headers },
    c.key,
    c.time,
  )
  .then((got) => {
    if (got !== c.expected.query) {
      console.error(`cjs smoke: ${c.name}\n got  ${got}\n want ${c.expected.query}`);
      process.exit(1);
    }
    if (SPEC_VERSION !== vectors.spec) {
      console.error(`cjs smoke: SPEC_VERSION ${SPEC_VERSION} != vectors ${vectors.spec}`);
      process.exit(1);
    }
    console.log(`cjs smoke ok under node ${process.version}: ${c.name}`);
  });
