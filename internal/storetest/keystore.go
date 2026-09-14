// Package storetest holds contract tests that every implementation of the
// root package's store interfaces must pass. An adapter's own test file
// calls these with a factory for its store, so the in-memory fake and the
// real adapters are held to the same behaviour and cannot drift apart.
package storetest

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/tunnaio/tunna"
)

// KeyStoreFactory builds a fresh store holding exactly the given keys.
// Cleanup, if any, is registered with t.Cleanup.
type KeyStoreFactory func(t *testing.T, keys []tunna.APIKey) tunna.KeyStore

// KeyStore runs the KeyStore contract against the store the factory builds.
func KeyStore(t *testing.T, newStore KeyStoreFactory) {
	t.Helper()
	ctx := context.Background()

	// Whole seconds, UTC: the SQLite adapter stores Unix seconds, and the
	// contract compares with Equal, so a monotonic reading would never match.
	created := time.Unix(1_788_912_000, 0).UTC()

	alice := tunna.APIKey{ID: "tk_alice", Secret: "s-alice", Name: "alice", Admin: true, CreatedAt: created}
	revoked := tunna.APIKey{ID: "tk_revoked", Secret: "s-revoked", Name: "revoked", Admin: true, Disabled: true, CreatedAt: created}
	reader := tunna.APIKey{
		ID: "tk_reader", Secret: "s-reader", Name: "reader",
		Scopes:    map[string]tunna.Access{"photos": tunna.Read, "*": tunna.Read},
		CreatedAt: created,
	}

	t.Run("GetKey returns the key with every field", func(t *testing.T) {
		s := newStore(t, []tunna.APIKey{alice, revoked, reader})
		for _, want := range []tunna.APIKey{alice, reader} {
			got, err := s.GetKey(ctx, want.ID)
			if err != nil {
				t.Fatalf("GetKey %s: %v", want.ID, err)
			}
			if d := diffKey(got, want); d != "" {
				t.Errorf("GetKey %s: %s", want.ID, d)
			}
		}
	})

	t.Run("GetKey returns a disabled key rather than hiding it", func(t *testing.T) {
		s := newStore(t, []tunna.APIKey{alice, revoked})
		got, err := s.GetKey(ctx, "tk_revoked")
		if err != nil {
			t.Fatalf("GetKey: %v", err)
		}
		if !got.Disabled {
			t.Errorf("Disabled = false, want true; policy belongs to the caller, not the store")
		}
	})

	t.Run("GetKey unknown id is ErrNotFound with a zero key", func(t *testing.T) {
		s := newStore(t, []tunna.APIKey{alice})
		got, err := s.GetKey(ctx, "tk_nobody")
		if !errors.Is(err, tunna.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if d := diffKey(got, tunna.APIKey{}); d != "" {
			t.Errorf("key on not-found: %s, want zero value", d)
		}
	})

	t.Run("ids are exact, not case-insensitive", func(t *testing.T) {
		s := newStore(t, []tunna.APIKey{alice})
		if _, err := s.GetKey(ctx, "TK_ALICE"); !errors.Is(err, tunna.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound for a differently cased id", err)
		}
	})

	t.Run("empty store finds and lists nothing", func(t *testing.T) {
		s := newStore(t, nil)
		if _, err := s.GetKey(ctx, "tk_alice"); !errors.Is(err, tunna.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
		keys, err := s.ListKeys(ctx)
		if err != nil {
			t.Fatalf("ListKeys: %v", err)
		}
		if len(keys) != 0 {
			t.Errorf("ListKeys = %d keys, want 0", len(keys))
		}
	})

	t.Run("CreateKey stores the key and GetKey returns it", func(t *testing.T) {
		s := newStore(t, nil)
		if err := s.CreateKey(ctx, reader); err != nil {
			t.Fatalf("CreateKey: %v", err)
		}
		got, err := s.GetKey(ctx, "tk_reader")
		if err != nil {
			t.Fatalf("GetKey: %v", err)
		}
		if d := diffKey(got, reader); d != "" {
			t.Error(d)
		}
	})

	t.Run("CreateKey with an existing id is ErrConflict and changes nothing", func(t *testing.T) {
		s := newStore(t, []tunna.APIKey{alice})
		dup := alice
		dup.Secret = "s-other"
		dup.Name = "impostor"
		if err := s.CreateKey(ctx, dup); !errors.Is(err, tunna.ErrConflict) {
			t.Fatalf("err = %v, want ErrConflict", err)
		}
		got, err := s.GetKey(ctx, "tk_alice")
		if err != nil {
			t.Fatalf("GetKey: %v", err)
		}
		if d := diffKey(got, alice); d != "" {
			t.Errorf("original changed: %s", d)
		}
	})

	t.Run("a key with no scopes reads back with none", func(t *testing.T) {
		// An admin key, or a scoped key an operator emptied out. Whether the
		// adapter hands back nil or an empty map is its business; the caller
		// only ranges over it and Allows is false either way.
		s := newStore(t, nil)
		none := tunna.APIKey{ID: "tk_none", Secret: "s", Name: "none", Scopes: map[string]tunna.Access{}, CreatedAt: created}
		if err := s.CreateKey(ctx, none); err != nil {
			t.Fatalf("CreateKey: %v", err)
		}
		got, err := s.GetKey(ctx, "tk_none")
		if err != nil {
			t.Fatalf("GetKey: %v", err)
		}
		if len(got.Scopes) != 0 {
			t.Errorf("Scopes = %v, want none", got.Scopes)
		}
	})

	t.Run("ListKeys returns every key ordered by id", func(t *testing.T) {
		s := newStore(t, []tunna.APIKey{revoked, alice, reader})
		keys, err := s.ListKeys(ctx)
		if err != nil {
			t.Fatalf("ListKeys: %v", err)
		}
		want := []tunna.APIKey{alice, reader, revoked}
		if len(keys) != len(want) {
			t.Fatalf("ListKeys = %v, want %v", ids(keys), ids(want))
		}
		for i := range want {
			if d := diffKey(keys[i], want[i]); d != "" {
				t.Errorf("ListKeys[%d]: %s", i, d)
			}
		}
	})

	t.Run("UpdateKey replaces the mutable fields and keeps created_at", func(t *testing.T) {
		s := newStore(t, []tunna.APIKey{reader})
		changed := reader
		changed.Secret = "s-rotated"
		changed.Name = "reader-2"
		changed.Admin = true
		changed.Scopes = nil
		changed.Disabled = true
		changed.CreatedAt = created.Add(time.Hour) // must be ignored
		if err := s.UpdateKey(ctx, changed); err != nil {
			t.Fatalf("UpdateKey: %v", err)
		}
		got, err := s.GetKey(ctx, "tk_reader")
		if err != nil {
			t.Fatalf("GetKey: %v", err)
		}
		want := changed
		want.CreatedAt = created
		if d := diffKey(got, want); d != "" {
			t.Error(d)
		}
	})

	t.Run("UpdateKey replaces scopes rather than merging them", func(t *testing.T) {
		s := newStore(t, []tunna.APIKey{reader})
		changed := reader
		changed.Scopes = map[string]tunna.Access{"photos": tunna.Write}
		if err := s.UpdateKey(ctx, changed); err != nil {
			t.Fatalf("UpdateKey: %v", err)
		}
		got, err := s.GetKey(ctx, "tk_reader")
		if err != nil {
			t.Fatalf("GetKey: %v", err)
		}
		if d := diffKey(got, changed); d != "" {
			t.Error(d)
		}
		if _, still := got.Scopes["*"]; still {
			t.Error("old wildcard scope survived the update; scopes must be replaced, not merged")
		}
	})

	t.Run("UpdateKey unknown id is ErrNotFound and creates nothing", func(t *testing.T) {
		s := newStore(t, []tunna.APIKey{alice})
		ghost := reader
		ghost.ID = "tk_ghost"
		if err := s.UpdateKey(ctx, ghost); !errors.Is(err, tunna.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if _, err := s.GetKey(ctx, "tk_ghost"); !errors.Is(err, tunna.ErrNotFound) {
			t.Errorf("UpdateKey of an unknown id created it")
		}
	})

	t.Run("DeleteKey removes the key; a second delete is ErrNotFound", func(t *testing.T) {
		s := newStore(t, []tunna.APIKey{alice, reader})
		if err := s.DeleteKey(ctx, "tk_reader"); err != nil {
			t.Fatalf("DeleteKey: %v", err)
		}
		if _, err := s.GetKey(ctx, "tk_reader"); !errors.Is(err, tunna.ErrNotFound) {
			t.Errorf("GetKey after delete: err = %v, want ErrNotFound", err)
		}
		if err := s.DeleteKey(ctx, "tk_reader"); !errors.Is(err, tunna.ErrNotFound) {
			t.Errorf("second DeleteKey: err = %v, want ErrNotFound", err)
		}
		keys, err := s.ListKeys(ctx)
		if err != nil {
			t.Fatalf("ListKeys: %v", err)
		}
		if len(keys) != 1 || keys[0].ID != "tk_alice" {
			t.Errorf("ListKeys after delete = %v, want only tk_alice", ids(keys))
		}
	})

	t.Run("a returned key is a copy: mutating its scopes does not change the store", func(t *testing.T) {
		s := newStore(t, []tunna.APIKey{reader})
		got, err := s.GetKey(ctx, "tk_reader")
		if err != nil {
			t.Fatalf("GetKey: %v", err)
		}
		got.Scopes["photos"] = tunna.Write
		again, err := s.GetKey(ctx, "tk_reader")
		if err != nil {
			t.Fatalf("GetKey: %v", err)
		}
		if again.Scopes["photos"] != tunna.Read {
			t.Error("mutating a returned key's Scopes map changed the stored key; the store must copy the map")
		}
	})
}

// diffKey reports the first field that differs, or "" when the keys match.
// APIKey holds a map, so == does not compile; Scopes compares nil and empty
// as equal, and CreatedAt uses Equal so location and monotonic bits do
// not matter.
func diffKey(got, want tunna.APIKey) string {
	switch {
	case got.ID != want.ID:
		return "ID = " + got.ID + ", want " + want.ID
	case got.Secret != want.Secret:
		return "Secret differs"
	case got.Name != want.Name:
		return "Name = " + got.Name + ", want " + want.Name
	case got.Admin != want.Admin:
		return "Admin differs"
	case got.Disabled != want.Disabled:
		return "Disabled differs"
	case !got.CreatedAt.Equal(want.CreatedAt):
		return "CreatedAt = " + got.CreatedAt.String() + ", want " + want.CreatedAt.String()
	case len(got.Scopes) != 0 || len(want.Scopes) != 0:
		if !reflect.DeepEqual(got.Scopes, want.Scopes) {
			return "Scopes differ"
		}
	}
	return ""
}

func ids(keys []tunna.APIKey) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k.ID)
	}
	return out
}
