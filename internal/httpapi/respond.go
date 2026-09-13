package httpapi

import (
	"encoding/json"
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

// allowRead is the read rule for buckets (spec/wire.md 5.2 and 7): a public
// bucket needs no caller, any other needs one. It writes the unauthenticated
// error itself and reports false, so a handler just returns. Decided here
// rather than by requireAuth because the answer depends on the bucket.
func (h *handler) allowRead(w http.ResponseWriter, r *http.Request, b tunna.Bucket) bool {
	if !b.Public {
		_, ok := r.Context().Value(callerKey{}).(tunna.APIKey)
		if !ok {
			writeAuthError(w, codeUnauthenticated, "reading from a private bucket requires credentials", nil)
			return false
		}
	}
	return true
}
