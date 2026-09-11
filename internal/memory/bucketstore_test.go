package memory_test

import (
	"testing"

	"github.com/tunnaio/tunna"
	"github.com/tunnaio/tunna/internal/memory"
	"github.com/tunnaio/tunna/internal/storetest"
)

func TestBucketStore(t *testing.T) {
	storetest.BucketStore(t, func(t *testing.T) tunna.BucketStore {
		return memory.NewBucketStore(nil)
	})
}
