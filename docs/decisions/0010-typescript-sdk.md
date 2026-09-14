# ADR-0010: TypeScript SDK shape

**Status:** Accepted
**Date:** 2026-09-15
**Deciders:** maintainer

## Context

The server covers the whole wire contract a client needs (ADR-0001 to
ADR-0009). The first SDK is TypeScript, decided 2026-09-15 in place of
Rust: the maintainer wants a known language after the Go server, and a
TypeScript client is the one that runs in a browser, so it is the first
client to exercise CORS and presigned uploads end to end rather than from
tests.

ADR-0005 already placed SDKs at `sdk/<language>/` in this repository, each
with its own toolchain. What this record decides is the shape of the
TypeScript one, and one thing the whole SDK programme needs and does not
yet have: a way for a runner outside the Go test binary to replay the
conformance cases.

Forces:

- **One contract, many API shapes.** SDKs share the spec, never the API
  shape (project notes). A TypeScript client should feel like `fetch` and
  `Promise`, not like a Go client transliterated.
- **Browser and server both.** The predecessor's main client was a browser
  uploading with presigned URLs; Node servers minting those URLs is the
  other half. One package must serve both without two builds.
- **The spec is the oracle.** Encoding, signing, CRC32C, authorization and
  CORS each have a vector file; the conformance cases have fixtures. The
  SDK's tests read those files, not copies.
- **Nothing specified twice.** The error-code table lives in
  `spec/errors.json`. A hand-maintained TypeScript union of the same codes
  would drift.
- **Small toolchain.** A rest project, not a tooling project. Every
  dependency and build step is a cost to justify.
- **Fast uploads are the product goal.** ADR-0001's shape, numbered parts
  of fixed size into one file, exists so a client can send parts
  concurrently. The SDK is where that pays off or does not.

## Options considered

### Option A: Runtime and crypto

**A1: Web platform APIs only: `fetch`, `crypto.subtle`, `TextEncoder`,
`ReadableStream`.** Present in every browser and in Node 20 and later as
globals, and in Bun and Deno. HMAC-SHA256 via `crypto.subtle` is
asynchronous, so signing is asynchronous; every call that signs is already
on the request path, which is asynchronous anyway. Zero runtime
dependencies.

**A2: Node's `crypto` module with a browser shim.** Synchronous HMAC, but
two code paths, a bundler concern for browser users, and a dependency on
Node's API surface for a package half of whose users are browsers.
Rejected.

**A3: Pure-TypeScript SHA-256 for a synchronous signer.** Removes the
`await` from `presign` at the cost of carrying a hash implementation and
its audit burden. The asynchrony is one `await` in the caller. Rejected.

CRC32C has no platform API; it is a 256-entry table and a loop, in pure
TypeScript, verified by `spec/vectors/crc32c.json`.

### Option B: Module format and tooling

**B1: ESM and CommonJS from one source, built with `tsdown`; Bun as the
development toolchain.** `tsdown` emits both formats and the `.d.ts` files
in one step from one `src/`, and the `exports` map serves `import` and
`require` callers the right file. Bun runs TypeScript directly, so
`bun test` runs the `.ts` test files with its built-in runner and no build
step, and `bun run` drives the scripts. Type checking is `tsc --noEmit`.
Development dependencies: `typescript` and `tsdown`; Bun is a tool on the
developer's machine and in CI, never a dependency of the package. The code
itself uses only platform APIs (A1), and one CI step proves it by running
a smoke script under plain Node against both built outputs, once with
`import` and once with `require`, so a Bun-only API or a broken CommonJS
entry cannot creep in unnoticed.

**B2: ESM only.** Halves the published surface and removes the
dual-package hazard, at the cost of every `require` caller on Node 20,
where `require(esm)` is not yet available. The hazard is real only for a
package with module-level state that two copies could duplicate; this SDK
has none, since every instance is one the caller constructs. With the
hazard moot and `tsdown` making the second format free, serving `require`
callers wins. Rejected.

**B3: Node's own toolchain: `node --test` with native type stripping.**
Zero tools beyond Node 22.6 and later, and it was the first draft of this
record. Bun was preferred because the maintainer's TypeScript work already
runs on it, its test runner is faster and its watch mode better, and
running the tests under the runtime the maintainer uses day to day is
worth more than one fewer install. The platform-only rule and the Node
smoke check keep the package itself indifferent to the choice.

**B4: A bundler and a test framework.** `tsup` or `esbuild` plus `vitest`
is the common stack and would work. It is four to eight dependencies to do
what `tsdown` and `bun test` do for this package's size; `tsdown` is one
tool doing the bundler's one job. Rejected for now.

### Option C: API shape

**C1: One client object with grouped methods and a small set of types.**

```ts
const tunna = new Tunna({ url: "https://store.example.com", key: { id, secret } });

await tunna.buckets.list();
await tunna.buckets.create("photos", { public: false });
await tunna.objects.put("photos", "2026/a.jpg", bytes, { contentType: "image/jpeg", metadata: { camera: "x" } });
const res = await tunna.objects.get("photos", "2026/a.jpg");   // { body: ReadableStream, size, contentType, etag, checksum, metadata, lastModified }
await tunna.objects.delete("photos", "2026/a.jpg");
for await (const obj of tunna.objects.list("photos", { prefix: "2026/" })) { ... }
await tunna.upload("photos", "big.bin", file, { partSize, concurrency, onProgress });
const url = await tunna.presign({ method: "GET", bucket: "photos", key: "a.jpg", expiresIn: 3600 });
await tunna.keys.create({ name: "web", scopes: { photos: "write" } });
```

Errors are one class, `TunnaError`, carrying `code`, `status`, `message`
and `details` from the wire error body, with `code` typed as the union
generated from `spec/errors.json`. Transport failures are a separate
class so a caller can tell "the server said no" from "the network broke".

**C2: One function per route, no client object.** Simpler to tree-shake,
but every call repeats the URL and key, and presign and upload need
shared state anyway. Rejected.

**C3: A generated client from an OpenAPI description.** There is no
OpenAPI description and writing one to generate a client that then needs
a hand-written signer and uploader is two artefacts to keep in step.
Rejected.

### Option D: Conformance for a non-Go runner

The Go runner provisions fixtures in-process and builds a fresh server per
case. An SDK runner cannot do that; it needs a server it can talk to over
the wire, in the fixture state, reset between cases.

**D1: A fixture server binary, `cmd/tunna-fixtures`.** It reuses the
provisioning code, moved from the Go runner's test file into an internal
package, to build stores in the fixture state, serves `httpapi` on a local
port, and exposes one control route outside `httpapi`, `POST /reset`,
that rebuilds the stores and swaps the handler. The SDK's conformance test
starts it, resets before each case, replays the case with the SDK's own
signer and HTTP layer, and checks the expectation the same way the Go
runner does. `httpapi` itself gains nothing.

**D2: A reset route inside `httpapi` behind an option.** Leaks a test
concern into the adapter and into the wire. Rejected.

**D3: Provision through the wire from the SDK runner.** Create keys and
buckets with the bootstrap key, upload the fixture objects, before each
case. Slower by an order of magnitude, and it means the runner depends on
the routes it is meant to test. Rejected as the primary path; it remains
possible for a hosted server if ever needed.

## Trade-off analysis

**The platform is the dependency.** A1 and B1 together mean the package's
runtime dependency list is empty and its development dependency list is
two items, with Bun as the tool that runs it all. That is unusual and worth
protecting: every added tool must beat "the platform already does this".
Bun is allowed because it replaces tools rather than adding one, and
because the Node smoke check makes its absence from the package a tested
fact rather than a promise.

**Asynchronous signing is a non-cost.** The only place a synchronous
signature would be nicer is `presign`, and its callers are request
handlers or event handlers, which are asynchronous already.

**The upload helper is the reason to have an SDK.** Initiate, `n` parts
sent with a bounded number in flight, complete by count, retry a failed
part alone: this is what ADR-0001 bought, and it is fifty lines the user
should not write. It computes each part's CRC32C on the way out and sends
it in the signed checksum header, so a corrupted part is caught by the
server per part, and the combined checksum is available to compare on
complete. In a browser it slices a `File` without reading it all into
memory.

**Generated codes, checked-in output.** A small script reads
`spec/errors.json` and writes `src/errors.generated.ts` with the union
type and a status map. The output is committed so consumers and editors
see it, and a test fails when it is stale. This is the smallest form of
"nothing specified twice" that works without a build step for users.

**The fixture server is shared infrastructure**, not a TypeScript
concern. Every later SDK uses it. Building it first, before any
TypeScript, means the SDK's conformance test exists from the first
commit rather than being retrofitted.

## Decision

**A1, B1, C1, D1.**

### Layout

```
sdk/typescript/
├── package.json          name "tunna", type "module", exports (import/require/types), files, scripts
├── bun.lock              Bun's lockfile; bunfig.toml only if a setting is needed
├── tsdown.config.ts      entry points, formats esm and cjs, dts
├── tsconfig.json         strict, ES2022 target, NodeNext resolution
├── dist/                 built output, not committed
├── src/
│   ├── index.ts          public surface
│   ├── client.ts         Tunna class; buckets, objects, keys, uploads groups
│   ├── sign.ts           canonical string, HMAC, header and presign forms
│   ├── encode.ts         segment, path, query encoding
│   ├── crc32c.ts         table, update, combine, wire form
│   ├── upload.ts         the concurrent upload helper
│   ├── errors.ts         TunnaError, TransportError
│   └── errors.generated.ts   from spec/errors.json; do not edit
├── scripts/
│   └── gen-errors.ts     reads ../../spec/errors.json
└── test/                 bun test
    ├── node-smoke.mjs    plain Node: import the built ESM, sign one vector
    ├── node-smoke.cjs    plain Node: require the built CJS, sign one vector
    ├── encode.test.ts    spec/vectors/encoding.json
    ├── sign.test.ts      spec/vectors/signing.json
    ├── crc32c.test.ts    spec/vectors/crc32c.json
    ├── errors.test.ts    generated file is current
    └── conformance.test.ts   starts cmd/tunna-fixtures, replays spec/conformance
```

Tests read the spec by relative path; the package does not ship the spec.

### Package

Name `tunna` on npm, ESM and CommonJS, `engines.node >= 20`, no runtime
dependencies. Exports `Tunna`, `TunnaError`, `TransportError`, the
generated `ErrorCode` type, the record types, and the low-level `sign`,
`encode` and `crc32c` modules as subpath exports for callers that want the
primitives without the client. `SPEC_VERSION` is exported and asserted
against `GET /-/version` in the conformance test.

### Fixture server

`cmd/tunna-fixtures`: flags for the address and the fixtures file
(default `spec/conformance/fixtures.json`); memory stores and a temporary
disk store; `POST /reset` outside the API handler; prints the URL on
stdout when listening. Provisioning code moves from
`internal/httpapi/conformance_test.go` into `internal/fixtures`, which
both the Go runner and the binary import.

### What the SDK does not do

- No retries except inside the upload helper, per part. A general retry
  policy is the caller's, since only they know what is idempotent.
- No caching, no request signing for other services, no S3 compatibility.

## Consequences

Easier:

- A client with no dependencies and two development dependencies; a
  contributor needs Bun and nothing else.
- Every SDK after this one gets the fixture server for free and follows
  the same test layout.
- The upload helper is the first real measurement of what ADR-0001 was
  for; `docs/benchmarks/http-load.md` gets a client-side entry.

Harder:

- `presign` and every request are asynchronous even when nothing is sent
  yet. Documented, not worked around.
- Development needs Bun installed, and CI installs it (`oven-sh/setup-bun`)
  next to Go. The package itself runs on Node 20 and later and in
  browsers; the README states both.
- Test files import from `bun:test`, so they run only under Bun. The Node
  smoke script is the one test that does not, on purpose.
- Moving fixture provisioning out of the test file touches the Go runner,
  which must stay at 108 of 108 through the move.

To revisit:

- Whether `upload` should resume a session after a page reload, which
  needs the session id persisted by the caller; the wire already allows
  it.

## Action items

1. [x] Maintainer accepted this record, 2026-09-15, amended for Bun and tsdown.
2. [x] `internal/fixtures` extracted from the Go runner, 2026-09-15; runner
       still 108 of 108.
3. [x] `cmd/tunna-fixtures` with `/reset`, 2026-09-15; a Go test builds and
       spawns it, reads the URL, mutates, resets, and sees the fixtures back.
4. [x] `sdk/typescript` scaffold, 2026-09-15: package.json, tsconfig, tsdown,
       generated errors, stubs with the API fixed, 89 vector tests of which
       81 fail against the stubs, Node smoke scripts, CI job.
5. [x] `encode`, `sign`, `crc32c`, 2026-09-15: 89 of 89 vector tests, Node
       smoke green in both formats.
6. [ ] The client, then the conformance test, then `upload`.
7. [ ] README for the package; the repo README's SDK table gains a row.
