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
	"github.com/jmalloc/usagent/internal/cli"
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
	if len(args) == 0 {
		return cli.RunUsage(context.Background(), nil, os.Stdout, os.Stderr)
	}
	if len(args) > 0 {
		switch args[0] {
		case "usage", "status":
			if len(args) > 1 && (args[1] == "help" || args[1] == "-h" || args[1] == "--help") {
				fmt.Fprint(os.Stdout, "usagent usage [--config PATH] [--host HOST] [--port PORT] [--json] [--offline] [--timeout 10s]\n")
				return nil
			}
			return cli.RunUsage(context.Background(), args[1:], os.Stdout, os.Stderr)
		case "serve":
			args = args[1:]
		case "help", "-h", "--help":
			fmt.Fprint(os.Stdout, "usagent usage: usagent [usage] [--config PATH] [--json] [--offline] [--timeout 10s]\n              usagent serve [--config PATH] [--host HOST] [--port PORT]\n")
			return nil
		}
	}
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
