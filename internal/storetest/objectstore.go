package storetest

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/tunnaio/tunna"
)

// ObjectStoreFactory builds a fresh, empty store. Cleanup, if any, is
// registered with t.Cleanup.
type ObjectStoreFactory func(t *testing.T) tunna.ObjectStore

// ObjectStore runs the ObjectStore contract against the store the factory
// builds. Key validation is not part of the contract: stores trust their
// callers. Bytes are not part of it either; that is BlobStore.
func ObjectStore(t *testing.T, newStore ObjectStoreFactory) {
	t.Helper()
	ctx := context.Background()
	at := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)

	obj := func(bucket, key, blob string) tunna.Object {
		return tunna.Object{
			Bucket: bucket, Key: key, BlobID: blob,
			Size: 3, ContentType: "text/plain", Checksum: "crc32c=AAAAAA==",
			Metadata:  map[string]string{"title": "t"},
			CreatedAt: at,
		}
	}

	t.Run("put then get returns every field", func(t *testing.T) {
		s := newStore(t)
		want := obj("photos", "a.txt", "blob-1")
		prev, err := s.PutObject(ctx, want)
		if err != nil {
			t.Fatalf("PutObject: %v", err)
		}
		if prev != "" {
			t.Errorf("first PutObject returned previous blob %q, want empty", prev)
		}
		got, err := s.GetObject(ctx, "photos", "a.txt")
		if err != nil {
			t.Fatalf("GetObject: %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("GetObject = %+v, want %+v", got, want)
		}
	})

	t.Run("get unknown is ErrNotFound with a zero object", func(t *testing.T) {
		s := newStore(t)
		got, err := s.GetObject(ctx, "photos", "nope")
		if !errors.Is(err, tunna.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if !reflect.DeepEqual(got, tunna.Object{}) {
			t.Errorf("object on not-found = %+v, want zero value", got)
		}
	})

	t.Run("overwrite replaces the row and returns the previous blob id", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.PutObject(ctx, obj("photos", "a.txt", "blob-1")); err != nil {
			t.Fatal(err)
		}
		second := obj("photos", "a.txt", "blob-2")
		second.Size = 99
		prev, err := s.PutObject(ctx, second)
		if err != nil {
			t.Fatalf("second PutObject: %v", err)
		}
		if prev != "blob-1" {
			t.Errorf("previous blob = %q, want blob-1", prev)
		}
		got, _ := s.GetObject(ctx, "photos", "a.txt")
		if got.BlobID != "blob-2" || got.Size != 99 {
			t.Errorf("after overwrite GetObject = %+v, want blob-2 with size 99", got)
		}
	})

	t.Run("same key in two buckets is two objects", func(t *testing.T) {
		s := newStore(t)
		s.PutObject(ctx, obj("photos", "a.txt", "blob-p"))
		s.PutObject(ctx, obj("docs", "a.txt", "blob-d"))
		p, _ := s.GetObject(ctx, "photos", "a.txt")
		d, _ := s.GetObject(ctx, "docs", "a.txt")
		if p.BlobID != "blob-p" || d.BlobID != "blob-d" {
			t.Errorf("buckets bled: photos=%q docs=%q", p.BlobID, d.BlobID)
		}
	})

	t.Run("delete returns the blob id and then get is ErrNotFound", func(t *testing.T) {
		s := newStore(t)
		s.PutObject(ctx, obj("photos", "a.txt", "blob-1"))
		blob, err := s.DeleteObject(ctx, "photos", "a.txt")
		if err != nil {
			t.Fatalf("DeleteObject: %v", err)
		}
		if blob != "blob-1" {
			t.Errorf("DeleteObject blob = %q, want blob-1", blob)
		}
		if _, err := s.GetObject(ctx, "photos", "a.txt"); !errors.Is(err, tunna.ErrNotFound) {
			t.Errorf("after delete, GetObject err = %v, want ErrNotFound", err)
		}
	})

	t.Run("delete unknown is ErrNotFound", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.DeleteObject(ctx, "photos", "nope"); !errors.Is(err, tunna.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("list is ordered by key bytewise and scoped to the bucket", func(t *testing.T) {
		s := newStore(t)
		for _, k := range []string{"b", "a", "B", "a/b", "a.b"} {
			s.PutObject(ctx, obj("photos", k, "blob-"+k))
		}
		s.PutObject(ctx, obj("docs", "a", "blob-other"))
		list, err := s.ListObjects(ctx, "photos", "", "", 100)
		if err != nil {
			t.Fatalf("ListObjects: %v", err)
		}
		got := keysOf(list)
		want := []string{"B", "a", "a.b", "a/b", "b"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("keys = %v, want %v (uppercase before lowercase, '.' before '/')", got, want)
		}
	})

	t.Run("list filters by prefix", func(t *testing.T) {
		s := newStore(t)
		for _, k := range []string{"2025/x", "2026/a", "2026/b", "2027/y", "2026"} {
			s.PutObject(ctx, obj("photos", k, "blob"))
		}
		list, _ := s.ListObjects(ctx, "photos", "2026/", "", 100)
		if got, want := keysOf(list), []string{"2026/a", "2026/b"}; !reflect.DeepEqual(got, want) {
			t.Errorf("prefix 2026/: %v, want %v", got, want)
		}
		list, _ = s.ListObjects(ctx, "photos", "2026", "", 100)
		if got, want := keysOf(list), []string{"2026", "2026/a", "2026/b"}; !reflect.DeepEqual(got, want) {
			t.Errorf("prefix 2026: %v, want %v (the bare key matches its own prefix)", got, want)
		}
	})

	t.Run("list pages with after and limit", func(t *testing.T) {
		s := newStore(t)
		for _, k := range []string{"a", "b", "c", "d", "e"} {
			s.PutObject(ctx, obj("photos", k, "blob"))
		}
		page1, _ := s.ListObjects(ctx, "photos", "", "", 2)
		if got, want := keysOf(page1), []string{"a", "b"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("page 1 = %v, want %v", got, want)
		}
		page2, _ := s.ListObjects(ctx, "photos", "", "b", 2)
		if got, want := keysOf(page2), []string{"c", "d"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("page 2 after b = %v, want %v (after is exclusive)", got, want)
		}
		page3, _ := s.ListObjects(ctx, "photos", "", "d", 2)
		if got, want := keysOf(page3), []string{"e"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("page 3 after d = %v, want %v", got, want)
		}
		empty, _ := s.ListObjects(ctx, "photos", "", "e", 2)
		if len(empty) != 0 {
			t.Errorf("page after the last key = %v, want empty", keysOf(empty))
		}
	})

	t.Run("list of an empty or unknown bucket is empty, not an error", func(t *testing.T) {
		s := newStore(t)
		list, err := s.ListObjects(ctx, "nothing-here", "", "", 10)
		if err != nil || len(list) != 0 {
			t.Errorf("ListObjects on unknown bucket = %v, %v; want empty, nil", list, err)
		}
	})

	t.Run("returned metadata is a copy", func(t *testing.T) {
		s := newStore(t)
		s.PutObject(ctx, obj("photos", "a.txt", "blob-1"))
		got, _ := s.GetObject(ctx, "photos", "a.txt")
		got.Metadata["title"] = "mutated"
		again, _ := s.GetObject(ctx, "photos", "a.txt")
		if again.Metadata["title"] != "t" {
			t.Errorf("mutating a returned map changed the store: %v", again.Metadata)
		}
	})
}

func keysOf(list []tunna.Object) []string {
	out := make([]string, 0, len(list))
	for _, o := range list {
		out = append(out, o.Key)
	}
	return out
}
