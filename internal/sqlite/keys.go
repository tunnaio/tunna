package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/tunnaio/tunna"
)

// scanner is what *sql.Row and *sql.Rows have in common, so one scanKey
// serves GetKey and ListKeys.
type scanner interface {
	Scan(dest ...any) error
}

// keyColumns is the select list scanKey expects, in scan order.
const keyColumns = "id, name, secret, admin, scopes, disabled, created_at"

// encodeScopes renders the scopes column: a JSON object, "{}" for no
// scopes, so the column holds one spelling of "none" and matches its
// default. Returned as a string because the column is STRICT TEXT.
func encodeScopes(scopes map[string]tunna.Access) (string, error) {
	if scopes == nil {
		scopes = map[string]tunna.Access{}
	}
	s, err := json.Marshal(scopes)
	if err != nil {
		return "", err
	}
	return string(s), nil
}

// decodeScopes is the inverse of encodeScopes.
func decodeScopes(s string, v *map[string]tunna.Access) error {
	return json.Unmarshal([]byte(s), v)
}

// scanKey fills k from a row selected with keyColumns.
func scanKey(k *tunna.APIKey, r scanner) error {
	var scopes string
	var created int64
	err := r.Scan(&k.ID, &k.Name, &k.Secret, &k.Admin, &scopes, &k.Disabled, &created)
	if err != nil {
		return err
	}
	k.CreatedAt = time.Unix(created, 0)
	return decodeScopes(scopes, &k.Scopes)
}

// GetKey implements tunna.KeyStore. sql.ErrNoRows is the one driver-level
// sentinel the adapter translates; callers see only the root error kinds.
func (d *DB) GetKey(ctx context.Context, id string) (tunna.APIKey, error) {
	var k tunna.APIKey
	err := scanKey(&k, d.db.QueryRowContext(ctx, "SELECT "+keyColumns+" FROM api_keys WHERE id = ?", id))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return tunna.APIKey{}, tunna.ErrNotFound
	case err != nil:
		return tunna.APIKey{}, err
	}
	return k, nil
}

// PutKey inserts a key or replaces every field of an existing one except
// created_at. Not part of tunna.KeyStore; it serves the bootstrap key
// (docs/configuration.md), which is restored on every start so that a
// disabled, rotated or de-admined bootstrap key is the documented way
// back in after a lockout (ADR-0008).
func (d *DB) PutKey(ctx context.Context, k tunna.APIKey) error {
	scopes, err := encodeScopes(k.Scopes)
	if err != nil {
		return err
	}
	_, err = d.db.ExecContext(ctx, `
		INSERT INTO api_keys (id, name, secret, admin, scopes, disabled, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			secret = excluded.secret,
			admin = excluded.admin,
			scopes = excluded.scopes,
			disabled = excluded.disabled`,
		k.ID, k.Name, k.Secret, k.Admin, scopes, k.Disabled, time.Now().Unix())
	return err
}

// CreateKey implements tunna.KeyStore. INSERT OR IGNORE plus the affected
// row count reports a taken id as ErrConflict, the same way CreateBucket
// does.
func (d *DB) CreateKey(ctx context.Context, k tunna.APIKey) error {
	scopes, err := encodeScopes(k.Scopes)
	if err != nil {
		return err
	}
	res, err := d.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO api_keys (id, name, secret, admin, scopes, disabled, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		k.ID, k.Name, k.Secret, k.Admin, scopes, k.Disabled, k.CreatedAt.Unix(),
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

// UpdateKey implements tunna.KeyStore. created_at is not in the SET list,
// so it survives. Zero rows matched is ErrNotFound; SQLite counts rows
// matched by the WHERE, not rows whose values changed.
func (d *DB) UpdateKey(ctx context.Context, k tunna.APIKey) error {
	scopes, err := encodeScopes(k.Scopes)
	if err != nil {
		return err
	}
	res, err := d.db.ExecContext(ctx, `
		UPDATE api_keys
		SET name = ?, secret = ?, admin = ?, scopes = ?, disabled = ?
		WHERE id = ?`,
		k.Name, k.Secret, k.Admin, scopes, k.Disabled, k.ID,
	)
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

// DeleteKey implements tunna.KeyStore.
func (d *DB) DeleteKey(ctx context.Context, id string) error {
	res, err := d.db.ExecContext(ctx, "DELETE FROM api_keys WHERE id = ?", id)
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

// ListKeys implements tunna.KeyStore. ORDER BY id is a walk of the primary
// key. The slice starts non-nil so an empty table lists as [].
func (d *DB) ListKeys(ctx context.Context) ([]tunna.APIKey, error) {
	keys := make([]tunna.APIKey, 0)
	rows, err := d.db.QueryContext(ctx, "SELECT "+keyColumns+" FROM api_keys ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		k := tunna.APIKey{}
		if err := scanKey(&k, rows); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return keys, nil
}

var _ tunna.KeyStore = (*DB)(nil)
