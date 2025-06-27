// cmd/serve.go
package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"mail-reflector/internal/reflector"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Continuously watch mailbox and forward matching mails",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !viper.InConfig("imap") || !viper.InConfig("smtp") {
			return fmt.Errorf("config.yaml is missing or incomplete. Run `mail-reflector init`")
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
