package main

// The one claim the memory stores could never make: state survives a
// restart. Built from the same constructors run() uses (ADR-0005, rule 7).

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/tunnaio/tunna"
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
