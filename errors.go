package tunna

import "errors"

// ErrNotFound is returned by stores when the requested item does not exist.
// Adapters map it to the wire error for whatever was looked up: unknown_key,
// bucket_not_found, and so on. Compare with errors.Is.
var ErrNotFound = errors.New("not found")
