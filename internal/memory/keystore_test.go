package memory_test

import (
	"testing"

	"github.com/tunnaio/tunna"
	"github.com/tunnaio/tunna/internal/memory"
	"github.com/tunnaio/tunna/internal/storetest"
)

func TestKeyStore(t *testing.T) {
	storetest.KeyStore(t, func(t *testing.T, keys []tunna.APIKey) tunna.KeyStore {
		return memory.NewKeyStore(keys)
	})
}
