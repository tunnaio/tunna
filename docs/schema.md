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
- `api_keys.created_at` is ahead of the domain type, which has no such field
  yet. It will when key management arrives with the authorization model;
  the adapter fills it from the clock on insert until then.
- `buckets` has no row count or size column. Those are derived from
  `objects` when needed, and cached only if a measurement says they must be.
- Listing buckets is `SELECT ... FROM buckets ORDER BY name`, a walk of the
  primary key.

## Later versions, sketched

Not written as migrations yet; recorded so v1 makes no choice that fights
them.

- **v2, objects.** `objects (bucket TEXT, key TEXT, id TEXT, size INTEGER,
  content_type TEXT, checksum TEXT, created_at INTEGER, metadata TEXT,
  PRIMARY KEY (bucket, key)) WITHOUT ROWID, STRICT`, with `bucket`
  referencing `buckets(name)`. `id` names the blob file on disk (the blob
  layout follow-up from ADR-0002), so an overwrite is a new file and a row
  update, and the old file is collected afterwards. Prefix listing is a
  range scan on the primary key, which the benchmark's prefix scan models.
- **v3, upload sessions.** `uploads (id TEXT PRIMARY KEY, bucket, key,
  part_size INTEGER, parts_received BLOB, expires_at INTEGER, ...)`. The
  received-parts set (ADR-0001) is a bitmap in a `BLOB`, one bit per part,
  ten thousand parts in about 1.3 KB. Complete is one transaction: check the
  bitmap, insert the object, delete the session.
- `bucket_not_empty` on delete becomes a query over `objects` and `uploads`
  by bucket, both of which want an index on `bucket` that the primary keys
  above already provide.
