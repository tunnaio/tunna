<img src="docs/logo/tunna-mark.svg" alt="" width="72" align="left">

# tunna

[![ci](https://github.com/tunnaio/tunna/actions/workflows/ci.yml/badge.svg)](https://github.com/tunnaio/tunna/actions/workflows/ci.yml)
[![server release](https://img.shields.io/github/v/release/tunnaio/tunna?include_prereleases&filter=v*&label=server)](https://github.com/tunnaio/tunna/releases)
[![npm](https://img.shields.io/npm/v/tunna?label=npm)](https://www.npmjs.com/package/tunna)

A small, fast object storage server in Go, with a wire contract written as
data and SDKs tested against it. *Tunna* is Swedish for barrel.

**Status: pre-release.** The server implements the whole contract (spec
`0.1.0-draft`): buckets, objects, multipart uploads, signed and presigned
requests, scoped API keys, CORS. The TypeScript SDK covers every route.
Releases so far are alphas, and the wire may still change before `0.1.0`;
the `-draft` suffix comes off the spec at that release.

## What it is

- **One binary, one directory.** Objects on disk, metadata in an embedded
  SQLite database, nothing else to run.
- **Signed requests.** HMAC-SHA256 over a canonical request, in header form
  and as presigned URLs; keys are admin or scoped per bucket. A presigned
  URL authorizes exactly one request: the routes whose parameters live in a
  body, which a URL cannot bind, refuse it.
- **Browsers without a key.** A page uploads and reads through URLs its own
  backend presigns, one per request, so the key never leaves the backend
  and the backend's policy is the whole of what the page can do. The SDK
  does this with one option.
- **Uploads built for concurrency.** Numbered parts of a fixed size written
  at offset into one file, completed by count, with per-part checksums that
  fold into the object's. The SDK sends parts in parallel.
- **A contract you can test against.** Vectors for encoding, signing,
  checksums, authorization and CORS, and a conformance suite the server and
  every SDK replay from the same files.

## Run it

From the published image, pinned to a release:

```
docker run -p 8000:8000 -v tunna-data:/data \
  -e TUNNA_BOOTSTRAP_KEY=tk_admin:change-me ghcr.io/tunnaio/tunna:0.1.0-alpha.6
```

Or from source, or with a binary from the
[releases page](https://github.com/tunnaio/tunna/releases):

```
go build ./cmd/tunna
TUNNA_DATA_DIR=./data TUNNA_BOOTSTRAP_KEY=tk_admin:change-me ./tunna
```

The server listens on `:8000` by default, or on `PORT` when a platform
sets it. `GET /-/health` needs no key;
everything else is signed. The bootstrap key is an admin key; use it to
create managed keys through `POST /-/keys`, then drop the variable. All
variables are in [`docs/configuration.md`](docs/configuration.md).

Signing by hand is not practical, which is the point; use an SDK, or the
`sig` package for Go. The TypeScript client:

```ts
import { Tunna } from "tunna";
const tunna = new Tunna({ url: "http://localhost:8000", key: { id, secret } });
await tunna.buckets.create("photos");
await tunna.objects.put("photos", "a.jpg", bytes, { contentType: "image/jpeg" });
```

See [`sdk/typescript`](sdk/typescript/README.md) for the rest.

## Layout

| Path | What |
|------|------|
| [`spec/`](spec/README.md) | The wire contract, error table, vectors, and conformance cases. Start here. |
| [`docs/decisions/`](docs/decisions/) | Architecture decision records: every non-obvious choice, with the alternative it beat. |
| [`docs/configuration.md`](docs/configuration.md), [`docs/deploy.md`](docs/deploy.md), [`docs/schema.md`](docs/schema.md) | Environment variables; the container image and what a host must provide; the SQLite schema and its migrations. |
| [`docs/benchmarks/`](docs/benchmarks/) | Measurements and how they were taken. |
| [`sig/`](sig/), [`internal/`](internal/), [`cmd/tunna`](cmd/tunna/) | The server. [ADR-0005](docs/decisions/0005-package-layout.md) describes the package layout. |
| [`cmd/tunna-fixtures`](cmd/tunna-fixtures/) | The API in the conformance fixture state, for SDK conformance runners. |
| [`cmd/tunna-load`](cmd/tunna-load/) | Load generator. |
| [`sdk/typescript`](sdk/typescript/README.md) | The TypeScript client: browser and Node, no dependencies. |

## Develop

Go 1.27 and, for the SDK, [Bun](https://bun.sh). `go test ./...` runs the
server's tests including the conformance suite; `bun test` in
`sdk/typescript` runs the SDK's, spawning the fixture server. CI runs both,
with the race detector on Linux.

How changes are proposed and what is welcome is in
[`CONTRIBUTING.md`](CONTRIBUTING.md), under the
[code of conduct](CODE_OF_CONDUCT.md). Security reports go through
[`SECURITY.md`](SECURITY.md).

## License

[MIT](LICENSE).
