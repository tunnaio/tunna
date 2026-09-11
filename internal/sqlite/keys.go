package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/tunnaio/tunna"
)

// GetKey implements tunna.KeyStore. sql.ErrNoRows is the one driver-level
// sentinel the adapter translates; callers see only the root error kinds.
func (d *DB) GetKey(ctx context.Context, id string) (tunna.APIKey, error) {
	k := tunna.APIKey{
		ID: id,
	}
	err := d.db.QueryRowContext(ctx, "SELECT secret, disabled FROM api_keys WHERE id = ?", id).Scan(&k.Secret, &k.Disabled)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return tunna.APIKey{}, tunna.ErrNotFound
	case err != nil:
		return tunna.APIKey{}, err
	}
	return k, nil
}

// PutKey inserts a key or replaces the mutable fields of an existing one.
// Not part of tunna.KeyStore; it is how keys get into the store until key
// management exists (ADR-0003 follow-up).
func (d *DB) PutKey(ctx context.Context, k tunna.APIKey) error {
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO api_keys (id, secret, disabled, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			secret = excluded.secret,
			disabled = excluded.disabled`,
		k.ID, k.Secret, k.Disabled, time.Now().Unix())
	return err
}

var _ tunna.KeyStore = (*DB)(nil)
