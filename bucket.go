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

type BucketStore interface {
	CreateBucket(ctx context.Context, b Bucket) error

	GetBucket(ctx context.Context, name string) (Bucket, error)

	ListBuckets(ctx context.Context) ([]Bucket, error)

	DeleteBucket(ctx context.Context, name string) error
}
