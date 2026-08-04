// Command moneyfly is the single-binary server: it serves the embedded web UI,
// the JSON API and the device sync endpoints.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v3"

	"moneyfly/internal/api"
	"moneyfly/internal/config"
	"moneyfly/internal/logger"
)

func main() {
	logger.Init()

	var configDir string
	flag.StringVar(&configDir, "config-dir", "",
		"config directory (default: $MONEYFLY_CONFIG_DIR or ./config)")
	flag.Parse()

	dir := resolveConfigDir(configDir)

	srv, err := api.New(dir)
	if err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
	defer srv.Close()

	// The listen address comes from the config (which has already folded in
	// MONEYFLY_ADDR), so there is exactly one place that decides it.
	addr := srv.Addr()
	slog.Info("starting moneyfly", "config_dir", dir, "addr", addr, "version", api.Version)

	app := srv.App()
	go func() {
		if err := app.Listen(addr, fiber.ListenConfig{DisableStartupMessage: true}); err != nil {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()
	slog.Info("listening", "addr", addr)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := app.ShutdownWithContext(shutdownCtx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
}

// resolveConfigDir picks the config directory: flag > env > default. Unlike
// upmonitor there is no state.json remembering a previously chosen directory —
// moneyfly cannot switch config dirs at runtime, so nothing needs remembering.
func resolveConfigDir(flagDir string) string {
	if flagDir != "" {
		return flagDir
	}
	if env := os.Getenv("MONEYFLY_CONFIG_DIR"); env != "" {
		return env
	}
	return config.DefaultConfigDir
}
