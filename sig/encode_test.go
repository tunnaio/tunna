package sig_test

import (
	"encoding/json"
	"net/url"
	"os"
	"testing"

	"github.com/tunnaio/tunna/sig"
)

// The encoding vectors in spec/vectors/encoding.json are the definition of
// correct. Every case runs through the function for its kind and must match
// byte for byte.

type encodingVectors struct {
	Spec  string `json:"spec"`
	Cases []struct {
		Name     string          `json:"name"`
		Kind     string          `json:"kind"`
		Input    json.RawMessage `json:"input"`
		Expected string          `json:"expected"`
		Note     string          `json:"note"`
	} `json:"cases"`
}

func TestEncodingVectors(t *testing.T) {
	raw, err := os.ReadFile("../spec/vectors/encoding.json")
	if err != nil {
		t.Fatalf("reading vectors: %v", err)
	}
	var v encodingVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("parsing vectors: %v", err)
	}
	if len(v.Cases) == 0 {
		t.Fatal("no cases")
	}

	for _, c := range v.Cases {
		t.Run(c.Name, func(t *testing.T) {
			var got string
			switch c.Kind {
			case "segment":
				var in string
				mustUnmarshal(t, c.Input, &in)
				got = sig.EncodeSegment(in)
			case "path":
				var in []string
				mustUnmarshal(t, c.Input, &in)
				got = sig.EncodePath(in)
			case "query":
				var pairs [][2]string
				mustUnmarshal(t, c.Input, &pairs)
				params := url.Values{}
				for _, p := range pairs {
					params.Add(p[0], p[1])
				}
				got = sig.EncodeQuery(params)
			default:
				t.Fatalf("unknown kind %q", c.Kind)
			}
			if got != c.Expected {
				t.Errorf("got  %q\nwant %q", got, c.Expected)
				if c.Note != "" {
					t.Logf("note: %s", c.Note)
				}
			}
		})
	}
}

func mustUnmarshal(t *testing.T, raw json.RawMessage, into any) {
	t.Helper()
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("bad input %s: %v", raw, err)
	}
}
