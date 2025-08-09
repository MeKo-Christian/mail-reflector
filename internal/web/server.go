package web

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

type Server struct {
	port   string
	bind   string
	server *http.Server
	auth   *AuthManager
}

func NewServer(port, bind string) *Server {
	return &Server{
		port: port,
		bind: bind,
		auth: NewAuthManager(),
	}
}

func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	// Static files
	staticFiles, err := fs.Sub(embeddedStaticFiles, "static")
	if err != nil {
		return fmt.Errorf("failed to create static file system: %w", err)
	}
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFiles))))

	// Public routes
	mux.HandleFunc("/login", s.handleLogin)
	mux.HandleFunc("/logout", s.handleLogout)

	// Protected routes
	mux.Handle("/", s.auth.RequireAuth(http.HandlerFunc(s.handleDashboard)))
	mux.Handle("/config", s.auth.RequireAuth(http.HandlerFunc(s.handleConfig)))

	s.server = &http.Server{
		Addr:         fmt.Sprintf("%s:%s", s.bind, s.port),
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start server in a goroutine
	go func() {
		slog.Info("Web server starting", "address", s.server.Addr)
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Web server failed to start", "error", err)
		}
	}()

	// Wait for shutdown signal
	<-ctx.Done()

	slog.Info("Shutting down web server")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return s.server.Shutdown(shutdownCtx)
}
