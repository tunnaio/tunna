package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tunnaio/tunna"
)

// BucketStoreFactory builds a fresh, empty store. Cleanup, if any, is
// registered with t.Cleanup.
type BucketStoreFactory func(t *testing.T) tunna.BucketStore

// BucketStore runs the BucketStore contract against the store the factory
// builds. Name validation is not part of the contract: stores trust their
// callers, and the rule is tested against its vectors in the root package.
func BucketStore(t *testing.T, newStore BucketStoreFactory) {
	t.Helper()
	ctx := context.Background()
	at := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	t.Run("create then get returns every field", func(t *testing.T) {
		s := newStore(t)
		want := tunna.Bucket{Name: "photos", Public: true, CreatedAt: at}
		if err := s.CreateBucket(ctx, want); err != nil {
			t.Fatalf("CreateBucket: %v", err)
		}
		got, err := s.GetBucket(ctx, "photos")
		if err != nil {
			t.Fatalf("GetBucket: %v", err)
		}
		if got != want {
			t.Errorf("GetBucket = %+v, want %+v", got, want)
		}
	})

	t.Run("get unknown is ErrNotFound with a zero bucket", func(t *testing.T) {
		s := newStore(t)
		got, err := s.GetBucket(ctx, "nope")
		if !errors.Is(err, tunna.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if got != (tunna.Bucket{}) {
			t.Errorf("bucket on not-found = %+v, want zero value", got)
		}
	})

	t.Run("create twice is ErrConflict and keeps the first", func(t *testing.T) {
		s := newStore(t)
		first := tunna.Bucket{Name: "photos", Public: false, CreatedAt: at}
		if err := s.CreateBucket(ctx, first); err != nil {
			t.Fatalf("CreateBucket: %v", err)
		}
		second := tunna.Bucket{Name: "photos", Public: true, CreatedAt: at.Add(time.Hour)}
		if err := s.CreateBucket(ctx, second); !errors.Is(err, tunna.ErrConflict) {
			t.Fatalf("second CreateBucket err = %v, want ErrConflict", err)
		}
		got, _ := s.GetBucket(ctx, "photos")
		if got != first {
			t.Errorf("after conflict, GetBucket = %+v, want the first %+v", got, first)
		}
	})

	t.Run("list is ordered by name and empty when empty", func(t *testing.T) {
		s := newStore(t)
		list, err := s.ListBuckets(ctx)
		if err != nil {
			t.Fatalf("ListBuckets: %v", err)
		}
		if len(list) != 0 {
			t.Fatalf("empty store listed %d buckets", len(list))
		}
		for _, name := range []string{"zeta", "alpha", "mid"} {
			if err := s.CreateBucket(ctx, tunna.Bucket{Name: name, CreatedAt: at}); err != nil {
				t.Fatalf("CreateBucket %s: %v", name, err)
			}
		}
		list, err = s.ListBuckets(ctx)
		if err != nil {
			t.Fatalf("ListBuckets: %v", err)
		}
		got := make([]string, len(list))
		for i, b := range list {
			got[i] = b.Name
		}
		want := []string{"alpha", "mid", "zeta"}
		if len(got) != len(want) {
			t.Fatalf("ListBuckets names = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("ListBuckets names = %v, want %v", got, want)
			}
		}
	})

	t.Run("list returns a copy the caller may modify", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateBucket(ctx, tunna.Bucket{Name: "photos", CreatedAt: at}); err != nil {
			t.Fatalf("CreateBucket: %v", err)
		}
		list, _ := s.ListBuckets(ctx)
		list[0].Name = "mutated"
		got, err := s.GetBucket(ctx, "photos")
		if err != nil || got.Name != "photos" {
			t.Errorf("modifying the listed slice changed the store: %+v, %v", got, err)
		}
	})

	t.Run("delete removes and then get is ErrNotFound", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateBucket(ctx, tunna.Bucket{Name: "photos", CreatedAt: at}); err != nil {
			t.Fatalf("CreateBucket: %v", err)
		}
		if err := s.DeleteBucket(ctx, "photos"); err != nil {
			t.Fatalf("DeleteBucket: %v", err)
		}
		if _, err := s.GetBucket(ctx, "photos"); !errors.Is(err, tunna.ErrNotFound) {
			t.Errorf("after delete, GetBucket err = %v, want ErrNotFound", err)
		}
		list, _ := s.ListBuckets(ctx)
		if len(list) != 0 {
			t.Errorf("after delete, ListBuckets = %v, want empty", list)
		}
	})

	t.Run("delete unknown is ErrNotFound", func(t *testing.T) {
		s := newStore(t)
		if err := s.DeleteBucket(ctx, "nope"); !errors.Is(err, tunna.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("delete then create again succeeds", func(t *testing.T) {
		s := newStore(t)
		b := tunna.Bucket{Name: "photos", CreatedAt: at}
		if err := s.CreateBucket(ctx, b); err != nil {
			t.Fatalf("CreateBucket: %v", err)
		}
		if err := s.DeleteBucket(ctx, "photos"); err != nil {
			t.Fatalf("DeleteBucket: %v", err)
		}
		if err := s.CreateBucket(ctx, b); err != nil {
			t.Errorf("re-create after delete: %v", err)
		}
	})
	t.Run("update replaces public and keeps created_at", func(t *testing.T) {
		s := newStore(t)
		if err := s.CreateBucket(ctx, tunna.Bucket{Name: "photos", Public: false, CreatedAt: at}); err != nil {
			t.Fatalf("CreateBucket: %v", err)
		}
		changed := tunna.Bucket{Name: "photos", Public: true, CreatedAt: at.Add(time.Hour)} // CreatedAt must be ignored
		if err := s.UpdateBucket(ctx, changed); err != nil {
			t.Fatalf("UpdateBucket: %v", err)
		}
		got, err := s.GetBucket(ctx, "photos")
		if err != nil {
			t.Fatalf("GetBucket: %v", err)
		}
		if !got.Public {
			t.Error("Public = false after update to true")
		}
		if !got.CreatedAt.Equal(at) {
			t.Errorf("CreatedAt = %v, want the original %v; update must not touch it", got.CreatedAt, at)
		}
	})

	t.Run("update unknown is ErrNotFound and creates nothing", func(t *testing.T) {
		s := newStore(t)
		if err := s.UpdateBucket(ctx, tunna.Bucket{Name: "ghost", Public: true, CreatedAt: at}); !errors.Is(err, tunna.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if _, err := s.GetBucket(ctx, "ghost"); !errors.Is(err, tunna.ErrNotFound) {
			t.Error("UpdateBucket of an unknown name created it")
		}
	})

}
