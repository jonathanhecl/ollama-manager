// Package server wires the HTTP layer: routing, embedded UI, auth and
// proxying to the Ollama client.
package server

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"

	"github.com/gense/ollama-manager/internal/config"
	"github.com/gense/ollama-manager/internal/ollama"
)

// WebFS is the embedded static frontend (set from main via //go:embed).
type WebFS = embed.FS

// Server holds shared state for HTTP handlers.
type Server struct {
	cfg     *config.Config
	ollama  *ollama.Client
	web     fs.FS
	tmpl    *template.Template
}

// New builds a Server. webRoot is the embedded "web/" directory.
func New(cfg *config.Config, ollamaClient *ollama.Client, webRoot fs.FS) (*Server, error) {
	tmpl, err := template.ParseFS(webRoot, "login.html")
	if err != nil {
		return nil, fmt.Errorf("parse login template: %w", err)
	}
	return &Server{
		cfg:    cfg,
		ollama: ollamaClient,
		web:    webRoot,
		tmpl:   tmpl,
	}, nil
}

// Routes returns the http.Handler with all routes mounted.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// Static assets (CSS, JS) are always public.
	staticFS, err := fs.Sub(s.web, ".")
	if err != nil {
		// Should never happen for an embed FS.
		panic(err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.handleLoginSubmit)
	mux.HandleFunc("POST /logout", s.handleLogout)

	mux.HandleFunc("GET /", s.handleIndex)

	mux.Handle("GET /api/models", s.requireAuth(s.handleListModels))
	mux.Handle("GET /api/models/{name...}", s.requireAuth(s.handleShowModel))
	mux.Handle("DELETE /api/models/{name...}", s.requireAuth(s.handleDeleteModel))
	mux.Handle("POST /api/pull", s.requireAuth(s.handlePull))
	mux.Handle("GET /api/status", s.requireAuth(s.handleStatus))

	return logging(mux)
}

// ListenAndServe binds and serves until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv := &http.Server{
		Addr:    s.cfg.BindAddress(),
		Handler: s.Routes(),
	}

	// Graceful shutdown.
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5_000_000_000)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) requireAuth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.isAuthenticated(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next(w, r)
	})
}
