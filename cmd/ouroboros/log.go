package main

import (
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/WildPino/ouroboros/internal/config"
	"github.com/WildPino/ouroboros/internal/logging"
)

// eventOpener opens the Event Log source; nil disables it.
type eventOpener func() (logging.EventSink, func() error, error)

// logDir is where the rotated log files go (DESIGN.md §5).
func logDir(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "logs")
}

func logLevel(level string) slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(strings.ToUpper(level))); err != nil {
		return slog.LevelInfo
	}
	return l
}

// openLog returns the logger of commands that change the repository: the
// console, the rotated files next to the configuration and, when enabled,
// warnings and errors to the Event Log. A destination that cannot be
// opened is reported on the console and skipped, so logging never stops a
// backup.
func openLog(cfg *config.Config, configPath string, console io.Writer, openEvents eventOpener) (*slog.Logger, func()) {
	opts := &slog.HandlerOptions{Level: logLevel(cfg.Logging.Level)}
	handlers := []slog.Handler{slog.NewTextHandler(console, opts)}
	var closers []func() error

	if f, err := logging.OpenRotating(logDir(configPath), cfg.Logging.KeepDays); err != nil {
		fmt.Fprintf(console, "warning: log files disabled: %v\n", err)
	} else {
		handlers = append(handlers, slog.NewTextHandler(f, opts))
		closers = append(closers, f.Close)
	}
	if cfg.Logging.EventLog && openEvents != nil {
		if sink, closeSink, err := openEvents(); err != nil {
			fmt.Fprintf(console, "warning: Event Log disabled: %v\n", err)
		} else {
			handlers = append(handlers, logging.NewEventHandler(sink))
			closers = append(closers, closeSink)
		}
	}
	return slog.New(slog.NewMultiHandler(handlers...)), func() {
		for _, c := range closers {
			c()
		}
	}
}
