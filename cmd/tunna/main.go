package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tunnaio/tunna/internal/config"
	"github.com/tunnaio/tunna/internal/httpapi"
	"github.com/tunnaio/tunna/internal/memory"
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

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: httpapi.New(httpapi.Options{
			ServerVersion: version,
			Keys:          memory.NewKeyStore(nil),
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
