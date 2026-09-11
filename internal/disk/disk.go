// Package disk is the blob store adapter (ADR-0007): one file per object
// version, named by a random id, under root/objects/<id[0:2]>/<id>. Ids are
// validated before they touch a path, so a key can never become a path.
package disk

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tunnaio/tunna"
)

// Store keeps blobs as files under root/objects/<id[0:2]>/<id> (ADR-0007).
// Store keeps blobs as files under a root directory.
type Store struct {
	root string
}

// New creates root/objects if needed and returns a store over it.
func New(root string) (*Store, error) {
	if err := os.MkdirAll(root+"/"+"objects", 0o755); err != nil {
		return nil, err
	}

	return &Store{
		root: root,
	}, nil
}

// path maps a validated id to its file. Every method goes through here.
func (s *Store) path(id string) (string, error) {
	if !validID(id) {
		return "", fmt.Errorf("%w: malformed blob id", tunna.ErrNotFound)
	}
	return filepath.Join(s.root, "objects", id[:2], id), nil
}

// validID reports whether id is exactly 32 lowercase hex characters, the
// only shape Create produces.
func validID(id string) bool {
	if len(id) != 32 {
		return false
	}

	for i := 0; i < len(id); i++ {
		if c := id[i]; !('a' <= c && c <= 'f') && !('0' <= c && c <= '9') {
			return false
		}
	}

	return true
}

// Create implements tunna.BlobStore: a fresh random id and an empty file
// under it. O_EXCL fails rather than reuses an existing file, which with
// 128-bit random ids should never happen.
func (s *Store) Create(_ context.Context) (string, error) {
	raw := make([]byte, 16)
	rand.Read(raw)
	id := hex.EncodeToString(raw)

	p, err := s.path(id)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	return id, f.Close()
}

// Write implements tunna.BlobStore: stream r into the blob at offset via an
// offset writer, so parts land at their place with no shared file position.
// A missing blob is ErrNotFound.
func (s *Store) Write(_ context.Context, id string, offset int64, r io.Reader) (int64, error) {
	p, err := s.path(id)
	if err != nil {
		return 0, nil
	}
	f, err := os.OpenFile(p, os.O_WRONLY, 0)
	if errors.Is(err, os.ErrNotExist) {
		return 0, tunna.ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return io.Copy(io.NewOffsetWriter(f, offset), r)
}

// Sync implements tunna.BlobStore: fsync the blob so its bytes survive a
// crash. Opened read-write because Windows refuses to flush a read-only
// handle.
func (s *Store) Sync(_ context.Context, id string) error {
	p, err := s.path(id)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return tunna.ErrNotFound
	}
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// Remove implements tunna.BlobStore. Removing an absent blob is not an
// error: delete and overwrite call this after the row has changed, and a
// retry must not fail because the first attempt got halfway.
func (s *Store) Remove(_ context.Context, id string) error {
	p, err := s.path(id)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Open implements tunna.BlobStore. The file supports Seek, which Range
// requests need. openShared is platform-specific: on Windows it must allow
// deletion while open (ADR-0007).
func (s *Store) Open(_ context.Context, id string) (io.ReadSeekCloser, error) {
	p, err := s.path(id)
	if err != nil {
		return nil, err
	}
	f, err := openShared(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, tunna.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return f, nil
}
