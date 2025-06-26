package cmd

import (
	"fmt"

	"mail-reflector/internal/reflector"

	"github.com/spf13/cobra"
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Check mailbox and forward mails if needed",
	Run: func(cmd *cobra.Command, args []string) {
		if err := reflector.CheckAndForward(); err != nil {
			fmt.Printf("Check failed: %v\n", err)
		}
	},
}
