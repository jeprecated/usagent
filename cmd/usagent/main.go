package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jmalloc/usagent/internal/app"
	"github.com/jmalloc/usagent/internal/config"
	"github.com/jmalloc/usagent/internal/httpapi"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("usagent failed", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	opts, err := config.ParseFlags(args)
	if err != nil {
		return err
	}
	cfg, err := config.LoadWithOverrides(opts.ConfigPath, opts)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{}))
	a := app.New(cfg, logger)
	if err := a.LoadState(); err != nil {
		logger.Warn("state load failed", "error", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go a.RunRefreshLoop(ctx)
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	srv := &http.Server{Addr: addr, Handler: httpapi.New(a, opts.ConfigPath), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	logger.Info("usagent listening", "host", cfg.Server.Host, "port", cfg.Server.Port, "configPath", opts.ConfigPath)
	err = srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
