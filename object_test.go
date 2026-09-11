package tunna_test

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/tunnaio/tunna"
)

// spec/vectors/object-keys.json is the definition of a valid object key.
// Cases given as hex are byte sequences JSON cannot carry, such as invalid
// UTF-8; a Go string holds arbitrary bytes, so they convert directly.

type objectKeyVectors struct {
	Spec  string `json:"spec"`
	Cases []struct {
		Key    *string `json:"key"`
		Hex    *string `json:"hex"`
		Valid  bool    `json:"valid"`
		Reason string  `json:"reason"`
	} `json:"cases"`
}

func TestValidateObjectKeyVectors(t *testing.T) {
	raw, err := os.ReadFile("spec/vectors/object-keys.json")
	if err != nil {
		t.Fatalf("reading vectors: %v", err)
	}
	var v objectKeyVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("parsing vectors: %v", err)
	}
	if len(v.Cases) == 0 {
		t.Fatal("no cases")
	}

	for i, c := range v.Cases {
		var key string
		switch {
		case c.Key != nil:
			key = *c.Key
		case c.Hex != nil:
			b, err := hex.DecodeString(*c.Hex)
			if err != nil {
				t.Fatalf("case %d: bad hex: %v", i, err)
			}
			key = string(b)
		default:
			t.Fatalf("case %d has neither key nor hex", i)
		}
		name := key
		if len(name) > 24 {
			name = name[:24] + "..."
		}
		t.Run(name, func(t *testing.T) {
			err := tunna.ValidateObjectKey(key)
			switch {
			case c.Valid && err != nil:
				t.Errorf("%q should be valid, got: %v", key, err)
			case !c.Valid && err == nil:
				t.Errorf("%q should be invalid (%s), got nil", key, c.Reason)
			case !c.Valid && !errors.Is(err, tunna.ErrInvalid):
				t.Errorf("%q: error %v does not wrap ErrInvalid", key, err)
			}
		})
	}
}
