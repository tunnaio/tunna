# Metadata schema

The SQLite schema behind `internal/sqlite` (ADR-0002). This document explains
the design; the migration files under `internal/sqlite/migrations/` are the
authority on the exact DDL. Probes that read the database directly should
read this first.

Not part of the wire spec: SDKs never see this. It lives in `docs/` for the
same reason the decisions do.

## Conventions

- **Timestamps are `INTEGER` Unix seconds**, the same representation the wire
  uses (spec/wire.md 1). No conversion between the store and the response.
- **Booleans are `INTEGER` 0 or 1.** SQLite has no boolean type; the adapter
  converts.
- **Text-keyed tables are `WITHOUT ROWID`.** The primary key is then the
  table's one B-tree, so a point read by key and an ordered prefix scan both
  walk a single structure with no rowid indirection. This is the shape the
  driver benchmark measured.
- **`STRICT` tables.** SQLite's strict mode rejects a value of the wrong type
  instead of coercing it, which turns an adapter bug into an error rather
  than a corrupt row.
- **The store trusts its callers.** No `CHECK` constraint restates a naming
  rule from the root package; validation is stage 4 of the ladder, once, in
  the core. Constraints here express integrity (keys, foreign keys, not-null),
  not policy.
- **Migrations are numbered SQL files** embedded in the binary, applied in
  order at startup inside one transaction each. The applied version is
  `PRAGMA user_version`, SQLite's built-in integer slot, so there is no
  bookkeeping table. A file is never edited after it ships; a change is a
  new file.

## Connection pragmas

Set on every connection the adapter opens, in this order:

| Pragma | Value | Why |
|--------|-------|-----|
| `journal_mode` | `WAL` | Readers never block the writer. Persistent once set; harmless to repeat. |
| `synchronous` | `FULL` | A committed transaction survives power loss. Per connection, so it is set every time. |
| `busy_timeout` | 5000 | Wait rather than fail when the writer holds the lock. |
| `foreign_keys` | `ON` | Off by default in SQLite; must be enabled per connection. |

Two pools: one connection for the writer goroutine (`SetMaxOpenConns(1)`),
a small pool for readers. Every write goes through the writer so group
commit can batch them.

## Version 1: keys and buckets

```sql
CREATE TABLE api_keys (
    id         TEXT    NOT NULL PRIMARY KEY,
    secret     TEXT    NOT NULL,
    disabled   INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
) WITHOUT ROWID, STRICT;

CREATE TABLE buckets (
    name       TEXT    NOT NULL PRIMARY KEY,
    public     INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
) WITHOUT ROWID, STRICT;
```

Notes:

- `api_keys.secret` is stored in usable form, as ADR-0003 requires. File
  permissions on the database are the boundary. Encryption at rest is a
  listed revisit, not a v1 feature.
- `api_keys.created_at` was ahead of the domain type until version 4 gave
  the type a `CreatedAt` field; see there.
- `buckets` has no row count or size column. Those are derived from
  `objects` when needed, and cached only if a measurement says they must be.
- Listing buckets is `SELECT ... FROM buckets ORDER BY name`, a walk of the
  primary key.

## Version 2: objects

```sql
CREATE TABLE objects (
    bucket       TEXT    NOT NULL REFERENCES buckets(name),
    key          TEXT    NOT NULL,
    blob_id      TEXT    NOT NULL,
    size         INTEGER NOT NULL,
    content_type TEXT    NOT NULL,
    checksum     TEXT    NOT NULL,
    metadata     TEXT    NOT NULL DEFAULT '{}',
    created_at   INTEGER NOT NULL,
    PRIMARY KEY (bucket, key)
) WITHOUT ROWID, STRICT;
```

Notes:

- `blob_id` names the file on disk (ADR-0007). An overwrite writes a new
  file, then updates this column; the previous id is returned to the caller
  for removal after commit. `PutObject` is therefore an upsert that also
  reads the old value, in one statement: `INSERT ... ON CONFLICT DO UPDATE
  ... RETURNING` cannot return the pre-update value, so the adapter does a
  `SELECT blob_id` and the upsert inside one transaction.
- `checksum` is the wire form, `crc32c=...` (ADR-0006), stored as text so it
  is served without conversion and readable in the CLI.
- `metadata` is a JSON object as text. SQLite's JSON functions can query it
  if that is ever wanted; today it is opaque to the store.
- `REFERENCES buckets(name)` with `foreign_keys=ON` means a bucket cannot be
  deleted while objects reference it, which is `bucket_not_empty`. The
  adapter checks explicitly first to answer with the right code rather than
  relying on the constraint error.
- Prefix listing is `WHERE bucket = ? AND key >= ? AND key < ?` on the
  primary key, with the upper bound being the prefix with its last byte
  incremented, or no upper bound for an empty prefix. `after` adds
  `AND key > ?`. Ordered by key, which is the index order, bytewise.

## Version 3: upload sessions

```sql
CREATE TABLE uploads (
    id           TEXT    NOT NULL PRIMARY KEY,
    bucket       TEXT    NOT NULL REFERENCES buckets(name),
    key          TEXT    NOT NULL,
    blob_id      TEXT    NOT NULL,
    part_size    INTEGER NOT NULL,
    content_type TEXT    NOT NULL,
    metadata     TEXT    NOT NULL DEFAULT '{}',
    parts        TEXT    NOT NULL DEFAULT '{}',
    created_at   INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL
) WITHOUT ROWID, STRICT;

CREATE INDEX uploads_by_bucket ON uploads (bucket);
```

Notes:

- `parts` is a JSON object mapping part number to `{"size": n, "checksum":
  "crc32c=..."}` for every part received. ADR-0001 sketched a bitmap; ADR-0006
  then required the per-part CRC so complete can combine them, and the
  length so a short non-final part is caught, so a bitmap is not enough. At
  ten thousand parts the object is a few hundred kilobytes and is rewritten
  once per part, which is negligible next to the part itself. It is also
  readable in the CLI, which a bitmap is not.
- `blob_id` is the file created at initiate (ADR-0007); parts write into it
  at offset. Complete moves the id into an `objects` row; abort removes the
  file.
- Complete is one transaction: read the session, insert or replace the
  object row (returning the previous blob id as `PutObject` does), delete
  the session. It runs under `_txlock=immediate` like every read-then-write.
- The index on `bucket` serves `bucket_not_empty`, which must also see
  active sessions, and the expiry sweep's per-bucket accounting.
- Expired sessions stay in the table until a sweep removes them and their
  files; until then they answer `session_not_active`. The sweep is not in
  this version.

## Version 4: key management

```sql
ALTER TABLE api_keys ADD COLUMN name   TEXT    NOT NULL DEFAULT '';
ALTER TABLE api_keys ADD COLUMN admin  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE api_keys ADD COLUMN scopes TEXT    NOT NULL DEFAULT '{}';
```

Notes:

- Three `ALTER TABLE ... ADD COLUMN` statements rather than a rebuild.
  SQLite's `ADD COLUMN` on a `STRICT`, `WITHOUT ROWID` table is allowed as
  long as the column has a default and is not a primary key, which these
  satisfy, and it does not rewrite the table.
- Rows that predate the migration get `name = ''`, `admin = 0`, `scopes =
  '{}'`: a key with no access at all. The only such row in practice is the
  bootstrap key, which `cmd/tunna` upserts as an admin named `bootstrap` on
  every start while the variable is set, so it heals itself on the first
  start after the upgrade.
- `scopes` is a JSON object mapping bucket name or `*` to `"read"` or
  `"write"` (ADR-0008), stored as text like `uploads.parts` and `objects.metadata`.
  It is never queried, only read whole with the row, so no index and no
  `json_extract`.
- `admin` and a non-empty `scopes` are exclusive on the wire; the store does
  not enforce it, the handler's stage-4 validation does, so the rule lives
  in one place.
- `created_at` now has a domain field to fill. `CreateKey` writes the value
  the caller passes (Unix seconds); `UpdateKey` never touches it.
- Listing is `SELECT ... FROM api_keys ORDER BY id`, a walk of the primary
  key, like buckets.

## Later versions, sketched

- `bucket_not_empty` on delete becomes a query over `objects` and `uploads`
  by bucket, both of which want an index on `bucket` that the primary keys
  above already provide.
