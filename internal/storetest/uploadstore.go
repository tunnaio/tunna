package storetest

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/tunnaio/tunna"
)

// UploadStoreFactory builds a fresh upload store together with the object
// store it completes into; the two share state. Buckets "photos" and "docs"
// must be usable. Cleanup, if any, is registered with t.Cleanup.
type UploadStoreFactory func(t *testing.T) (tunna.UploadStore, tunna.ObjectStore)

// UploadStore runs the UploadStore contract against the stores the factory
// builds. Validation of names and sizes is not part of the contract.
func UploadStore(t *testing.T, newStore UploadStoreFactory) {
	t.Helper()
	ctx := context.Background()
	at := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)

	session := func(id, bucket, key string) tunna.UploadSession {
		return tunna.UploadSession{
			ID: id, Bucket: bucket, Key: key, BlobID: "blob-" + id,
			PartSize: 5 << 20, ContentType: "application/octet-stream",
			Metadata:  map[string]string{"title": "t"},
			Parts:     map[int]tunna.Part{},
			CreatedAt: at, ExpiresAt: at.Add(24 * time.Hour),
		}
	}

	t.Run("create then get returns every field", func(t *testing.T) {
		s, _ := newStore(t)
		want := session("up1", "photos", "a.bin")
		if err := s.CreateUpload(ctx, want); err != nil {
			t.Fatalf("CreateUpload: %v", err)
		}
		got, err := s.GetUpload(ctx, "up1")
		if err != nil {
			t.Fatalf("GetUpload: %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("GetUpload = %+v, want %+v", got, want)
		}
	})

	t.Run("get unknown is ErrNotFound with a zero session", func(t *testing.T) {
		s, _ := newStore(t)
		got, err := s.GetUpload(ctx, "nope")
		if !errors.Is(err, tunna.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if !reflect.DeepEqual(got, tunna.UploadSession{}) {
			t.Errorf("session on not-found = %+v, want zero value", got)
		}
	})

	t.Run("create with a taken id is ErrConflict", func(t *testing.T) {
		s, _ := newStore(t)
		s.CreateUpload(ctx, session("up1", "photos", "a.bin"))
		if err := s.CreateUpload(ctx, session("up1", "photos", "b.bin")); !errors.Is(err, tunna.ErrConflict) {
			t.Errorf("second CreateUpload err = %v, want ErrConflict", err)
		}
	})

	t.Run("put part records, replaces, and keeps others", func(t *testing.T) {
		s, _ := newStore(t)
		s.CreateUpload(ctx, session("up1", "photos", "a.bin"))
		if err := s.PutPart(ctx, "up1", 3, tunna.Part{Size: 1024, Checksum: "crc32c=AAAAAA=="}); err != nil {
			t.Fatalf("PutPart 3: %v", err)
		}
		if err := s.PutPart(ctx, "up1", 1, tunna.Part{Size: 5 << 20, Checksum: "crc32c=BBBBBB=="}); err != nil {
			t.Fatalf("PutPart 1: %v", err)
		}
		if err := s.PutPart(ctx, "up1", 1, tunna.Part{Size: 5 << 20, Checksum: "crc32c=CCCCCC=="}); err != nil {
			t.Fatalf("PutPart 1 again: %v", err)
		}
		got, _ := s.GetUpload(ctx, "up1")
		want := map[int]tunna.Part{
			1: {Size: 5 << 20, Checksum: "crc32c=CCCCCC=="},
			3: {Size: 1024, Checksum: "crc32c=AAAAAA=="},
		}
		if !reflect.DeepEqual(got.Parts, want) {
			t.Errorf("Parts = %v, want %v", got.Parts, want)
		}
	})

	t.Run("put part on unknown session is ErrNotFound", func(t *testing.T) {
		s, _ := newStore(t)
		if err := s.PutPart(ctx, "nope", 1, tunna.Part{Size: 1}); !errors.Is(err, tunna.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("complete records the object and drops the session", func(t *testing.T) {
		s, objects := newStore(t)
		s.CreateUpload(ctx, session("up1", "photos", "a.bin"))
		s.PutPart(ctx, "up1", 1, tunna.Part{Size: 10, Checksum: "crc32c=AAAAAA=="})
		obj := tunna.Object{
			Bucket: "photos", Key: "a.bin", BlobID: "blob-up1", Size: 10,
			ContentType: "application/octet-stream", Checksum: "crc32c=AAAAAA==",
			Metadata: map[string]string{"title": "t"}, CreatedAt: at,
		}
		prev, err := s.CompleteUpload(ctx, "up1", obj)
		if err != nil {
			t.Fatalf("CompleteUpload: %v", err)
		}
		if prev != "" {
			t.Errorf("previous blob = %q, want empty for a new key", prev)
		}
		got, err := objects.GetObject(ctx, "photos", "a.bin")
		if err != nil {
			t.Fatalf("GetObject after complete: %v", err)
		}
		if !reflect.DeepEqual(got, obj) {
			t.Errorf("completed object = %+v, want %+v", got, obj)
		}
		if _, err := s.GetUpload(ctx, "up1"); !errors.Is(err, tunna.ErrNotFound) {
			t.Errorf("session after complete err = %v, want ErrNotFound", err)
		}
	})

	t.Run("complete over an existing object returns its blob id", func(t *testing.T) {
		s, objects := newStore(t)
		objects.PutObject(ctx, tunna.Object{Bucket: "photos", Key: "a.bin", BlobID: "old-blob", Size: 1, ContentType: "x", Checksum: "crc32c=AAAAAA==", CreatedAt: at})
		s.CreateUpload(ctx, session("up1", "photos", "a.bin"))
		prev, err := s.CompleteUpload(ctx, "up1", tunna.Object{Bucket: "photos", Key: "a.bin", BlobID: "blob-up1", Size: 2, ContentType: "x", Checksum: "crc32c=AAAAAA==", CreatedAt: at})
		if err != nil {
			t.Fatalf("CompleteUpload: %v", err)
		}
		if prev != "old-blob" {
			t.Errorf("previous blob = %q, want old-blob", prev)
		}
		got, _ := objects.GetObject(ctx, "photos", "a.bin")
		if got.BlobID != "blob-up1" || got.Size != 2 {
			t.Errorf("object after complete = %+v, want the completed one", got)
		}
	})

	t.Run("complete unknown session is ErrNotFound and records nothing", func(t *testing.T) {
		s, objects := newStore(t)
		_, err := s.CompleteUpload(ctx, "nope", tunna.Object{Bucket: "photos", Key: "ghost.bin", BlobID: "b", ContentType: "x", Checksum: "crc32c=AAAAAA==", CreatedAt: at})
		if !errors.Is(err, tunna.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if _, err := objects.GetObject(ctx, "photos", "ghost.bin"); !errors.Is(err, tunna.ErrNotFound) {
			t.Errorf("object was recorded for an unknown session")
		}
	})

	t.Run("delete returns the session and then get is ErrNotFound", func(t *testing.T) {
		s, _ := newStore(t)
		s.CreateUpload(ctx, session("up1", "photos", "a.bin"))
		s.PutPart(ctx, "up1", 2, tunna.Part{Size: 7, Checksum: "crc32c=AAAAAA=="})
		got, err := s.DeleteUpload(ctx, "up1")
		if err != nil {
			t.Fatalf("DeleteUpload: %v", err)
		}
		if got.BlobID != "blob-up1" || got.Parts[2].Size != 7 {
			t.Errorf("DeleteUpload returned %+v, want the session with its parts", got)
		}
		if _, err := s.GetUpload(ctx, "up1"); !errors.Is(err, tunna.ErrNotFound) {
			t.Errorf("after delete, GetUpload err = %v, want ErrNotFound", err)
		}
		if _, err := s.DeleteUpload(ctx, "up1"); !errors.Is(err, tunna.ErrNotFound) {
			t.Errorf("second DeleteUpload err = %v, want ErrNotFound", err)
		}
	})

	t.Run("count is per bucket and follows create, complete and delete", func(t *testing.T) {
		s, _ := newStore(t)
		count := func(bucket string) int {
			n, err := s.CountUploads(ctx, bucket)
			if err != nil {
				t.Fatalf("CountUploads %s: %v", bucket, err)
			}
			return n
		}
		if n := count("photos"); n != 0 {
			t.Fatalf("empty count = %d, want 0", n)
		}
		s.CreateUpload(ctx, session("up1", "photos", "a.bin"))
		s.CreateUpload(ctx, session("up2", "photos", "b.bin"))
		s.CreateUpload(ctx, session("up3", "docs", "c.bin"))
		if p, d := count("photos"), count("docs"); p != 2 || d != 1 {
			t.Fatalf("counts = photos %d, docs %d; want 2, 1", p, d)
		}
		s.CompleteUpload(ctx, "up1", tunna.Object{Bucket: "photos", Key: "a.bin", BlobID: "blob-up1", ContentType: "x", Checksum: "crc32c=AAAAAA==", CreatedAt: at})
		s.DeleteUpload(ctx, "up2")
		if p := count("photos"); p != 0 {
			t.Errorf("count after complete and delete = %d, want 0", p)
		}
	})

	t.Run("returned maps are copies", func(t *testing.T) {
		s, _ := newStore(t)
		s.CreateUpload(ctx, session("up1", "photos", "a.bin"))
		s.PutPart(ctx, "up1", 1, tunna.Part{Size: 1, Checksum: "crc32c=AAAAAA=="})
		got, _ := s.GetUpload(ctx, "up1")
		got.Parts[9] = tunna.Part{Size: 9}
		got.Metadata["title"] = "mutated"
		again, _ := s.GetUpload(ctx, "up1")
		if _, ok := again.Parts[9]; ok || again.Metadata["title"] != "t" {
			t.Errorf("mutating returned maps changed the store: %+v", again)
		}
	})
}
