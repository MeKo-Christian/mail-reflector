package main

import (
	"flag"
	"log/slog"
	"os"

	"github.com/meko-christian/mail-reflector/cmd"
)

func main() {
	// Define the flag for log level
	logLevel := flag.String("log-level", "info", "Set the log level (debug, info, warn, error, fatal)")
	flag.Parse()

	// Map the string value of log-level to slog.Level
	var level slog.Level
	switch *logLevel {
	case "debug":
		level = slog.LevelDebug
	case "info":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	// Use a JSON handler for structured logs and set the log level
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})

	// Set this handler as the default for slog
	slog.SetDefault(slog.New(handler))

	// Run the command-line interface
	if err := cmd.Execute(); err != nil {
		slog.Error("Command execution failed", "error", err)
		os.Exit(1)
	}
}
