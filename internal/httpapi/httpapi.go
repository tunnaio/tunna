// Package httpapi is the HTTP adapter: routes, the request pipeline, and the
// mapping from error kinds to the wire error body defined in spec/wire.md.
package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/tunnaio/tunna"
)

// New builds the HTTP handler for the server. serverVersion is reported by
// GET /-/version alongside the spec version.
func New(serverVersion string) http.Handler {
	mux := http.NewServeMux()
	h := &handler{serverVersion: serverVersion, mux: mux}

	mux.HandleFunc("GET /-/health", h.health)
	mux.HandleFunc("GET /-/version", h.version)
	mux.HandleFunc("/", h.notFound)

	return mux
}

// handler carries the dependencies the route methods need.
type handler struct {
	serverVersion string
	mux           *http.ServeMux
}

var probeMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost,
	http.MethodPut, http.MethodDelete, http.MethodOptions,
}

// writeJSON sends v as the JSON body with the given status. Headers must be
// set before this call; the first byte written freezes them.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError sends the wire error body (spec/wire.md section 9). Every error
// response goes through here so the shape cannot drift.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorBody{
		Error: errorDetail{
			Code:    code,
			Message: message,
		},
	})
}

func (h *handler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *handler) version(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"version": h.serverVersion,
		"spec":    tunna.SpecVersion,
	})
}

func (h *handler) notFound(w http.ResponseWriter, r *http.Request) {
	var allowed []string
	for _, m := range probeMethods {
		probe := r.Clone(r.Context())
		probe.Method = m
		if _, pattern := h.mux.Handler(probe); pattern != "" && pattern != "/" {
			allowed = append(allowed, m)
		}
	}

	if len(allowed) > 0 {
		w.Header().Set("Allow", strings.Join(allowed, ", "))
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed for this path")
		return
	}
	writeError(w, http.StatusNotFound, "unknown_route", "no such route")
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorBody struct {
	Error errorDetail `json:"error"`
}
