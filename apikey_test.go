package tunna_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/tunnaio/tunna"
)

// spec/vectors/authorization.json is the definition of the stage-3 rule.

type authorizationVectors struct {
	Spec  string `json:"spec"`
	Cases []struct {
		Name string `json:"name"`
		Key  struct {
			Admin  bool              `json:"admin"`
			Scopes map[string]string `json:"scopes"`
		} `json:"key"`
		Level   string `json:"level"`
		Bucket  string `json:"bucket"`
		Allowed bool   `json:"allowed"`
		Reason  string `json:"reason"`
	} `json:"cases"`
}

func TestAllowsVectors(t *testing.T) {
	raw, err := os.ReadFile("spec/vectors/authorization.json")
	if err != nil {
		t.Fatalf("reading vectors: %v", err)
	}
	var v authorizationVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("parsing vectors: %v", err)
	}
	if len(v.Cases) == 0 {
		t.Fatal("no cases")
	}

	for _, c := range v.Cases {
		t.Run(c.Name, func(t *testing.T) {
			key := tunna.APIKey{Admin: c.Key.Admin}
			if c.Key.Scopes != nil {
				key.Scopes = map[string]tunna.Access{}
				for bucket, level := range c.Key.Scopes {
					key.Scopes[bucket] = tunna.Access(level)
				}
			}
			got := key.Allows(tunna.Access(c.Level), c.Bucket)
			if got != c.Allowed {
				t.Errorf("Allows(%s, %q) = %v, want %v (%s)", c.Level, c.Bucket, got, c.Allowed, c.Reason)
			}
		})
	}
}

func TestAccessIsValid(t *testing.T) {
	for _, ok := range []tunna.Access{tunna.Read, tunna.Write} {
		if !ok.Valid() {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []tunna.Access{"", "READ", "admin", "rw", "readwrite"} {
		if bad.Valid() {
			t.Errorf("%q should be invalid", bad)
		}
	}
}
