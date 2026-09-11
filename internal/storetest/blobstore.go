package storetest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/tunnaio/tunna"
)

// BlobStoreFactory builds a fresh, empty blob store. Cleanup, if any, is
// registered with t.Cleanup.
type BlobStoreFactory func(t *testing.T) tunna.BlobStore

// BlobStore runs the BlobStore contract against the store the factory builds.
// The contract is about bytes and ids only; checksums, metadata and keys are
// the caller's business.
func BlobStore(t *testing.T, newStore BlobStoreFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("create returns a fresh 32-hex id each time", func(t *testing.T) {
		s := newStore(t)
		seen := map[string]bool{}
		for i := 0; i < 5; i++ {
			id, err := s.Create(ctx)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" {
				t.Errorf("id %q is not 32 lowercase hex characters", id)
			}
			if seen[id] {
				t.Errorf("id %q returned twice", id)
			}
			seen[id] = true
		}
	})

	t.Run("write then open reads the same bytes", func(t *testing.T) {
		s := newStore(t)
		id, _ := s.Create(ctx)
		n, err := s.Write(ctx, id, 0, strings.NewReader("hello, tunna"))
		if err != nil {
			t.Fatalf("Write: %v", err)
		}
		if n != 12 {
			t.Errorf("Write n = %d, want 12", n)
		}
		if err := s.Sync(ctx, id); err != nil {
			t.Fatalf("Sync: %v", err)
		}
		r, err := s.Open(ctx, id)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer r.Close()
		got, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if string(got) != "hello, tunna" {
			t.Errorf("read back %q, want hello, tunna", got)
		}
	})

	t.Run("a new blob is empty", func(t *testing.T) {
		s := newStore(t)
		id, _ := s.Create(ctx)
		r, err := s.Open(ctx, id)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer r.Close()
		got, _ := io.ReadAll(r)
		if len(got) != 0 {
			t.Errorf("new blob has %d bytes, want 0", len(got))
		}
	})

	t.Run("writes at offset in any order compose the whole", func(t *testing.T) {
		s := newStore(t)
		id, _ := s.Create(ctx)
		// Three "parts" of size 4, written last first.
		parts := []string{"aaaa", "bbbb", "cc"}
		for i := len(parts) - 1; i >= 0; i-- {
			if _, err := s.Write(ctx, id, int64(i*4), strings.NewReader(parts[i])); err != nil {
				t.Fatalf("Write part %d: %v", i, err)
			}
		}
		r, _ := s.Open(ctx, id)
		defer r.Close()
		got, _ := io.ReadAll(r)
		if string(got) != "aaaabbbbcc" {
			t.Errorf("composed = %q, want aaaabbbbcc", got)
		}
	})

	t.Run("rewriting an offset replaces those bytes", func(t *testing.T) {
		s := newStore(t)
		id, _ := s.Create(ctx)
		s.Write(ctx, id, 0, strings.NewReader("aaaabbbb"))
		s.Write(ctx, id, 4, strings.NewReader("XXXX"))
		r, _ := s.Open(ctx, id)
		defer r.Close()
		got, _ := io.ReadAll(r)
		if string(got) != "aaaaXXXX" {
			t.Errorf("after rewrite = %q, want aaaaXXXX", got)
		}
	})

	t.Run("open supports seeking for range reads", func(t *testing.T) {
		s := newStore(t)
		id, _ := s.Create(ctx)
		s.Write(ctx, id, 0, strings.NewReader("0123456789"))
		r, _ := s.Open(ctx, id)
		defer r.Close()
		if _, err := r.Seek(3, io.SeekStart); err != nil {
			t.Fatalf("Seek: %v", err)
		}
		buf := make([]byte, 4)
		if _, err := io.ReadFull(r, buf); err != nil {
			t.Fatalf("read after seek: %v", err)
		}
		if string(buf) != "3456" {
			t.Errorf("bytes 3..7 = %q, want 3456", buf)
		}
	})

	t.Run("large write round-trips", func(t *testing.T) {
		s := newStore(t)
		id, _ := s.Create(ctx)
		data := bytes.Repeat([]byte("0123456789abcdef"), 1<<16) // 1 MiB
		n, err := s.Write(ctx, id, 0, bytes.NewReader(data))
		if err != nil || n != int64(len(data)) {
			t.Fatalf("Write: n=%d err=%v", n, err)
		}
		r, _ := s.Open(ctx, id)
		defer r.Close()
		got, _ := io.ReadAll(r)
		if !bytes.Equal(got, data) {
			t.Errorf("1 MiB round-trip differs (len %d vs %d)", len(got), len(data))
		}
	})

	t.Run("open unknown id is ErrNotFound", func(t *testing.T) {
		s := newStore(t)
		_, err := s.Open(ctx, "00000000000000000000000000000000")
		if err == nil {
			t.Fatal("Open of an unknown id succeeded")
		}
		if !isNotFound(err) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("write to unknown id is ErrNotFound", func(t *testing.T) {
		s := newStore(t)
		_, err := s.Write(ctx, "00000000000000000000000000000000", 0, strings.NewReader("x"))
		if !isNotFound(err) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("remove then open is ErrNotFound; removing again is not an error", func(t *testing.T) {
		s := newStore(t)
		id, _ := s.Create(ctx)
		s.Write(ctx, id, 0, strings.NewReader("bye"))
		if err := s.Remove(ctx, id); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		if _, err := s.Open(ctx, id); !isNotFound(err) {
			t.Errorf("Open after Remove err = %v, want ErrNotFound", err)
		}
		if err := s.Remove(ctx, id); err != nil {
			t.Errorf("second Remove err = %v, want nil (idempotent)", err)
		}
	})

	t.Run("a reader opened before remove keeps reading (ADR-0007 overwrite)", func(t *testing.T) {
		s := newStore(t)
		id, _ := s.Create(ctx)
		s.Write(ctx, id, 0, strings.NewReader("old bytes"))
		r, err := s.Open(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		if err := s.Remove(ctx, id); err != nil {
			t.Fatalf("Remove while a reader is open: %v (on Windows this needs delete sharing)", err)
		}
		got, err := io.ReadAll(r)
		if err != nil || string(got) != "old bytes" {
			t.Errorf("read after remove = %q, %v; want old bytes", got, err)
		}
	})

	t.Run("ids are rejected if they are not the store's own shape", func(t *testing.T) {
		s := newStore(t)
		for _, bad := range []string{"", "..", "../../etc/passwd", "ZZ", "abc/def", strings.Repeat("a", 31)} {
			if _, err := s.Open(ctx, bad); err == nil {
				t.Errorf("Open(%q) succeeded; ids must be validated, never used as paths", bad)
			}
		}
	})
}

func isNotFound(err error) bool {
	return errors.Is(err, tunna.ErrNotFound)
}
