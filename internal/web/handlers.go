package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/spf13/viper"
)

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.renderTemplate(w, "login", nil)
		return
	}

	if r.Method == http.MethodPost {
		username := r.FormValue("username")
		password := r.FormValue("password")

		if s.auth.ValidateCredentials(username, password) {
			session, err := s.auth.CreateSession(username)
			if err != nil {
				slog.Error("Failed to create session", "error", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			http.SetCookie(w, &http.Cookie{
				Name:     "session",
				Value:    session.ID,
				Path:     "/",
				Expires:  session.ExpiresAt,
				HttpOnly: true,
				Secure:   false, // Set to true in production with HTTPS
				SameSite: http.SameSiteStrictMode,
			})

			http.Redirect(w, r, "/", http.StatusSeeOther)
		} else {
			s.renderTemplate(w, "login", map[string]interface{}{
				"Error": "Invalid username or password",
			})
		}
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cookie, err := r.Cookie("session")
	if err == nil {
		s.auth.DeleteSession(cookie.Value)
	}

	// Clear the cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
	})

	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get current configuration
	config := map[string]interface{}{
		"IMAP": map[string]interface{}{
			"Server":   viper.GetString("imap.server"),
			"Port":     viper.GetInt("imap.port"),
			"Security": viper.GetString("imap.security"),
			"Username": viper.GetString("imap.username"),
		},
		"SMTP": map[string]interface{}{
			"Server":   viper.GetString("smtp.server"),
			"Port":     viper.GetInt("smtp.port"),
			"Security": viper.GetString("smtp.security"),
			"Username": viper.GetString("smtp.username"),
		},
		"Filter": map[string]interface{}{
			"From": viper.GetStringSlice("filter.from"),
		},
		"Recipients": viper.GetStringSlice("recipients"),
	}

	data := map[string]interface{}{
		"Title":  "Mail Reflector Dashboard",
		"Config": config,
	}

	s.renderTemplate(w, "dashboard", data)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		// Return current config as JSON for API access
		w.Header().Set("Content-Type", "application/json")

		config := map[string]interface{}{
			"imap": map[string]interface{}{
				"server":   viper.GetString("imap.server"),
				"port":     viper.GetInt("imap.port"),
				"security": viper.GetString("imap.security"),
				"username": viper.GetString("imap.username"),
				// Don't expose password
			},
			"smtp": map[string]interface{}{
				"server":   viper.GetString("smtp.server"),
				"port":     viper.GetInt("smtp.port"),
				"security": viper.GetString("smtp.security"),
				"username": viper.GetString("smtp.username"),
				// Don't expose password
			},
			"filter": map[string]interface{}{
				"from": viper.GetStringSlice("filter.from"),
			},
			"recipients": viper.GetStringSlice("recipients"),
		}

		if err := json.NewEncoder(w).Encode(config); err != nil {
			slog.Error("Failed to encode config", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// For now, we'll implement config updates in Sprint 1.2
	http.Error(w, "Config updates not yet implemented", http.StatusNotImplemented)
}
