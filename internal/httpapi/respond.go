package httpapi

import (
	"encoding/json"
	"net/http"
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
