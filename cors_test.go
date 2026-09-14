package tunna_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/tunnaio/tunna"
)

// spec/vectors/cors.json is the definition of origin matching (ADR-0009).

type corsVectors struct {
	Cases []struct {
		Name    string   `json:"name"`
		Allow   []string `json:"allow"`
		Origin  string   `json:"origin"`
		Match   bool     `json:"match"`
		Value   string   `json:"value"`
		Invalid bool     `json:"invalid"`
		Reason  string   `json:"reason"`
	} `json:"cases"`
}

func TestOriginsVectors(t *testing.T) {
	raw, err := os.ReadFile("spec/vectors/cors.json")
	if err != nil {
		t.Fatalf("reading vectors: %v", err)
	}
	var v corsVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("parsing vectors: %v", err)
	}
	if len(v.Cases) == 0 {
		t.Fatal("no cases")
	}

	for _, c := range v.Cases {
		t.Run(c.Name, func(t *testing.T) {
			origins, err := tunna.ParseOrigins(c.Allow)
			if c.Invalid {
				if err == nil {
					t.Errorf("ParseOrigins(%q) accepted an invalid list (%s)", c.Allow, c.Reason)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseOrigins(%q): %v", c.Allow, err)
			}
			value, ok := origins.Match(c.Origin)
			if ok != c.Match {
				t.Fatalf("Match(%q) = %v, want %v (%s)", c.Origin, ok, c.Match, c.Reason)
			}
			if ok && value != c.Value {
				t.Errorf("Match(%q) value = %q, want %q (%s)", c.Origin, value, c.Value, c.Reason)
			}
		})
	}
}

func TestOriginsZeroValueMatchesNothing(t *testing.T) {
	// CORS off is the zero value: no configuration, no headers, ever.
	var off tunna.Origins
	if _, ok := off.Match("https://app.example.com"); ok {
		t.Error("zero Origins matched; CORS off must match nothing")
	}
	if _, ok := off.Match("null"); ok {
		t.Error("zero Origins matched null")
	}
}
