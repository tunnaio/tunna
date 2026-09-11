package memory

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/tunnaio/tunna"
)

// ObjectStore keeps object metadata in a map per bucket. Safe for concurrent
// use. Metadata maps are copied on the way in and out.
type ObjectStore struct {
	mu      sync.RWMutex
	objects map[string]map[string]tunna.Object
}

// NewObjectStore returns a store holding exactly the given objects. nil is a
// valid, empty seed.
func NewObjectStore(objects []tunna.Object) *ObjectStore {
	s := &ObjectStore{objects: make(map[string]map[string]tunna.Object)}
	for _, o := range objects {
		inner, ok := s.objects[o.Bucket]
		if !ok {
			inner = map[string]tunna.Object{}
			s.objects[o.Bucket] = inner
		}
		o.Metadata = maps.Clone(o.Metadata)
		inner[o.Key] = o
	}

	return s
}

// PutObject implements tunna.ObjectStore.
func (s *ObjectStore) PutObject(_ context.Context, o tunna.Object) (previousBlobID string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.objects[o.Bucket]
	if !ok {
		b = map[string]tunna.Object{}
		s.objects[o.Bucket] = b
	}
	prev := b[o.Key].BlobID
	o.Metadata = maps.Clone(o.Metadata)
	b[o.Key] = o
	return prev, nil
}

// GetObject implements tunna.ObjectStore.
func (s *ObjectStore) GetObject(_ context.Context, bucket, key string) (tunna.Object, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.objects[bucket][key]
	if !ok {
		return tunna.Object{}, tunna.ErrNotFound
	}
	o.Metadata = maps.Clone(o.Metadata)
	return o, nil
}

// DeleteObject implements tunna.ObjectStore.
func (s *ObjectStore) DeleteObject(_ context.Context, bucket, key string) (blobID string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, exists := s.objects[bucket][key]
	if !exists {
		return "", tunna.ErrNotFound
	}
	delete(s.objects[bucket], key)
	if len(s.objects[bucket]) == 0 {
		delete(s.objects, bucket)
	}
	return o.BlobID, nil
}

// ListObjects implements tunna.ObjectStore. Matches are collected under the
// read lock and sorted outside it, so a narrow prefix on a large bucket
// sorts only what it returns.
func (s *ObjectStore) ListObjects(_ context.Context, bucket, prefix, after string, limit int) ([]tunna.Object, error) {
	s.mu.RLock()
	var matched []tunna.Object
	for key, o := range s.objects[bucket] {
		if !strings.HasPrefix(key, prefix) || key <= after {
			continue
		}
		matched = append(matched, o)
	}
	s.mu.RUnlock()
	slices.SortFunc(matched, func(a, b tunna.Object) int {
		return strings.Compare(a.Key, b.Key)
	})
	if limit > 0 && len(matched) > limit {
		matched = matched[:limit]
	}
	for i := range matched {
		matched[i].Metadata = maps.Clone(matched[i].Metadata)
	}

	return matched, nil
}

var _ tunna.ObjectStore = (*ObjectStore)(nil)
