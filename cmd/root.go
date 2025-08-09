package cmd

import (
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var rootCmd = &cobra.Command{
	Use:   "mail-reflector",
	Short: "Forward filtered mails to a recipient list",
	PersistentPreRun: func(_ *cobra.Command, _ []string) {
		// Setup logger after flag parsing
		setupLogger()
	},
}

func init() {
	// Add persistent flag to enable verbose logging
	rootCmd.PersistentFlags().Bool("verbose", false, "Enable verbose (info/debug) logging")
	_ = viper.BindPFlag("verbose", rootCmd.PersistentFlags().Lookup("verbose"))

	cobra.OnInitialize(initConfig)

	// Register subcommands
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(checkCmd)
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(initCmd)
}

func Execute() error {
	return rootCmd.Execute()
}

func initConfig() {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AutomaticEnv()

	err := viper.ReadInConfig()
	if err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			slog.Warn("No config.yaml found in current directory.",
				"hint", "Run `mail-reflector init` to create one interactively.")
		} else {
			slog.Error("Failed to read config", "error", err)
		}
	} else {
		// Validate config after successful load
		validateConfig()
	}
}

func validateConfig() {
	// Validate filter.from addresses
	filterFroms := viper.GetStringSlice("filter.from")
	if len(filterFroms) > 0 {
		hasUppercase := false
		for _, email := range filterFroms {
			if email != strings.ToLower(email) {
				hasUppercase = true
				break
			}
		}
		if hasUppercase {
			slog.Warn("Filter email addresses contain uppercase letters",
				"configured_emails", filterFroms,
				"hint", "Email matching is case-insensitive, consider using lowercase for consistency")
		}
	}

	// Check for other potential config issues
	if len(filterFroms) == 0 {
		slog.Warn("No filter.from addresses configured - no emails will be processed")
	}

	recipients := viper.GetStringSlice("recipients")
	if len(recipients) == 0 {
		slog.Warn("No recipients configured - forwarding will not work")
	}
}

func setupLogger() {
	var level slog.Level
	if viper.GetBool("verbose") {
		level = slog.LevelDebug
	} else {
		level = slog.LevelError
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})

	slog.SetDefault(slog.New(handler))
}
