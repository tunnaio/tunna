package tunna

import (
	"context"
	"time"
)

// Access is what a scoped key may do in a bucket (ADR-0008). Write
// includes read; there is no level above write, since bucket and key
// management belong to admin keys rather than to a scope.
type Access string

// The two access levels. Their string forms are the wire values.
const (
	Read  Access = "read"
	Write Access = "write"
)

// APIKey is a credential: a public id and a secret. The secret is stored in
// usable form because the server recomputes HMACs with it (ADR-0003).
type APIKey struct {
	ID        string
	Secret    string
	Name      string
	Admin     bool
	Scopes    map[string]Access
	Disabled  bool
	CreatedAt time.Time
}

// Valid reports whether a is one of the defined levels, exactly and
// case-sensitively. It is the stage-4 check for a scope value off the wire.
func (a Access) Valid() bool {
	return a == Read || a == Write
}

// covers reports whether a level grants want: the same level, or write
// asked for read. The zero value covers nothing, which is what a missing
// scope entry looks like.
func (a Access) covers(want Access) bool {
	if a == want {
		return true
	}
	return a == Write && want == Read
}

// Allows is the stage-3 rule (ADR-0004, ADR-0008): may this key do level in
// bucket? An admin key may do anything. Otherwise the bucket's own scope
// entry or the "*" entry must cover the level; both grant, neither takes
// away. Bucket names match exactly, and "*" is a wildcard only as the whole
// scope key. spec/vectors/authorization.json is the definition.
func (k APIKey) Allows(level Access, bucket string) bool {
	if k.Admin {
		return true
	}
	return k.Scopes[bucket].covers(level) || k.Scopes["*"].covers(level)
}

// KeyStore is what the core needs from wherever keys are kept. Declared here,
// by the consumer; adapters satisfy it without naming it (ADR-0005).
type KeyStore interface {
	// GetKey returns the key with the given id, disabled or not. Whether a
	// disabled key may authenticate is policy and belongs to the caller.
	// It returns ErrNotFound when no such key exists.
	GetKey(ctx context.Context, id string) (APIKey, error)

	// CreateKey stores a new key with every field as given, CreatedAt
	// included. It returns ErrConflict when the id is taken.
	CreateKey(ctx context.Context, k APIKey) error

	// UpdateKey replaces Secret, Name, Admin, Scopes and Disabled of the key
	// with k's id. Scopes are replaced, not merged. CreatedAt is kept from
	// the stored record. It returns ErrNotFound when no such key exists.
	UpdateKey(ctx context.Context, k APIKey) error

	// DeleteKey removes the key. It returns ErrNotFound when no such key
	// exists, so a second delete is distinguishable from the first.
	DeleteKey(ctx context.Context, id string) error

	// ListKeys returns every key, secrets included, ordered by id. The
	// caller decides what to show; the store does not redact.
	ListKeys(ctx context.Context) ([]APIKey, error)
}
