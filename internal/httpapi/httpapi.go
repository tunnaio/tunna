// Package httpapi is the HTTP adapter: routes, the request pipeline, and the
// mapping from error kinds to the wire error body defined in spec/wire.md.
package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/tunnaio/tunna"
	"github.com/tunnaio/tunna/sig"
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
	CORSOrigins   tunna.Origins // allowed browser origins; the zero value turns CORS off
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
	origins       tunna.Origins
}

// New builds the HTTP handler for the server: the routes, wrapped in the
// authentication stage, wrapped in CORS (stage 0, outermost, so preflights
// never meet authentication). Defaults from Options are applied here.
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
		origins:       o.CORSOrigins,
	}

	mux.HandleFunc("GET /-/health", h.health)
	mux.HandleFunc("GET /-/version", h.version)
	mux.HandleFunc("GET /-/limits", h.limits)

	// buckets routes
	mux.HandleFunc("GET /-/buckets", chain(h.listBuckets, requireAuth))
	mux.HandleFunc("GET /-/buckets/{bucket}", chain(h.getBucket, requireAuth, requireAccess(tunna.Read)))
	mux.HandleFunc("PUT /-/buckets/{bucket}", chain(h.createBucket, headerFormOnly, requireAuth, requireAdmin))
	mux.HandleFunc("DELETE /-/buckets/{bucket}", chain(h.deleteBucket, requireAuth, requireAdmin))
	mux.HandleFunc("PATCH /-/buckets/{bucket}", chain(h.patchBucket, headerFormOnly, requireAuth, requireAdmin))

	// uploads routes
	mux.HandleFunc("POST /-/uploads", chain(h.createUpload, headerFormOnly, requireAuth))
	mux.HandleFunc("PUT /-/uploads/{id}/parts/{n}", chain(h.putUploadPart, requireAuth))
	mux.HandleFunc("GET /-/uploads/{id}", chain(h.getUpload, requireAuth))
	mux.HandleFunc("DELETE /-/uploads/{id}", chain(h.deleteUpload, requireAuth))
	mux.HandleFunc("POST /-/uploads/{id}/complete", chain(h.completeUpload, requireAuth))

	// object routes
	mux.HandleFunc("PUT /{bucket}/{key...}", chain(h.putObject, reserved, requireAuth, requireAccess(tunna.Write)))
	mux.HandleFunc("GET /{bucket}/{key...}", chain(h.getObject, reserved))
	mux.HandleFunc("DELETE /{bucket}/{key...}", chain(h.deleteObject, reserved, requireAuth, requireAccess(tunna.Write)))
	mux.HandleFunc("GET /{bucket}", chain(h.listObjects, reserved))

	// key management
	mux.HandleFunc("GET /-/keys", chain(h.listKeys, requireAuth, requireAdmin))
	mux.HandleFunc("POST /-/keys", chain(h.createKey, headerFormOnly, requireAuth, requireAdmin))
	mux.HandleFunc("GET /-/keys/{id}", chain(h.getKey, requireAuth, requireAdmin))
	mux.HandleFunc("PATCH /-/keys/{id}", chain(h.patchKey, headerFormOnly, requireAuth, requireAdmin))
	mux.HandleFunc("DELETE /-/keys/{id}", chain(h.deleteKey, requireAuth, requireAdmin))
	mux.HandleFunc("POST /-/keys/{id}/rotate", chain(h.rotateKey, requireAuth, requireAdmin))
	mux.HandleFunc("GET /-/keys/self", chain(h.getSelfKey, requireAuth))

	mux.HandleFunc("/", h.notFound)

	return h.cors(h.authenticate(mux))
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

// headerFormOnly is a stage 2 rule for routes that take their parameters
// from a JSON body: a presigned URL binds method, path, query and headers
// but not the body, so it would authorize any body (ADR-0013). Listed first
// in a chain, so a scoped key is told this and not forbidden.
func headerFormOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has(sig.ParamSig) {
			writeAuthError(w, codePresignNotAllowed, "this route takes its parameters from the request body, which a presigned URL cannot bind; sign the request in header form", nil)
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

// limitsRecord is the wire shape of GET /-/limits (spec/wire.md 11.1):
// sizes in bytes, durations in seconds.
type limitsRecord struct {
	ObjectSizeMax             int64 `json:"object_size_max"`
	PartSizeMin               int64 `json:"part_size_min"`
	PartSizeMax               int64 `json:"part_size_max"`
	PartsMax                  int64 `json:"parts_max"`
	ListLimitMax              int64 `json:"list_limit_max"`
	KeyLengthMax              int64 `json:"key_length_max"`
	PresignLifetimeMaxSeconds int64 `json:"presign_lifetime_max_seconds"`
	UploadExpirySeconds       int64 `json:"upload_expiry_seconds"`
	ClockSkewSeconds          int64 `json:"clock_skew_seconds"`
}

// limits answers GET /-/limits. Anonymous. Every value is read from the
// variable that enforces it, never a second copy of the number.
func (h *handler) limits(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, limitsRecord{
		PartSizeMin:               h.partSizeMin,
		PartSizeMax:               h.partSizeMax,
		PartsMax:                  h.maxParts,
		PresignLifetimeMaxSeconds: int64(h.maxPresign.Seconds()),
		UploadExpirySeconds:       int64(h.uploadTTL.Seconds()),
		ClockSkewSeconds:          int64(h.skew.Seconds()),
		ListLimitMax:              maxListLimit,
		ObjectSizeMax:             maxBodyLength,
		KeyLengthMax:              tunna.MaxKeyLength,
	})
}

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
