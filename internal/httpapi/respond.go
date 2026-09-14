package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/tunnaio/tunna"
)

// writeJSON sends v as the JSON body with the given status. Headers must be
// set before this call; the first byte written freezes them.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError sends the wire error body (spec/wire.md section 9). Every error
// response goes through here so the shape cannot drift. details may be nil;
// omitempty keeps it out of the body when it is. A code missing from statusOf
// is a programming error and panics by name.
func writeError(w http.ResponseWriter, c code, message string, details map[string]any) {
	status, ok := statusOf[c]
	if !ok {
		panic("httpapi: error code " + string(c) + " is not in statusOf")
	}
	writeJSON(w, status, errorBody{
		Error: errorDetail{
			Code:    c,
			Message: message,
			Details: details,
		},
	})
}

// readableBucket is stages 2 and 3 plus the bucket lookup for the read
// routes, where a public bucket needs no caller (spec/wire.md 5.2 and 7,
// ADR-0008). A wrapper cannot decide this because "public" is only known
// from the row. The answers, after one lookup:
//
//   - found and public: anyone may read.
//   - caller allowed read on the name: the bucket, or bucket_not_found;
//     such a caller may know whether it exists.
//   - caller not allowed: forbidden, found or not, so nothing is disclosed.
//   - no caller: unauthenticated when found, bucket_not_found otherwise.
//
// It writes the error and reports false, so a handler just returns.
func (h *handler) readableBucket(w http.ResponseWriter, r *http.Request, name string) (tunna.Bucket, bool) {
	k, authed := caller(r)
	b, err := h.buckets.GetBucket(r.Context(), name)
	if err != nil && !errors.Is(err, tunna.ErrNotFound) {
		writeError(w, codeInternal, "bucket lookup failed", nil)
		return tunna.Bucket{}, false
	}
	found := err == nil
	switch {
	case found && b.Public:
		return b, true
	case authed && k.Allows(tunna.Read, name):
		// allowed to know wether it exists
	case authed:
		writeError(w, codeForbidden, "read access to "+name+" is required", nil)
		return tunna.Bucket{}, false
	case found:
		writeAuthError(w, codeUnauthenticated, "reading from a private bucket requires credentials", nil)
		return tunna.Bucket{}, false
	}
	if !found {
		writeError(w, codeBucketNotFound, "no bucket named "+name, map[string]any{"bucket": name})
		return tunna.Bucket{}, false
	}
	return b, true
}
