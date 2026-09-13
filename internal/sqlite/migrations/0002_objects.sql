CREATE TABLE objects (
  bucket TEXT NOT NULL REFERENCES buckets (name),
  key TEXT NOT NULL,
  blob_id TEXT NOT NULL,
  size INTEGER NOT NULL,
  content_type TEXT NOT NULL,
  checksum TEXT NOT NULL,
  metadata TEXT NOT NULL DEFAULT '{}',
  created_at INTEGER NOT NULL,
  PRIMARY KEY (bucket, key)
) WITHOUT ROWID,
STRICT;
