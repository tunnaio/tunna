# tunna (TypeScript)

[![npm](https://img.shields.io/npm/v/tunna)](https://www.npmjs.com/package/tunna)
[![ci](https://github.com/tunnaio/tunna/actions/workflows/ci.yml/badge.svg)](https://github.com/tunnaio/tunna/actions/workflows/ci.yml)

Client for the [tunna](../../README.md) object store. Runs in browsers and
in Node 20.3 or later with no dependencies: the code uses `fetch`,
`crypto.subtle` and `ReadableStream` and nothing else. ESM and CommonJS
from one source (ADR-0010). Every route in the wire contract is covered,
and every conformance case in `spec/conformance` is replayed through this
package's encoder and signer. In a browser it needs no key: see
[In a browser, without a key](#in-a-browser-without-a-key).

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
| `tunna.presign`, `tunna.presignRequest` | a presigned URL for one object request; for any request the server accepts presigned (the backend half of a provider, below) |
| `tunna.health`, `tunna.version`, `tunna.limits` | is the server up; does it speak this package's spec (`compatible`); its part size bounds, presign lifetime and other limits. All sent unsigned |

## Upload progress in bytes

`upload`'s `onProgress(sent, total)` moves once per part: for a 2.8 GB
file that is 350 steps, for a 20 MB file it is three, and `objects.put`
has no progress at all. `fetch` cannot report upload bytes; in a browser,
`XMLHttpRequest` can. The `tunna/xhr` subpath is a transport that uses
it for exactly the requests that ask:

```ts
import { xhrFetch } from "tunna/xhr";

const tunna = new Tunna({ url, presign, fetch: xhrFetch });
await tunna.upload("photos", "big.bin", file, { onProgress: (sent, total) => bar.value = sent / total });
await tunna.objects.put("photos", "a.jpg", file, { onProgress: (sent, total) => {} });
```

With it, `onProgress` reports bytes as the browser sends them, from every
part in flight, and never runs backwards: a retried part starts again from
zero and the bar holds until real progress passes where it was. Requests
without a listener, downloads included, still go through `fetch` and keep
streaming. Under Node there is no `XMLHttpRequest` and `xhrFetch` is plain
`fetch`, so the same code runs everywhere.

## In a browser, without a key

A page should not hold a key. Give the client a `presign` function
instead: it is asked for a URL before each request, and your backend, which
holds the key, decides whether to sign.

```ts
// Backend (any framework). The key never leaves it.
const tunna = new Tunna({ url, publicUrl, key });

app.post("/api/uploads", async (req, res) => {          // start an upload for this user
  const session = await tunna.uploads.create("uploads", `${req.user.id}/${req.body.name}`, { partSize: 8 << 20 });
  remember(req.user.id, session.id);
  res.json(session);
});

app.post("/api/presign", async (req, res) => {          // sign one request, or refuse
  if (!mayDo(req.user, req.body)) return res.status(403).end();
  res.send(await tunna.presignRequest({ ...req.body, expiresIn: 300 }));
});

// Page. No key: every request goes out on a URL the backend signed.
const tunna = new Tunna({
  url,
  presign: (request, { signal }) =>
    fetch("/api/presign", { method: "POST", body: JSON.stringify(request), headers: { "Content-Type": "application/json" }, signal })
      .then((r) => (r.ok ? r.text() : Promise.reject(new Error("not allowed")))),
});

const session = await (await fetch("/api/uploads", { method: "POST", body: JSON.stringify({ name: file.name }) })).json();
await tunna.upload(session.bucket, session.key, file, { session, onProgress });
```

`mayDo` is your policy, and it is the whole of what the page can do. A
sound one is short: sign paths under `/-/uploads/<a session I created for
this user>`, and object paths under this user's prefix. `request` is
`{ method, path, query?, headers? }` with `path` as decoded segments, so
`request.path[0]` is the bucket, or `"-"` for the control plane.

The session comes from the backend because a page cannot start one: a
presigned URL binds the method, the path, the query and the listed headers,
not the body, and an upload's bucket, key and part size are in the body.
The server refuses the presigned form on those routes
(`presign_not_allowed`: session initiate, bucket create and patch, key
create and patch), and `presignRequest` refuses to sign them. `session`
crosses the wire as JSON, so turn `createdAt` and `expiresAt` back into
`Date`s if you read them; `upload` needs only `id` and `partSize`.

Each part costs one call to your backend before its PUT: a few
milliseconds against the seconds an 8 MiB part takes, and the parts still go out concurrently.

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
| `src/client.ts` | `Tunna`: the request pipeline, `upload`, `presign` and `presignRequest`. |
| `src/xhr.ts` | The `tunna/xhr` transport: upload progress through `XMLHttpRequest`. |
| `src/types.ts`, `src/presign.ts` | The types every file shares; the presign provider types and the rule for what may be presigned. |
| `src/api/` | One file per route group, all one shape: `buckets`, `objects`, `api-keys`, `uploads`, and `server` (version and limits). |
| `src/wire/encode.ts` | Segment, path and query encoding for the canonical request (`spec/vectors/encoding.json`). |
| `src/wire/sign.ts` | Canonical string, HMAC-SHA256, header and presigned forms (`spec/vectors/signing.json`). |
| `src/wire/crc32c.ts` | CRC-32C, combine, and the `crc32c=` wire form (`spec/vectors/crc32c.json`). |
| `src/errors.ts` | `TunnaError`, `TransportError`. |
| `src/errors.generated.ts` | `ErrorCode`, status and stage tables, `SPEC_VERSION`. Generated. |
| `test/` | Vector tests, the client and upload helper against an injected `fetch`, the conformance runner, the plain-Node smoke scripts. |
| `scripts/gen-errors.ts` | The generator. |
