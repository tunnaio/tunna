package disk_test

import (
	"testing"

	"github.com/tunnaio/tunna"
	"github.com/tunnaio/tunna/internal/disk"
	"github.com/tunnaio/tunna/internal/storetest"
)

func TestBlobStore(t *testing.T) {
	storetest.BlobStore(t, func(t *testing.T) tunna.BlobStore {
		s, err := disk.New(t.TempDir())
		if err != nil {
			t.Fatalf("disk.New: %v", err)
		}
		return s
	})
}
