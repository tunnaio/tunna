package memory_test

import (
	"testing"

	"github.com/tunnaio/tunna"
	"github.com/tunnaio/tunna/internal/memory"
	"github.com/tunnaio/tunna/internal/storetest"
)

func TestUploadStore(t *testing.T) {
	storetest.UploadStore(t, func(t *testing.T) (tunna.UploadStore, tunna.ObjectStore) {
		objects := memory.NewObjectStore(nil)
		return memory.NewUploadStore(nil, objects), objects
	})
}
