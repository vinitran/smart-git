package logger

import (
	"log/slog"
	"os"
	"sync"
)

var (
	once sync.Once
	base *slog.Logger
)

// Setup configures the global logger once.
func Setup(level slog.Leveler) {
	once.Do(func() {
		handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: level,
		})
		base = slog.New(handler)
	})
}

// L returns the configured global logger.
func L() *slog.Logger {
	if base == nil {
		Setup(slog.LevelInfo)
	}
	return base
}
