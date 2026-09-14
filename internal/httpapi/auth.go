package httpapi

import (
	"context"
	"crypto/hmac"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/tunnaio/tunna"
	"github.com/tunnaio/tunna/sig"
)

// credentials is what both forms of the scheme carry: which key, which
// headers were signed, and the signature itself.
type credentials struct {
	keyID     string
	signed    []string
	signature string
}

// callerKey is the context key under which authenticate stores the verified
// API key. An unexported struct type cannot collide with any other package's
// context keys, and the empty struct costs nothing.
type callerKey struct{}

// Every 401 carries the challenge HTTP requires; the value is the scheme and
// MAC name the client must use, owned by sig.
const (
	headerChallengeName  = "WWW-Authenticate"
	headerChallengeValue = sig.Algorithm
)

// authenticate is stage 2 of the pipeline (ADR-0004). It verifies any
// credentials the request carries, in header form or presigned form, and
// rejects bad ones with the codes from the error table. A request with no
// credentials passes through; whether a route requires them is decided later,
// by authorization. Failures that are syntax rather than authentication
// (both forms at once, an unparseable header or parameter) are stage 1 and
// answer 400 without a challenge.
func (h *handler) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := h.now()

		authorization, hasAuthorization := r.Header[http.CanonicalHeaderKey("Authorization")]
		hasPresignSig := r.URL.Query().Has(sig.ParamSig)
		var mode sig.Mode
		if hasAuthorization {
			mode = sig.Header
		} else {
			mode = sig.Presign
		}

		if !hasAuthorization && !hasPresignSig {
			next.ServeHTTP(w, r)
			return
		}

		if hasAuthorization && hasPresignSig {
			writeError(w, codeMalformedRequest, "request carries both an Authorization header and presigned query parameters; use one", nil)
			return
		}

		var timestamp int64
		var creds credentials
		if mode == sig.Header {
			ts, err := strconv.ParseInt(r.Header.Get("X-Tunna-Date"), 10, 64)
			if err != nil {
				writeError(w, codeMalformedRequest, "X-Tunna-Date must be Unix seconds", nil)
				return
			}

			c, success := parseAuthorization(authorization[0])
			if !success {
				writeError(w, codeMalformedRequest, "Authorization header is not of the form "+sig.Algorithm+" key=<id>, headers=<a;b>, sig=<hex>", nil)
				return
			}

			if d := time.Unix(ts, 0).Sub(now); d > h.skew || d < -h.skew {
				writeAuthError(w, codeClockSkew, "request timestamp outside the accepted window", map[string]any{
					"server_time":  now.Unix(),
					"skew_seconds": int64(h.skew.Seconds()),
				})
				return
			}

			timestamp = ts
			creds = c
		}

		if mode == sig.Presign {
			c, success := parsePresign(r.URL.Query())
			if !success {
				writeError(w, codeMalformedRequest, "presigned URL must carry "+sig.ParamKey+" and "+sig.ParamSig, nil)
				return
			}

			ts, err := strconv.ParseInt(r.URL.Query().Get(sig.ParamExpires), 10, 64)
			if err != nil {
				writeError(w, codeMalformedRequest, sig.ParamExpires+" must be Unix seconds", nil)
				return
			}
			if ts < now.Unix() {
				writeAuthError(w, codePresignExpired, "presigned URL has expired", map[string]any{
					"server_time": now.Unix(),
				})
				return
			}
			if ts-now.Unix() > int64(h.maxPresign.Seconds()) {
				writeAuthError(w, codePresignTooLong, "presigned URL expiry is further ahead than this server allows", map[string]any{
					"max_seconds": int64(h.maxPresign.Seconds()),
				})
				return
			}

			timestamp = ts
			creds = c
		}

		apikey, err := h.keys.GetKey(r.Context(), creds.keyID)
		switch {
		case errors.Is(err, tunna.ErrNotFound) || (err == nil && apikey.Disabled):
			writeAuthError(w, codeUnknownKey, "unknown or disabled key", nil)
			return
		case err != nil:
			writeError(w, codeInternal, "key lookup failed", nil)
			return
		}

		for _, name := range creds.signed {
			if _, found := r.Header[http.CanonicalHeaderKey(name)]; found {
				continue
			}

			writeAuthError(w, codeMissingSignedHeader, "signed header "+name+" is not present on the request", map[string]any{
				"header": name,
			})
			return
		}

		req, err := sigRequest(r, creds.signed)
		if err != nil {
			writeError(w, codeMalformedRequest, "request path or query cannot be decoded", nil)
			return
		}

		expected := sig.Signature(apikey.Secret, sig.Canonical(req, sig.Key{ID: apikey.ID, Secret: apikey.Secret}, timestamp, mode))
		if !hmac.Equal([]byte(expected), []byte(creds.signature)) {
			writeAuthError(w, codeBadSignature, "signature does not match", nil)
			return
		}

		r = r.WithContext(context.WithValue(r.Context(), callerKey{}, apikey))
		next.ServeHTTP(w, r)
	})
}

// caller returns the authenticated key, if stage 2 found one.
func caller(r *http.Request) (tunna.APIKey, bool) {
	k, ok := r.Context().Value(callerKey{}).(tunna.APIKey)
	return k, ok
}

// requireAuth is the first rule of stage 3 (ADR-0004): the wrapped route
// needs a verified caller. Routes not wrapped are anonymous by declaration.
func requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := caller(r); !ok {
			writeAuthError(w, codeUnauthenticated, "this route requires credentials", nil)
			return
		}

		next(w, r)
	}
}

// requireAccess is stage 3 for routes whose bucket is the {bucket} path
// segment: the caller must be allowed level on it (ADR-0008). It answers
// forbidden before any lookup, so the response is the same whether or not
// the bucket exists, and before stage-4 validation, so a scoped key never
// learns whether a name it cannot read is even valid. It sits inside
// requireAuth in every chain; on its own, an anonymous request would land
// on the zero key and be told forbidden rather than unauthenticated.
func requireAccess(level tunna.Access) middleware {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			bucket := r.PathValue("bucket")
			k, _ := caller(r)
			if !k.Allows(level, bucket) {
				writeError(w, codeForbidden, string(level)+" access to "+bucket+" is required", nil)
				return
			}

			next(w, r)
		}
	}
}

// requireAdmin is stage 3 for the control plane: bucket create and delete
// and every /-/keys route need an admin key (ADR-0008). Like requireAccess
// it sits inside requireAuth.
func requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		k, _ := caller(r)
		if !k.Admin {
			writeError(w, codeForbidden, "this route requires an admin key", nil)
			return
		}

		next(w, r)
	}
}

// parseAuthorization reads the header form from spec/wire.md 3.3:
// "TUNNA1-HMAC-SHA256 key=<id>, headers=<a;b>, sig=<hex>". Every field must
// appear once, key and sig must be non-empty, and nothing else is accepted.
func parseAuthorization(value string) (credentials, bool) {
	rest, ok := strings.CutPrefix(value, sig.Algorithm+" ")
	if !ok {
		return credentials{}, false
	}

	var c credentials
	seen := map[string]bool{}
	for _, field := range strings.Split(rest, ", ") {
		name, val, ok := strings.Cut(field, "=")
		if !ok || seen[name] {
			return credentials{}, false
		}
		seen[name] = true
		switch name {
		case "key":
			c.keyID = val
		case "headers":
			if val != "" {
				c.signed = strings.Split(val, ";")
			}
		case "sig":
			c.signature = val
		default:
			return credentials{}, false
		}
	}

	if c.keyID == "" || c.signature == "" {
		return credentials{}, false
	}
	return c, true
}

// parsePresign reads the presigned form from spec/wire.md 3.4 out of the
// query parameters. The expiry is parsed by the caller because its failure
// modes have their own error codes.
func parsePresign(query url.Values) (creds credentials, ok bool) {
	c := credentials{
		keyID:     query.Get(sig.ParamKey),
		signature: query.Get(sig.ParamSig),
	}

	if c.keyID == "" {
		return credentials{}, false
	}

	if c.signature == "" {
		return credentials{}, false
	}

	headers := query.Get(sig.ParamHeaders)
	if headers != "" {
		c.signed = strings.Split(headers, ";")
	}

	return c, true
}

// sigRequest rebuilds the signable view of an incoming request. It decodes
// from the raw forms on purpose: EscapedPath split per segment so an encoded
// slash inside a key stays inside its segment, and RawQuery with PathUnescape
// so a literal '+' stays a '+'. r.URL.Path and r.URL.Query() would both lose
// information the signature depends on. The signature parameter itself is
// excluded from the query, as the canonical form requires.
func sigRequest(r *http.Request, signed []string) (sig.Request, error) {
	raw := strings.TrimPrefix(r.URL.EscapedPath(), "/")
	var segments []string
	if raw != "" {
		for s := range strings.SplitSeq(raw, "/") {
			seg, err := url.PathUnescape(s)
			if err != nil {
				return sig.Request{}, err
			}
			segments = append(segments, seg)
		}
	}

	query := url.Values{}
	for pair := range strings.SplitSeq(r.URL.RawQuery, "&") {
		if pair == "" {
			continue
		}

		name, val, _ := strings.Cut(pair, "=")
		n, err1 := url.PathUnescape(name)
		v, err2 := url.PathUnescape(val)
		if err1 != nil || err2 != nil {
			return sig.Request{}, errors.Join(err1, err2)
		}
		if n == sig.ParamSig {
			continue
		}
		query.Add(n, v)
	}

	headers := make(map[string]string, len(r.Header))
	for name, values := range r.Header {
		headers[name] = strings.Join(values, ", ")
	}

	return sig.Request{
		Method:        r.Method,
		Path:          segments,
		Query:         query,
		Headers:       headers,
		SignedHeaders: signed,
	}, nil
}

// writeAuthError is writeError for stage 2: every 401 carries the
// WWW-Authenticate challenge HTTP requires. details may be nil.
func writeAuthError(w http.ResponseWriter, code code, message string, details map[string]any) {
	w.Header().Set(headerChallengeName, headerChallengeValue)
	writeError(w, code, message, details)
}
