package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"mail-reflector/internal/reflector"
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Check mailbox and forward mails if needed",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !viper.InConfig("imap") || !viper.InConfig("smtp") {
			return fmt.Errorf("config.yaml is missing or incomplete. Run `mail-reflector init`")
		}
		return nil
	},
	Run: func(_ *cobra.Command, _ []string) {
		if err := reflector.CheckAndForward(); err != nil {
			fmt.Printf("Check failed: %v\n", err)
		}
	},
}
