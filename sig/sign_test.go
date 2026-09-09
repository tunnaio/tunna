package sig_test

import (
	"encoding/json"
	"net/url"
	"os"
	"testing"

	"github.com/tunnaio/tunna/sig"
)

// The signing vectors in spec/vectors/signing.json define a correct signer.
// For each case: the canonical string must match first (no cryptography
// involved), then the signature, then the header value or presign query.

type signingVectors struct {
	Spec  string `json:"spec"`
	Cases []struct {
		Name string `json:"name"`
		Note string `json:"note"`
		Mode string `json:"mode"`
		Key  struct {
			ID     string `json:"id"`
			Secret string `json:"secret"`
		} `json:"key"`
		Request struct {
			Method        string            `json:"method"`
			Path          []string          `json:"path"`
			Query         [][2]string       `json:"query"`
			Headers       map[string]string `json:"headers"`
			SignedHeaders []string          `json:"signed_headers"`
		} `json:"request"`
		Time     int64 `json:"time"`
		Expected struct {
			Canonical     string `json:"canonical"`
			Signature     string `json:"signature"`
			Authorization string `json:"authorization"`
			Query         string `json:"query"`
		} `json:"expected"`
	} `json:"cases"`
}

func TestSigningVectors(t *testing.T) {
	raw, err := os.ReadFile("../spec/vectors/signing.json")
	if err != nil {
		t.Fatalf("reading vectors: %v", err)
	}
	var v signingVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("parsing vectors: %v", err)
	}
	if len(v.Cases) == 0 {
		t.Fatal("no cases")
	}

	for _, c := range v.Cases {
		t.Run(c.Name, func(t *testing.T) {
			key := sig.Key{ID: c.Key.ID, Secret: c.Key.Secret}
			req := sig.Request{
				Method:        c.Request.Method,
				Path:          c.Request.Path,
				Headers:       c.Request.Headers,
				SignedHeaders: c.Request.SignedHeaders,
			}
			if len(c.Request.Query) > 0 {
				req.Query = url.Values{}
				for _, p := range c.Request.Query {
					req.Query.Add(p[0], p[1])
				}
			}

			var mode sig.Mode
			switch c.Mode {
			case "header":
				mode = sig.Header
			case "presign":
				mode = sig.Presign
			default:
				t.Fatalf("unknown mode %q", c.Mode)
			}

			canonical := sig.Canonical(req, key, c.Time, mode)
			if canonical != c.Expected.Canonical {
				t.Errorf("canonical:\n got %q\nwant %q", canonical, c.Expected.Canonical)
				if c.Note != "" {
					t.Logf("note: %s", c.Note)
				}
				return // later checks would only repeat the same fault
			}

			if got := sig.Signature(key.Secret, canonical); got != c.Expected.Signature {
				t.Errorf("signature:\n got %s\nwant %s", got, c.Expected.Signature)
				return
			}

			switch mode {
			case sig.Header:
				if got := sig.Authorization(req, key, c.Time); got != c.Expected.Authorization {
					t.Errorf("authorization:\n got %q\nwant %q", got, c.Expected.Authorization)
				}
			case sig.Presign:
				if got := sig.PresignQuery(req, key, c.Time); got != c.Expected.Query {
					t.Errorf("presign query:\n got %q\nwant %q", got, c.Expected.Query)
				}
			}
		})
	}
}
