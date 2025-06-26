package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var Version string = "dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version info",
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Printf("mail-reflector version %s\n", Version)
	},
}
