// Command tunna-fixtures serves the API in the state spec/conformance/
// fixtures.json describes, for conformance runners that live outside the Go
// test binary (ADR-0010). Memory stores and a temporary disk store, so it
// is fast and leaves nothing behind. One control route outside the API,
// POST /reset, rebuilds every store from the fixtures, which is how a runner
// gets the fresh-server-per-case guarantee the Go runner has in-process.
//
//	tunna-fixtures [-addr 127.0.0.1:0] [-fixtures spec/conformance/fixtures.json]
//
// It prints the URL it listens on as the first line of stdout once the
// listener is open, so a runner that spawns it can read that line and start.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/tunnaio/tunna/internal/disk"
	"github.com/tunnaio/tunna/internal/fixtures"
	"github.com/tunnaio/tunna/internal/httpapi"
	"github.com/tunnaio/tunna/internal/memory"
)

func main() {
	if err := run(); err != nil {
		slog.Error("tunna-fixtures exited", "err", err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", "127.0.0.1:0", "listen address; port 0 picks a free one")
	path := flag.String("fixtures", "spec/conformance/fixtures.json", "fixtures file")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fx, err := fixtures.Load(*path)
	if err != nil {
		return err
	}

	// The API handler is rebuilt on every reset and swapped in atomically;
	// requests in flight finish on the handler they started with.
	var current atomic.Pointer[server]
	rebuild := func() error {
		s, err := build(ctx, fx)
		if err != nil {
			return err
		}
		if old := current.Swap(s); old != nil {
			old.cleanup()
		}
		return nil
	}
	if err := rebuild(); err != nil {
		return err
	}
	defer func() {
		if s := current.Load(); s != nil {
			s.cleanup()
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /reset", func(w http.ResponseWriter, r *http.Request) {
		if err := rebuild(); err != nil {
			slog.Error("reset failed", "err", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		current.Load().handler.ServeHTTP(w, r)
	})

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	fmt.Printf("http://%s\n", ln.Addr())
	slog.Info("listening", "addr", ln.Addr().String(), "fixtures", *path)

	srv := &http.Server{Handler: mux}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	slog.Info("stopped")
	return nil
}

// server is one provisioned API handler and the temporary directory its
// blobs live in.
type server struct {
	handler http.Handler
	dir     string
}

func (s *server) cleanup() {
	if err := os.RemoveAll(s.dir); err != nil {
		slog.Warn("removing blob directory", "dir", s.dir, "err", err)
	}
}

// build provisions fresh stores from the fixtures, the same way the Go
// conformance runner does per case.
func build(ctx context.Context, fx fixtures.File) (*server, error) {
	dir, err := os.MkdirTemp("", "tunna-fixtures-")
	if err != nil {
		return nil, err
	}
	blobs, err := disk.New(dir)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	now := time.Now()
	objs, err := fx.ObjectRecords(ctx, blobs, now)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	origins, err := fx.Origins()
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	objects := memory.NewObjectStore(objs)
	handler := httpapi.New(httpapi.Options{
		ServerVersion: "fixtures",
		CORSOrigins:   origins,
		Keys:          memory.NewKeyStore(fx.APIKeys(now)),
		Buckets:       memory.NewBucketStore(fx.BucketRecords(now)),
		Objects:       objects,
		Uploads:       memory.NewUploadStore(nil, objects),
		Blobs:         blobs,
	})
	return &server{handler: handler, dir: dir}, nil
}
