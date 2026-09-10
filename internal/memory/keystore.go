// Package memory holds in-memory implementations of the root package store
// interfaces, for tests and for the conformance runner.
package memory

import (
	"context"
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
		s.keys[k.ID] = k
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

	return k, nil
}

// Compile-time check that *KeyStore satisfies the interface.
var _ tunna.KeyStore = (*KeyStore)(nil)
