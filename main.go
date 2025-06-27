package main

import (
	"log/slog"
	"os"

	"github.com/meko-christian/mail-reflector/cmd"
)

func main() {
	// Use a JSON handler for structured logs
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	})

	// Set this handler as the default for slog
	slog.SetDefault(slog.New(handler))

	// Run the command-line interface
	if err := cmd.Execute(); err != nil {
		slog.Error("Command execution failed", "error", err)
		os.Exit(1)
	}
}
