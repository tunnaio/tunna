package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/tunnaio/tunna"
	"github.com/tunnaio/tunna/sig"
)

// sessionRecord is the wire shape of an upload session (spec/wire.md 6.1 and
// 6.3). Parts is the sorted received part numbers, built non-nil so an empty
// session encodes as [].
type sessionRecord struct {
	ID          string `json:"id"`
	Bucket      string `json:"bucket"`
	Key         string `json:"key"`
	PartSize    int64  `json:"part_size"`
	ContentType string `json:"content_type"`
	CreatedAt   int64  `json:"created_at"`
	ExpiresAt   int64  `json:"expires_at"`
	Parts       []int  `json:"parts"`
}

type putUploadPartRecord struct {
	Part     int    `json:"part"`
	Size     int64  `json:"size"`
	Checksum string `json:"checksum"`
}

// toSessionRecord converts a domain session to its wire shape.
func toSessionRecord(s tunna.UploadSession) sessionRecord {
	parts := slices.Sorted(maps.Keys(s.Parts))
	if parts == nil {
		parts = []int{}
	}
	return sessionRecord{
		ID:          s.ID,
		Bucket:      s.Bucket,
		Key:         s.Key,
		PartSize:    s.PartSize,
		ContentType: s.ContentType,
		CreatedAt:   s.CreatedAt.Unix(),
		ExpiresAt:   s.ExpiresAt.Unix(),
		Parts:       parts,
	}
}

// createUpload answers POST /-/uploads: validates the body at stage 4, checks
// the bucket exists, creates the blob the parts will write into, and records
// the session. A store failure removes the blob so nothing is orphaned.
func (h *handler) createUpload(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Bucket      string            `json:"bucket"`
		Key         string            `json:"key"`
		PartSize    *int64            `json:"part_size"`
		ContentType string            `json:"content_type"`
		Metadata    map[string]string `json:"metadata"`
	}

	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		details := map[string]any{}
		if typeErr, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
			details["name"] = typeErr.Field
		}
		writeError(w, codeInvalidParameter, "body must be a JSON object", details)
		return
	}

	if err := tunna.ValidateBucketName(body.Bucket); err != nil {
		writeError(w, codeInvalidBucketName, err.Error(), nil)
		return
	}

	if err := tunna.ValidateObjectKey(body.Key); err != nil {
		writeError(w, codeInvalidKey, err.Error(), nil)
		return
	}

	if body.PartSize == nil {
		writeError(w, codeInvalidParameter, "part_size is required", map[string]any{
			"name": "part_size",
		})
		return
	}

	if *body.PartSize < h.partSizeMin || *body.PartSize > h.partSizeMax {
		writeError(w, codeInvalidPartSize, fmt.Sprintf("part_size must be between %d and %d bytes", h.partSizeMin, h.partSizeMax), map[string]any{
			"min": h.partSizeMin,
			"max": h.partSizeMax,
		})
		return
	}

	_, err := h.buckets.GetBucket(r.Context(), body.Bucket)
	if errors.Is(err, tunna.ErrNotFound) {
		writeError(w, codeBucketNotFound, "no bucket named "+body.Bucket, map[string]any{"bucket": body.Bucket})
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

	metadata := make(map[string]string)
	for k, v := range body.Metadata {
		metadata[strings.ToLower(k)] = v
	}
	raw := make([]byte, 16)
	rand.Read(raw)
	sessID := "up_" + hex.EncodeToString(raw)
	now := h.now()
	sess := tunna.UploadSession{
		ID:          sessID,
		BlobID:      id,
		Bucket:      body.Bucket,
		Key:         body.Key,
		PartSize:    *body.PartSize,
		ContentType: body.ContentType,
		Metadata:    maps.Clone(metadata),
		Parts:       make(map[int]tunna.Part),
		CreatedAt:   now,
		ExpiresAt:   now.Add(h.uploadTTL),
	}
	if sess.ContentType == "" {
		sess.ContentType = "application/octet-stream"
	}
	if err = h.uploads.CreateUpload(r.Context(), sess); err != nil {
		slog.Warn("create upload failed", "blob_id", sess.BlobID, "bucket", sess.Bucket, "key", sess.Key, "err", err)
		if err = h.blobs.Remove(r.Context(), sess.BlobID); err != nil {
			slog.Warn("orphaned blob", "blob_id", sess.BlobID, "bucket", sess.Bucket, "key", sess.Key, "err", err)
		}
		writeError(w, codeInternal, "creating upload session failed", nil)
		return
	}

	writeJSON(w, http.StatusCreated, toSessionRecord(sess))
}

// putUploadPart answers PUT /-/uploads/{id}/parts/{n}: the body streams
// once through the CRC32C hasher into the blob at offset (n-1)*part_size,
// then the part's length and checksum are recorded. A short body is accepted
// on arrival because only complete knows which part is last. Nothing is
// removed on failure: the file is the session's and a resend overwrites the
// offset.
func (h *handler) putUploadPart(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		writeError(w, codeInvalidPartNumber, "part number must be an integer", nil)
		return
	}
	if n < 1 || n > int(h.maxParts) {
		writeError(w, codeInvalidPartNumber, fmt.Sprintf("part number must be between 1 and %d", h.maxParts), map[string]any{
			"max": h.maxParts,
		})
		return
	}
	id := r.PathValue("id")
	sess, err := h.uploads.GetUpload(r.Context(), id)
	if errors.Is(err, tunna.ErrNotFound) {
		writeError(w, codeUploadNotFound, "upload with id="+id+" not found", nil)
		return
	}
	if err != nil {
		writeError(w, codeInternal, "upload lookup failed", nil)
		return
	}
	if h.now().After(sess.ExpiresAt) {
		writeError(w, codeSessionNotActive, "upload session has expired", nil)
		return
	}
	if r.ContentLength == -1 {
		writeError(w, codeMalformedRequest, "Content-Length is required; a chunked body without one is not accepted", nil)
		return
	}
	if r.ContentLength == 0 {
		writeError(w, codeInvalidParameter, "a part body must not be empty", map[string]any{"name": "Content-Length"})
		return
	}
	if r.ContentLength > sess.PartSize {
		writeError(w, codeBodyTooLarge, fmt.Sprintf("body exceeds the session part size of %d bytes", sess.PartSize), map[string]any{
			"max": h.partSizeMax,
		})
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
	hasher := crc32.New(crc32Table)
	written, err := h.blobs.Write(r.Context(), sess.BlobID, int64(n-1)*sess.PartSize, io.TeeReader(r.Body, hasher))
	wire := sig.EncodeCRC32C(hasher.Sum32())
	if errors.Is(err, io.ErrUnexpectedEOF) {
		writeError(w, codeBodyLengthMismatch, "body ended before Content-Length bytes arrived", nil)
		return
	}
	if err != nil {
		writeError(w, codeInternal, "writing part failed", nil)
		return
	}
	if hasChecksum && hasher.Sum32() != wantCRC {
		writeError(w, codeChecksumMismatch, "body checksum does not match X-Tunna-Checksum", map[string]any{"algorithm": "crc32c"})
		return
	}
	if err := h.blobs.Sync(r.Context(), sess.BlobID); err != nil {
		writeError(w, codeInternal, "syncing part failed", nil)
		return
	}
	if err := h.uploads.PutPart(r.Context(), sess.ID, n, tunna.Part{Size: written, Checksum: wire}); err != nil {
		writeError(w, codeInternal, "recording part failed", nil)
		return
	}

	w.Header().Set("X-Tunna-Checksum", wire)
	writeJSON(w, http.StatusOK, putUploadPartRecord{
		Part:     n,
		Size:     written,
		Checksum: wire,
	})
}

// getUpload answers GET /-/uploads/{id} with the session and its received
// parts; resume is fetch this, send what is missing.
func (h *handler) getUpload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, err := h.uploads.GetUpload(r.Context(), id)
	if errors.Is(err, tunna.ErrNotFound) {
		writeError(w, codeUploadNotFound, "upload with id="+id+" not found", nil)
		return
	}
	if err != nil {
		writeError(w, codeInternal, "upload lookup failed", nil)
		return
	}
	if h.now().After(sess.ExpiresAt) {
		writeError(w, codeSessionNotActive, "upload session has expired", nil)
		return
	}
	writeJSON(w, http.StatusOK, toSessionRecord(sess))
}

// deleteUpload answers DELETE /-/uploads/{id}: drops the session, then the
// blob. Expiry is not checked here on purpose; an expired session is
// exactly what a client should still be able to release.
func (h *handler) deleteUpload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, err := h.uploads.GetUpload(r.Context(), id)
	if errors.Is(err, tunna.ErrNotFound) {
		writeError(w, codeUploadNotFound, "upload with id="+id+" not found", nil)
		return
	}
	if err != nil {
		writeError(w, codeInternal, "upload lookup failed", nil)
		return
	}
	if h.now().After(sess.ExpiresAt) {
		writeError(w, codeSessionNotActive, "upload session has expired", nil)
		return
	}
	deleted, err := h.uploads.DeleteUpload(r.Context(), sess.ID)
	if errors.Is(err, tunna.ErrNotFound) {
		writeError(w, codeUploadNotFound, "upload with id="+id+" not found", nil)
		return
	}
	if err != nil {
		writeError(w, codeInternal, "aborting upload failed", nil)
		return
	}
	if err := h.blobs.Remove(r.Context(), deleted.BlobID); err != nil {
		slog.Warn("orphaned blob", "blob_id", deleted.BlobID, "bucket", deleted.Bucket, "key", deleted.Key, "err", err)
	}

	w.WriteHeader(http.StatusNoContent)
}

// completeUpload answers POST /-/uploads/{id}/complete: verifies every part
// is present, every non-final part is full size, and any supplied checksums
// match; folds the per-part CRCs into the object's (ADR-0006); then records
// the object and drops the session in one store transaction (ADR-0001).
func (h *handler) completeUpload(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Parts     int      `json:"parts"`
		Checksums []string `json:"checksums"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		details := map[string]any{}
		if typeErr, ok := errors.AsType[*json.UnmarshalTypeError](err); ok {
			details["name"] = typeErr.Field
		}
		writeError(w, codeInvalidParameter, "body must be a JSON object", details)
		return
	}
	if body.Parts < 1 {
		writeError(w, codeInvalidParameter, "parts must be at least 1", map[string]any{
			"name": "parts",
		})
		return
	}
	if body.Checksums != nil && len(body.Checksums) != body.Parts {
		writeError(w, codeInvalidParameter, "checksums must have exactly one entry per part", map[string]any{
			"name": "checksums",
		})
		return
	}
	id := r.PathValue("id")
	sess, err := h.uploads.GetUpload(r.Context(), id)
	if errors.Is(err, tunna.ErrNotFound) {
		writeError(w, codeUploadNotFound, "upload with id="+id+" not found", nil)
		return
	}
	if err != nil {
		writeError(w, codeInternal, "upload lookup failed", nil)
		return
	}
	if h.now().After(sess.ExpiresAt) {
		writeError(w, codeSessionNotActive, "upload session has expired", nil)
		return
	}
	// Three checks in the contract's order (spec/wire.md 6.4), each over every
	// part, so a client fixing an upload learns everything wrong at once.
	missing := []int{}
	for i := 1; i <= body.Parts; i++ {
		if _, found := sess.Parts[i]; !found {
			missing = append(missing, i)
		}
	}
	if len(missing) > 0 {
		writeError(w, codeUploadIncomplete, "not every part has been received", map[string]any{
			"missing": missing,
		})
		return
	}
	// Every part before the last must be exactly part_size; only the last
	// may be shorter.
	for i := 1; i < body.Parts; i++ {
		if sess.Parts[i].Size != sess.PartSize {
			writeError(w, codePartSizeMismatch, fmt.Sprintf("part %d is %d bytes, not the session part size %d", i, sess.Parts[i].Size, sess.PartSize), map[string]any{
				"part": i,
			})
			return
		}
	}
	if body.Checksums != nil {
		for i := 1; i <= body.Parts; i++ {
			if body.Checksums[i-1] != sess.Parts[i].Checksum {
				writeError(w, codeChecksumMismatch, fmt.Sprintf("checksum supplied for part %d does not match the recorded one", i), map[string]any{
					"part":      i,
					"algorithm": "crc32c",
				})
				return
			}
		}
	}

	// Fold the per-part CRCs into the object's, in part order (ADR-0006). The
	// recorded strings came from EncodeCRC32C, so a parse failure means a
	// corrupt record, which is internal.
	acc, err := sig.ParseChecksum(sess.Parts[1].Checksum)
	if err != nil {
		writeError(w, codeInternal, "recorded part checksum is corrupt", nil)
		return
	}
	size := sess.Parts[1].Size
	for i := 2; i <= body.Parts; i++ {
		crc, err := sig.ParseChecksum(sess.Parts[i].Checksum)
		if err != nil {
			writeError(w, codeInternal, "recorded part checksum is corrupt", nil)
			return
		}
		acc = sig.CombineCRC32C(acc, crc, sess.Parts[i].Size)
		size += sess.Parts[i].Size
	}

	obj := tunna.Object{
		Bucket:      sess.Bucket,
		Key:         sess.Key,
		BlobID:      sess.BlobID,
		Size:        size,
		ContentType: sess.ContentType,
		Checksum:    sig.EncodeCRC32C(acc),
		Metadata:    maps.Clone(sess.Metadata),
		CreatedAt:   h.now(),
	}
	prev, err := h.uploads.CompleteUpload(r.Context(), sess.ID, obj)
	if errors.Is(err, tunna.ErrNotFound) {
		writeError(w, codeUploadNotFound, "upload with id="+id+" not found", nil)
		return
	}
	if err != nil {
		slog.Error("completing upload failed", "upload_id", id, "bucket", sess.Bucket, "key", sess.Key, "err", err)
		writeError(w, codeInternal, "completing upload failed", nil)
		return
	}
	// The row now points at the session blob; an overwritten object's old
	// blob goes only after that commit (ADR-0007).
	if prev != "" {
		if err := h.blobs.Remove(r.Context(), prev); err != nil {
			slog.Warn("removing previous blob failed", "blob_id", prev, "bucket", sess.Bucket, "key", sess.Key, "err", err)
		}
	}

	w.Header().Add("ETag", "\""+obj.Checksum+"\"")
	w.Header().Add("X-Tunna-Checksum", obj.Checksum)
	writeJSON(w, http.StatusCreated, toObjectRecord(obj))
}
