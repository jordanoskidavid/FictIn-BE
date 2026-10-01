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

	"fictin/internal/app"
	"fictin/internal/config"
	"fictin/internal/database"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := newLogger(cfg.IsDevelopment())
	slog.SetDefault(log)

	// --- migrations ---
	if err := database.RunMigrations(cfg.DatabaseURL); err != nil {
		return err
	}
	log.Info("migrations applied")

	// --- db pool ---
	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := database.Connect(rootCtx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// --- wiring, admin seed (development only), resume live sessions ---
	a := app.New(cfg, pool, app.Options{Log: log})
	a.Bootstrap(rootCtx, cfg, log)

	srv := &http.Server{
		Addr:              ":" + cfg.AppPort,
		Handler:           a.Handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// --- background cleanup ---
	cleanupDone := app.StartCleanup(rootCtx, a.Store, log)

	// --- serve ---
	serveErr := make(chan error, 1)
	go func() {
		log.Info("http server listening", "addr", srv.Addr, "env", cfg.AppEnv)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	select {
	case err := <-serveErr:
		return err
	case <-rootCtx.Done():
		log.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", "err", err)
	}
	a.Close()
	<-cleanupDone
	log.Info("bye")
	return nil
}
func newLogger(dev bool) *slog.Logger {
	level := slog.LevelInfo
	if dev {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}
