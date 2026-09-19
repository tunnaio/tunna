package httpapi

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/tunnaio/tunna"
)

// keyRecord is the wire shape of a key (spec/wire.md 4.2). Scopes is omitted
// when empty, which is the admin case; Secret is omitted unless a handler
// sets it, which only create and rotate do.
type keyRecord struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Admin     bool              `json:"admin"`
	Scopes    map[string]string `json:"scopes,omitempty"`
	Disabled  bool              `json:"disabled"`
	CreatedAt int64             `json:"created_at"`
	Secret    string            `json:"secret,omitempty"`
}

// toKeyRecord maps a key to the wire without its secret.
func toKeyRecord(k tunna.APIKey) keyRecord {
	return keyRecord{
		ID:        k.ID,
		Name:      k.Name,
		Admin:     k.Admin,
		Scopes:    fromScopes(k.Scopes),
		Disabled:  k.Disabled,
		CreatedAt: k.CreatedAt.Unix(),
	}
}

// toScopes converts a request's scopes to the domain map; nil for none, so
// an admin key stores no map. Go does not convert map types even when the
// element types convert, hence the loop.
func toScopes(in map[string]string) map[string]tunna.Access {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]tunna.Access, len(in))
	for bucket, level := range in {
		out[bucket] = tunna.Access(level)
	}
	return out
}

// fromScopes is the inverse of toScopes, for the wire record.
func fromScopes(in map[string]tunna.Access) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for bucket, level := range in {
		out[bucket] = string(level)
	}
	return out
}

// validateKeyShape is stage 4 for create and patch (spec/wire.md 4.2): name
// 1 to 128 bytes; admin and scopes exclusive, one of them required; every
// scope key a bucket name or "*", every value a defined level. Patch runs
// it on the merged record, so a key is never stored in a shape the rules
// reject. An empty field means valid.
func validateKeyShape(name string, admin bool, scopes map[string]tunna.Access) (field, message string) {
	if n := len(name); n < 1 || n > 128 {
		return "name", "name must be 1 to 128 bytes"
	}
	if admin && len(scopes) > 0 {
		return "scopes", "an admin key has no scopes; send admin without scopes"
	}
	if !admin && len(scopes) == 0 {
		return "scopes", "a key that is not admin needs at least one scope"
	}
	for b, a := range scopes {
		if b != "*" {
			if err := tunna.ValidateBucketName(b); err != nil {
				return "scopes", "scope " + b + ": " + err.Error()
			}
		}
		if !tunna.Access(a).Valid() {
			return "scopes", "scope " + b + ": level must be " + string(tunna.Read) + " or " + string(tunna.Write)
		}
	}
	return "", ""
}

// newKeyID returns "tk_" plus 16 hex characters: 8 bytes from the operating
// system's random source (ADR-0008). An id is public and only needs to be
// unique, so it is short.
func newKeyID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "tk_" + hex.EncodeToString(b[:]), nil
}

// newSecret returns 43 unpadded base64url characters: 32 random bytes, the
// HMAC-SHA256 key size (ADR-0003). The URL-safe alphabet keeps it clean in
// headers and shells.
func newSecret() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// createKey answers POST /-/keys: validate, mint id and secret, store, 201
// with the record and the secret, the first of its two appearances. A
// conflict from the store means an 8-byte random id collided, which is
// internal, not the client's doing.
func (h *handler) createKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name   string            `json:"name"`
		Admin  bool              `json:"admin"`
		Scopes map[string]string `json:"scopes"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		details := map[string]any{}
		if typeErr, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
			details["name"] = typeErr.Field
		}
		writeError(w, codeInvalidParameter, "body must be a JSON object", details)
		return
	}
	if field, msg := validateKeyShape(body.Name, body.Admin, toScopes(body.Scopes)); field != "" {
		writeError(w, codeInvalidParameter, msg, map[string]any{"name": field})
		return
	}

	id, err := newKeyID()
	if err != nil {
		writeError(w, codeInternal, "key id generation failed", nil)
		return
	}
	secret, err := newSecret()
	if err != nil {
		writeError(w, codeInternal, "secret generation failed", nil)
		return
	}
	k := tunna.APIKey{
		ID:        id,
		Secret:    secret,
		Name:      body.Name,
		Admin:     body.Admin,
		Scopes:    toScopes(body.Scopes),
		CreatedAt: h.now(),
	}
	if err = h.keys.CreateKey(r.Context(), k); err != nil {
		writeError(w, codeInternal, "key creation failed", nil)
		return
	}
	rec := toKeyRecord(k)
	rec.Secret = secret
	writeJSON(w, http.StatusCreated, rec)
}

// getKey answers GET /-/keys/{id} with the record, never the secret.
func (h *handler) getKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	k, err := h.keys.GetKey(r.Context(), id)
	if errors.Is(err, tunna.ErrNotFound) {
		writeError(w, codeKeyNotFound, "no key with id="+id, nil)
		return
	}
	if err != nil {
		writeError(w, codeInternal, "key lookup failed", nil)
		return
	}
	writeJSON(w, http.StatusOK, toKeyRecord(k))
}

// listKeys answers GET /-/keys with every key by id, wrapped as
// {"keys": [...]}, secrets omitted. Non-nil so an empty list encodes as [].
func (h *handler) listKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.keys.ListKeys(r.Context())
	if err != nil {
		writeError(w, codeInternal, "key listing failed", nil)
		return
	}
	records := make([]keyRecord, 0, len(keys))
	for _, k := range keys {
		records = append(records, toKeyRecord(k))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"keys": records,
	})
}

// patchKey answers PATCH /-/keys/{id}. Body fields are pointers so an
// absent field is left alone and a sent false or empty map is applied.
// The merged record is validated as a whole, so turning a scoped key into
// an admin must send "scopes": {} in the same patch. Takes effect on the
// key's next request, since stage 2 reads the store every time.
func (h *handler) patchKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Name     *string            `json:"name"`
		Admin    *bool              `json:"admin"`
		Scopes   *map[string]string `json:"scopes"`
		Disabled *bool              `json:"disabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		details := map[string]any{}
		if typeErr, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
			details["name"] = typeErr.Field
		}
		writeError(w, codeInvalidParameter, "body must be a JSON object", details)
		return
	}
	k, err := h.keys.GetKey(r.Context(), id)
	if errors.Is(err, tunna.ErrNotFound) {
		writeError(w, codeKeyNotFound, "no key with id="+id, nil)
		return
	}
	if err != nil {
		writeError(w, codeInternal, "key lookup failed", nil)
		return
	}
	if body.Name != nil {
		k.Name = *body.Name
	}
	if body.Admin != nil {
		k.Admin = *body.Admin
	}
	if body.Disabled != nil {
		k.Disabled = *body.Disabled
	}
	if body.Scopes != nil {
		k.Scopes = toScopes(*body.Scopes)
	}
	if field, msg := validateKeyShape(k.Name, k.Admin, k.Scopes); field != "" {
		writeError(w, codeInvalidParameter, msg, map[string]any{"name": field})
		return
	}
	err = h.keys.UpdateKey(r.Context(), k)
	if errors.Is(err, tunna.ErrNotFound) {
		writeError(w, codeKeyNotFound, "no key with id="+id, nil)
		return
	}
	if err != nil {
		writeError(w, codeInternal, "key update failed", nil)
		return
	}
	writeJSON(w, http.StatusOK, toKeyRecord(k))
}

// rotateKey answers POST /-/keys/{id}/rotate: a new secret, stored, and
// returned once. Requests signed with the old secret fail stage 2 as
// bad_signature from the next one on, presigned URLs included (ADR-0003).
func (h *handler) rotateKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	k, err := h.keys.GetKey(r.Context(), id)
	if errors.Is(err, tunna.ErrNotFound) {
		writeError(w, codeKeyNotFound, "no key with id="+id, nil)
		return
	}
	if err != nil {
		writeError(w, codeInternal, "key lookup failed", nil)
		return
	}
	k.Secret, err = newSecret()
	if err != nil {
		writeError(w, codeInternal, "secret generation failed", nil)
		return
	}
	err = h.keys.UpdateKey(r.Context(), k)
	if errors.Is(err, tunna.ErrNotFound) {
		writeError(w, codeKeyNotFound, "no key with id="+id, nil)
		return
	}
	if err != nil {
		writeError(w, codeInternal, "key update failed", nil)
		return
	}
	rec := toKeyRecord(k)
	rec.Secret = k.Secret
	writeJSON(w, http.StatusOK, rec)
}

// deleteKey answers DELETE /-/keys/{id} with 204. The server does not stop
// an admin deleting its own key or the last admin; the bootstrap variable
// is the recovery (docs/configuration.md).
func (h *handler) deleteKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := h.keys.DeleteKey(r.Context(), id)
	if errors.Is(err, tunna.ErrNotFound) {
		writeError(w, codeKeyNotFound, "no key with id="+id, nil)
		return
	}
	if err != nil {
		writeError(w, codeInternal, "key deletion failed", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getSelfKey answers GET /-/keys/self with the caller's own record, for any
// key (ADR-0008). Stage 2 loaded it for this request, so there is no lookup.
func (h *handler) getSelfKey(w http.ResponseWriter, r *http.Request) {
	k, _ := caller(r)
	writeJSON(w, http.StatusOK, toKeyRecord(k))
}
