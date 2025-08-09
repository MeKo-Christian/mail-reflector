package cmd

import (
	"fmt"

	"github.com/meko-christian/mail-reflector/internal/reflector"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Check mailbox and forward mails if needed",
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
		return nil
	},
	Run: func(_ *cobra.Command, _ []string) {
		if err := reflector.CheckAndForward(); err != nil {
			fmt.Printf("Check failed: %v\n", err)
		}
	},
}
