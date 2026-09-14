package main

// The one claim the memory stores could never make: state survives a
// restart. Built from the same constructors run() uses (ADR-0005, rule 7).

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tunnaio/tunna"
	"github.com/tunnaio/tunna/internal/disk"
	"github.com/tunnaio/tunna/internal/httpapi"
	"github.com/tunnaio/tunna/internal/sqlite"
	"github.com/tunnaio/tunna/sig"
)

func TestBucketSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "tunna.db")
	key := sig.Key{ID: "tk_test", Secret: "test-secret-not-real"}

	// First life: open, seed a key, create a bucket over HTTP.
	db, err := sqlite.Open(dbPath, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutKey(context.Background(), tunna.APIKey{ID: key.ID, Secret: key.Secret}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(httpapi.New(httpapi.Options{ServerVersion: "test", Keys: db, Buckets: db}))

	if status := signedRequest(t, srv, key, http.MethodPut, []string{"-", "buckets", "persist"}); status != http.StatusCreated {
		t.Fatalf("create: status %d, want 201", status)
	}

	srv.Close()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Second life: same file, fresh process state.
	db, err = sqlite.Open(dbPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	srv = httptest.NewServer(httpapi.New(httpapi.Options{ServerVersion: "test", Keys: db, Buckets: db}))
	defer srv.Close()

	if status := signedRequest(t, srv, key, http.MethodGet, []string{"-", "buckets", "persist"}); status != http.StatusOK {
		t.Fatalf("get after restart: status %d, want 200", status)
	}
	if status := signedRequest(t, srv, key, http.MethodGet, []string{"-", "buckets", "never-made"}); status != http.StatusNotFound {
		t.Fatalf("get of an absent bucket after restart: status %d, want 404", status)
	}
}

func signedRequest(t *testing.T, srv *httptest.Server, key sig.Key, method string, path []string) int {
	t.Helper()
	req := sig.Request{Method: method, Path: path}
	ts := time.Now().Unix()
	r, err := http.NewRequest(method, srv.URL+sig.EncodePath(path), nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("X-Tunna-Date", strconv.FormatInt(ts, 10))
	r.Header.Set("Authorization", sig.Authorization(req, key, ts))
	resp, err := srv.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestObjectSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "tunna.db")
	key := sig.Key{ID: "tk_test", Secret: "test-secret-not-real"}

	open := func() (*sqlite.DB, *disk.Store, *httptest.Server) {
		db, err := sqlite.Open(dbPath, nil)
		if err != nil {
			t.Fatal(err)
		}
		blobs, err := disk.New(dir)
		if err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(httpapi.New(httpapi.Options{
			ServerVersion: "test", Keys: db, Buckets: db, Objects: db, Blobs: blobs,
		}))
		return db, blobs, srv
	}

	// First life: key, bucket, object.
	db, _, srv := open()
	if err := db.PutKey(context.Background(), tunna.APIKey{ID: key.ID, Secret: key.Secret}); err != nil {
		t.Fatal(err)
	}
	if status := signedRequest(t, srv, key, http.MethodPut, []string{"-", "buckets", "persist"}); status != http.StatusCreated {
		t.Fatalf("create bucket: status %d", status)
	}
	status, _ := signedBody(t, srv, key, http.MethodPut, []string{"persist", "a/b.txt"}, "survives")
	if status != http.StatusCreated {
		t.Fatalf("put object: status %d", status)
	}
	srv.Close()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Second life: the object is there, bytes and metadata.
	db, _, srv = open()
	defer db.Close()
	defer srv.Close()
	status, body := signedBody(t, srv, key, http.MethodGet, []string{"persist", "a/b.txt"}, "")
	if status != http.StatusOK || body != "survives" {
		t.Fatalf("get after restart: status %d body %q, want 200 survives", status, body)
	}
}

// signedBody is signedRequest with a request body and the response body
// returned, for object routes.
func signedBody(t *testing.T, srv *httptest.Server, key sig.Key, method string, path []string, reqBody string) (int, string) {
	t.Helper()
	req := sig.Request{Method: method, Path: path}
	ts := time.Now().Unix()
	r, err := http.NewRequest(method, srv.URL+sig.EncodePath(path), strings.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("X-Tunna-Date", strconv.FormatInt(ts, 10))
	r.Header.Set("Authorization", sig.Authorization(req, key, ts))
	resp, err := srv.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// An upload session in progress survives a restart: parts sent before the
// restart are still recorded and their bytes still on disk, so the client
// resumes with query and finishes with complete. This is what migration 3
// exists for.
func TestUploadSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "tunna.db")
	key := sig.Key{ID: "tk_test", Secret: "test-secret-not-real"}
	const partSize = 5 << 20

	open := func() (*sqlite.DB, *httptest.Server) {
		db, err := sqlite.Open(dbPath, nil)
		if err != nil {
			t.Fatal(err)
		}
		blobs, err := disk.New(dir)
		if err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(httpapi.New(httpapi.Options{
			ServerVersion: "test", Keys: db, Buckets: db, Objects: db, Uploads: db, Blobs: blobs,
		}))
		return db, srv
	}

	// First life: initiate and send part 1 of 2.
	db, srv := open()
	if err := db.PutKey(context.Background(), tunna.APIKey{ID: key.ID, Secret: key.Secret}); err != nil {
		t.Fatal(err)
	}
	if status := signedRequest(t, srv, key, http.MethodPut, []string{"-", "buckets", "persist"}); status != http.StatusCreated {
		t.Fatalf("create bucket: status %d", status)
	}
	status, body := signedJSON(t, srv, key, http.MethodPost, []string{"-", "uploads"},
		`{"bucket":"persist","key":"big.bin","part_size":`+strconv.Itoa(partSize)+`}`)
	if status != http.StatusCreated {
		t.Fatalf("initiate: status %d body %s", status, body)
	}
	var session struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &session); err != nil || session.ID == "" {
		t.Fatalf("initiate body %q: %v", body, err)
	}
	part1 := bytes.Repeat([]byte{0xA5}, partSize)
	if status, _ := signedBody(t, srv, key, http.MethodPut, []string{"-", "uploads", session.ID, "parts", "1"}, string(part1)); status != http.StatusOK {
		t.Fatalf("part 1: status %d", status)
	}
	srv.Close()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Second life: the session still lists part 1; send part 2, complete, read.
	db, srv = open()
	defer db.Close()
	defer srv.Close()
	status, body = signedBody(t, srv, key, http.MethodGet, []string{"-", "uploads", session.ID}, "")
	if status != http.StatusOK || !strings.Contains(body, `"parts":[1]`) {
		t.Fatalf("query after restart: status %d body %s; want parts [1]", status, body)
	}
	if status, _ := signedBody(t, srv, key, http.MethodPut, []string{"-", "uploads", session.ID, "parts", "2"}, "tail"); status != http.StatusOK {
		t.Fatalf("part 2 after restart: status %d", status)
	}
	if status, body := signedJSON(t, srv, key, http.MethodPost, []string{"-", "uploads", session.ID, "complete"}, `{"parts":2}`); status != http.StatusCreated {
		t.Fatalf("complete: status %d body %s", status, body)
	}
	status, got := signedBody(t, srv, key, http.MethodGet, []string{"persist", "big.bin"}, "")
	if status != http.StatusOK || len(got) != partSize+4 || got[:4] != "\xA5\xA5\xA5\xA5" || got[partSize:] != "tail" {
		t.Fatalf("object after complete: status %d, len %d, want %d bytes ending in tail", status, len(got), partSize+4)
	}
}

// signedJSON is signedBody with a JSON content type.
func signedJSON(t *testing.T, srv *httptest.Server, key sig.Key, method string, path []string, body string) (int, string) {
	t.Helper()
	req := sig.Request{Method: method, Path: path}
	ts := time.Now().Unix()
	r, err := http.NewRequest(method, srv.URL+sig.EncodePath(path), strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Tunna-Date", strconv.FormatInt(ts, 10))
	r.Header.Set("Authorization", sig.Authorization(req, key, ts))
	resp, err := srv.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}
