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
	"time"

	"github.com/aduverger/prunelle/server/internal/config"
	"github.com/aduverger/prunelle/server/internal/httpapi"
	"github.com/aduverger/prunelle/server/internal/review"
	"github.com/aduverger/prunelle/server/internal/store"
)

func loadConfig(path string) (config.Config, error) {
	if path == "" {
		var err error
		if path, err = config.DefaultPath(); err != nil {
			return config.Config{}, err
		}
	}
	return config.Load(path)
}

func serve(ctx context.Context, cfg config.Config, args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.StringVar(&cfg.Listen, "listen", cfg.Listen, "loopback address to listen on")
	flags.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "directory holding the database")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := requireLoopback(cfg.Listen); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	db, err := store.Open(ctx, cfg.DataDir)
	if err != nil {
		return err
	}
	defer db.Close()

	reviews := review.NewService(db, cfg.Snapshot.DefaultBaseRef)
	handler, err := httpapi.NewHandler(ctx, db, reviews, httpapi.NewAuthorizer(cfg.AllowedLogins, httpapi.TailscaleOwner), version)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	slog.Info("prunelle serving", "listen", listener.Addr().String(), "data_dir", cfg.DataDir, "version", version)

	errs := make(chan error, 1)
	go func() { errs <- server.Serve(listener) }()
	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// requireLoopback enforces the access model: the server is reached remotely only through Tailscale Serve.
func requireLoopback(listen string) error {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("listen address %q: %w", listen, err)
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen address %q is not a loopback address; expose the server with `tailscale serve` instead", listen)
	}
	return nil
}
