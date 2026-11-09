package main

import (
	"context"
	"encoding/json"
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
	"github.com/aduverger/prunelle/server/internal/sessions"
	"github.com/aduverger/prunelle/server/internal/sessions/rex"
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
	if len(cfg.WorkspaceRootPaths) == 0 {
		slog.Warn("no workspace_root_paths configured; workspaces cannot be registered")
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	db, err := store.Open(ctx, cfg.DataDir)
	if err != nil {
		return err
	}
	defer db.Close()

	reviews := review.NewService(db, cfg.WorkspaceRootPaths)
	sessionSvc, err := sessions.NewService(ctx, db, reviews, sessionRegistrations(cfg.SessionBackends))
	if err != nil {
		return err
	}
	handler, err := httpapi.NewHandler(ctx, db, reviews, sessionSvc, httpapi.NewAuthorizer(cfg.AllowedLogins, httpapi.TailscaleOwner), version)
	if err != nil {
		return err
	}
	observeCtx, stopObserving := context.WithCancel(ctx)
	observing := make(chan struct{})
	go func() { sessionSvc.Run(observeCtx); close(observing) }()
	defer func() { stopObserving(); <-observing }() // observers stop before the database closes
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

func sessionRegistrations(backends []config.SessionBackend) []sessions.Registration {
	var regs []sessions.Registration
	for _, b := range backends {
		rexCfg := rex.Config{Path: b.RexPath, Server: b.Server}
		encoded, _ := json.Marshal(rexCfg)
		regs = append(regs, sessions.Registration{Name: b.Name, Config: encoded, Backend: rex.New(rexCfg)})
	}
	return regs
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
