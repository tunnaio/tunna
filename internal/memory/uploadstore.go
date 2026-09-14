package memory

import (
	"context"
	"maps"
	"sync"

	"github.com/tunnaio/tunna"
)

// UploadStore keeps upload sessions in a map and completes into the
// ObjectStore it was built with. Safe for concurrent use. Metadata and
// Parts maps are copied on the way in and out. CompleteUpload is not atomic
// across the two maps; the SQLite adapter is, and nothing in one process
// observes the gap.
type UploadStore struct {
	mu       sync.RWMutex
	sessions map[string]tunna.UploadSession
	objects  tunna.ObjectStore
}

// NewUploadStore returns a store holding exactly the given sessions,
// completing into objects. nil is a valid, empty seed.
func NewUploadStore(sessions []tunna.UploadSession, objectStore tunna.ObjectStore) *UploadStore {
	s := &UploadStore{
		sessions: make(map[string]tunna.UploadSession, len(sessions)),
		objects:  objectStore,
	}
	for _, sess := range sessions {
		sess.Metadata = maps.Clone(sess.Metadata)
		sess.Parts = maps.Clone(sess.Parts)
		s.sessions[sess.ID] = sess
	}

	return s
}

// CreateUpload implements tunna.UploadStore. A stored session always has a
// non-nil Parts map, so PutPart can write into it.
func (s *UploadStore) CreateUpload(_ context.Context, se tunna.UploadSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.sessions[se.ID]; exists {
		return tunna.ErrConflict
	}
	se.Parts = maps.Clone(se.Parts)
	if se.Parts == nil {
		se.Parts = make(map[int]tunna.Part)
	}
	se.Metadata = maps.Clone(se.Metadata)
	s.sessions[se.ID] = se
	return nil
}

// GetUpload returns the session, or ErrNotFound.
func (s *UploadStore) GetUpload(_ context.Context, id string) (tunna.UploadSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	se, exists := s.sessions[id]
	if !exists {
		return tunna.UploadSession{}, tunna.ErrNotFound
	}
	se.Parts = maps.Clone(se.Parts)
	se.Metadata = maps.Clone(se.Metadata)
	return se, nil
}

// PutPart records part n, replacing any earlier record of it. ErrNotFound
// if the session does not exist.
func (s *UploadStore) PutPart(_ context.Context, id string, n int, p tunna.Part) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	se, exists := s.sessions[id]
	if !exists {
		return tunna.ErrNotFound
	}
	se.Parts[n] = p
	return nil
}

func (s *UploadStore) CompleteUpload(ctx context.Context, id string, o tunna.Object) (previousBlobID string, err error) {
	s.mu.RLock()
	_, exists := s.sessions[id]
	s.mu.RUnlock()
	if !exists {
		return "", tunna.ErrNotFound
	}
	prevID, err := s.objects.PutObject(ctx, o)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
	return prevID, nil
}

// DeleteUpload removes the session and returns it, so the caller can
// remove its blob. ErrNotFound if absent.
func (s *UploadStore) DeleteUpload(_ context.Context, id string) (tunna.UploadSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	se, exists := s.sessions[id]
	if !exists {
		return tunna.UploadSession{}, tunna.ErrNotFound
	}
	delete(s.sessions, id)
	se.Parts = maps.Clone(se.Parts)
	se.Metadata = maps.Clone(se.Metadata)
	return se, nil
}

// CountUploads reports how many sessions a bucket has, for bucket_not_empty.
func (s *UploadStore) CountUploads(_ context.Context, bucket string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	count := 0
	for _, se := range s.sessions {
		if se.Bucket != bucket {
			continue
		}
		count++
	}
	return count, nil
}

var _ tunna.UploadStore = (*UploadStore)(nil)
