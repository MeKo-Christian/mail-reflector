package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"mail-reflector/internal/reflector"
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Check mailbox and forward mails if needed",
	Run: func(_ *cobra.Command, args []string) {
		if err := reflector.CheckAndForward(); err != nil {
			fmt.Printf("Check failed: %v\n", err)
		}
	},
}
