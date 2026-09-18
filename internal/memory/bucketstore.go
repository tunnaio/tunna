package memory

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/tunnaio/tunna"
)

// BucketStore keeps buckets in a map. Safe for concurrent use.
type BucketStore struct {
	mu      sync.RWMutex
	buckets map[string]tunna.Bucket
}

// NewBucketStore returns a store holding exactly the given buckets. nil is a
// valid, empty seed.
func NewBucketStore(buckets []tunna.Bucket) *BucketStore {
	s := &BucketStore{buckets: make(map[string]tunna.Bucket, len(buckets))}
	for _, b := range buckets {
		s.buckets[b.Name] = b
	}

	return s
}

// GetBucket implements tunna.BucketStore.
func (s *BucketStore) GetBucket(_ context.Context, name string) (tunna.Bucket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, exists := s.buckets[name]
	if !exists {
		return tunna.Bucket{}, tunna.ErrNotFound
	}

	return b, nil
}

// CreateBucket implements tunna.BucketStore. The existence check and the
// insert happen under one write lock, so concurrent creates of the same name
// cannot both succeed.
func (s *BucketStore) CreateBucket(_ context.Context, b tunna.Bucket) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.buckets[b.Name]; exists {
		return tunna.ErrConflict
	}
	s.buckets[b.Name] = b
	return nil
}

// ListBuckets implements tunna.BucketStore. It returns a fresh slice sorted
// by name; map iteration order is random, so the sort is not optional.
func (s *BucketStore) ListBuckets(_ context.Context) ([]tunna.Bucket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]tunna.Bucket, 0, len(s.buckets))
	for _, b := range s.buckets {
		list = append(list, b)
	}
	slices.SortFunc(list, func(a, b tunna.Bucket) int {
		return strings.Compare(a.Name, b.Name)
	})
	return list, nil
}

// DeleteBucket implements tunna.BucketStore.
func (s *BucketStore) DeleteBucket(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.buckets[name]; !exists {
		return tunna.ErrNotFound
	}
	delete(s.buckets, name)
	return nil
}

// UpdateBucket implements tunna.BucketStore.
func (s *BucketStore) UpdateBucket(_ context.Context, b tunna.Bucket) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, exists := s.buckets[b.Name]
	if !exists {
		return tunna.ErrNotFound
	}
	old.Public = b.Public
	s.buckets[b.Name] = old
	return nil
}

// Compile-time check that *BucketStore satisfies the interface.
var _ tunna.BucketStore = (*BucketStore)(nil)
