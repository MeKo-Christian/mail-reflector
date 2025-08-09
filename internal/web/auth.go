package web

import (
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type Session struct {
	ID        string
	UserID    string
	CreatedAt time.Time
	ExpiresAt time.Time
}

type AuthManager struct {
	sessions map[string]*Session
	mutex    sync.RWMutex

	// For now, we'll use a simple hardcoded user
	// In Sprint 4.1, this will be replaced with proper user management
	username string
	password string // bcrypt hash
}

func NewAuthManager() *AuthManager {
	// Default credentials: admin / admin123
	// This should be configurable in production
	hash, _ := bcrypt.GenerateFromPassword([]byte("admin123"), bcrypt.DefaultCost)

	auth := &AuthManager{
		sessions: make(map[string]*Session),
		username: "admin",
		password: string(hash),
	}

	// Start cleanup routine for expired sessions
	go auth.cleanupExpiredSessions()

	return auth
}

func (a *AuthManager) ValidateCredentials(username, password string) bool {
	if username != a.username {
		return false
	}

	err := bcrypt.CompareHashAndPassword([]byte(a.password), []byte(password))
	return err == nil
}

func (a *AuthManager) CreateSession(userID string) (*Session, error) {
	sessionID, err := a.generateSessionID()
	if err != nil {
		return nil, err
	}

	session := &Session{
		ID:        sessionID,
		UserID:    userID,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(24 * time.Hour), // 24 hour sessions
	}

	a.mutex.Lock()
	a.sessions[sessionID] = session
	a.mutex.Unlock()

	slog.Info("Session created", "sessionID", sessionID, "userID", userID)
	return session, nil
}

func (a *AuthManager) GetSession(sessionID string) (*Session, bool) {
	a.mutex.RLock()
	session, exists := a.sessions[sessionID]
	a.mutex.RUnlock()

	if !exists || time.Now().After(session.ExpiresAt) {
		if exists {
			a.DeleteSession(sessionID)
		}
		return nil, false
	}

	return session, true
}

func (a *AuthManager) DeleteSession(sessionID string) {
	a.mutex.Lock()
	delete(a.sessions, sessionID)
	a.mutex.Unlock()

	slog.Info("Session deleted", "sessionID", sessionID)
}

func (a *AuthManager) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session")
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		session, valid := a.GetSession(cookie.Value)
		if !valid {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// Add user info to request context if needed
		slog.Debug("Authenticated request", "userID", session.UserID, "path", r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func (a *AuthManager) generateSessionID() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(bytes), nil
}

func (a *AuthManager) cleanupExpiredSessions() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		a.mutex.Lock()
		now := time.Now()
		for sessionID, session := range a.sessions {
			if now.After(session.ExpiresAt) {
				delete(a.sessions, sessionID)
				slog.Debug("Expired session cleaned up", "sessionID", sessionID)
			}
		}
		a.mutex.Unlock()
	}
}
