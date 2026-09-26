//go:build dev

package main

import (
	"log/slog"
	"net/http"
	_ "net/http/pprof" //nolint:gosec // profiling endpoint enabled only in dev builds
	"os"
	"time"
)

func initDevPprof() {
	addr := os.Getenv("PPROF_ADDR")
	if addr == "" {
		return
	}
	go func() {
		slog.Info("pprof server listening", "addr", addr)
		srv := &http.Server{
			Addr:              addr,
			ReadHeaderTimeout: 5 * time.Second,
		}
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Warn("pprof server exited", "error", err)
		}
	}()
}
