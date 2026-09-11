// Package httpapi is the HTTP adapter: routes, the request pipeline, and the
// mapping from error kinds to the wire error body defined in spec/wire.md.
package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/tunnaio/tunna"
)

// Options configures the HTTP adapter. Zero values take the defaults noted
// on each field, so callers set only what they need.
type Options struct {
	ServerVersion string           // reported by GET /-/version
	Keys          tunna.KeyStore   // where stage 2 looks up API keys
	Now           func() time.Time // clock; nil means time.Now
	Skew          time.Duration    // accepted X-Tunna-Date drift; 0 means 15 minutes
	MaxPresign    time.Duration    // longest presigned lifetime; 0 means 7 days
}

// New builds the HTTP handler for the server: the routes, wrapped in the
// authentication stage. Defaults from Options are applied here.
func New(o Options) http.Handler {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Skew == 0 {
		o.Skew = 15 * time.Minute
	}
	if o.MaxPresign == 0 {
		o.MaxPresign = 7 * 24 * time.Hour
	}

	mux := http.NewServeMux()
	h := &handler{
		mux:           mux,
		serverVersion: o.ServerVersion,
		keys:          o.Keys,
		now:           o.Now,
		skew:          o.Skew,
		maxPresign:    o.MaxPresign,
	}

	mux.HandleFunc("GET /-/health", h.health)
	mux.HandleFunc("GET /-/version", h.version)
	mux.HandleFunc("/", h.notFound)

	return h.authenticate(mux)
}

// handler carries the dependencies the route methods need.
type handler struct {
	mux           *http.ServeMux
	serverVersion string
	keys          tunna.KeyStore
	now           func() time.Time
	skew          time.Duration
	maxPresign    time.Duration
}

// probeMethods is the fixed list notFound tries when deciding between "no
// such path" and "path exists, wrong method". A literal, never mutated.
var probeMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost,
	http.MethodPut, http.MethodDelete, http.MethodOptions,
}

// health answers GET /-/health. Anonymous.
func (h *handler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// version answers GET /-/version with the server build and the spec version
// it implements. Anonymous.
func (h *handler) version(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"version": h.serverVersion,
		"spec":    tunna.SpecVersion,
	})
}

// notFound is the catch-all. It distinguishes an unknown path from a known
// path with the wrong method by asking the mux what other methods would have
// matched, so the Allow header is accurate (spec/wire.md 4).
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
		writeError(w, codeMethodNotAllowed, "method not allowed for this path", nil)
		return
	}
	writeError(w, codeUnknownRoute, "no such route", nil)
}
