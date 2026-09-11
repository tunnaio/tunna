package memory_test

import (
	"testing"

	"github.com/tunnaio/tunna"
	"github.com/tunnaio/tunna/internal/memory"
	"github.com/tunnaio/tunna/internal/storetest"
)

func TestObjectStore(t *testing.T) {
	storetest.ObjectStore(t, func(t *testing.T) tunna.ObjectStore {
		return memory.NewObjectStore(nil)
	})
}
