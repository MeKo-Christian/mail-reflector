package cmd

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var rootCmd = &cobra.Command{
	Use:   "mail-reflector",
	Short: "Forward filtered mails to a recipient list",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// Setup logger after flag parsing
		setupLogger()
	},
}

func init() {
	// Add persistent flag to enable verbose logging
	rootCmd.PersistentFlags().Bool("verbose", false, "Enable verbose (info/debug) logging")
	viper.BindPFlag("verbose", rootCmd.PersistentFlags().Lookup("verbose"))

	cobra.OnInitialize(initConfig)

	// Register subcommands
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(checkCmd)
	// rootCmd.AddCommand(recipientsCmd) // future
	// rootCmd.AddCommand(serveCmd) // future
}

func Execute() error {
	return rootCmd.Execute()
}

func initConfig() {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AutomaticEnv()
	_ = viper.ReadInConfig() // optional config
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
