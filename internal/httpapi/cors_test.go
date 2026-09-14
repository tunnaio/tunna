package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tunnaio/tunna"
	"github.com/tunnaio/tunna/internal/httpapi"
)

// The conformance fixtures pin one allowlist. The two configurations they
// cannot express are covered here: "*" and CORS off (ADR-0009).

func corsServer(t *testing.T, allow []string) *httptest.Server {
	t.Helper()
	origins, err := tunna.ParseOrigins(allow)
	if err != nil {
		t.Fatalf("ParseOrigins: %v", err)
	}
	srv := httptest.NewServer(httpapi.New(httpapi.Options{ServerVersion: "test", CORSOrigins: origins}))
	t.Cleanup(srv.Close)
	return srv
}

func withOrigin(t *testing.T, srv *httptest.Server, method, origin string, preflight bool) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+"/-/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", origin)
	if preflight {
		req.Header.Set("Access-Control-Request-Method", "GET")
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestCORSStarAnswersStarWithoutVary(t *testing.T) {
	srv := corsServer(t, []string{"*"})

	resp := withOrigin(t, srv, http.MethodGet, "https://anything.example.test", false)
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
	}
	if _, ok := resp.Header["Vary"]; ok {
		t.Errorf("Vary present on a * answer; the same answer goes to everyone, so caches need not vary")
	}

	pre := withOrigin(t, srv, http.MethodOptions, "null", true)
	if pre.StatusCode != http.StatusNoContent {
		t.Errorf("preflight status = %d, want 204", pre.StatusCode)
	}
	if got := pre.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("preflight from a null origin: Access-Control-Allow-Origin = %q, want *", got)
	}
}

func TestCORSOffWritesNothing(t *testing.T) {
	srv := corsServer(t, nil)

	resp := withOrigin(t, srv, http.MethodGet, "https://app.example.test", false)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200; CORS off must not change the response", resp.StatusCode)
	}
	for _, h := range []string{"Access-Control-Allow-Origin", "Access-Control-Expose-Headers", "Vary"} {
		if _, ok := resp.Header[h]; ok {
			t.Errorf("%s present with CORS off", h)
		}
	}

	pre := withOrigin(t, srv, http.MethodOptions, "https://app.example.test", true)
	if pre.StatusCode != http.StatusNoContent {
		t.Errorf("preflight status = %d, want a bare 204 even when off", pre.StatusCode)
	}
	if _, ok := pre.Header["Access-Control-Allow-Origin"]; ok {
		t.Error("preflight with CORS off carried Access-Control-Allow-Origin")
	}
}
