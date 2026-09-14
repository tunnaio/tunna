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
	ServerVersion string         // reported by GET /-/version
	Keys          tunna.KeyStore // where stage 2 looks up API keys
	Buckets       tunna.BucketStore
	Objects       tunna.ObjectStore
	Blobs         tunna.BlobStore
	Uploads       tunna.UploadStore
	Now           func() time.Time // clock; nil means time.Now
	Skew          time.Duration    // accepted X-Tunna-Date drift; 0 means 15 minutes
	MaxPresign    time.Duration    // longest presigned lifetime; 0 means 7 days
	PartSizeMin   int64
	PartSizeMax   int64
	MaxParts      int64
	UploadTTL     time.Duration
}

// handler carries the dependencies the route methods need.
type handler struct {
	mux           *http.ServeMux
	serverVersion string
	keys          tunna.KeyStore
	buckets       tunna.BucketStore
	objects       tunna.ObjectStore
	blobs         tunna.BlobStore
	uploads       tunna.UploadStore
	now           func() time.Time
	skew          time.Duration
	maxPresign    time.Duration
	partSizeMin   int64
	partSizeMax   int64
	maxParts      int64
	uploadTTL     time.Duration
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
	if o.PartSizeMin == 0 {
		o.PartSizeMin = 5 << 20
	}
	if o.PartSizeMax == 0 {
		o.PartSizeMax = 100 << 20
	}
	if o.MaxParts == 0 {
		o.MaxParts = 10_000
	}
	if o.UploadTTL == 0 {
		o.UploadTTL = 24 * time.Hour
	}

	mux := http.NewServeMux()
	h := &handler{
		mux:           mux,
		serverVersion: o.ServerVersion,
		keys:          o.Keys,
		buckets:       o.Buckets,
		objects:       o.Objects,
		blobs:         o.Blobs,
		uploads:       o.Uploads,
		now:           o.Now,
		skew:          o.Skew,
		maxPresign:    o.MaxPresign,
		partSizeMin:   o.PartSizeMin,
		partSizeMax:   o.PartSizeMax,
		maxParts:      o.MaxParts,
		uploadTTL:     o.UploadTTL,
	}

	mux.HandleFunc("GET /-/health", h.health)
	mux.HandleFunc("GET /-/version", h.version)

	// buckets routes
	mux.HandleFunc("GET /-/buckets", chain(h.listBuckets, requireAuth))
	mux.HandleFunc("GET /-/buckets/{bucket}", chain(h.getBucket, requireAuth, requireAccess(tunna.Read)))
	mux.HandleFunc("PUT /-/buckets/{bucket}", chain(h.createBucket, requireAuth, requireAdmin))
	mux.HandleFunc("DELETE /-/buckets/{bucket}", chain(h.deleteBucket, requireAuth, requireAdmin))

	// uploads routes
	mux.HandleFunc("POST /-/uploads", requireAuth(h.createUpload))
	mux.HandleFunc("PUT /-/uploads/{id}/parts/{n}", requireAuth(h.putUploadPart))
	mux.HandleFunc("GET /-/uploads/{id}", requireAuth(h.getUpload))
	mux.HandleFunc("DELETE /-/uploads/{id}", requireAuth(h.deleteUpload))
	mux.HandleFunc("POST /-/uploads/{id}/complete", requireAuth(h.completeUpload))

	// object routes
	mux.HandleFunc("PUT /{bucket}/{key...}", chain(h.putObject, reserved, requireAuth, requireAccess(tunna.Write)))
	mux.HandleFunc("GET /{bucket}/{key...}", chain(h.getObject, reserved))
	mux.HandleFunc("DELETE /{bucket}/{key...}", chain(h.deleteObject, reserved, requireAuth, requireAccess(tunna.Write)))
	mux.HandleFunc("GET /{bucket}", chain(h.listObjects, reserved))

	// key management
	mux.HandleFunc("GET /-/keys", chain(h.listKeys, requireAuth, requireAdmin))
	mux.HandleFunc("POST /-/keys", chain(h.createKey, requireAuth, requireAdmin))
	mux.HandleFunc("GET /-/keys/{id}", chain(h.getKey, requireAuth, requireAdmin))
	mux.HandleFunc("PATCH /-/keys/{id}", chain(h.patchKey, requireAuth, requireAdmin))
	mux.HandleFunc("DELETE /-/keys/{id}", chain(h.deleteKey, requireAuth, requireAdmin))
	mux.HandleFunc("POST /-/keys/{id}/rotate", chain(h.rotateKey, requireAuth, requireAdmin))

	mux.HandleFunc("/", h.notFound)

	return h.authenticate(mux)
}

// middleware is one rule of the ladder (ADR-0004) wrapped around a handler.
// requireAuth, requireAdmin and reserved have this shape as they are;
// requireAccess takes a level first and returns one.
type middleware func(http.HandlerFunc) http.HandlerFunc

// chain wraps h so that the first middleware listed runs first.
func chain(h http.HandlerFunc, ms ...middleware) http.HandlerFunc {
	for i := len(ms) - 1; i >= 0; i-- {
		h = ms[i](h)
	}
	return h
}

// reserved is stage 1's routing rule: "/-/" is the control plane, so an
// object path whose bucket segment is "-" is an unknown route, answered
// before authentication as the ladder requires.
// reserved is stage 1's routing rule: "/-/" is the control plane, so an
// object path whose bucket segment is "-" is an unknown route, answered
// before authentication as the ladder requires. The mux cannot express this
// itself: a "/-/" pattern and the method-specific object wildcards are
// neither more nor less specific than each other, and registering both
// panics.
func reserved(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("bucket") == "-" {
			writeError(w, codeUnknownRoute, "no such route", nil)
			return
		}
		next(w, r)
	}
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

// counts reports whether a matched pattern proves the request path exists.
// The catch-all proves nothing, and under the reserved prefix neither does
// the object wildcard: "-" is a legal bucket segment to the mux but not to us.
// counts reports whether a matched pattern proves the request path exists.
// The catch-all proves nothing, and under the reserved prefix neither does
// the object wildcard: "-" is a legal bucket segment to the mux but not to us.
func counts(path, pattern string) bool {
	if pattern == "" || pattern == "/" {
		return false
	}
	if strings.HasPrefix(path, "/-/") && strings.Contains(pattern, "{bucket}/{key") {
		return false
	}
	return true
}

// notFound is the catch-all. It distinguishes an unknown path from a known
// path with the wrong method by asking the mux what other methods would have
// matched, so the Allow header is accurate (spec/wire.md 4).
func (h *handler) notFound(w http.ResponseWriter, r *http.Request) {
	var allowed []string
	for _, m := range probeMethods {
		probe := r.Clone(r.Context())
		probe.Method = m
		if _, pattern := h.mux.Handler(probe); counts(r.URL.Path, pattern) {
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
