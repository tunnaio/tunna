package main

// BenchmarkSmallGET measures the server's own cost for a signed 4 KiB GET
// over loopback, with SQLite and disk stores in a temporary directory: the
// setup a real deployment has, minus the network. Run with -cpuprofile to
// see where the time goes:
//
//	go test ./cmd/tunna -run xxx -bench SmallGET -benchtime 3s -cpuprofile cpu.out
//	go tool pprof -top cpu.out

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/tunnaio/tunna"
	"github.com/tunnaio/tunna/internal/disk"
	"github.com/tunnaio/tunna/internal/httpapi"
	"github.com/tunnaio/tunna/internal/sqlite"
	"github.com/tunnaio/tunna/sig"
)

func BenchmarkSmallGET(b *testing.B) {
	dir := b.TempDir()
	db, err := sqlite.Open(filepath.Join(dir, "tunna.db"), slog.New(slog.DiscardHandler))
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	blobs, err := disk.New(dir)
	if err != nil {
		b.Fatal(err)
	}
	key := sig.Key{ID: "tk_bench", Secret: "bench-secret-not-real"}
	ctx := context.Background()
	if err := db.PutKey(ctx, tunna.APIKey{ID: key.ID, Secret: key.Secret}); err != nil {
		b.Fatal(err)
	}
	srv := httptest.NewServer(httpapi.New(httpapi.Options{ServerVersion: "bench", Keys: db, Buckets: db, Objects: db, Uploads: db, Blobs: blobs}))
	defer srv.Close()

	put := func(method string, path []string, body []byte, ct string) int {
		req := sig.Request{Method: method, Path: path}
		ts := time.Now().Unix()
		r, _ := http.NewRequest(method, srv.URL+sig.EncodePath(path), bytes.NewReader(body))
		if body != nil {
			r.ContentLength = int64(len(body))
			r.Header.Set("Content-Type", ct)
		}
		r.Header.Set("X-Tunna-Date", strconv.FormatInt(ts, 10))
		r.Header.Set("Authorization", sig.Authorization(req, key, ts))
		resp, err := srv.Client().Do(r)
		if err != nil {
			b.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp.StatusCode
	}
	if st := put(http.MethodPut, []string{"-", "buckets", "bench"}, nil, ""); st != 201 {
		b.Fatalf("bucket: %d", st)
	}
	if st := put(http.MethodPut, []string{"bench", "small.bin"}, bytes.Repeat([]byte{1}, 4096), "application/octet-stream"); st != 201 {
		b.Fatalf("object: %d", st)
	}

	// Pre-sign once: the client's HMAC is not what is being measured.
	path := []string{"bench", "small.bin"}
	ts := time.Now().Unix()
	authz := sig.Authorization(sig.Request{Method: http.MethodGet, Path: path}, key, ts)
	url := srv.URL + sig.EncodePath(path)
	// The default transport keeps two idle connections per host; with more
	// parallel workers than that it dials and closes a socket per request and
	// the benchmark measures itself. Keep every connection.
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: 64}}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			r, _ := http.NewRequest(http.MethodGet, url, nil)
			r.Header.Set("X-Tunna-Date", strconv.FormatInt(ts, 10))
			r.Header.Set("Authorization", authz)
			resp, err := client.Do(r)
			if err != nil {
				b.Fatal(err)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 {
				b.Fatalf("status %d", resp.StatusCode)
			}
		}
	})
}
