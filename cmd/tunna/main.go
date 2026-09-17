package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/tunnaio/tunna"
	"github.com/tunnaio/tunna/internal/config"
	"github.com/tunnaio/tunna/internal/disk"
	"github.com/tunnaio/tunna/internal/httpapi"
	"github.com/tunnaio/tunna/internal/sqlite"
)

// version is the server build version, set at build time with
// -ldflags "-X main.version=1.2.3". Defaults to dev.
var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("tunna exited", "err", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	cfg.LogValues(slog.Default())

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return err
	}

	db, err := sqlite.Open(filepath.Join(cfg.DataDir, "tunna.db"), slog.Default())
	if err != nil {
		return err
	}
	defer db.Close()

	if cfg.BootstrapKeyID != "" {
		if err := db.PutKey(ctx, tunna.APIKey{ID: cfg.BootstrapKeyID, Secret: cfg.BootstrapKeySecret, Admin: true, Name: "bootstrap"}); err != nil {
			return fmt.Errorf("bootstrap key: %w", err)
		}
		slog.Info("bootstrap key upserted", "id", cfg.BootstrapKeyID)
	}

	blobs, err := disk.New(cfg.DataDir)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: httpapi.New(httpapi.Options{
			ServerVersion: version,
			Keys:          db,
			Buckets:       db,
			Objects:       db,
			Blobs:         blobs,
			Uploads:       db,
			CORSOrigins:   cfg.CORSOrigins,
		}),
	}

	errc := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", srv.Addr)
		errc <- srv.ListenAndServe()
	}()

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
