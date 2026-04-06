// Package logging configures the global zerolog logger.
package logging

import (
	"io"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Setup configures the global logger. It returns the logger so callers can
// use it directly if they prefer a non-global handle.
func Setup(level string, jsonOutput bool) zerolog.Logger {
	zerolog.TimeFieldFormat = time.RFC3339Nano
	zerolog.DurationFieldUnit = time.Millisecond

	lvl, err := zerolog.ParseLevel(strings.ToLower(level))
	if err != nil || lvl == zerolog.NoLevel {
		lvl = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(lvl)

	var w io.Writer = os.Stdout
	if !jsonOutput {
		w = zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}
	}

	logger := zerolog.New(w).
		With().
		Timestamp().
		Str("service", "marauder-backend").
		Logger()

	log.Logger = logger
	return logger
}
