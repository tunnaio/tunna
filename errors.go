package tunna

import "errors"

// ErrNotFound is returned by stores when the requested item does not exist.
// Adapters map it to the wire error for whatever was looked up: unknown_key,
// bucket_not_found, and so on. Compare with errors.Is.
var ErrNotFound = errors.New("not found")

// ErrInvalid means the request's own content breaks a rule: a bad name,
// a size outside limits. Stage 4 in the ladder.
var ErrInvalid = errors.New("invalid")

// ErrConflict means the request is well-formed but the world's state
// refuses it: already exists, not empty. Stage 6.
var ErrConflict = errors.New("conflict")
