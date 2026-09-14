package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/tunnaio/tunna"
)

// bucketRecord is the wire shape of a bucket (spec/wire.md 4.1). The domain
// type stays as it is; this adapter decides how it looks on the wire.
type bucketRecord struct {
	Name      string `json:"name"`
	Public    bool   `json:"public"`
	CreatedAt int64  `json:"created_at"`
}

// toBucketRecord converts a domain bucket to its wire shape. Timestamps go
// out as Unix seconds, as every timestamp on the wire does (spec/wire.md 1).
func toBucketRecord(b tunna.Bucket) bucketRecord {
	return bucketRecord{
		Name:      b.Name,
		Public:    b.Public,
		CreatedAt: b.CreatedAt.Unix(),
	}
}

// getBucket answers GET /-/buckets/{bucket}. Requires credentials.
func (h *handler) getBucket(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("bucket")
	if err := tunna.ValidateBucketName(name); err != nil {
		writeError(w, codeInvalidBucketName, err.Error(), nil)
		return
	}

	b, err := h.buckets.GetBucket(r.Context(), name)
	switch {
	case errors.Is(err, tunna.ErrNotFound):
		writeError(w, codeBucketNotFound, "bucket not found", map[string]any{
			"bucket": name,
		})
		return
	case err != nil:
		writeError(w, codeInternal, "bucket lookup failed", nil)
		return
	}

	writeJSON(w, http.StatusOK, toBucketRecord(b))
}

// createBucket answers PUT /-/buckets/{bucket}. The body is optional JSON
// with a public flag; absent means defaults. Stage 4 checks the name and the
// body before the store is consulted; a taken name is a stage 6 conflict.
func (h *handler) createBucket(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("bucket")
	if err := tunna.ValidateBucketName(name); err != nil {
		writeError(w, codeInvalidBucketName, err.Error(), nil)
		return
	}
	var body struct {
		Public bool `json:"public"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		details := map[string]any{}
		if typeErr, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
			details["name"] = typeErr.Field
		}
		writeError(w, codeInvalidParameter, "body must be a JSON object with optional boolean public", details)
		return
	}

	bucket := tunna.Bucket{
		Name:      name,
		Public:    body.Public,
		CreatedAt: h.now(),
	}

	err := h.buckets.CreateBucket(r.Context(), bucket)

	switch {
	case errors.Is(err, tunna.ErrConflict):
		writeError(w, codeBucketExists, "bucket already exists", nil)
		return

	case err != nil:
		writeError(w, codeInternal, "bucket create failed", nil)
		return
	}

	writeJSON(w, http.StatusCreated, toBucketRecord(bucket))
}

// listBuckets answers GET /-/buckets with the buckets the caller may read,
// in name order, wrapped as {"buckets": [...]}. An admin sees every bucket;
// a scoped key sees those its scopes cover (ADR-0008). The slice is built
// non-nil so an empty list encodes as [] rather than null.
func (h *handler) listBuckets(w http.ResponseWriter, r *http.Request) {
	list, err := h.buckets.ListBuckets(r.Context())
	if err != nil {
		writeError(w, codeInternal, "bucket listing failed", nil)
		return
	}

	k, _ := caller(r)
	listRecords := make([]bucketRecord, 0, len(list))
	for _, b := range list {
		if !k.Allows(tunna.Read, b.Name) {
			continue
		}
		listRecords = append(listRecords, toBucketRecord(b))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"buckets": listRecords,
	})
}

// deleteBucket answers DELETE /-/buckets/{bucket} with 204 and no body.
// Refusing to delete a non-empty bucket arrives with the object store.
func (h *handler) deleteBucket(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("bucket")
	if err := tunna.ValidateBucketName(name); err != nil {
		writeError(w, codeInvalidBucketName, err.Error(), nil)
		return
	}

	// One object is enough to refuse. A PUT landing between this check and the
	// delete would slip past it; the SQLite foreign key refuses that delete and
	// it surfaces as internal, and the single writer of ADR-0002 phase C closes
	// the window properly.
	objs, err := h.objects.ListObjects(r.Context(), name, "", "", 1)
	if err != nil {
		writeError(w, codeInternal, "bucket delete failed", nil)
		return
	}
	if len(objs) > 0 {
		writeError(w, codeBucketNotEmpty, "bucket still holds objects", map[string]any{
			"bucket": name,
		})
		return
	}
	uploadCount, err := h.uploads.CountUploads(r.Context(), name)
	if err != nil {
		writeError(w, codeInternal, "bucket delete failed", nil)
		return
	}
	if uploadCount > 0 {
		writeError(w, codeBucketNotEmpty, "bucket still holds uploads", map[string]any{
			"bucket": name,
		})
		return
	}

	err = h.buckets.DeleteBucket(r.Context(), name)
	switch {
	case errors.Is(err, tunna.ErrNotFound):
		writeError(w, codeBucketNotFound, "bucket not found", map[string]any{
			"bucket": name,
		})
		return
	case err != nil:
		writeError(w, codeInternal, "bucket delete failed", nil)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
