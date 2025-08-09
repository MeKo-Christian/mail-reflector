package web

import (
	"fmt"
	"io"
	"log/slog"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type ConfigValidator struct {
	errors []string
}

func NewConfigValidator() *ConfigValidator {
	return &ConfigValidator{errors: make([]string, 0)}
}

func (cv *ConfigValidator) ValidateConfig() []string {
	cv.errors = make([]string, 0)

	cv.validateIMAP()
	cv.validateSMTP()
	cv.validateFilters()
	cv.validateRecipients()

	return cv.errors
}

func (cv *ConfigValidator) addError(message string) {
	cv.errors = append(cv.errors, message)
	slog.Debug("Config validation error", "error", message)
}

func (cv *ConfigValidator) validateIMAP() {
	server := viper.GetString("imap.server")
	if server == "" {
		cv.addError("IMAP server is required")
	}

	port := viper.GetInt("imap.port")
	if port <= 0 || port > 65535 {
		cv.addError("IMAP port must be between 1 and 65535")
	}

	security := viper.GetString("imap.security")
	validSecurityTypes := []string{"ssl", "tls", "none"}
	if !contains(validSecurityTypes, strings.ToLower(security)) {
		cv.addError("IMAP security must be one of: ssl, tls, none")
	}

	username := viper.GetString("imap.username")
	if username == "" {
		cv.addError("IMAP username is required")
	}

	password := viper.GetString("imap.password")
	if password == "" {
		cv.addError("IMAP password is required")
	}
}

func (cv *ConfigValidator) validateSMTP() {
	server := viper.GetString("smtp.server")
	if server == "" {
		cv.addError("SMTP server is required")
	}

	port := viper.GetInt("smtp.port")
	if port <= 0 || port > 65535 {
		cv.addError("SMTP port must be between 1 and 65535")
	}

	security := viper.GetString("smtp.security")
	validSecurityTypes := []string{"ssl", "tls", "none"}
	if !contains(validSecurityTypes, strings.ToLower(security)) {
		cv.addError("SMTP security must be one of: ssl, tls, none")
	}

	username := viper.GetString("smtp.username")
	if username == "" {
		cv.addError("SMTP username is required")
	}

	password := viper.GetString("smtp.password")
	if password == "" {
		cv.addError("SMTP password is required")
	}
}

func (cv *ConfigValidator) validateFilters() {
	fromFilters := viper.GetStringSlice("filter.from")
	if len(fromFilters) == 0 {
		cv.addError("At least one sender filter is required")
	}

	for _, filter := range fromFilters {
		if filter == "" {
			cv.addError("Empty sender filter found")
			continue
		}

		// Validate email format
		if _, err := mail.ParseAddress(filter); err != nil {
			cv.addError(fmt.Sprintf("Invalid email format in sender filter: %s", filter))
		}
	}
}

func (cv *ConfigValidator) validateRecipients() {
	recipients := viper.GetStringSlice("recipients")
	if len(recipients) == 0 {
		cv.addError("At least one recipient is required")
	}

	for _, recipient := range recipients {
		if recipient == "" {
			cv.addError("Empty recipient found")
			continue
		}

		// Validate email format
		if _, err := mail.ParseAddress(recipient); err != nil {
			cv.addError(fmt.Sprintf("Invalid email format in recipient: %s", recipient))
		}
	}
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// ConfigBackup handles creating and restoring configuration backups
type ConfigBackup struct {
	backupDir string
}

func NewConfigBackup() *ConfigBackup {
	return &ConfigBackup{
		backupDir: "config_backups",
	}
}

func (cb *ConfigBackup) CreateBackup(reason string) error {
	if err := os.MkdirAll(cb.backupDir, 0o755); err != nil {
		return fmt.Errorf("failed to create backup directory: %w", err)
	}

	timestamp := time.Now().Format("2006-01-02_15-04-05")
	backupName := fmt.Sprintf("config_%s_%s.yaml", timestamp, sanitizeFilename(reason))
	backupPath := filepath.Join(cb.backupDir, backupName)

	srcFile, err := os.Open("config.yaml")
	if err != nil {
		return fmt.Errorf("failed to open config.yaml: %w", err)
	}
	defer func() {
		if err := srcFile.Close(); err != nil {
			slog.Error("Failed to close source file", "error", err)
		}
	}()

	dstFile, err := os.Create(backupPath)
	if err != nil {
		return fmt.Errorf("failed to create backup file: %w", err)
	}
	defer func() {
		if err := dstFile.Close(); err != nil {
			slog.Error("Failed to close backup file", "error", err)
		}
	}()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("failed to copy config to backup: %w", err)
	}

	slog.Info("Configuration backup created", "path", backupPath, "reason", reason)
	return nil
}

func (cb *ConfigBackup) ListBackups() ([]string, error) {
	if _, err := os.Stat(cb.backupDir); os.IsNotExist(err) {
		return []string{}, nil
	}

	files, err := os.ReadDir(cb.backupDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read backup directory: %w", err)
	}

	var backups []string
	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".yaml") {
			backups = append(backups, file.Name())
		}
	}

	return backups, nil
}

func (cb *ConfigBackup) RestoreBackup(backupName string) error {
	backupPath := filepath.Join(cb.backupDir, backupName)

	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		return fmt.Errorf("backup file does not exist: %s", backupName)
	}

	// Create a backup of the current config before restoring
	if err := cb.CreateBackup("pre_restore"); err != nil {
		slog.Warn("Failed to create pre-restore backup", "error", err)
	}

	srcFile, err := os.Open(backupPath)
	if err != nil {
		return fmt.Errorf("failed to open backup file: %w", err)
	}
	defer func() {
		if err := srcFile.Close(); err != nil {
			slog.Error("Failed to close backup file", "error", err)
		}
	}()

	dstFile, err := os.Create("config.yaml")
	if err != nil {
		return fmt.Errorf("failed to create config.yaml: %w", err)
	}
	defer func() {
		if err := dstFile.Close(); err != nil {
			slog.Error("Failed to close config file", "error", err)
		}
	}()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("failed to restore config: %w", err)
	}

	slog.Info("Configuration restored from backup", "backup", backupName)
	return nil
}

func sanitizeFilename(name string) string {
	// Replace problematic characters with underscores
	sanitized := strings.NewReplacer(
		" ", "_",
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
	).Replace(name)

	// Limit length
	if len(sanitized) > 50 {
		sanitized = sanitized[:50]
	}

	return sanitized
}
