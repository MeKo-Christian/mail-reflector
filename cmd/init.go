package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Interactively generate a config.yaml file",
	RunE: func(cmd *cobra.Command, args []string) error {
		configFile := "config.yaml"

		if _, err := os.Stat(configFile); err == nil {
			fmt.Printf("config.yaml already exists. Use --force to overwrite.\n")
			return nil
		}

		reader := bufio.NewReader(os.Stdin)

		fmt.Println("Let's set up your config.yaml!")

		fmt.Println("\n--- IMAP ---")
		imapServer := prompt(reader, "IMAP server (e.g. imap.strato.de): ")
		imapPort := prompt(reader, "IMAP port (e.g. 993): ")
		imapSecurity := prompt(reader, "IMAP security (ssl/starttls): ")
		imapUser := prompt(reader, "IMAP username: ")
		imapPass := prompt(reader, "IMAP password: ")

		fmt.Println("\n--- SMTP ---")
		smtpServer := prompt(reader, "SMTP server (e.g. smtp.strato.de): ")
		smtpPort := prompt(reader, "SMTP port (e.g. 465): ")
		smtpSecurity := prompt(reader, "SMTP security (ssl/starttls): ")
		smtpUser := prompt(reader, "SMTP username: ")
		smtpPass := prompt(reader, "SMTP password: ")

		fmt.Println("\n--- FILTER ---")
		froms := promptMulti(reader, "Allowed sender email(s) (comma-separated): ")

		fmt.Println("\n--- RECIPIENTS ---")
		recipients := promptMulti(reader, "BCC recipient email(s) (comma-separated): ")

		content := fmt.Sprintf(`imap:
  server: %s
  port: %s
  security: %s
  username: %s
  password: %s

smtp:
  server: %s
  port: %s
  security: %s
  username: %s
  password: %s

filter:
  from:
%s

recipients:
%s
`, imapServer, imapPort, imapSecurity, imapUser, imapPass,
			smtpServer, smtpPort, smtpSecurity, smtpUser, smtpPass,
			yamlList("  - ", froms), yamlList("  - ", recipients))

		if err := os.WriteFile(configFile, []byte(content), 0o600); err != nil {
			return fmt.Errorf("failed to write config.yaml: %w", err)
		}

		fmt.Println("\n✅ config.yaml created successfully.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}

func prompt(r *bufio.Reader, label string) string {
	fmt.Print(label)
	text, _ := r.ReadString('\n')
	return strings.TrimSpace(text)
}

func promptMulti(r *bufio.Reader, label string) []string {
	raw := prompt(r, label)
	parts := strings.Split(raw, ",")
	var cleaned []string
	for _, s := range parts {
		s = strings.TrimSpace(s)
		if s != "" {
			cleaned = append(cleaned, s)
		}
	}
	return cleaned
}

func yamlList(prefix string, values []string) string {
	var lines []string
	for _, v := range values {
		lines = append(lines, fmt.Sprintf("%s%s", prefix, v))
	}
	return strings.Join(lines, "\n")
}
