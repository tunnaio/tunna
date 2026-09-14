package httpapi

import (
	"errors"
	"hash/crc32"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"strconv"
	"strings"

	"github.com/tunnaio/tunna"
	"github.com/tunnaio/tunna/sig"
)

// maxBodyLength is the single-request object cap (spec/wire.md 11). It
// belongs in Options once limits are configurable; a constant until then.
const maxBodyLength = 100 << 20

// crc32Table is the Castagnoli table (ADR-0006), built once: it is 1 KiB and
// building it per request would be waste. A literal-shaped value, never
// mutated.
var crc32Table = crc32.MakeTable(crc32.Castagnoli)

// objectRecord is the wire shape of an object (spec/wire.md 5.3). metadata
// is omitted when empty.
type objectRecord struct {
	Bucket      string            `json:"bucket"`
	Key         string            `json:"key"`
	Size        int64             `json:"size"`
	ContentType string            `json:"content_type"`
	Checksum    string            `json:"checksum"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	CreatedAt   int64             `json:"created_at"`
}

// listResponse is the wire shape of a listing (spec/wire.md 7). Objects is
// built non-nil so an empty page encodes as [], and Next is omitted unless
// the page is full.
type listResponse struct {
	Objects []objectRecord `json:"objects"`
	Next    string         `json:"next,omitempty"`
}

// toObjectRecord converts a domain object to its wire shape.
func toObjectRecord(o tunna.Object) objectRecord {
	return objectRecord{
		Bucket:      o.Bucket,
		Key:         o.Key,
		Size:        o.Size,
		ContentType: o.ContentType,
		Checksum:    o.Checksum,
		CreatedAt:   o.CreatedAt.Unix(),
		Metadata:    maps.Clone(o.Metadata),
	}
}

// putObject answers PUT /{bucket}/{key}. It walks the ladder (ADR-0004):
// names and headers at stage 4, bucket existence as a precondition before
// the body, then the body streamed once through the CRC32C hasher into a
// new blob (stage 5), then the metadata row. Any failure after the blob is
// created removes it, so a rejected PUT leaves no orphan. On overwrite the
// previous blob is removed only after the new row has committed (ADR-0007).
// The orchestration is a use case that ADR-0005 places in the root package;
// it lives here until uploads need the same steps, a known deviation.
func (h *handler) putObject(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	key := r.PathValue("key")

	if err := tunna.ValidateBucketName(bucket); err != nil {
		writeError(w, codeInvalidBucketName, err.Error(), nil)
		return
	}

	if err := tunna.ValidateObjectKey(key); err != nil {
		writeError(w, codeInvalidKey, err.Error(), nil)
		return
	}

	if r.ContentLength < 0 {
		writeError(w, codeMalformedRequest, "Content-Length is required; a chunked body without one is not accepted", nil)
		return
	}

	if r.ContentLength > maxBodyLength {
		writeError(w, codeBodyTooLarge, "The body exceeds the applicable limit: the single-request object cap, or the session's part size.", nil)
		return
	}

	var wantCRC uint32
	hasChecksum := false
	if v := r.Header.Get("X-Tunna-Checksum"); v != "" {
		crc, err := sig.ParseChecksum(v)
		if err != nil {
			writeError(w, codeInvalidParameter, "X-Tunna-Checksum is not of the form crc32c=<base64>", map[string]any{"name": "X-Tunna-Checksum"})
			return
		}
		wantCRC, hasChecksum = crc, true
	}

	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	metadata := make(map[string]string)
	for hn, v := range r.Header {
		name, found := strings.CutPrefix(hn, "X-Tunna-Meta-")
		if !found {
			continue
		}
		lower := strings.ToLower(name)

		if len(v) > 1 {
			metadata[lower] = strings.Join(v, ",")
		} else {
			metadata[lower] = v[0]
		}
	}

	b, err := h.buckets.GetBucket(r.Context(), bucket)
	if errors.Is(err, tunna.ErrNotFound) {
		writeError(w, codeBucketNotFound, "no bucket named "+bucket, map[string]any{"bucket": bucket})
		return
	}
	if err != nil {
		writeError(w, codeInternal, "bucket lookup failed", nil)
		return
	}

	id, err := h.blobs.Create(r.Context())
	if err != nil {
		writeError(w, codeInternal, "creating blob failed", nil)
		return
	}
	hasher := crc32.New(crc32Table)
	n, err := h.blobs.Write(r.Context(), id, 0, io.TeeReader(r.Body, hasher))
	if errors.Is(err, io.ErrUnexpectedEOF) {
		h.blobs.Remove(r.Context(), id)
		writeError(w, codeBodyLengthMismatch, "body ended before Content-Length bytes arrived", nil)
		return
	}
	if err != nil {
		h.blobs.Remove(r.Context(), id)
		writeError(w, codeInternal, "writing body failed", nil)
		return
	}
	if n != r.ContentLength {
		h.blobs.Remove(r.Context(), id)
		writeError(w, codeBodyLengthMismatch, "body length differs from Content-Length", nil)
		return
	}
	if err := h.blobs.Sync(r.Context(), id); err != nil {
		h.blobs.Remove(r.Context(), id)
		writeError(w, codeInternal, "syncing blob failed", nil)
		return
	}

	if hasChecksum && hasher.Sum32() != wantCRC {
		h.blobs.Remove(r.Context(), id)
		writeError(w, codeChecksumMismatch, "body checksum does not match X-Tunna-Checksum", map[string]any{"algorithm": "crc32c"})
		return
	}

	obj := tunna.Object{
		Bucket:      b.Name,
		Key:         key,
		BlobID:      id,
		Size:        r.ContentLength,
		ContentType: contentType,
		Checksum:    sig.EncodeCRC32C(hasher.Sum32()),
		CreatedAt:   h.now(),
		Metadata:    maps.Clone(metadata),
	}
	prevId, err := h.objects.PutObject(r.Context(), obj)
	if err != nil {
		writeError(w, codeInternal, "recording object failed", nil)
		return
	}
	if prevId != "" {
		if err := h.blobs.Remove(r.Context(), prevId); err != nil {
			slog.Warn("removing previous blob failed", "blob_id", prevId, "err", err)
		}
	}

	w.Header().Add("ETag", "\""+obj.Checksum+"\"")
	w.Header().Add("X-Tunna-Checksum", obj.Checksum)
	writeJSON(w, http.StatusCreated, toObjectRecord(obj))
}

// getObject answers GET and HEAD /{bucket}/{key}; the mux routes HEAD through
// GET patterns and ServeContent sends headers only. A public bucket needs no
// caller; any other bucket requires one, decided here rather than by
// requireAuth because the answer depends on the bucket. ServeContent handles
// Range, If-None-Match, Last-Modified and Content-Length from the seekable
// blob, which is why BlobStore.Open returns an io.ReadSeekCloser.
func (h *handler) getObject(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	key := r.PathValue("key")

	_, allowed := h.readableBucket(w, r, bucket)
	if !allowed {
		return
	}

	if err := tunna.ValidateBucketName(bucket); err != nil {
		writeError(w, codeInvalidBucketName, err.Error(), nil)
		return
	}

	if err := tunna.ValidateObjectKey(key); err != nil {
		writeError(w, codeInvalidKey, err.Error(), nil)
		return
	}

	obj, err := h.objects.GetObject(r.Context(), bucket, key)
	if errors.Is(err, tunna.ErrNotFound) {
		writeError(w, codeObjectNotFound, "no key named "+key, map[string]any{"key": key})
		return
	}
	if err != nil {
		writeError(w, codeInternal, "object lookup failed", nil)
		return
	}
	f, err := h.blobs.Open(r.Context(), obj.BlobID)
	if err != nil {
		slog.Error("blob not found", "blob_id", obj.BlobID, "bucket", bucket, "key", key, "err", err)
		writeError(w, codeInternal, "object lookup failed", nil)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", obj.ContentType)
	w.Header().Set("ETag", "\""+obj.Checksum+"\"")
	w.Header().Set("X-Tunna-Checksum", obj.Checksum)
	for name, value := range obj.Metadata {
		w.Header().Set("X-Tunna-Meta-"+name, value)
	}
	http.ServeContent(w, r, "", obj.CreatedAt, f)
}

// deleteObject answers DELETE /{bucket}/{key} with 204 and no body. The row
// goes first, then the blob; a failed blob removal is logged and the file is
// an orphan for the sweep, never a dangling row (ADR-0007).
func (h *handler) deleteObject(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	key := r.PathValue("key")

	if err := tunna.ValidateBucketName(bucket); err != nil {
		writeError(w, codeInvalidBucketName, err.Error(), nil)
		return
	}

	if err := tunna.ValidateObjectKey(key); err != nil {
		writeError(w, codeInvalidKey, err.Error(), nil)
		return
	}

	_, err := h.buckets.GetBucket(r.Context(), bucket)
	if errors.Is(err, tunna.ErrNotFound) {
		writeError(w, codeBucketNotFound, "no bucket named "+bucket, map[string]any{"bucket": bucket})
		return
	}
	if err != nil {
		writeError(w, codeInternal, "bucket lookup failed", nil)
		return
	}
	blobID, err := h.objects.DeleteObject(r.Context(), bucket, key)
	if errors.Is(err, tunna.ErrNotFound) {
		writeError(w, codeObjectNotFound, "no object named "+key, map[string]any{"key": key})
		return
	}
	if err != nil {
		writeError(w, codeInternal, "object delete failed", nil)
		return
	}
	if err := h.blobs.Remove(r.Context(), blobID); err != nil {
		slog.Warn("removing blob failed", "blob_id", blobID, "bucket", bucket, "key", key, "err", err)
	}

	w.WriteHeader(http.StatusNoContent)
}

// listObjects answers GET /{bucket}: keys in bytewise order, filtered by
// prefix, paged by after and limit. delimiter is rejected until grouping is
// specified, so no client can depend on its absence by accident.
func (h *handler) listObjects(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")

	_, allowed := h.readableBucket(w, r, bucket)
	if !allowed {
		return
	}

	if err := tunna.ValidateBucketName(bucket); err != nil {
		writeError(w, codeInvalidBucketName, err.Error(), nil)
		return
	}

	q := r.URL.Query()
	if q.Has("delimiter") {
		writeError(w, codeInvalidParameter, "delimiter is not supported in this version", map[string]any{
			"name": "delimiter",
		})
		return
	}
	var limit int
	prefix := q.Get("prefix")
	after := q.Get("after")
	if q.Has("limit") {
		raw := q.Get("limit")
		l, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, codeInvalidParameter, "limit must be an integer", map[string]any{
				"name": "limit",
			})
			return
		}
		if l <= 0 {
			writeError(w, codeInvalidParameter, "limit must be at least 1", map[string]any{
				"name": "limit",
			})
			return
		}
		limit = min(l, 1000)
	} else {
		limit = 1000
	}

	list, err := h.objects.ListObjects(r.Context(), bucket, prefix, after, limit)
	if err != nil {
		writeError(w, codeInternal, "object lookup failed", nil)
		return
	}
	response := listResponse{
		Objects: make([]objectRecord, 0, len(list)),
	}
	if len(list) == limit {
		response.Next = list[len(list)-1].Key
	}
	for _, o := range list {
		response.Objects = append(response.Objects, toObjectRecord(o))
	}
	writeJSON(w, http.StatusOK, response)
}
