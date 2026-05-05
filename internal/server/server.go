// Package server wires the HTTP layer: routing, embedded UI, auth and
// proxying to the Ollama client.
package server

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
	"sync"

	"github.com/gense/ollama-manager/internal/config"
	"github.com/gense/ollama-manager/internal/jobs"
	"github.com/gense/ollama-manager/internal/ollama"
)

// WebFS is the embedded static frontend (set from main via //go:embed).
type WebFS = embed.FS

// Server holds shared state for HTTP handlers.
type Server struct {
	cfg    *config.Config
	ollama *ollama.Client
	web    fs.FS
	tmpl   *template.Template
	jobs   *jobs.Manager

	// Guards mutations to cfg done by /api/config endpoints.
	cfgMu sync.RWMutex

	// Cache of context_length keyed by model digest. Model info doesn't
	// change unless the model is reinstalled (digest changes), so we never
	// need to invalidate by name.
	ctxMu     sync.RWMutex
	ctxCache  map[string]int64
	capsCache map[string][]string
}

// New builds a Server. webRoot is the embedded "web/" directory.
func New(cfg *config.Config, ollamaClient *ollama.Client, webRoot fs.FS) (*Server, error) {
	tmpl, err := template.ParseFS(webRoot, "login.html")
	if err != nil {
		return nil, fmt.Errorf("parse login template: %w", err)
	}

	// Store jobs.json next to config.json so "config" and "jobs" always
	// travel together.
	jobsPath := filepath.Join(filepath.Dir(cfg.Path()), "jobs.json")
	historyPath := filepath.Join(filepath.Dir(cfg.Path()), "download_history.json")
	jobMgr := jobs.New(jobsPath, historyPath, ollamaClient, log.Default())
	if err := jobMgr.Load(); err != nil {
		log.Printf("jobs: could not load %s: %v", jobsPath, err)
	}
	jobMgr.Start()

	return &Server{
		cfg:       cfg,
		ollama:    ollamaClient,
		web:       webRoot,
		tmpl:      tmpl,
		jobs:      jobMgr,
		ctxCache:  make(map[string]int64),
		capsCache: make(map[string][]string),
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
	mux.Handle("GET /api/running", s.requireAuth(s.handleListRunning))
	mux.Handle("POST /api/running/unload-all", s.requireAuth(s.handleUnloadAllRunning))
	mux.Handle("POST /api/model-repair/preview", s.requireAuth(s.handleRepairPreview))
	mux.Handle("POST /api/model-repair/apply", s.requireAuth(s.handleRepairApply))
	mux.Handle("GET /api/models/{name...}", s.requireAuth(s.handleShowModel))
	mux.Handle("POST /api/models/unload", s.requireAuth(s.handleUnloadModel))
	mux.Handle("DELETE /api/models/{name...}", s.requireAuth(s.handleDeleteModel))
	mux.Handle("POST /api/chat", s.requireAuth(s.handleChat))
	mux.Handle("POST /api/embed", s.requireAuth(s.handleEmbed))
	mux.Handle("POST /api/pull", s.requireAuth(s.handlePull))
	mux.Handle("GET /api/status", s.requireAuth(s.handleStatus))

	mux.Handle("GET /api/jobs", s.requireAuth(s.handleJobsList))
	mux.Handle("GET /api/jobs/events", s.requireAuth(s.handleJobsEvents))
	mux.Handle("GET /api/download-history/{name...}", s.requireAuth(s.handleDownloadHistory))
	mux.Handle("POST /api/jobs/clear", s.requireAuth(s.handleJobsClear))
	mux.Handle("POST /api/jobs/{id}/cancel", s.requireAuth(s.handleJobCancel))
	mux.Handle("DELETE /api/jobs/{id}", s.requireAuth(s.handleJobRemove))

	mux.Handle("GET /api/config", s.requireAuth(s.handleGetConfig))
	mux.Handle("PATCH /api/config", s.requireAuth(s.handlePatchConfig))
	mux.Handle("POST /api/config/password", s.requireAuth(s.handleSetPassword))

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
		log.Println("shutting down…")
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
