// Package logger configures the application's structured logger (log/slog).
package logger

import (
	"log/slog"
	"os"
	"strings"
)

// Init installs a JSON slog handler on stdout as the default logger, tagged with
// the application name and hostname. The level is read from MONEYFLY_LOG_LEVEL
// (debug | info | warn | error), defaulting to info.
func Init() {
	host, _ := os.Hostname()
	opts := &slog.HandlerOptions{
		AddSource: true,
		Level:     levelFromEnv(),
	}
	handler := slog.NewJSONHandler(os.Stdout, opts).WithAttrs([]slog.Attr{
		slog.String("application", "moneyfly"),
		slog.String("hostname", host),
	})
	slog.SetDefault(slog.New(handler))
}

func levelFromEnv() slog.Level {
	switch strings.ToLower(os.Getenv("MONEYFLY_LOG_LEVEL")) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
