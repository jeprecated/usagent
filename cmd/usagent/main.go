package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jeprecated/usagent/internal/app"
	"github.com/jeprecated/usagent/internal/cli"
	"github.com/jeprecated/usagent/internal/config"
	"github.com/jeprecated/usagent/internal/httpapi"
	"github.com/jeprecated/usagent/internal/mcp"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("usagent failed", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	return runWithIO(args, os.Stdout, os.Stderr)
}

func runWithIO(args []string, stdout, stderr io.Writer) error {
	return runWithContext(context.Background(), args, stdout, stderr)
}

func wantsHelp(args []string) bool {
	if len(args) > 0 && args[0] == "help" {
		return true
	}
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}

func runWithContext(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return cli.RunUsage(ctx, nil, stdout, stderr)
	}
	if len(args) > 0 {
		switch args[0] {
		case "usage", "status":
			if wantsHelp(args[1:]) {
				return cli.WriteHelp(stdout, "usage")
			}
			return cli.RunUsage(ctx, args[1:], stdout, stderr)
		case "expiring-usage", "expiring":
			if wantsHelp(args[1:]) {
				return cli.WriteHelp(stdout, "expiring")
			}
			return cli.RunExpiringUsage(ctx, args[1:], stdout, stderr)
		case "mcp":
			if wantsHelp(args[1:]) {
				return cli.WriteHelp(stdout, "mcp")
			}
			return mcp.Run(ctx, args[1:], os.Stdin, stdout, stderr)
		case "serve":
			if wantsHelp(args[1:]) {
				return cli.WriteHelp(stdout, "serve")
			}
			args = args[1:]
		case "help", "-h", "--help":
			return cli.WriteHelp(stdout, "")
		default:
			return cli.RunUsage(ctx, args, stdout, stderr)
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
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
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
