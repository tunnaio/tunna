package tunna_test

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/tunnaio/tunna"
)

// spec/vectors/bucket-names.json is the definition of a valid bucket name.

type bucketNameVectors struct {
	Spec  string `json:"spec"`
	Cases []struct {
		Name   string `json:"name"`
		Valid  bool   `json:"valid"`
		Reason string `json:"reason"`
	} `json:"cases"`
}

func TestValidateBucketNameVectors(t *testing.T) {
	raw, err := os.ReadFile("spec/vectors/bucket-names.json")
	if err != nil {
		t.Fatalf("reading vectors: %v", err)
	}
	var v bucketNameVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("parsing vectors: %v", err)
	}
	if len(v.Cases) == 0 {
		t.Fatal("no cases")
	}

	for _, c := range v.Cases {
		t.Run(c.Name, func(t *testing.T) {
			err := tunna.ValidateBucketName(c.Name)
			switch {
			case c.Valid && err != nil:
				t.Errorf("%q should be valid, got: %v", c.Name, err)
			case !c.Valid && err == nil:
				t.Errorf("%q should be invalid (%s), got nil", c.Name, c.Reason)
			case !c.Valid && !errors.Is(err, tunna.ErrInvalid):
				t.Errorf("%q: error %v does not wrap ErrInvalid", c.Name, err)
			}
		})
	}
}
