CREATE TABLE uploads (
  id TEXT NOT NULL PRIMARY KEY,
  bucket TEXT NOT NULL REFERENCES buckets(name),
  key TEXT NOT NULL,
  blob_id TEXT NOT NULL,
  part_size INTEGER NOT NULL,
  content_type TEXT NOT NULL,
  metadata TEXT NOT NULL DEFAULT '{}',
  parts TEXT NOT NULL DEFAULT '{}',
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
) WITHOUT ROWID,
STRICT;
CREATE INDEX uploads_by_bucket ON uploads (bucket);
