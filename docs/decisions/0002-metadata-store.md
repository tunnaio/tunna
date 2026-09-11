# ADR-0002: Metadata store

**Status:** Accepted
**Date:** 2026-09-09
**Deciders:** maintainer

## Context

Object bytes live in files on one local disk. Everything else about them needs
a store: which keys exist in which bucket, how big each object is, its content
type, checksum, timestamps and user metadata, where its bytes are, the state of
in-progress uploads, and the API keys that may touch any of it.

ADR-0001 settled the upload shape and, in doing so, removed the constraint
this question was expected to carry. An upload session is one row with a
received-parts set. Complete is a single transaction: check the set is full,
make the object visible. Every candidate store can do that. The store is
therefore chosen on the ordinary access pattern of an object server, not on
upload bookkeeping.

That access pattern, per request:

| Request | Store operation | Frequency |
|---------|-----------------|-----------|
| GET / HEAD object | one point read by (bucket, key) | the hot path, by far |
| PUT small object | one durable insert-or-replace | frequent |
| LIST objects | ordered scan by key within a bucket, prefix-bounded, paginated, with delimiter grouping | common |
| DELETE object | one durable delete | occasional |
| Upload part | one durable update to a session's parts set | frequent during large uploads |
| Complete upload | one transaction: check session, insert object, drop session | once per large upload |
| Auth | point read of an API key by id | every request, but cacheable in memory |

Forces:

- **Fast.** The product goal. For GET the store must be a rounding error next
  to reading the file and writing the socket. For PUT the cost that matters is
  durability: an acknowledged write must survive power loss, and the object's
  bytes already need one fsync of their own. A store that needs a second,
  un-batched fsync per write halves small-object write throughput.
- **One process, one disk, one binary.** The deployment story is a static
  binary and a data directory. Anything that adds a second process to operate
  has to earn it.
- **Trust the wire, not the docs.** Probes need to read the store directly to
  check what the server actually recorded. A store with a CLI is a store that
  can be probed without writing a tool first.
- **Schema will change.** This is a fresh project. Columns will be added and
  meanings will move. The cost of a schema change is paid many times.
- **Learning Go.** Time spent learning a store's API is time not spent learning
  Go, unless the store's API is itself idiomatic Go worth learning.
- **The store sits behind an interface** (open question 5: the core has no I/O
  imports; the store is an adapter). A wrong choice is a bounded rewrite of one
  package, not of the server. This lowers the stakes and argues for the
  simplest option that meets the goals.

Not a goal: multi-node. The server is one process on one disk by design.

## Options considered

### Option A: SQLite, embedded, single writer

One database file next to the data directory. WAL mode so readers never block
on the writer. `synchronous=FULL` so a committed transaction survives power
loss. All writes funnel through one writer goroutine that groups whatever is
pending into a single transaction, so many concurrent small PUTs share one
fsync. Reads use a small pool of connections with prepared statements. The
schema is SQL; changes are numbered migration files applied at startup.

Driver: a pure-Go driver to keep the static, cross-compiled binary. Two exist
with real adoption: a C-to-Go transpilation of SQLite, and SQLite compiled to
WebAssembly run in-process. Both are slower than the cgo driver by a constant
factor that a benchmark should measure; the choice is confined to the adapter.

| Dimension | Assessment |
|-----------|------------|
| Point read cost | Order of tens of microseconds through `database/sql` with a pure-Go driver. Noise next to a file read and a socket write. |
| Durable write cost | One WAL fsync per committed transaction. With a group-commit writer goroutine, one fsync covers every write that arrived while the previous commit was in flight. |
| Ordered listing | Native. An index on (bucket, key) gives prefix-bounded ordered scans. Delimiter grouping is a loop of seeks, the same as in any store. |
| Transactions | Real, with rollback. Complete-upload is `BEGIN IMMEDIATE`, three statements, `COMMIT`. |
| Inspectability | Highest. Any SQLite CLI or GUI opens the file. A probe is a query. |
| Schema evolution | SQL migrations, a solved problem. |
| Operations | One file. Backup is `VACUUM INTO` or an online backup; consistent copies without stopping the server. |
| Static binary | Yes with a pure-Go driver. No with the cgo driver unless a C cross-toolchain is added to the build. |
| Learning value | `database/sql`, prepared statements, a single-writer goroutine with a request channel and reply channels: idiomatic Go worth learning. SQL itself is already known. |
| Concurrency ceiling | One writer at a time. For this workload that writer is a goroutine doing microsecond-scale work between fsyncs; the disk, not the lock, is the ceiling. |

**Pros**

- Probes read the store directly. Every claim the server makes about metadata
  can be checked with a query.
- Schema changes are migration files, reviewed and versioned like code.
- Group commit is about fifty lines of Go and a good first concurrency
  exercise; it is also the one piece of this option that must exist for the
  performance goal to hold.
- Battle-tested for exactly this shape: at least one production object store
  offers SQLite as a supported metadata backend.

**Cons**

- Per-operation overhead is higher than a raw key-value store, by a factor that
  does not matter at the request rates one HTTP server reaches, but that a
  benchmark must confirm rather than this sentence.
- Group commit is not built in. Without the writer goroutine, each small PUT
  pays its own fsync.
- Driver gotchas are real: WAL and busy-timeout must be set per connection,
  `database/sql` pooling interacts with SQLite's single writer in ways that
  need care, and a pure-Go driver has its own quirks. Each will become a
  one-line lesson in the project file.

### Option B: Embedded ordered key-value store (Pebble, bbolt, Badger)

The domain is nearly a key-value store already: `objects/<bucket>/<key>` as the
key, an encoded record as the value, prefix iteration for listing. Three pure-Go
candidates with different internals:

- **Pebble** (log-structured merge tree, the engine under CockroachDB): very
  high write throughput, automatic grouping of concurrent synced batches into
  one WAL fsync, ordered iteration, a data directory of many files.
- **bbolt** (B+tree, the engine under etcd): one file, memory-mapped reads at
  sub-microsecond cost, one writer at a time with a built-in batch helper for
  group commit, fsync per transaction, slow for write-heavy loads.
- **Badger** (log-structured with a separate value log): fast, but a history of
  memory pressure and compaction surprises, and less active than the other two.

| Dimension | Assessment |
|-----------|------------|
| Point read cost | Order of microseconds. Faster than SQLite by an amount that is invisible behind HTTP. |
| Durable write cost | Same physics: one fsync per synced batch. Pebble groups concurrent syncs automatically; bbolt via its batch helper; Badger via its own batching. |
| Ordered listing | Native, and the key layout makes prefix listing the primary access path. |
| Transactions | Pebble: atomic batches, no read-your-writes inside a batch without extra work. bbolt: real serializable transactions. Badger: real transactions. |
| Inspectability | Low. No CLI that understands the record encoding. Every probe needs a tool written first. |
| Schema evolution | Versioned byte encodings maintained by hand. Every record carries a version; every reader handles every version ever written. |
| Operations | bbolt: one file, copy under a read transaction. Pebble: checkpoint into a directory. Badger: its own backup format. |
| Static binary | Yes, all three are pure Go. |
| Learning value | Byte-slice key design, iterators, manual encoding. Go-flavored, but it is learning a store, not the language. |
| Concurrency ceiling | Pebble: many concurrent writers. bbolt: one. Higher than needed in all cases. |

**Pros**

- The lowest per-operation cost of any option, and with Pebble the best write
  path available in Go with no extra code.
- The key layout is the index. Nothing to declare, nothing to forget.
- Single static binary, trivially.

**Cons**

- Opaque on disk. The house method is to probe what actually got recorded;
  this option makes every probe cost a tool.
- Hand-rolled encoding and its versioning are a permanent tax on every schema
  change, and schema changes are certain.
- Secondary access paths (find by object id, by upload session, by expiry time)
  each need their own key space maintained by hand, in the same batch as the
  primary write.
- The performance advantage buys nothing measurable at this server's request
  rates. It is speed in the wrong place.

### Option C: Postgres from day one

A real database server, real concurrent writers, and a path to more than one
tunna process sharing metadata.

| Dimension | Assessment |
|-----------|------------|
| Point read cost | Order of hundreds of microseconds on the same host; a network round trip elsewhere. Ten to a hundred times an embedded read, on the hottest path. |
| Durable write cost | One WAL fsync per commit, with group commit built in. Plus the round trip. |
| Ordered listing | Native. |
| Transactions | The best of any option. |
| Inspectability | High. `psql`. |
| Schema evolution | SQL migrations. |
| Operations | A second process with its own config, upgrades, backups, connection limits, and failure modes. Every deployment doubles in size. |
| Static binary | The binary is static; the deployment is not. |
| Learning value | Connection pools and a Postgres driver. Not Go, and not the domain. |
| Concurrency ceiling | Highest. Also irrelevant to one process on one disk. |

**Pros**

- Multi-node metadata is possible without changing the store.
- Strongest transactional guarantees.

**Cons**

- Pays a network hop on every GET to solve a problem the project has ruled
  out of scope.
- Breaks the one-binary, one-directory deployment story.
- The predecessor was replaced partly because operating it was more than it
  needed to be. Adding a database server goes the wrong direction.

### Option D: No store; the filesystem is the metadata

Objects at `<bucket>/<key>` on disk, metadata in a sidecar file or extended
attributes, listing by directory walk. No database at all.

Rejected before scoring. Object keys are not paths: `a` and `a/b` may both be
keys, and one would have to be a file while the other is a directory. Keys
exceed path-component length limits, contain characters filesystems reject,
and are case-sensitive on some hosts and not others. Working around this means
hashing the key into a path, which requires an index from key to path, which is
a metadata store. Listing by prefix across millions of keys via `readdir` is
also the slowest possible implementation of the second most common request.

## Trade-off analysis

**Throughput does not separate the embedded options.** GET is dominated by the
file read and the socket write; the store's ten-versus-one microseconds is
invisible. PUT is dominated by fsync, and every embedded option can share one
fsync across concurrent writes. The store's own CPU cost only becomes visible
at request rates far above what one Go HTTP server handles on one disk. This
claim is exactly the kind that must be measured, and the action items say so,
but the physics make it a safe bet.

**Postgres is the only option that loses on throughput**, and it loses on the
hot path, to buy multi-node capability the project does not want.

**The real axes are inspectability and schema evolution.** SQLite has a CLI and
migrations. A key-value store has neither and asks the maintainer to write
both. For a solo project that changes shape often and whose method is to probe
what was actually stored, that is the whole decision.

**Group commit is the non-negotiable part of the SQLite choice.** Without a
writer goroutine that batches, small-object PUT throughput is bounded by two
fsyncs per object instead of one. With it, the metadata fsync is amortized over
every PUT in flight. This is the first piece of the server where the
performance goal and a Go concurrency idiom meet, and it belongs in the
server's first benchmark.

**Pebble is the option to keep in the back pocket.** If a benchmark ever shows
the metadata store on the critical path after group commit, Pebble is the
replacement: same access pattern, automatic sync grouping, pure Go. The store
interface from open question 5 is the seam; a swap should touch one package.

## Decision

**Option A: SQLite, embedded, WAL mode, `synchronous=FULL`, a single writer
goroutine with group commit, a pure-Go driver, SQL migrations applied at
startup.**

The driver and its exact version are chosen and pinned when the adapter is
written, after a benchmark of the two pure-Go candidates against the cgo
driver on the actual workload (point read, grouped insert, prefix scan).

### Query layer: plain SQL to start, ORM deliberately left open

The adapter begins on `database/sql` with hand-written SQL and prepared
statements. No ORM and no query generator in the first version.

Reasons this is the starting point rather than the final answer:

- The store has a handful of tables and a few dozen queries, mostly single-row
  reads and writes. That is the shape where an ORM's overhead is all that is
  felt and its conveniences are barely used.
- The writer goroutine must own the transaction: `BEGIN IMMEDIATE`, the
  statements that arrived, one `COMMIT`. Object-relational mappers want to own
  the transaction themselves. Their unit-of-work abstraction is the thing in
  the way.
- The query the server runs must be the query a probe can paste into the
  SQLite CLI. Generated SQL has to be logged to be known.
- The hot path is a point read. Reflection-based query building is a per-call
  cost on the one operation the server is measured on.
- Learning. `database/sql` is where Go's data-access idioms live: `Scan` into
  typed variables, rows that must be closed, null types, `context` on every
  call. A mapper that resembles Entity Framework would teach the mapper, not
  the language. Writing the SQL by hand is also deliberate practice of SQL
  itself, which the maintainer wants.

What "left open" means: the question is revisited, not reopened at random,
when a concrete trigger fires.

| Trigger | Candidate | Why that one |
|---------|-----------|--------------|
| The same `Scan` block is written for the fourth time, or a column add touches more than a couple of call sites | **sqlc** | SQL stays hand-written and visible; generated typed functions over `database/sql`; takes a transaction handle, so the writer goroutine keeps ownership. The Go community's answer to this exact complaint. SQLite dialect support is less complete than its Postgres support; check the queries it rejects before committing. |
| The domain grows relations that are genuinely graph-shaped (many-to-many, polymorphic ownership) and the query count passes what a person can hold in their head | An ORM or schema code generator, evaluated then | Not expected for an object store, whose metadata is flat. If it happens, ent is the Go-native candidate and GORM the familiar one; the trade is reflection on the hot path and loss of transaction control, which must be re-measured, not assumed. |

Until a trigger fires, the store interface (open question 5) is the seam. Any
of these can replace the adapter's internals without the core noticing.

## Consequences

Easier:

- Every metadata claim is probe-able with a query. Conformance runners can
  assert on the store, not only on the wire.
- Schema changes are migration files. The migration runner is small and the
  schema version lives in the database.
- Backups are one file copy taken online.
- The deployment story stays one binary and one directory.

Harder:

- The writer goroutine must exist from the first PUT handler onward, not be
  retrofitted. Its contract (submit an operation, receive a result after
  commit) is part of the store interface.
- Per-connection pragmas, busy timeouts, and pool sizing are configuration
  that must be logged at startup like everything else.
- A pure-Go driver is slower than cgo by a constant factor. Accepting this is a
  deliberate trade for a static cross-compiled binary. The benchmark records
  the factor so the trade is visible.

To revisit:

- Query layer: plain SQL until a trigger in the table above fires; sqlc is
  the expected next step, an ORM the unlikely one.
- If the benchmark shows metadata commit on the critical path for small-object
  PUT after group commit, evaluate Pebble behind the same interface.
- If multi-node ever becomes a goal, the store interface is where Postgres
  would plug in. Nothing about the schema should make that harder than it
  needs to be, but nothing should be built for it either.

## Follow-ups this decision creates

- **Blob layout** (its own ADR): with a store mapping key to location, files
  can be named by object id rather than by key. That makes overwrite an atomic
  rename plus a row update, keeps key characters out of paths, and lets a
  delete be a row update with the file collected later.
- **Schema v1**: buckets, objects, upload sessions, API keys; the (bucket, key)
  primary index; the received-parts set encoding for sessions.
- **Store interface** (open question 5): the operations the core needs,
  expressed without SQL, so the adapter is the only package that imports the
  driver.

## Driver benchmark (2026-09-11)

Run on the maintainer's development machine (8-core desktop CPU, NVMe, Windows)
with `docs/benchmarks/sqlite-driver`: WAL, `synchronous=FULL`, one
connection, `WITHOUT ROWID` table keyed by (bucket, key).

| Workload | modernc.org/sqlite v1.58.0 | ncruces/go-sqlite3 v0.35.4 |
|----------|---------------------------:|---------------------------:|
| Point read by (bucket, key), 100k rows | 10.7 µs | 12.9 µs |
| Insert, 1 row per commit | 1054 µs | 1053 µs |
| Insert, 100 rows per commit | 1390 µs | 1672 µs |
| Prefix scan, 1000 keys ordered | 519 µs | 679 µs |

What the numbers say:

- **A point read is about ten microseconds** through `database/sql` with
  either driver. The "rounding error next to a file read and a socket write"
  claim in the context section holds.
- **A durable commit is about one millisecond** and is the fsync, not the
  driver: both drivers land on the same figure for one row per commit.
- **Group commit is worth roughly seventy-five times** on small-object PUT
  metadata: a hundred rows under one fsync cost 30% more than one row. The
  writer goroutine is not optional for the performance goal.
- **modernc is 20 to 30% faster on every workload; ncruces allocates less.**
  Both are fine for this server. The difference will not be visible behind
  HTTP, so the choice is on speed as measured and on maturity, and modernc
  has both.
- **The cgo driver could not be built on the development machine**, which has
  no C toolchain. That is the static-binary argument from the decision
  section, met in practice on day one.

**Pinned: `modernc.org/sqlite v1.58.0`.** Revisit if a release of either
driver changes the picture, by re-running the benchmark module.

### Adapter baseline before group commit (2026-09-11)

`internal/sqlite` with one transaction per write, sixteen goroutines
calling `CreateBucket` concurrently (`BenchmarkCreateBucketParallel`):

| | |
|---|---|
| Per write, wall clock under contention | 1.10 ms |
| Sustained durable writes per second | about 900 |

The writes serialize on the fsync, as expected: parallel callers do not
help because each commit waits for the disk. The driver benchmark above
puts a hundred rows under one fsync at 1.39 ms, so the writer goroutine
with group commit should land near seventy thousand durable writes per
second on the same hardware. That is the target for phase C, and the
same benchmark is the measurement.

## Action items

1. [x] Maintainer accepted this record 2026-09-09.
2. [x] Benchmark run 2026-09-11; results above; modernc pinned.
3. [ ] Write the schema v1 and migration format into the spec.
4. [ ] Draft the store interface as part of the package layout decision
       (open question 5).
5. [ ] Add each SQLite-in-Go gotcha to the project file as it is hit, with the
       probe that proved it.
