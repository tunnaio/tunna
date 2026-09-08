# ADR-0001: Upload shape

**Status:** Accepted
**Date:** 2026-09-09
**Deciders:** maintainer

## Context

Objects can be larger than one request. Two forces make that unavoidable:

- **Proxy body caps.** Cloudflare rejects request bodies over 100 MB on Free/Pro
  plans (200 MB on Business). Any deployment behind such a proxy must split
  uploads. Downloads are not capped.
- **Per-request latency.** The predecessor uploaded strictly one chunk at a time
  and measured upload throughput at roughly 63% of download throughput on the
  same line. The whole gap was the idle line between one chunk's last byte and
  the next chunk's first byte. Any shape that keeps more than one request in
  flight hides that gap.

Facts that bound the design:

- **One disk is one disk.** The server writes to local storage. The throughput
  ceiling is the same for every option; the options differ in client freedom
  and server complexity, not in peak speed.
- **Several SDKs will implement the client side.** Rust first, then C#, then
  others. Every rule in the protocol gets written four or five times, so the
  cost of a rule is multiplied.
- **The spec is data.** Conformance cases must be able to express the upload
  protocol, including failure and resume, as request → expected response.
- **This decision picks the metadata store** (open question 2). It should say
  exactly what the store must be able to do, not more.
- S3 compatibility is not a goal. Nothing from the predecessor is binding.

Framed precisely, the question is: **what may the client do concurrently, and
what must the server track to allow it?**

## Options considered

All three options split an object into pieces sent as separate requests, keep
several pieces in flight, and support resume after a failure. They differ in
ordering rules, in how the server stores pieces, and in what resume returns.

### Option A: Pipelined sequential chunks (append at offset)

The client creates an upload session, then sends chunks tagged with their byte
offset. The server writes each chunk at its offset into one file and tracks the
*committed length*: the longest contiguous prefix received and fsynced. Chunks
may be in flight concurrently, but only within a window ahead of the committed
length; a chunk further ahead than the window is rejected. Resume asks the
server for its committed length and re-sends from there. Finish flips the
object visible once the committed length equals the declared size.

With a window of one this degenerates to strict append, where the file's own
size is the committed length and no metadata is needed at all.

| Dimension | Assessment |
|-----------|------------|
| Server complexity | Low. One file, `WriteAt`, one integer per session plus a small in-memory range set for the window. |
| Client complexity | Low. A loop with a bounded window; on any failure, ask for the committed length and restart the window from there. |
| Metadata store needs | One integer per session, updated after fsync. Finish is one transaction (check length, flip visible). |
| Crash safety | On restart, truncate to the persisted committed length. At most a window's worth of data is re-sent. |
| Throughput | Hides per-request latency up to the window size. A lost or slow chunk stalls the window until it lands (head-of-line blocking). |

**Pros**

- Resume is a single integer. Nothing for the client to reconcile.
- Data arrives in order, so the server can maintain a running whole-object
  hash for free while committing. No second pass over the file.
- The committed prefix is always a valid partial object.
- Smallest protocol surface: create, put chunk, finish, and a query for length.

**Cons**

- Head-of-line blocking: the window cannot advance past a missing chunk, so one
  slow request idles every connection behind it.
- The window is a server-side policy the client must discover or be told.
- Chunks sent from more than one machine are impossible by construction.
- Retry is not per chunk: after a failure the client re-sends from the committed
  length, including chunks that may already have landed beyond the gap.

### Option B: Numbered parts, assembled on complete (S3 shape)

The client initiates an upload, sends parts numbered 1..N in any order and any
size (within limits), then calls complete with the list of parts. The server
stores each part as its own file, validates each on arrival, and on complete
concatenates them into the final object. Resume lists the parts the server has.

| Dimension | Assessment |
|-----------|------------|
| Server complexity | High. Per-part files, a parts table, a crash-safe assembly step (write to temp, fsync, rename), garbage collection of orphaned parts. |
| Client complexity | Medium. Bounded parallel map over parts, collect per-part receipts, send them at complete. Familiar to anyone who has used an S3 SDK. |
| Metadata store needs | A parts table with per-part size and checksum; complete is a multi-row transaction. This is the case that "wants transactions". |
| Crash safety | Each part is an independent idempotent unit. Assembly must be atomic, which is the hard part. |
| Throughput | Full client freedom. The assembly step re-reads and re-writes the whole object on the server, which is a second pass of disk I/O per upload. |

**Pros**

- Any order, any concurrency, independent per-part retry.
- Parts can come from different machines.
- Variable part sizes.

**Cons**

- Assembly doubles server disk work per upload, which contradicts the product
  goal directly.
- The most server code and the most metadata of the three options.
- Variable part sizes buy nothing here. They are an accident of S3's history.

### Option C: Numbered parts of fixed size, written at offset (no assembly)

The client initiates an upload and declares the part size. Every part except
the last is exactly that size, so part *n* lives at offset `(n-1) * partSize`
and the server writes it straight into the one file with `WriteAt`. Parts may
arrive in any order and any concurrency. The server tracks the set of received
parts (a bitmap or a list). Resume returns that set. Complete states the part
count, the server checks the set is full, and flips the object visible.

Total size need not be known up front: the last part is simply shorter, and
complete declares how many parts there are.

| Dimension | Assessment |
|-----------|------------|
| Server complexity | Low to medium. One file, `WriteAt`, a received-parts set per session. No assembly, no per-part files. |
| Client complexity | Medium. Bounded parallel map over parts with per-part retry; on resume, diff the server's set against the total. |
| Metadata store needs | One set per session, updated after each part's fsync. Complete is one transaction (check set is full, flip visible). |
| Crash safety | Each part is an independent idempotent unit: write at offset, fsync, mark received. A crash between the last two steps costs one re-sent part. |
| Throughput | Full client freedom, no head-of-line blocking, no server-side second pass. |

**Pros**

- Option B's client freedom at Option A's server cost.
- Per-part retry is independent; nothing stalls behind a slow part.
- Multi-machine upload works, at no extra cost, should anyone ever want it.
- No window policy to expose; the server's only concurrency limit is
  operational (connections, open files), not protocol.

**Cons**

- Part size is fixed for the session. The client must choose it before the
  first byte. (A sensible default in every SDK makes this invisible.)
- Out-of-order arrival means a whole-object hash is not free. Either the
  checksum scheme is composable (CRC32C combines across parts; a hash of
  per-part hashes works too), or the server keeps an opportunistic running hash
  over the contiguous prefix and hashes any remaining tail at complete. In the
  common case parts arrive nearly in order and the tail is empty.
- Resume returns a set, not an integer. Slightly more client code than A.

## Trade-off analysis

**A versus C is a smaller difference than it looks.** On the wire, A tags a
piece with an offset and C tags it with a number; the server does the same
`WriteAt` either way. The real difference is that A forbids a piece more than a
window ahead of the committed prefix and C does not. That restriction buys A a
single-integer resume and a free running hash, and costs A head-of-line
blocking and a window policy. For a product whose first goal is speed, paying
idle line time to keep a metadata integer small is the wrong trade.

**B is dominated by C.** Everything B offers that C does not (variable part
sizes) is worthless here, and B's assembly step is a second full pass of disk
I/O per upload on a server whose disk is the bottleneck.

**Parallelism is needed for a reason beyond per-request latency.** A single
TCP connection over a long, fat pipe often cannot fill the line, regardless of
chunk size. Clients far from the server, which is the normal case behind a
CDN, get their throughput from several connections at once. Options A and C
both allow this; B and C allow it without a stall.

**Chunk size alone is a lever worth measuring.** With pieces near the proxy cap,
per-request latency is a low single-digit percentage of transfer time on a
typical line. It is possible that the predecessor's gap was mostly small
pieces, not the lack of concurrency. This does not change the decision (C
still costs nothing extra), but the first upload benchmark should vary piece
size and concurrency independently so the spec's recommended defaults rest on
a measurement rather than on this paragraph.

**Effect on open question 2 (metadata store).** The premise that "a parts table
wants transactions in a way an append-only session does not" holds only for
Option B, where parts are separate files. Under C the per-upload state is one
row with a received-parts set, and the only transaction is complete: check the
set, flip visible. That is the same transaction A needs. Every candidate store
handles it, including SQLite with a single writer. The upload shape therefore
does **not** force the store choice; throughput on object metadata reads and
writes does. Open question 2 can be decided on that basis alone.

## Decision

**Option C: numbered parts of a fixed, per-session part size, written at their
offset into a single file, completed by a part count.**

Client concurrency is unbounded by the protocol. The server tracks one
received-parts set per upload session, persisted after each part is durable.
Complete verifies the set is full and makes the object visible in one
metadata transaction. There is no assembly step.

## Consequences

Easier:

- Server upload code is one file handle, `WriteAt`, and a set. Nothing to
  assemble, nothing to garbage-collect except abandoned sessions.
- Every SDK implements the same small pattern: bounded parallel map over parts,
  per-part retry, one complete call. Resume is "fetch the set, send the rest."
- Conformance cases can express every state transition as plain requests:
  initiate, part in any order, duplicate part, part with wrong size, complete
  with missing part, complete with wrong count, resume query.
- The metadata store is free to be chosen for read/write throughput.

Harder:

- A whole-object hash needs a decision (see follow-ups). Per-part checksums are
  natural; the object-level one is either composable or costs a tail read.
- The client must pick a part size up front. The SDKs own a default; the spec
  states the minimum and maximum the server accepts.
- Two size rules to specify and enforce: every non-final part equals the
  session's part size, and the final part is at most that size. A part that
  breaks either rule is rejected before any byte is written.

To revisit:

- If a deployment ever needs pieces from many machines with no shared
  knowledge of part size, C already allows it, but the session's part size
  would need to be discoverable. Not needed now.
- If measurement shows single-connection uploads with large pieces already
  saturate real lines, the SDK default concurrency can be 1 with no protocol
  change.

## Protocol obligations (for the spec, not decided here)

The wire contract must be able to express:

1. Initiate: key, declared part size, optional declared total size, returns a
   session id.
2. Put part: session id, part number, body; server checks the size rule,
   writes at offset, fsyncs, records the part, returns a per-part checksum.
3. Query: session id, returns the received-parts set and the part size.
4. Complete: session id, part count (and, if the checksum scheme needs it, the
   per-part checksums); server checks the set is full, flips visible.
5. Abort: session id, releases the file and the row.
6. Expiry of abandoned sessions after a server-defined time.

Endpoint names, headers, error codes, part-size limits, and the checksum
scheme are separate decisions and belong to the wire contract and its ADRs.

## Action items

1. [ ] Maintainer accepts, amends, or rejects this record.
2. [ ] Decide the checksum scheme (per-part and whole-object) in its own ADR;
       it is the one place where C is more expensive than A.
3. [ ] Decide open question 2 (metadata store) on read/write throughput alone,
       now that the upload shape does not constrain it.
4. [ ] Write the upload section of the wire contract and the conformance cases
       listed under "Easier".
5. [ ] First benchmark: vary part size and concurrency independently, record
       the numbers, and set SDK defaults from them.
