package tunna

import (
	"context"
	"time"
)

// UploadSession is a multipart upload in progress (ADR-0001): a file created
// at initiate that parts write into at offset, plus what the server has
// recorded about each part so complete can verify lengths and combine
// checksums (ADR-0006) without rereading the file.
type UploadSession struct {
	ID          string
	Bucket      string
	Key         string
	BlobID      string // the file parts write into, created at initiate
	PartSize    int64
	ContentType string
	Metadata    map[string]string
	Parts       map[int]Part
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// Part is what the server recorded about one received part.
type Part struct {
	Size     int64  `json:"size"`
	Checksum string `json:"checksum"` // wire form
}

// UploadStore is what the core needs from wherever sessions are kept.
// CompleteUpload spans sessions and objects and is one method because it
// must be one transaction; the store owns that, not the caller.
type UploadStore interface {
	// CreateUpload stores a new session. ErrConflict if the id is taken.
	CreateUpload(ctx context.Context, s UploadSession) error

	// GetUpload returns the session, or ErrNotFound.
	GetUpload(ctx context.Context, id string) (UploadSession, error)

	// PutPart records part n, replacing any earlier record of it. ErrNotFound
	// if the session does not exist.
	PutPart(ctx context.Context, id string, n int, p Part) error

	// CompleteUpload records o as the object and deletes the session, in one
	// transaction. Returns the blob id of an object it replaced, or "".
	// ErrNotFound if the session does not exist.
	CompleteUpload(ctx context.Context, id string, o Object) (previousBlobID string, err error)

	// DeleteUpload removes the session and returns it, so the caller can
	// remove its blob. ErrNotFound if absent.
	DeleteUpload(ctx context.Context, id string) (UploadSession, error)

	// CountUploads reports how many sessions a bucket has, for bucket_not_empty.
	CountUploads(ctx context.Context, bucket string) (int, error)
}
