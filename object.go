package tunna

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// Object is the metadata of one stored object. The bytes live in a blob
// named by BlobID (ADR-0007); Checksum is the CRC32C wire form and the ETag
// (ADR-0006). Metadata holds user headers without the X-Tunna-Meta- prefix.
type Object struct {
	Bucket      string
	Key         string
	BlobID      string
	Size        int64
	ContentType string
	Checksum    string
	Metadata    map[string]string
	CreatedAt   time.Time
}

// MaxKeyLength is the longest object key, in bytes (spec/wire.md 1). Part
// of the contract, not configuration.
const MaxKeyLength = 1024

// ValidateObjectKey applies the key rule from spec/wire.md section 1: 1 to
// 1024 bytes of valid UTF-8 with no NUL. The returned error wraps ErrInvalid.
func ValidateObjectKey(key string) error {
	if len(key) < 1 || len(key) > MaxKeyLength {
		return fmt.Errorf("%w: object key must be 1 to %d bytes, got %d", ErrInvalid, MaxKeyLength, len(key))
	}

	if valid := utf8.ValidString(key); !valid {
		return fmt.Errorf("%w: object key must be valid UTF-8", ErrInvalid)
	}

	if strings.IndexByte(key, 0) >= 0 {
		return fmt.Errorf("%w: object key must not contain NUL (U+0000)", ErrInvalid)
	}

	return nil
}

// ObjectStore is what the core needs from wherever object metadata is kept.
// It never touches bytes; the blob ids it returns let the caller remove files
// after the row change has committed, which keeps overwrite and delete
// crash-safe by ordering.
type ObjectStore interface {
	// PutObject inserts or replaces the row for (bucket, key). It returns
	// the previous BlobID, or "" if there was none, so the caller can
	// remove the old file after the row commits (ADR-0007, overwrite).
	PutObject(ctx context.Context, o Object) (previousBlobID string, err error)
	// GetObject returns the row, or ErrNotFound.
	GetObject(ctx context.Context, bucket, key string) (Object, error)
	// DeleteObject removes the row and returns its BlobID for the caller to
	// remove the file, or ErrNotFound.
	DeleteObject(ctx context.Context, bucket, key string) (blobID string, err error)
	// ListObjects returns up to limit objects in bucket whose key starts
	// with prefix and sorts after `after`, in key order.
	ListObjects(ctx context.Context, bucket, prefix, after string, limit int) ([]Object, error)
}

// BlobStore is what the core needs from wherever bytes are kept: create a
// blob, write into it at an offset, make it durable, open it for reading,
// remove it. Ids are opaque; the caller records them in ObjectStore.
type BlobStore interface {
	// Create makes a new empty blob and returns its id.
	Create(ctx context.Context) (id string, err error)
	// Write streams r into the blob at offset, returning bytes written.
	// Callers compute the checksum; the blob store moves bytes.
	Write(ctx context.Context, id string, offset int64, r io.Reader) (int64, error)
	// Sync makes the blob's bytes durable.
	Sync(ctx context.Context, id string) error
	// Open returns the blob for reading; the caller closes it.
	Open(ctx context.Context, id string) (io.ReadSeekCloser, error)
	// Remove deletes the blob. Removing an absent id is not an error.
	Remove(ctx context.Context, id string) error
}
