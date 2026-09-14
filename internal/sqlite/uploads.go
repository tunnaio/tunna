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

// scanUpload fills s from a row selected in GetUpload's column order and
// decodes the two JSON columns. The Scan error is returned unchanged so
// callers can translate sql.ErrNoRows.
func scanUpload(s *tunna.UploadSession, r *sql.Row) error {
	var created int64
	var expires int64
	var parts string
	var metadata string
	err := r.Scan(&s.Bucket, &s.Key, &s.BlobID, &s.PartSize, &s.ContentType, &metadata, &parts, &created, &expires)
	if err != nil {
		return err
	}
	s.CreatedAt = time.Unix(created, 0).UTC()
	s.ExpiresAt = time.Unix(expires, 0).UTC()
	err = json.Unmarshal([]byte(metadata), &s.Metadata)
	if err != nil {
		return fmt.Errorf("sqlite: upload_id=%s metadata: %w", s.ID, err)
	}
	err = json.Unmarshal([]byte(parts), &s.Parts)
	if err != nil {
		return fmt.Errorf("sqlite: upload_id=%s parts: %w", s.ID, err)
	}
	return nil
}

// CreateUpload implements tunna.UploadStore. INSERT OR IGNORE plus the
// affected-row count reports a taken id as ErrConflict. Metadata and parts
// are JSON text; nil maps are stored as empty objects.
func (d *DB) CreateUpload(ctx context.Context, s tunna.UploadSession) error {
	if s.Metadata == nil {
		s.Metadata = map[string]string{}
	}
	metadata, err := json.Marshal(s.Metadata)
	if err != nil {
		return err
	}
	if s.Parts == nil {
		s.Parts = map[int]tunna.Part{}
	}
	parts, err := json.Marshal(s.Parts)
	if err != nil {
		return err
	}
	res, err := d.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO uploads (id, bucket, key, blob_id, part_size, content_type, metadata, parts, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
		s.ID, s.Bucket, s.Key, s.BlobID, s.PartSize, s.ContentType, string(metadata), string(parts), s.CreatedAt.Unix(), s.ExpiresAt.Unix(),
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return tunna.ErrConflict
	}
	return nil
}

// GetUpload implements tunna.UploadStore.
func (d *DB) GetUpload(ctx context.Context, id string) (tunna.UploadSession, error) {
	s := tunna.UploadSession{
		ID: id,
	}
	err := scanUpload(&s, d.db.QueryRowContext(ctx, `
		SELECT bucket, key, blob_id, part_size, content_type, metadata, parts, created_at, expires_at
		FROM uploads WHERE id = ?`,
		id,
	))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return tunna.UploadSession{}, tunna.ErrNotFound
	case err != nil:
		return tunna.UploadSession{}, err
	}

	return s, nil
}

// PutPart implements tunna.UploadStore with one atomic statement: json_set
// on the parts column at the key for part n, so no read-modify-write and no
// transaction. Zero affected rows is ErrNotFound.
func (d *DB) PutPart(ctx context.Context, id string, n int, p tunna.Part) error {
	part, err := json.Marshal(p)
	if err != nil {
		return err
	}
	res, err := d.db.ExecContext(ctx, "UPDATE uploads SET parts = json_set(parts, ?, json(?)) WHERE id = ?", fmt.Sprintf("$.\"%d\"", n), string(part), id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return tunna.ErrNotFound
	}
	return nil
}

// CompleteUpload implements tunna.UploadStore as one immediate transaction:
// the session must exist, the object is upserted through the same path as
// PutObject, and the session is deleted; all commit together or not at all
// (ADR-0001).
func (d *DB) CompleteUpload(ctx context.Context, id string, o tunna.Object) (previousBlobID string, err error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback() // no-op after a successful Commit
	var c int
	err = tx.QueryRowContext(ctx, "SELECT 1 FROM uploads WHERE id = ?", id).Scan(&c)
	if errors.Is(err, sql.ErrNoRows) {
		return "", tunna.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	prev, err := putObjectTx(ctx, tx, o)
	if err != nil {
		return "", err
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM uploads WHERE id = ?", id)
	if err != nil {
		return "", err
	}
	return prev, tx.Commit()
}

// DeleteUpload implements tunna.UploadStore: read the session, then delete,
// in one immediate transaction, so what is returned is what was removed.
func (d *DB) DeleteUpload(ctx context.Context, id string) (tunna.UploadSession, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	defer tx.Rollback()
	if err != nil {
		return tunna.UploadSession{}, err
	}
	s := tunna.UploadSession{
		ID: id,
	}
	err = scanUpload(&s, tx.QueryRowContext(ctx, `
		SELECT bucket, key, blob_id, part_size, content_type, metadata, parts, created_at, expires_at
		FROM uploads WHERE id = ?`,
		id,
	))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return tunna.UploadSession{}, tunna.ErrNotFound
	case err != nil:
		return tunna.UploadSession{}, err
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM uploads WHERE id = ?", id)
	if err != nil {
		return tunna.UploadSession{}, err
	}
	return s, tx.Commit()
}

// CountUploads implements tunna.UploadStore, served by the index on bucket.
func (d *DB) CountUploads(ctx context.Context, bucket string) (int, error) {
	var count int
	err := d.db.QueryRowContext(ctx, "SELECT count(*) FROM uploads WHERE bucket = ?", bucket).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

var _ tunna.UploadStore = (*DB)(nil)
