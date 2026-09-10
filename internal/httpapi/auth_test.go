package httpapi

// Internal test (package httpapi, not httpapi_test) so it can reach the
// unexported sigRequest. This is the ADR-0004 probe: does the server
// canonicalize from the raw request the way the spec and sig assume?

import (
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestSigRequestDecodesFromRawForms(t *testing.T) {
	cases := []struct {
		name      string
		target    string
		wantPath  []string
		wantQuery map[string][]string
		wantErr   bool
	}{
		{
			name:     "root",
			target:   "/",
			wantPath: nil,
		},
		{
			name:     "plain segments",
			target:   "/photos/hello.txt",
			wantPath: []string{"photos", "hello.txt"},
		},
		{
			name:     "encoded slash stays inside one segment",
			target:   "/photos/dir%2Ffile.txt",
			wantPath: []string{"photos", "dir/file.txt"},
		},
		{
			name:     "unicode and space in a segment",
			target:   "/photos/%C3%A5r%202026",
			wantPath: []string{"photos", "år 2026"},
		},
		{
			name:      "literal plus in the query is a plus, not a space",
			target:    "/b?q=a+b",
			wantPath:  []string{"b"},
			wantQuery: map[string][]string{"q": {"a+b"}},
		},
		{
			name:      "encoded plus in the query is a plus",
			target:    "/b?q=a%2Bb",
			wantPath:  []string{"b"},
			wantQuery: map[string][]string{"q": {"a+b"}},
		},
		{
			name:      "empty value keeps its name",
			target:    "/b?list&prefix=",
			wantPath:  []string{"b"},
			wantQuery: map[string][]string{"list": {""}, "prefix": {""}},
		},
		{
			name:      "repeated names keep every value",
			target:    "/b?k=1&k=2",
			wantPath:  []string{"b"},
			wantQuery: map[string][]string{"k": {"1", "2"}},
		},
		{
			name:      "signature parameter is excluded",
			target:    "/b?x-tunna-key=tk&x-tunna-sig=abc",
			wantPath:  []string{"b"},
			wantQuery: map[string][]string{"x-tunna-key": {"tk"}},
		},
		// A bad escape in the path never reaches sigRequest: url.Parse rejects
		// it, and Go's HTTP server answers 400 before any handler runs. The
		// query is not validated at parse time, so that branch is reachable.
		{
			name:    "bad percent escape in query is an error",
			target:  "/b?q=%zz",
			wantErr: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", c.target, nil)
			got, err := sigRequest(r, nil)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("sigRequest: %v", err)
			}
			if !reflect.DeepEqual(got.Path, c.wantPath) {
				t.Errorf("path = %q, want %q", got.Path, c.wantPath)
			}
			wantQuery := c.wantQuery
			if wantQuery == nil {
				wantQuery = map[string][]string{}
			}
			if !reflect.DeepEqual(map[string][]string(got.Query), wantQuery) {
				t.Errorf("query = %v, want %v", got.Query, wantQuery)
			}
		})
	}
}
