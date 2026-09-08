# ADR-0005: Package layout and import graph

**Status:** Accepted
**Date:** 2026-09-09
**Deciders:** maintainer

## Context

In Go the import graph is the architecture. There are no visibility layers,
no dependency-injection containers, and no interfaces-by-default; there are
packages, the rule that a package may not import itself through a cycle, and
the `internal/` convention that stops packages outside a subtree from
importing what is inside it. A layout is therefore a statement of what may
depend on what, enforced by the compiler.

The prior records set requirements this one has to satisfy:

- ADR-0002: the metadata store is SQLite behind an interface the core owns.
  The writer goroutine and group commit are hidden inside the adapter.
- ADR-0001: object bytes are written at offset into one file per object. That
  is a second adapter, for the disk.
- ADR-0003: a signer and canonicalizer, pure and vector-tested, that a future
  Go SDK should be able to import without dragging in the server.
- ADR-0004: an HTTP pipeline with six named stages.
- The project file's stated target: a core with no I/O imports, adapters for
  the store, the disk and HTTP, and a `main` that builds and injects. Nothing
  constructed at package init.

Forces:

- **Learning Go means learning Go's idioms, not transplanting another
  language's.** Packages are named for what they provide, not for the layer
  they occupy. Interfaces are declared by the code that consumes them, kept
  small, and satisfied implicitly. A `models` or `repository` package is a
  sign of a C# or Java layout wearing a Go coat.
- **Cycles are the failure mode.** Every layout that splits a small domain
  into many packages eventually wants two of them to import each other. The
  compiler refuses, and the fix is either a merge or an awkward third package.
- **Tests must be able to run the core without a disk or a socket.** The
  conformance suite should run against an in-process server with a temporary
  directory and a temporary database, in one `go test`.
- **The repo is a monorepo** with non-Go directories (`spec/`, `docs/`,
  `sdk/`). The Go module lives at the root; the toolchain ignores directories
  without Go files.
- **Public surface is a promise.** Anything outside `internal/` is importable
  by the world. Keep that set small and deliberate.

## Options considered

### Option A: Flat

One package under `cmd/tunna`, or one `internal/server` package holding
everything. Some well-known Go object stores are built this way.

**Pros:** nothing to design, no cycles possible, fastest first commit.

**Cons:** the import graph says nothing, so it enforces nothing. The core
cannot be tested without the adapters. There is no seam for the store swap
that ADR-0002 relies on. It abandons the stated target on day one.

### Option B: Layer-named packages

`internal/domain`, `internal/usecase`, `internal/repository`,
`internal/handler`. Clean architecture as a directory listing.

**Pros:** familiar to anyone from the .NET or Java worlds; the dependency
rule is visible in the names.

**Cons:** package names describe a layer, not a thing, so every import reads
as `repository.Something` and every file inside is a grab-bag. Interfaces end
up declared by the provider (in `repository`) rather than the consumer, which
inverts Go's convention and inflates them. The first time `domain` needs a
type that `usecase` owns, there is a cycle. It teaches the structure the
maintainer already knows instead of the one the language prefers.

### Option C: Root domain package, adapters named by dependency, `cmd` wires

The root package `tunna` holds the domain: types, rules, error kinds, the
interfaces the core needs from the outside world, and the use-case
orchestration written against those interfaces. It imports only the standard
library, and nothing from it that performs I/O. Each external dependency gets
one adapter package under `internal/`, named for the dependency it wraps,
implementing the root's interfaces. One public package, `sig`, holds the
signer. `cmd/tunna` constructs everything and injects it.

This is the layout usually called the "standard package layout" in the Go
community, adjusted for this project's surfaces.

**Pros:** the graph enforces the target directly. The root package is
testable with in-memory fakes. No cycles are possible because the root
imports nothing in the module and adapters import only the root. Package
names say what they wrap.

**Cons:** one root package can grow large; splitting it later along domain
lines needs care. Importing a package named after the module
(`tunna.Object`) reads unusually at first.

### Option D: Package per domain

`internal/object`, `internal/upload`, `internal/auth`, each with its own
types, rules and store interface, plus adapters.

**Pros:** small packages with clear ownership.

**Cons:** the domains are not independent. Completing an upload creates an
object; every request needs auth; a bucket delete touches objects and
sessions. Those are imports between the domain packages, and the third one
is a cycle. This is the right shape for a much larger domain and the wrong
one for four tables. Named as the revisit path, not the starting point.

## Decision

**Option C.** The tree, the packages, and the rules for what each may import.

```
tunna/
├── go.mod                     module github.com/tunnaio/tunna
├── *.go                       package tunna: the core
├── sig/                       package sig: canonical request, HMAC, presign (public)
├── internal/
│   ├── sqlite/                metadata store adapter (ADR-0002)
│   ├── disk/                  blob store adapter: one file per object (ADR-0001)
│   ├── httpapi/               HTTP adapter: six-stage pipeline (ADR-0004), CORS, error mapping
│   └── config/                environment parsing, effective-config logging
├── cmd/
│   └── tunna/                 main: build, inject, run, shut down
├── spec/                      wire contract, vectors, conformance cases, error table (not Go)
├── docs/decisions/            this ledger
└── sdk/                       SDKs, each its own toolchain (not Go, or its own module)
```

| Package | Provides | May import (module) | May import (stdlib I/O) | Must not |
|---------|----------|---------------------|-------------------------|----------|
| `tunna` (root) | Domain types (`Bucket`, `Object`, `UploadSession`, `APIKey`, part set), naming and size rules, error kinds, the interfaces the core needs (`ObjectStore`, `UploadStore`, `KeyStore`, `BlobStore`, a clock), and the use cases that orchestrate them | nothing | none: no `os`, `net`, `net/http`, `database/sql`. `io` and `context` are types, not I/O, and are allowed | Import anything in the module |
| `sig` | Canonical string, HMAC-SHA256 sign and verify, presign build and parse | nothing | none | Import `tunna`. It is a leaf so a Go SDK can take it alone |
| `internal/sqlite` | Implements the store interfaces; owns the schema, embedded migrations, the writer goroutine and group commit | `tunna` | `database/sql`, the driver | Import other adapters |
| `internal/disk` | Implements `BlobStore` on a directory: create by id, write at offset, sync, open for read, delete | `tunna` | `os`, `io` | Import other adapters |
| `internal/httpapi` | Routes, the six-stage pipeline, auth stage via `sig` and `KeyStore`, error-kind to wire-code mapping, CORS | `tunna`, `sig` | `net/http` | Import `sqlite` or `disk`; it sees only interfaces |
| `internal/config` | Parse environment into a struct, log the effective values with secrets redacted | none | `os` | Import adapters |
| `cmd/tunna` | Construct config, open sqlite and disk, build httpapi, run the server, handle shutdown | everything | anything | Contain logic. It is wiring |

Rules the tree does not show:

1. **Interfaces are declared by the consumer.** The root package declares
   `ObjectStore` and the rest in terms of what the use cases need, at the
   granularity of a transaction. "Complete upload" is one method on
   `UploadStore` because it must be one transaction (ADR-0002); the adapter
   decides how. The adapter never declares an interface for itself.
2. **The root package holds the use cases, not just the types.** Orchestration
   such as "put part: load session, apply the size rule, write at offset, sync,
   mark received" lives in the root, written against the interfaces. This is
   what makes it testable without a disk. It is still free of I/O imports
   because it only calls interfaces.
3. **Nothing at package init.** No package-level database handles, no
   `init()` that registers routes or opens files, no globals that hold state.
   Constructors take their dependencies as arguments and return structs.
   The one accepted exception is the SQLite driver registering itself with
   `database/sql` from its own `init`, which is how that API works; it is
   imported for that side effect only inside `internal/sqlite`.
4. **Errors cross the boundary as kinds, not strings.** The root defines the
   error kinds (not found, conflict, invalid, unauthenticated, and so on).
   Adapters return them. `httpapi` maps kinds to status codes and wire error
   codes from the spec's error table, and a test keeps that mapping and the
   table in sync.
5. **The public surface is `sig`, and by position `tunna`.** `sig` is public by
   promise: it is the piece a Go SDK would share, and the vector file is its
   contract. The root package is public because Go cannot hide a module's
   root, not because it is supported API. Everything else is `internal/`.
6. **Adapters are tested two ways.** Each adapter has its own tests against a
   real temporary database or directory. In addition, a shared contract test
   suite for the store interfaces runs against both the in-memory fake used
   by core tests and the real adapter, so the fake cannot drift from the
   thing it stands in for.
7. **The conformance runner is a Go test** that builds the server in-process
   from the same constructors `cmd/tunna` uses, on a temporary directory, and
   replays `spec/conformance` against it. If `cmd/tunna` and the test build
   the server differently, the test is wrong.

## Consequences

Easier:

- The compiler enforces the architecture. A stray `os` import in the root
  package is a build error the moment a test asserts on the import list.
- Swapping the store (ADR-0002's Pebble fallback) is a new `internal/` package
  and one line in `main`.
- The core's tests run in milliseconds with fakes. The adapters' tests are the
  only ones that touch disk.
- A Go SDK, if one comes, imports `sig` and nothing else.

Harder:

- The root package will hold most of the interesting code. It needs internal
  file organization (`object.go`, `upload.go`, `key.go`, `errors.go`, and so
  on) to stay navigable, since Go offers no sub-structure below the package.
- Consumer-declared interfaces mean the root package decides the store's
  method set. Adding a store operation is an edit in two places, root and
  adapter, plus the fake. That is the cost of the seam.
- `cmd/tunna` must stay free of logic. Every time something is convenient to
  do in `main`, it belongs in `config` or in the root.

To revisit:

- **Split the root along domain lines** (Option D) if it passes a few
  thousand lines or if two clearly separate areas stop sharing types. Split
  by domain, never by layer.
- **Make `sig` its own module** with its own `go.mod` when a Go SDK exists,
  so the SDK's dependency graph does not include the server's. Until then a
  package inside the root module is enough.
- **`internal/config`** may fold into `cmd/tunna` if it stays tiny, or grow
  into the place where startup validation lives. Either is fine.

## Probes and tests this record asks for

1. **Import-list test.** A test in the root package that lists its transitive
   imports and fails on any I/O package. Mutation check: add an `os` import,
   confirm the test fails, remove it.
2. **Import-graph test.** A test that fails if any `internal/` adapter imports
   another adapter, or if anything other than `cmd/tunna` imports an adapter.
3. **Store contract suite** shared by the in-memory fake and `internal/sqlite`.

## Action items

1. [ ] Maintainer accepts, amends, or rejects this record.
2. [ ] Create the directories and the root package with its types, rules,
       error kinds and interfaces, in that order, before any adapter exists.
3. [ ] Write the two import tests and the store contract suite (Claude's,
       on request).
4. [ ] Backfill the pre-ledger decisions (Go for the server, Rust as first
       SDK, spec as data, spec versioned separately) as ADR-0000 or as
       entries in a ledger index.
