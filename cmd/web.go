package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/meko-christian/mail-reflector/internal/web"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var webCmd = &cobra.Command{
	Use:   "web",
	Short: "Start the web interface for configuration and monitoring",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !viper.InConfig("imap") || !viper.InConfig("smtp") {
			return fmt.Errorf("config.yaml is missing or incomplete. Run `mail-reflector init`")
		}

		port := viper.GetString("web.port")
		bind := viper.GetString("web.bind")

		slog.Info("Starting web interface", "port", port, "bind", bind)

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		server := web.NewServer(port, bind)
		return server.Start(ctx)
	},
}

func init() {
	rootCmd.AddCommand(webCmd)

	// Add web-specific flags
	webCmd.Flags().String("port", "8080", "Port to bind the web server to")
	webCmd.Flags().String("bind", "127.0.0.1", "Address to bind the web server to")

	if err := viper.BindPFlag("web.port", webCmd.Flags().Lookup("port")); err != nil {
		slog.Error("Failed to bind port flag", "error", err)
	}
	if err := viper.BindPFlag("web.bind", webCmd.Flags().Lookup("bind")); err != nil {
		slog.Error("Failed to bind bind flag", "error", err)
	}
}
