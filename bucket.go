package tunna

import (
	"context"
	"fmt"
	"time"
)

// Bucket is a namespace for objects
type Bucket struct {
	Name      string
	Public    bool
	CreatedAt time.Time
}

func ValidateBucketName(name string) error {
	if len(name) < 3 || len(name) > 63 {
		return fmt.Errorf("%w: bucket name must be 3 to 63 bytes, got %d", ErrInvalid, len(name))
	}

	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case 'a' <= c && c <= 'z', '0' <= c && c <= '9':
			// allowed anywhere
			continue
		case c == '-':
			if i == 0 || i == len(name)-1 {
				return fmt.Errorf("%w: bucket name must begin and end with a letter or digit", ErrInvalid)
			}
		default:
			return fmt.Errorf("%w: bucket name may contain only a-z, 0-9 and '-', found %q at byte %d", ErrInvalid, c, i)
		}
	}

	return nil
}

// BucketStore is what the core needs from wherever buckets are kept. Declared
// here, by the consumer; adapters satisfy it without naming it (ADR-0005).
type BucketStore interface {
	// CreateBucket stores a new bucket with every field as given, CreatedAt
	// included. It returns ErrConflict when the name is taken, and the
	// stored bucket is left as it was.
	CreateBucket(ctx context.Context, b Bucket) error

	// GetBucket returns the bucket with the given name. It returns
	// ErrNotFound and a zero Bucket when there is none.
	GetBucket(ctx context.Context, name string) (Bucket, error)

	// ListBuckets returns every bucket, ordered by name, as a slice the
	// caller may modify. An empty store gives an empty list, not an error.
	ListBuckets(ctx context.Context) ([]Bucket, error)

	// DeleteBucket removes the bucket. It returns ErrNotFound when there is
	// none. Whether the bucket must be empty first is the caller's rule.
	DeleteBucket(ctx context.Context, name string) error

	// UpdateBucket replaces Public on the bucket with b's name. CreatedAt is
	// kept from the stored record. It returns ErrNotFound when there is no
	// such bucket, and creates nothing.
	UpdateBucket(ctx context.Context, b Bucket) error
}
