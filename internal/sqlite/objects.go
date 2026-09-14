package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/tunnaio/tunna"
)

// putObjectTx is the select-previous-blob-id-then-upsert that PutObject and
// CompleteUpload share, run inside the caller's transaction.
func putObjectTx(ctx context.Context, tx *sql.Tx, o tunna.Object) (prevBlobID string, err error) {
	prev := ""
	err = tx.QueryRowContext(ctx, "SELECT blob_id FROM objects WHERE bucket = ? AND key = ?", o.Bucket, o.Key).Scan(&prev)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if o.Metadata == nil {
		o.Metadata = map[string]string{}
	}
	metadata, err := json.Marshal(o.Metadata)
	if err != nil {
		return "", err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO objects (bucket, key, blob_id, size, content_type, checksum, metadata, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (bucket, key) DO UPDATE SET
		blob_id = excluded.blob_id, size = excluded.size, content_type = excluded.content_type,
		checksum = excluded.checksum, metadata = excluded.metadata, created_at = excluded.created_at`,
		o.Bucket, o.Key, o.BlobID, o.Size, o.ContentType, o.Checksum, string(metadata), o.CreatedAt.Unix(),
	)
	if err != nil {
		return "", err
	}
	return prev, nil
}

// GetObject implements tunna.ObjectStore. Metadata is stored as JSON text;
// a row that fails to decode is reported as an error, since only this
// adapter writes the column. The returned map is never nil.
func (d *DB) GetObject(ctx context.Context, bucket, key string) (tunna.Object, error) {
	var created int64
	var metadata string
	o := tunna.Object{
		Bucket: bucket,
		Key:    key,
	}
	err := d.db.QueryRowContext(ctx, `
		SELECT blob_id, size, content_type, checksum, metadata, created_at FROM objects
		WHERE bucket = ? AND key = ?`,
		bucket, key,
	).Scan(&o.BlobID, &o.Size, &o.ContentType, &o.Checksum, &metadata, &created)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return tunna.Object{}, tunna.ErrNotFound
	case err != nil:
		return tunna.Object{}, err
	}
	o.CreatedAt = time.Unix(created, 0).UTC()
	err = json.Unmarshal([]byte(metadata), &o.Metadata)
	if err != nil {
		return tunna.Object{}, fmt.Errorf("sqlite: object %s/%s metadata: %w", bucket, key, err)
	}
	if o.Metadata == nil {
		o.Metadata = map[string]string{}
	}
	return o, nil
}

// PutObject implements tunna.ObjectStore: read the previous blob id, then
// upsert, in one transaction. The DSN's _txlock=immediate takes the write
// lock at BEGIN, so the read cannot go stale before the write.
func (d *DB) PutObject(ctx context.Context, o tunna.Object) (previousBlobID string, err error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback() // no-op after a successful Commit
	prev, err := putObjectTx(ctx, tx, o)
	if err != nil {
		return "", err
	}
	return prev, tx.Commit()
}

// DeleteObject implements tunna.ObjectStore: read the blob id, then delete,
// in one immediate transaction, so the id returned is the row removed.
func (d *DB) DeleteObject(ctx context.Context, bucket, key string) (blobID string, err error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback() // no-op after a successful Commit
	err = tx.QueryRowContext(ctx, "SELECT blob_id FROM objects WHERE bucket = ? AND key = ?", bucket, key).Scan(&blobID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", tunna.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	res, err := tx.ExecContext(ctx, "DELETE FROM objects WHERE bucket = ? AND key = ?", bucket, key)
	if err != nil {
		return "", err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "", tunna.ErrNotFound
	}
	return blobID, tx.Commit()
}

// ListObjects implements tunna.ObjectStore. key >= prefix is the range scan
// on the primary key; substr(key, 1, len) = prefix is the exact starts-with
// test, since a prefix ending in 0xFF has no clean upper bound; key > after
// is the exclusive cursor, and an empty after is below every key.
func (d *DB) ListObjects(ctx context.Context, bucket, prefix, after string, limit int) ([]tunna.Object, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT key, blob_id, size, content_type, checksum, metadata, created_at FROM objects
		WHERE bucket = ? AND key >= ? AND substr(key, 1, ?) = ? AND key > ?
		ORDER BY key
		LIMIT ?`,
		bucket, prefix, len(prefix), prefix, after, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]tunna.Object, 0)
	for rows.Next() {
		var created int64
		var metadata string
		o := tunna.Object{
			Bucket: bucket,
		}
		if err := rows.Scan(&o.Key, &o.BlobID, &o.Size, &o.ContentType, &o.Checksum, &metadata, &created); err != nil {
			return nil, err
		}
		o.CreatedAt = time.Unix(created, 0).UTC()
		if metadata == "" {
			o.Metadata = map[string]string{}
		} else {
			if err := json.Unmarshal([]byte(metadata), &o.Metadata); err != nil {
				return nil, err
			}
		}
		list = append(list, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return list, nil
}

var _ tunna.ObjectStore = (*DB)(nil)
