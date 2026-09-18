package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/tunnaio/tunna"
)

// GetBucket implements tunna.BucketStore. created_at is stored as Unix
// seconds and comes back in UTC so values compare equal with ==.
func (d *DB) GetBucket(ctx context.Context, name string) (tunna.Bucket, error) {
	var created int64
	b := tunna.Bucket{
		Name: name,
	}
	err := d.db.QueryRowContext(ctx, "SELECT public, created_at FROM buckets WHERE name = ?", name).Scan(&b.Public, &created)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return tunna.Bucket{}, tunna.ErrNotFound
	case err != nil:
		return tunna.Bucket{}, err
	}
	b.CreatedAt = time.Unix(created, 0).UTC()
	return b, nil
}

// CreateBucket implements tunna.BucketStore. INSERT OR IGNORE plus the
// affected-row count reports a taken name as ErrConflict in one atomic
// statement, with no driver error codes to parse.
func (d *DB) CreateBucket(ctx context.Context, b tunna.Bucket) error {
	res, err := d.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO buckets (name, public, created_at) VALUES (?, ?, ?)`,
		b.Name, b.Public, b.CreatedAt.Unix())
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

// ListBuckets implements tunna.BucketStore. ORDER BY name is a walk of the
// primary key on a WITHOUT ROWID table.
func (d *DB) ListBuckets(ctx context.Context) ([]tunna.Bucket, error) {
	buckets := make([]tunna.Bucket, 0)

	rows, err := d.db.QueryContext(ctx, "SELECT name, public, created_at FROM buckets ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var created int64
		b := tunna.Bucket{}
		if err := rows.Scan(&b.Name, &b.Public, &created); err != nil {
			return nil, err
		}
		b.CreatedAt = time.Unix(created, 0).UTC()
		buckets = append(buckets, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return buckets, nil
}

// DeleteBucket implements tunna.BucketStore. Zero affected rows is
// ErrNotFound. Refusing to delete a non-empty bucket arrives with objects.
func (d *DB) DeleteBucket(ctx context.Context, name string) error {
	res, err := d.db.ExecContext(ctx, "DELETE FROM buckets WHERE name = ?", name)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return tunna.ErrNotFound
	}
	return nil
}

// UpdateBucket implements tunna.BucketStore. Zero affected rows is
// ErrNotFound.
func (d *DB) UpdateBucket(ctx context.Context, b tunna.Bucket) error {
	res, err := d.db.ExecContext(ctx, "UPDATE buckets SET public = ? WHERE name = ?", b.Public, b.Name)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return tunna.ErrNotFound
	}
	return nil
}

var _ tunna.BucketStore = (*DB)(nil)
