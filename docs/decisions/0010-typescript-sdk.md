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
├── src/                  (reorganised 2026-09-22: folders by what a file is)
│   ├── index.ts          public surface
│   ├── client.ts         Tunna: the request pipeline, upload, presign, presignRequest
│   ├── types.ts          CallOptions and the other types every file shares
│   ├── presign.ts        the provider types and the rule for what may be presigned
│   ├── errors.ts         TunnaError, TransportError
│   ├── errors.generated.ts   from spec/errors.json; do not edit
│   ├── api/              one file per route group, all one shape: wire types, public types, a class
│   │   ├── buckets.ts, objects.ts, api-keys.ts, uploads.ts
│   │   └── server.ts     version and limits
│   └── wire/             the primitives: no client, each a subpath export
│       ├── sign.ts       canonical string, HMAC, header and presign forms
│       ├── encode.ts     segment, path, query encoding
│       └── crc32c.ts     table, update, combine, wire form
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

Name `tunna` on npm, ESM and CommonJS, `engines.node >= 20.3`, no runtime
dependencies. Exports `Tunna`, `TunnaError`, `TransportError`, the
generated `ErrorCode` type, the record types, and the low-level `sign`,
`encode` and `crc32c` modules as subpath exports for callers that want the
primitives without the client. `SPEC_VERSION` is exported and asserted
against `GET /-/version` in the conformance test.

### Method signatures (added 2026-09-19)

Every method, in every group, takes its arguments in the same three
parts, so a caller who has learned one group can guess the next:

1. **Identifiers:** `name`, or `bucket, key`, or `id`.
2. **The required payload, as its own parameter:** `body`, `bytes`,
   `key`, `changes`, `parts`. What the method exists to send.
3. **One trailing, optional `options` object:** optional settings plus
   `signal` (`CallOptions`). Never a positional optional before it.

So `buckets.patch(name, changes, options?)` and
`apiKeys.patch(id, changes, options?)` have one shape, and
`uploads.complete(id, parts, { checksums?, signal? })` never needs a
placeholder `undefined`. `buckets.create(name, { public?, signal? })` keeps
`public` in the options because it is optional with a default.
`uploads.create` and `presign` take one required object because their
required fields read better named than positional; they are the two
exceptions, on purpose.

It beat "the signal goes wherever an options object already is", which is
what the first pass did: a payload that is serialized whole then carries
the signal onto the wire (`JSON.stringify` of a signal is
`{"signal":{}}`), and two `patch` methods ended up with different shapes.
The rule that prevents both: a method that serializes its argument gets
the signal as a separate parameter; a method that picks fields may share
the object. Tests pin it (`the signal never reaches a request body`).

An aborted call rejects with the signal's reason, never a
`TransportError`, so the upload helper's retry does not retry a
cancellation and `err.name`-based caller code keeps working; the helper's
cleanup `abort` of the session is sent without the caller's signal.
Probe 2026-09-19, Node 24 and Bun 1.4 against a server that never answers:
`AbortSignal.timeout` gives `TimeoutError`, `abort()` gives `AbortError`,
`abort(reason)` gives the reason.

The shape of the rule carries to other languages; its spelling does not
(SDKs share the contract, never the API shape). A Go client takes
`context.Context` first and has no options object for cancellation at all.

### Upload progress in bytes (added 2026-09-21)

`fetch` has no upload progress. A streaming request body, counted as it is
pulled, is the only way inside `fetch`, and it is Chromium only, HTTP/2
only, and counts what was buffered rather than what was sent.
`XMLHttpRequest` has `upload.onprogress` in every browser. A console built
on the SDK worked around this by uploading small files with its own XHR
and large ones with `upload`, whose `onProgress` moved once per 8 MiB
part: three jumps for a 20 MB file, nothing at all for `objects.put`.

Decision: **the transport stays `fetch`, and byte progress is something a
transport may report.**

- The pipeline passes one extra field in the `fetch` init,
  `onUploadProgress(loaded, total)`, when the call has a listener. A
  standard `fetch` ignores init keys it does not know, so nothing changes
  for anyone who does not opt in.
- `tunna/xhr` exports `xhrFetch`, a `fetch`-shaped function for browsers:
  `new Tunna({ url, fetch: xhrFetch })`. A request that carries
  `onUploadProgress` goes out through `XMLHttpRequest`; **every other
  request is handed to the real `fetch`**, so downloads still stream
  instead of being buffered whole, which is what an XHR-for-everything
  adapter would do to a 2 GB `objects.get`. It touches no global at import
  time, so importing it under Node is harmless; calling it without
  `XMLHttpRequest` falls back to `fetch`.
- `objects.put` and `uploads.putPart` take `onProgress`. `upload`'s
  `onProgress(sent, total)` becomes byte-accurate when the transport
  reports, and stays once per part when it does not. `sent` is the bytes
  of finished parts plus the bytes reported so far by the parts in flight,
  and **it never decreases**: a part that is retried starts again from
  zero, and the reported value holds until the sum passes it. A bar that
  waits is honest; one that runs backwards is not.

It beat making XHR the transport in browsers. That is a second request
pipeline: error mapping, abort, the presign provider branch and header
parsing each written twice and kept in step; it breaks the platform rule
above (A1), since XHR exists in browsers only; and it would bypass the
`fetch` option that every test here and some callers inject. It also beat
leaving it to callers, which is what produced the console's split.

It is a subpath, not a package of its own (`@tunna/xhr` was considered,
2026-09-22). The adapter's whole contract is one init field the pipeline
passes: a private agreement between two files, which a second package
would turn into a public one to version and document, and which drifts
by construction once the two ship on their own tags. A subpath costs
nothing to those who never import it, is what `tunna/sign` and the other
primitives already are, and rides the one release. A scoped package earns
its place when a piece has dependencies a Node user must not install, or
when there is a family of them with their own cadence; the `@tunna` scope
is reserved for that day.

The cost: one public subpath, and an init field that is not in the
`fetch` standard. If the standard ever gains upload progress, the adapter
becomes a no-op and the field is already in the right place.

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
  next to Go. The package itself runs on Node 20.3 and later (the floor moved from
  20.0 on 2026-09-19: `upload` combines signals with `AbortSignal.any`) and in
  browsers; the README states both.
- Test files import from `bun:test`, so they run only under Bun. The Node
  smoke script is the one test that does not, on purpose.
- Moving fixture provisioning out of the test file touches the Go runner,
  which must stay at 108 of 108 through the move.

To revisit:

- Whether `upload` should resume a session after a page reload, which
  needs the session id persisted by the caller; the wire already allows
  it.
- A presign provider as an alternative to a key: `new Tunna({ url, presign })`
  where `presign(req)` returns a URL for one request, so a browser page
  runs every method, `objects.put`, `upload` per part, reads on private
  buckets, without holding a key; the page's provider calls the app's own
  backend, which holds the key and decides policy, and mints with a
  general `presignRequest({ method, path, headers, expiresIn })`. One
  branch in `request`; one round trip per request. Wanted 2026-09-17 for a
  browser console; the wire already allows it (spec/wire.md 6).
- Adaptive concurrency in `upload`: start low and add workers while
  throughput rises. Measured 2026-09-17 on a 500 Mbit link: with
  read-ahead, 4 in flight gave 37 MiB/s, 8 and 16 both about 43, so the
  default is 8. A slow link loses nothing at 8, only holds more memory.

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
6. [x] The client, the conformance test, and `upload`, 2026-09-15/16: every
       route covered, all 108 cases replayed through the SDK signer against
       cmd/tunna-fixtures, the uploader tested against a fake server.
7. [x] README for the package, 2026-09-16; the repo README's layout table
       names it.
