# tunna (TypeScript)

[![npm](https://img.shields.io/npm/v/tunna)](https://www.npmjs.com/package/tunna)
[![ci](https://github.com/tunnaio/tunna/actions/workflows/ci.yml/badge.svg)](https://github.com/tunnaio/tunna/actions/workflows/ci.yml)

Client for the [tunna](../../README.md) object store. Runs in browsers and
in Node 20.3 or later with no dependencies: the code uses `fetch`,
`crypto.subtle` and `ReadableStream` and nothing else. ESM and CommonJS
from one source (ADR-0010). Every route in the wire contract is covered,
and every conformance case in `spec/conformance` is replayed through this
package's encoder and signer.

```
npm install tunna@alpha
```

Prereleases publish under the `alpha` tag while the wire contract is a
draft; `0.1.0` follows the first stable spec (ADR-0011).

## Use

```ts
import { Tunna, TunnaError } from "tunna";

const tunna = new Tunna({
  url: "https://store.example.com",
  key: { id: process.env.TUNNA_KEY_ID!, secret: process.env.TUNNA_KEY_SECRET! },
});

await tunna.buckets.create("photos");
await tunna.objects.put("photos", "2026/a.jpg", bytes, {
  contentType: "image/jpeg",
  metadata: { camera: "x100" },
});

const res = await tunna.objects.get("photos", "2026/a.jpg");
res.contentType;                 // "image/jpeg"
res.metadata.camera;             // "x100"
await res.response.arrayBuffer(); // or read res.body as a stream

for await (const obj of tunna.objects.list("photos", { prefix: "2026/" })) {
  console.log(obj.key, obj.size);
}

// Or one page at a time, for a UI with its own "next" button.
const page = await tunna.objects.page("photos", { limit: 50 });
if (page.next) await tunna.objects.page("photos", { limit: 50, after: page.next });

// Large files: numbered parts, several in flight, per-part checksums.
await tunna.upload("photos", "big.bin", file, {
  partSize: 8 << 20, // the default
  concurrency: 8,    // the default
  onProgress: (sent, total) => console.log(sent / total),
});

// Cancelling: every method takes a signal. For an upload, aborting stops the
// parts in flight and removes the half-written session from the server.
const controller = new AbortController();
cancelButton.onclick = () => controller.abort();
try {
  await tunna.upload("photos", "big.bin", file, { signal: controller.signal });
} catch (err) {
  if (controller.signal.aborted) { /* the user cancelled; nothing to report */ }
  else throw err;
}

// Or a deadline on any single call.
await tunna.objects.head("photos", "2026/a.jpg", { signal: AbortSignal.timeout(5_000) });

// What may this key do? Works for any key; the type narrows on admin.
const me = await tunna.apiKeys.self();
if (me.admin) { /* key management, every bucket */ }
else console.log(Object.keys(me.scopes)); // the buckets a scoped key can reach

// A URL a browser can PUT to for the next hour, without holding the key.
const url = await tunna.presign({ method: "PUT", bucket: "photos", key: "b.jpg", expiresIn: 3600 });

try {
  await tunna.buckets.get("nope");
} catch (err) {
  if (err instanceof TunnaError && err.code === "bucket_not_found") { /* ... */ }
}
```

Without a `key` the client is anonymous and can read public buckets only.
Every signing call is asynchronous because HMAC comes from `crypto.subtle`.

When the client reaches the server under one name and browsers under
another (`http://tunna:8000` inside a compose network,
`https://store.example.com` outside), set `publicUrl`: requests keep going
to `url`, presigned URLs are built on `publicUrl`. The signature is the
same either way, because the host is not signed.

| Group | Methods |
|-------|---------|
| `tunna.buckets` | `list`, `get`, `create`, `patch`, `delete` (all but `list` and `get` need an admin key) |
| `tunna.objects` | `put`, `get`, `head`, `delete`, `list` (async iterator over every page), `page` (one page and its cursor) |
| `tunna.apiKeys` | `list`, `get`, `create`, `patch`, `rotate`, `delete` (admin key only); `self` (any key: its own record) |
| `tunna.uploads` | `create`, `putPart`, `get`, `complete`, `abort` (the raw routes) |
| `tunna.upload` | the concurrent uploader on top of them |
| `tunna.presign` | a presigned URL for one request |
| `tunna.health`, `tunna.version`, `tunna.limits` | is the server up; does it speak this package's spec (`compatible`); its part size bounds, presign lifetime and other limits. All sent unsigned |

Errors: `TunnaError` is the server's answer, with `code` typed as the
union generated from `spec/errors.json`; `TransportError` is no answer or
one outside the contract, with the underlying error as `cause`. A failed
`head` is a `TunnaError` too: its answer has no body, so the code is read
from the `X-Tunna-Error` header (servers from 0.1.0-alpha.4), and `message`
and `details` are the SDK's own and absent.

Every method takes `{ signal }` (an `AbortSignal`) as or in its last
argument, `upload` included, where an abort also removes the half-written
session from the server. A cancelled call rejects with the signal's reason,
never a `TransportError`: an `AbortError` from `abort()`, a `TimeoutError`
from `AbortSignal.timeout`, or the value passed to `abort(reason)`. Check
`signal.aborted` rather than the error's name.

A `TunnaError` also carries `stage`, the step of the server's request
ladder that refused: 1 syntax, 2 authentication, 3 authorization,
4 validation, 5 body, 6 state. It groups codes without a table of your own
(`err.stage === 2 || err.stage === 3` is "sign in or ask for access"), and
`ERROR_STAGE_NAME[err.stage]` gives the name.

Subpath exports `tunna/sign`, `tunna/encode` and `tunna/crc32c` give the
primitives without the client, for anyone building on the wire contract
directly.

## Develop

Needs [Bun](https://bun.sh) and, for the conformance test, Go. Everything
runs from this directory.

```
bun install              # exact-pinned dev dependencies
bun test                 # vectors, client, upload, and conformance against cmd/tunna-fixtures
bun run check            # tsc --noEmit
bun run gen              # regenerate src/errors.generated.ts from ../../spec
bun run build            # dist/ via tsdown: .mjs, .cjs, declarations
bun run smoke            # build, then plain Node imports both formats
```

`src/errors.generated.ts` is committed and checked in CI; edit
`spec/errors.json`, not the output.

## Layout

| Path | What |
|------|------|
| `src/client.ts` | `Tunna`, the request pipeline, `upload` and `presign`. |
| `src/buckets.ts`, `objects.ts`, `api-keys.ts`, `uploads.ts` | One group per section of the wire contract. |
| `src/encode.ts` | Segment, path and query encoding for the canonical request (`spec/vectors/encoding.json`). |
| `src/sign.ts` | Canonical string, HMAC-SHA256, header and presigned forms (`spec/vectors/signing.json`). |
| `src/crc32c.ts` | CRC-32C, combine, and the `crc32c=` wire form (`spec/vectors/crc32c.json`). |
| `src/errors.ts` | `TunnaError`, `TransportError`. |
| `src/errors.generated.ts` | `ErrorCode`, status and stage tables, `SPEC_VERSION`. Generated. |
| `test/` | Vector tests, the client and upload helper against an injected `fetch`, the conformance runner, the plain-Node smoke scripts. |
| `scripts/gen-errors.ts` | The generator. |
