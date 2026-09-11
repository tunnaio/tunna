# ADR-0007: Blob layout on disk

**Status:** Accepted
**Date:** 2026-09-11
**Deciders:** maintainer

## Context

ADR-0002 named this as a follow-up: with a metadata store mapping key to
location, object bytes need not live at a path derived from the key. This
record decides how files are named, where they sit, and what a write, an
overwrite and a delete do on disk.

Forces:

- **Keys are not paths.** Object keys are up to 1024 bytes of almost any
  Unicode, may contain `/`, and `a` and `a/b` may both exist. Filesystems
  cap path components, reject some characters, and cannot hold a file and a
  directory of the same name. Any layout that derives the path from the key
  needs an escaping scheme and an index anyway.
- **Overwrite must be atomic to readers.** A `PUT` on an existing key while
  another client is mid-`GET` must give the reader the old bytes or the new
  bytes, never a mixture.
- **Crash safety.** A crash between writing bytes and recording metadata
  must leave the store consistent: either the object exists with correct
  bytes, or it does not exist. Orphan files are acceptable and collectable;
  a row pointing at wrong bytes is not.
- **Uploads write at offset** (ADR-0001) into a file that exists before its
  metadata row does, and may be abandoned.
- **Directory size.** Filesystems degrade with hundreds of thousands of
  entries in one directory. Fanning out by a prefix keeps directories small.
- **One disk, local.** No sharding across volumes is needed. A single data
  directory is the deployment story.

## Options considered

### Option A: Content-addressed (path from the checksum)

File named by the object's CRC32C or a stronger hash. Identical bytes share
a file.

**Pros:** deduplication for free.

**Cons:** the name is not known until the last byte is written, so every
write goes to a temporary name and is renamed, which is fine, but delete
becomes reference counting across objects, which is a whole subsystem. With
CRC32C as the only checksum (ADR-0006), collisions are certain at scale, so
this would need a second hash purely for naming. Rejected: deduplication is
not a goal and reference counting is a permanent tax.

### Option B: Path derived from bucket and key

`<data>/<bucket>/<escaped key>`. The filesystem is the index.

**Cons:** every force above argues against it: escaping, component limits,
the file-versus-directory conflict, and no atomic overwrite without a
rename dance that then also needs escaping. Rejected before scoring; it is
the layout ADR-0002 already ruled out as "the filesystem is the metadata".

### Option C: Random id per object version, fanned out, metadata points to it

Every object version gets a fresh random id when its bytes are first
written. The file lives at `<data>/objects/<aa>/<id>` where `aa` is the
first byte of the id in hex, Git's layout. The metadata row holds
the id. Overwrite writes a new file under a new id, then updates the row to
point at it; the old file is deleted after the row commits. Delete removes
the row, then the file.

**Pros:** no escaping, no key limits, atomic overwrite by construction,
crash-safe by ordering, and uploads at offset fit because the file exists
under its id from initiate onward.

**Cons:** orphan files are possible after a crash between "file written" and
"row committed", and after a crash between "row updated" and "old file
removed". Both are harmless and collectable by a sweep that lists files and
checks rows.

## Decision

**Option C.**

Fixed by this record:

1. **Ids.** Sixteen random bytes from the operating system, hex-encoded:
   thirty-two lowercase hex characters. No time component, no structure;
   the row is the only place an id means anything.
2. **Path.** `<data>/objects/<id[0:2]>/<id>`. One level of 256-way
   fan-out, so a million objects is about four thousand per directory,
   comfortable for every filesystem and every tool an operator runs. Flat
   was rejected because listing, backup and directory-index splits degrade
   past a few hundred thousand entries and a layout change later means
   moving every file; a second level was rejected as structure for a scale
   the design does not target (decided 2026-09-11). Directories are created
   on demand.
3. **Write, single request.** Create the file under its final id, stream
   the body into it computing the CRC32C as it passes, `fsync` the file,
   `fsync` the leaf directory so the entry itself is durable, then commit
   the metadata row. Only after the row commits is the object visible.
4. **Write, multipart.** Create the file at initiate, under the session's
   object id. Parts write at offset and each `fsync`s. Complete commits the
   row pointing at the id. Abort or expiry deletes the file.
5. **Overwrite.** Same as write with a new id; the row update swaps the id
   in one transaction; the previous id's file is deleted after commit.
   Readers holding the old file open keep reading it: on Unix an open
   file survives unlink, and on Windows the adapter opens files with
   delete-sharing so the same holds.
6. **Delete.** Delete the row, then the file. A crash between the two
   leaves an orphan, never a dangling row.
7. **Read.** Open by id, serve bytes with `Content-Length` from the row,
   honour `Range`. No checksum verification on read in v1; recorded as an
   open question below.
8. **Sweep.** A maintenance operation, not in v1, lists `objects/` and
   deletes files whose id no row references and whose modification time is
   older than the upload session expiry. Until it exists, orphans are bounded
   by crash frequency and are visible to `du`.
9. **Interface.** The root package declares what the core needs:

   ```go
   type BlobStore interface {
       Create(ctx) (id string, w BlobWriter, err error)   // new file, returns its id
       Open(ctx, id) (io.ReadSeekCloser, error)           // for reads and Range
       WriteAt(ctx, id, offset int64, r io.Reader) (n int64, err error)  // multipart parts
       Sync(ctx, id) error
       Remove(ctx, id) error
   }
   ```

   Exact method set is settled when the disk adapter is written; the shape
   above is the intent, and `io` types are allowed in the root package.

## Consequences

Easier:

- Keys are free-form; nothing on disk ever sees one.
- Overwrite and delete are one-row transactions plus a file operation whose
  failure is harmless.
- The disk adapter is small: create, open, write-at, sync, remove, all on
  paths it computes itself.

Harder:

- Orphans exist until the sweep does. Documented, bounded, visible.
- Backups must copy both the database and the `objects/` tree, and a
  consistent backup takes the database snapshot first, then the files, so
  every row's file exists in the copy.
- On Windows, file deletion while open needs the delete-sharing flag on
  open, which Go's `os.Open` does not set. Probed 2026-09-11 on Windows:
  with a plain `os.Open` held, `os.Remove` fails with "the file is in use"
  while the open handle keeps reading, and `Stat` still sees the file. So
  overwrite and delete would fail whenever a reader is mid-download. The
  adapter opens with `FILE_SHARE_DELETE` on Windows via `syscall`, behind a
  build tag, and the same delete-while-open test runs on both platforms as
  the adapter's contract. First platform-conditional code in the server.

Open, for the disk adapter's own tests to settle:

- Whether reads verify the stored checksum. Costs a CRC pass per read, which
  is cheap but not free at line rate; a sampling scrub may be the better
  shape.
- Whether `fsync` of the leaf directory is needed on every write or can be
  batched. Correctness says every write; measurement may say otherwise.

## Action items

1. [ ] Maintainer accepts, amends, or rejects this record.
2. [ ] `docs/schema.md` v2 gains the `objects` table with an `id` column as
       sketched there.
3. [ ] Root package: `Object`, `ObjectStore`, `BlobStore`.
4. [ ] `internal/disk` with a contract test, including the open-then-delete
       probe on both platforms.
