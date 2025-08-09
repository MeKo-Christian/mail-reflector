package main

import (
	"log/slog"
	"os"

	"github.com/meko-christian/mail-reflector/cmd"
)

func main() {
	// Run the command-line interface
	if err := cmd.Execute(); err != nil {
		slog.Error("Command execution failed", "error", err)
		os.Exit(1)
	}
}
