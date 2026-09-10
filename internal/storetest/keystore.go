// Package storetest holds contract tests that every implementation of the
// root package's store interfaces must pass. An adapter's own test file
// calls these with a factory for its store, so the in-memory fake and the
// real adapters are held to the same behaviour and cannot drift apart.
package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/tunnaio/tunna"
)

// KeyStoreFactory builds a fresh store holding exactly the given keys.
// Cleanup, if any, is registered with t.Cleanup.
type KeyStoreFactory func(t *testing.T, keys []tunna.APIKey) tunna.KeyStore

// KeyStore runs the KeyStore contract against the store the factory builds.
func KeyStore(t *testing.T, newStore KeyStoreFactory) {
	t.Helper()
	ctx := context.Background()

	alice := tunna.APIKey{ID: "tk_alice", Secret: "s-alice"}
	revoked := tunna.APIKey{ID: "tk_revoked", Secret: "s-revoked", Disabled: true}

	t.Run("returns the key with every field", func(t *testing.T) {
		s := newStore(t, []tunna.APIKey{alice, revoked})
		got, err := s.GetKey(ctx, "tk_alice")
		if err != nil {
			t.Fatalf("GetKey: %v", err)
		}
		if got != alice {
			t.Errorf("GetKey = %+v, want %+v", got, alice)
		}
	})

	t.Run("returns a disabled key rather than hiding it", func(t *testing.T) {
		s := newStore(t, []tunna.APIKey{alice, revoked})
		got, err := s.GetKey(ctx, "tk_revoked")
		if err != nil {
			t.Fatalf("GetKey: %v", err)
		}
		if !got.Disabled {
			t.Errorf("Disabled = false, want true; policy belongs to the caller, not the store")
		}
	})

	t.Run("unknown id is ErrNotFound with a zero key", func(t *testing.T) {
		s := newStore(t, []tunna.APIKey{alice})
		got, err := s.GetKey(ctx, "tk_nobody")
		if !errors.Is(err, tunna.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if got != (tunna.APIKey{}) {
			t.Errorf("key on not-found = %+v, want zero value", got)
		}
	})

	t.Run("ids are exact, not case-insensitive", func(t *testing.T) {
		s := newStore(t, []tunna.APIKey{alice})
		if _, err := s.GetKey(ctx, "TK_ALICE"); !errors.Is(err, tunna.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound for a differently cased id", err)
		}
	})

	t.Run("empty store finds nothing", func(t *testing.T) {
		s := newStore(t, nil)
		if _, err := s.GetKey(ctx, "tk_alice"); !errors.Is(err, tunna.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})
}
