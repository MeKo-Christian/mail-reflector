// cmd/serve.go
package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/meko-christian/mail-reflector/internal/reflector"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Continuously watch mailbox and forward matching mails",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !viper.InConfig("imap") || !viper.InConfig("smtp") {
			return fmt.Errorf(`configuration missing or incomplete.

Create a config.yaml file by running:
  mail-reflector init

The configuration file should be in your current directory and contain:
- IMAP server settings (to read emails)
- SMTP server settings (to forward emails)
- Email filter rules (which senders to monitor)
- Recipients list (who receives forwarded emails)`)
		}

		slog.Info("Starting serve mode (watching mailbox)")
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		return reflector.Serve(ctx)
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
}
