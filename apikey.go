package tunna

import "context"

// APIKey is a credential: a public id and a secret. The secret is stored in
// usable form because the server recomputes HMACs with it (ADR-0003).
type APIKey struct {
	ID, Secret string
	Disabled   bool
}

// KeyStore is what the core needs from wherever keys are kept. Declared here,
// by the consumer; adapters satisfy it without naming it (ADR-0005).
type KeyStore interface {
	// GetKey returns the key with the given id, disabled or not. Whether a
	// disabled key may authenticate is policy and belongs to the caller.
	// It returns ErrNotFound when no such key exists.
	GetKey(ctx context.Context, id string) (APIKey, error)
}
