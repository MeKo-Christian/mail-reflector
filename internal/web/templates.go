package web

import (
	"html/template"
	"log/slog"
	"net/http"
	"path/filepath"
)

var templates *template.Template

func init() {
	var err error
	templates, err = template.ParseGlob(filepath.Join("internal", "web", "templates", "*.html"))
	if err != nil {
		slog.Error("Failed to parse templates", "error", err)
		// Don't panic here - templates might not exist during initial setup
	}
}

func (s *Server) renderTemplate(w http.ResponseWriter, tmpl string, data interface{}) {
	if templates == nil {
		// Try to reload templates
		var err error
		templates, err = template.ParseGlob(filepath.Join("internal", "web", "templates", "*.html"))
		if err != nil {
			slog.Error("Failed to load templates", "error", err)
			http.Error(w, "Template system not available", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if err := templates.ExecuteTemplate(w, tmpl+".html", data); err != nil {
		slog.Error("Failed to execute template", "template", tmpl, "error", err)
		http.Error(w, "Template execution failed", http.StatusInternalServerError)
	}
}
