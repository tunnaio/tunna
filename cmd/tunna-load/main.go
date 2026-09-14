// Command tunna-load is a load generator for a running tunna server. It is
// dev tooling, not a product surface: it exists so the numbers behind
// ADR-0002 phase C and the SDK defaults come from measurement.
//
//	tunna-load -base http://localhost:8000 -key tk_admin -secret ... -profile put -size 4096 -workers 16 -duration 10s
//
// Profiles:
//
//	put    each request PUTs a fresh object of -size bytes into -bucket
//	get    one object is PUT once, then every request GETs it
//	mixed  90% get, 10% put, over the same object set
//
// The secret is taken from -secret or the TUNNA_LOAD_SECRET environment
// variable; prefer the variable so it stays out of shell history.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tunnaio/tunna/sig"
)

type result struct {
	latency time.Duration
	bytes   int64
	err     bool
}

func main() {
	base := flag.String("base", "http://localhost:8000", "server origin")
	keyID := flag.String("key", "tk_admin", "key id")
	secret := flag.String("secret", os.Getenv("TUNNA_LOAD_SECRET"), "key secret (or TUNNA_LOAD_SECRET)")
	bucket := flag.String("bucket", "load", "bucket to use; created if absent")
	profile := flag.String("profile", "put", "put, get or mixed")
	size := flag.Int("size", 4096, "object size in bytes")
	workers := flag.Int("workers", 16, "concurrent workers")
	duration := flag.Duration("duration", 10*time.Second, "how long to run")
	flag.Parse()
	if *secret == "" {
		fmt.Fprintln(os.Stderr, "a secret is required: -secret or TUNNA_LOAD_SECRET")
		os.Exit(2)
	}

	key := sig.Key{ID: *keyID, Secret: *secret}
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: *workers * 2}}
	payload := make([]byte, *size)
	rand.New(rand.NewSource(1)).Read(payload)

	// Bucket: create, ignore "exists".
	if st, _ := do(client, key, *base, http.MethodPut, []string{"-", "buckets", *bucket}, nil, ""); st != 201 && st != 409 {
		fmt.Fprintf(os.Stderr, "creating bucket: status %d\n", st)
		os.Exit(1)
	}
	// Warm object for get and mixed.
	warm := "warm.bin"
	if *profile != "put" {
		if st, _ := do(client, key, *base, http.MethodPut, []string{*bucket, warm}, payload, "application/octet-stream"); st != 201 {
			fmt.Fprintf(os.Stderr, "warm PUT: status %d\n", st)
			os.Exit(1)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), *duration)
	defer cancel()
	var seq atomic.Int64
	results := make(chan result, 1<<16)
	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w)))
			for ctx.Err() == nil {
				var method string
				var path []string
				var body []byte
				doPut := *profile == "put" || (*profile == "mixed" && rng.Intn(10) == 0)
				if doPut {
					method, body = http.MethodPut, payload
					path = []string{*bucket, "obj-" + strconv.FormatInt(seq.Add(1), 10)}
				} else {
					method = http.MethodGet
					path = []string{*bucket, warm}
				}
				t0 := time.Now()
				st, n := do(client, key, *base, method, path, body, "application/octet-stream")
				r := result{latency: time.Since(t0), bytes: n, err: st/100 != 2}
				if doPut {
					r.bytes = int64(len(body))
				}
				select {
				case results <- r:
				default: // drop if the collector is behind; counts stay honest via errors only
				}
			}
		}(w)
	}
	wg.Wait()
	elapsed := time.Since(start)
	close(results)

	var lat []time.Duration
	var total, errs int
	var nbytes int64
	for r := range results {
		total++
		if r.err {
			errs++
			continue
		}
		lat = append(lat, r.latency)
		nbytes += r.bytes
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	pct := func(p float64) time.Duration {
		if len(lat) == 0 {
			return 0
		}
		return lat[int(float64(len(lat)-1)*p)]
	}
	fmt.Printf("profile=%s size=%d workers=%d duration=%s\n", *profile, *size, *workers, elapsed.Round(time.Millisecond))
	fmt.Printf("requests=%d errors=%d rps=%.0f MB/s=%.1f\n", total, errs, float64(total-errs)/elapsed.Seconds(), float64(nbytes)/elapsed.Seconds()/1e6)
	fmt.Printf("latency p50=%s p95=%s p99=%s max=%s\n", pct(0.50).Round(10*time.Microsecond), pct(0.95).Round(10*time.Microsecond), pct(0.99).Round(10*time.Microsecond), pct(1).Round(10*time.Microsecond))
}

// do sends one signed request and returns the status and the response body
// length. Errors from the transport count as status 0.
func do(client *http.Client, key sig.Key, base, method string, path []string, body []byte, contentType string) (int, int64) {
	req := sig.Request{Method: method, Path: path}
	ts := time.Now().Unix()
	r, err := http.NewRequest(method, base+sig.EncodePath(path), bytes.NewReader(body))
	if err != nil {
		return 0, 0
	}
	if body != nil {
		r.ContentLength = int64(len(body))
		r.Header.Set("Content-Type", contentType)
	}
	r.Header.Set("X-Tunna-Date", strconv.FormatInt(ts, 10))
	r.Header.Set("Authorization", sig.Authorization(req, key, ts))
	resp, err := client.Do(r)
	if err != nil {
		return 0, 0
	}
	defer resp.Body.Close()
	n, _ := io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, n
}
