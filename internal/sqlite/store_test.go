package sqlite_test

import (
	"context"
	"testing"

	"github.com/tunnaio/tunna"
	"github.com/tunnaio/tunna/internal/storetest"
)

// The SQLite adapter must pass the same contract tests as the in-memory
// fakes. Each subtest gets a fresh database file in a temporary directory.

func TestKeyStoreContract(t *testing.T) {
	storetest.KeyStore(t, func(t *testing.T, keys []tunna.APIKey) tunna.KeyStore {
		db, _ := openFresh(t)
		for _, k := range keys {
			if err := db.PutKey(context.Background(), k); err != nil {
				t.Fatalf("PutKey %s: %v", k.ID, err)
			}
		}
		return db
	})
}

func TestBucketStoreContract(t *testing.T) {
	storetest.BucketStore(t, func(t *testing.T) tunna.BucketStore {
		db, _ := openFresh(t)
		return db
	})
}

// PutKey is not part of tunna.KeyStore; it is how keys get into the store
// until key management exists. Its own behaviour: insert, then replace the
// mutable fields on a second call with the same id.
func TestPutKeyUpserts(t *testing.T) {
	db, path := openFresh(t)
	ctx := context.Background()

	if err := db.PutKey(ctx, tunna.APIKey{ID: "tk_x", Secret: "one"}); err != nil {
		t.Fatalf("first PutKey: %v", err)
	}
	if err := db.PutKey(ctx, tunna.APIKey{ID: "tk_x", Secret: "two", Disabled: true}); err != nil {
		t.Fatalf("second PutKey: %v", err)
	}

	got, err := db.GetKey(ctx, "tk_x")
	if err != nil {
		t.Fatalf("GetKey: %v", err)
	}
	if got.Secret != "two" || !got.Disabled {
		t.Errorf("after upsert GetKey = %+v, want secret two, disabled", got)
	}

	// Trust the file: exactly one row, and the secret column holds the new value.
	var n int
	var secret string
	raw := inspect(t, path)
	if err := raw.QueryRow("SELECT count(*), max(secret) FROM api_keys WHERE id = 'tk_x'").Scan(&n, &secret); err != nil {
		t.Fatal(err)
	}
	if n != 1 || secret != "two" {
		t.Errorf("rows for tk_x = %d with secret %q, want 1 row with two", n, secret)
	}
}
