package main

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tunnaio/tunna/sig"
)

// The binary is built and spawned the way an SDK's conformance test would
// spawn it: read the URL from stdout, reset before a case, replay over the
// wire with a real signer. This is the contract a non-Go runner relies on.

func startFixtures(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "tunna-fixtures.exe")
	buildCmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, "-fixtures", filepath.Join("..", "..", "spec", "conformance", "fixtures.json"))
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		cmd.Wait()
	})

	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("reading the URL line: %v", err)
	}
	url := strings.TrimSpace(line)
	if !strings.HasPrefix(url, "http://") {
		t.Fatalf("first stdout line = %q, want a URL", url)
	}
	return url
}

func signed(t *testing.T, method, url, path string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	key := sig.Key{ID: "tk_alice", Secret: "test-secret-alice-not-real"}
	r := sig.Request{Method: method, Path: strings.Split(strings.Trim(path, "/"), "/")}
	req.Header.Set("X-Tunna-Date", strconv.FormatInt(now, 10))
	req.Header.Set("Authorization", sig.Authorization(r, key, now))
	return req
}

func TestFixtureServerServesResetsAndIsolates(t *testing.T) {
	url := startFixtures(t)
	client := &http.Client{Timeout: 5 * time.Second}

	// The fixture state is there: a public object, readable anonymously.
	resp, err := client.Get(url + "/public-site/index.html")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "tunna") {
		t.Fatalf("GET fixture object: %d %q", resp.StatusCode, body)
	}

	// A signed mutation with a fixture key: delete the object.
	resp, err = client.Do(signed(t, http.MethodDelete, url, "/public-site/index.html"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Fatalf("DELETE with alice: %d, want 204", resp.StatusCode)
	}
	resp, _ = client.Get(url + "/public-site/index.html")
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("GET after delete: %d, want 404", resp.StatusCode)
	}

	// Reset restores the fixture state, so the next case starts clean.
	resp, err = client.Post(url+"/reset", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Fatalf("POST /reset: %d, want 204", resp.StatusCode)
	}
	resp, _ = client.Get(url + "/public-site/index.html")
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET after reset: %d, want 200; reset did not restore the fixtures", resp.StatusCode)
	}

	// The control route is outside the API: the API's own routing still
	// answers for everything else under the same origin.
	resp, _ = client.Get(url + "/-/health")
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("GET /-/health through the fixture server: %d", resp.StatusCode)
	}
	// Only POST /reset is the control route; a GET falls through to the API,
	// where "reset" is just a bucket name that does not exist, so the control
	// plane is invisible otherwise.
	resp, _ = client.Get(url + "/reset")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 404 || !strings.Contains(string(body), "bucket_not_found") {
		t.Errorf("GET /reset: %d %s, want the API's bucket_not_found", resp.StatusCode, body)
	}
}
