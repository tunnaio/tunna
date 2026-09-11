package httpapi

// Internal test: the Go code table must agree with spec/errors.json in both
// directions. The JSON is the authority; this file exists so the Go copy
// cannot drift from it.

import (
	"encoding/json"
	"os"
	"testing"
)

func TestErrorTableMatchesSpec(t *testing.T) {
	raw, err := os.ReadFile("../../spec/errors.json")
	if err != nil {
		t.Fatalf("reading errors.json: %v", err)
	}
	var spec struct {
		Errors []struct {
			Code   string `json:"code"`
			Status int    `json:"status"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("parsing errors.json: %v", err)
	}

	inSpec := map[code]int{}
	for _, e := range spec.Errors {
		inSpec[code(e.Code)] = e.Status
	}

	// Every code the server can send must be in the spec with the same status.
	for c, status := range statusOf {
		want, ok := inSpec[c]
		if !ok {
			t.Errorf("code %q is in statusOf but not in spec/errors.json", c)
			continue
		}
		if status != want {
			t.Errorf("code %q: statusOf says %d, spec/errors.json says %d", c, status, want)
		}
	}

	// Every code in the spec must be one the server can send. A code the
	// server cannot produce is either dead spec or unimplemented behaviour,
	// and both should be visible.
	for c := range inSpec {
		if _, ok := statusOf[c]; !ok {
			t.Errorf("code %q is in spec/errors.json but not in statusOf", c)
		}
	}
}
