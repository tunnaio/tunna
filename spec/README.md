# tunna specification

The wire contract for tunna, as data. The server and every SDK are tested
against the files in this directory. Nothing here is specified twice: prose
explains, JSON decides.

## Version

The spec has its own version, in [`VERSION`](VERSION), independent of any
implementation's version. Each implementation declares which spec version it
conforms to.

Semantic versioning, applied to the wire:

| Bump | Meaning |
|------|---------|
| major | An existing request or response changes incompatibly. Old clients break. |
| minor | New endpoints, new optional fields, new error codes. Old clients keep working. |
| patch | Clarifications, new vectors, new conformance cases, no wire change. |

`-draft` suffix: the contract is being written and nothing is stable. Removed
at the first server release.

## Layout

| Path | What | Who reads it |
|------|------|--------------|
| [`wire.md`](wire.md) | The wire contract in prose: endpoints, headers, canonical request, encoding rules | People |
| [`errors.json`](errors.json) | The error table: every error code, its HTTP status, and the pipeline stage that produces it | The server's error mapping test; SDK error types |
| [`vectors/`](vectors/) | Pure function oracles: input in, expected output out, no server needed | Every signer and encoder, in every language |
| [`conformance/`](conformance/) | Request → expected response cases, replayed against a running server | The server's conformance test; SDK integration tests |

Each JSON data file has a sibling `*.schema.json` (JSON Schema 2020-12). A
data file that does not validate against its schema is a bug in the data.

## How implementations use this

**A signer** (in any language) loads `vectors/signing.json` and, for each
case, builds the canonical string and signature from the inputs and compares
them to the expected values. If every case passes, the signer is correct by
definition. There is no other definition.

**An encoder** does the same with `vectors/encoding.json`.

**The server** runs `conformance/` cases against itself, in-process, in its
own test suite. The runner provisions the fixtures in
`conformance/fixtures.json`, then replays each case's steps in order and
compares responses.

**An SDK** uses the vectors for its signer and its own integration tests
against a running server for the rest. Conformance cases describe the server's
behaviour; an SDK may also read them to know what to expect.

## Rules for editing

- A change to the wire is a change to `wire.md` *and* to the vectors or cases
  that prove it, in the same commit. Prose without data is a wish.
- Every error code in `wire.md` exists in `errors.json`. Every code in
  `errors.json` appears in at least one conformance case.
- Vectors are never edited to make a failing implementation pass. Either the
  implementation is wrong, or the spec is wrong and the change is a spec
  version bump with a note saying why.
- Decisions behind the contract live in [`../docs/decisions/`](../docs/decisions/).
  This directory says *what*; the ledger says *why*.
