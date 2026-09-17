package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// The binary, not the handler: every configuration variable must reach the
// component that acts on it. The conformance suite builds httpapi.Options
// itself, so it cannot see a variable that main parses and drops.

func startBinary(t *testing.T, env ...string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "tunna.exe")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin)
	cmd.Env = append(os.Environ(), "TUNNA_ADDR="+addr, "TUNNA_DATA_DIR="+t.TempDir())
	cmd.Env = append(cmd.Env, env...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); cmd.Wait() })
	return waitHealthy(t, "http://"+addr)
}

// waitHealthy polls health rather than trusting the "listening" log line,
// which the server prints just before it binds.
func waitHealthy(t *testing.T, url string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url + "/-/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return url
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("nothing healthy at %s", url)
	return ""
}

func TestBinaryWiresCORSOrigins(t *testing.T) {
	url := startBinary(t, "TUNNA_CORS_ORIGINS=https://*.example.test, http://localhost:5173")

	req, _ := http.NewRequest(http.MethodOptions, url+"/-/buckets/x", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "GET")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Errorf("preflight Access-Control-Allow-Origin = %q, want the origin; TUNNA_CORS_ORIGINS is parsed but not reaching httpapi", got)
	}

	req, _ = http.NewRequest(http.MethodGet, url+"/-/health", nil)
	req.Header.Set("Origin", "https://app.example.test")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://app.example.test" {
		t.Errorf("wildcard origin: Access-Control-Allow-Origin = %q, want the origin", got)
	}
}

func TestBinaryHonoursPORT(t *testing.T) {
	// startBinary sets TUNNA_ADDR, so this one builds its own environment.
	bin := filepath.Join(t.TempDir(), "tunna.exe")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin)
	cmd.Env = append(os.Environ(), "TUNNA_ADDR=", "PORT="+strconv.Itoa(port), "TUNNA_DATA_DIR="+t.TempDir())
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); cmd.Wait() })

	waitHealthy(t, "http://127.0.0.1:"+strconv.Itoa(port))
}
