# tunna (TypeScript)

Client for the [tunna](../../README.md) object store: the request signer,
presigned URLs, and a concurrent uploader. Runs in browsers and in Node 20
or later with no dependencies; the code uses `fetch`, `crypto.subtle` and
`ReadableStream` and nothing else. ESM and CommonJS from one source
(ADR-0010).

Status: in progress. The primitives (`encode`, `sign`, `crc32c`) come
first, tested against the spec's vector files; the client after.

## Develop

Needs [Bun](https://bun.sh). Everything runs from this directory.

```
bun install              # exact-pinned dev dependencies
bun test                 # vector tests, from ../../spec
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
| `src/encode.ts` | Segment, path and query encoding for the canonical request (`spec/vectors/encoding.json`). |
| `src/sign.ts` | Canonical string, HMAC-SHA256, header and presigned forms (`spec/vectors/signing.json`). |
| `src/crc32c.ts` | CRC-32C, combine, and the `crc32c=` wire form (`spec/vectors/crc32c.json`). |
| `src/errors.generated.ts` | `ErrorCode`, status and stage tables, `SPEC_VERSION`. Generated. |
| `src/index.ts` | Public surface. |
| `test/` | `bun test` files, one per vector file, plus the two plain-Node smoke scripts. |
| `scripts/gen-errors.ts` | The generator. |

Subpath exports `tunna/sign`, `tunna/encode` and `tunna/crc32c` give the
primitives without the client.
