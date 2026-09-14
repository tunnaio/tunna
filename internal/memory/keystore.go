// Package memory holds in-memory implementations of the root package store
// interfaces, for tests and for the conformance runner.
package memory

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/tunnaio/tunna"
)

// KeyStore keeps API keys in a map. Safe for concurrent use.
type KeyStore struct {
	mu   sync.RWMutex
	keys map[string]tunna.APIKey
}

// NewKeyStore returns a store holding exactly the given keys.
func NewKeyStore(keys []tunna.APIKey) *KeyStore {
	s := &KeyStore{keys: make(map[string]tunna.APIKey, len(keys))}
	for _, k := range keys {
		s.keys[k.ID] = clone(k)
	}

	return s
}

// GetKey implements tunna.KeyStore.
func (s *KeyStore) GetKey(_ context.Context, id string) (tunna.APIKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	k, ok := s.keys[id]
	if !ok {
		return tunna.APIKey{}, tunna.ErrNotFound
	}

	return clone(k), nil
}

// CreateKey implements tunna.KeyStore.
func (s *KeyStore) CreateKey(_ context.Context, k tunna.APIKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.keys[k.ID]; exists {
		return tunna.ErrConflict
	}
	s.keys[k.ID] = clone(k)

	return nil
}

// ListKeys implements tunna.KeyStore. The result is ordered by id and is
// never nil, so it encodes as [] rather than null.
func (s *KeyStore) ListKeys(_ context.Context) ([]tunna.APIKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]tunna.APIKey, 0, len(s.keys))
	for _, k := range s.keys {
		out = append(out, clone(k))
	}
	slices.SortFunc(out, func(a, b tunna.APIKey) int { return strings.Compare(a.ID, b.ID) })

	return out, nil
}

// UpdateKey implements tunna.KeyStore. Scopes are replaced, not merged;
// CreatedAt is kept from the stored record.
func (s *KeyStore) UpdateKey(_ context.Context, k tunna.APIKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, ok := s.keys[k.ID]
	if !ok {
		return tunna.ErrNotFound
	}
	k.CreatedAt = stored.CreatedAt
	s.keys[k.ID] = clone(k)

	return nil
}

// DeleteKey implements tunna.KeyStore.
func (s *KeyStore) DeleteKey(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.keys[id]; !ok {
		return tunna.ErrNotFound
	}
	delete(s.keys, id)

	return nil
}

// clone returns a copy whose Scopes map is not shared with the input, so
// neither the caller nor the store can change the other's record. A nil
// map stays nil.
func clone(k tunna.APIKey) tunna.APIKey {
	if k.Scopes != nil {
		k.Scopes = maps.Clone(k.Scopes)
	}

	return k
}

// Compile-time check that *KeyStore satisfies the interface.
var _ tunna.KeyStore = (*KeyStore)(nil)
