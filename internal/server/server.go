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

	"github.com/gense/ollama-manager/internal/agent"
	"github.com/gense/ollama-manager/internal/config"
	"github.com/gense/ollama-manager/internal/jobs"
	"github.com/gense/ollama-manager/internal/ollama"
	"github.com/gense/ollama-manager/internal/runner"
	"github.com/gense/ollama-manager/internal/tests"
)

// WebFS is the embedded static frontend (set from main via //go:embed).
type WebFS = embed.FS

// Server holds shared state for HTTP handlers.
type Server struct {
	cfg         *config.Config
	ollama      *ollama.Client
	web         fs.FS
	tmpl        *template.Template
	jobs        *jobs.Manager
	uninst      *uninstallHistoryStore
	archived    *archivedModelsStore
	testsStore  *tests.Store
	agentStore  *agent.SessionStore
	runnerStore *runner.ResultStore
	runner      *runner.Client

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
	uninstallPath := filepath.Join(filepath.Dir(cfg.Path()), "uninstall_history.json")
	jobMgr := jobs.New(jobsPath, historyPath, ollamaClient, log.Default())
	if err := jobMgr.Load(); err != nil {
		log.Printf("jobs: could not load %s: %v", jobsPath, err)
	}
	jobMgr.Start()
	uninst := newUninstallHistoryStore(uninstallPath)
	if err := uninst.Load(); err != nil {
		log.Printf("uninstall-history: could not load %s: %v", uninstallPath, err)
	}
	archivedPath := filepath.Join(filepath.Dir(cfg.Path()), "archived_models.json")
	archivedStore := newArchivedModelsStore(archivedPath)
	if err := archivedStore.Load(); err != nil {
		log.Printf("archived-models: could not load %s: %v", archivedPath, err)
	}

	testsPath := filepath.Join(filepath.Dir(cfg.Path()), "tests.json")
	testsStore := tests.New(testsPath)
	if err := testsStore.Load(); err != nil {
		log.Printf("tests: could not load %s: %v", testsPath, err)
	}
	if err := testsStore.SeedIfEmpty(); err != nil {
		log.Printf("tests: seed failed: %v", err)
	}

	agentStore := agent.NewSessionStore(filepath.Dir(cfg.Path()))

	runnerPath := filepath.Join(filepath.Dir(cfg.Path()), "tests-history.json")
	runnerStore := runner.NewResultStore(runnerPath)
	if err := runnerStore.Load(); err != nil {
		log.Printf("runner: could not load %s: %v", runnerPath, err)
	}

	return &Server{
		cfg:         cfg,
		ollama:      ollamaClient,
		web:         webRoot,
		tmpl:        tmpl,
		jobs:        jobMgr,
		uninst:      uninst,
		archived:    archivedStore,
		testsStore:  testsStore,
		agentStore:  agentStore,
		runnerStore: runnerStore,
		runner:      runner.NewClient(ollamaClient),
		ctxCache:    make(map[string]int64),
		capsCache:   make(map[string][]string),
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
	mux.Handle("POST /api/models/archive", s.requireAuth(s.handleArchiveModel))
	mux.Handle("POST /api/models/unarchive", s.requireAuth(s.handleUnarchiveModel))
	mux.Handle("DELETE /api/models/{name...}", s.requireAuth(s.handleDeleteModel))
	mux.Handle("POST /api/chat", s.requireAuth(s.handleChat))
	mux.Handle("POST /api/embed", s.requireAuth(s.handleEmbed))
	mux.Handle("POST /api/pull", s.requireAuth(s.handlePull))
	mux.Handle("GET /api/status", s.requireAuth(s.handleStatus))

	mux.Handle("GET /api/jobs", s.requireAuth(s.handleJobsList))
	mux.Handle("GET /api/jobs/events", s.requireAuth(s.handleJobsEvents))
	mux.Handle("GET /api/download-history/{name...}", s.requireAuth(s.handleDownloadHistory))
	mux.Handle("POST /api/jobs/clear", s.requireAuth(s.handleJobsClear))
	mux.Handle("POST /api/jobs/pause", s.requireAuth(s.handleJobsPauseQueue))
	mux.Handle("POST /api/jobs/resume", s.requireAuth(s.handleJobsResumeQueue))
	mux.Handle("POST /api/jobs/{id}/cancel", s.requireAuth(s.handleJobCancel))
	mux.Handle("POST /api/jobs/{id}/pause", s.requireAuth(s.handleJobPause))
	mux.Handle("POST /api/jobs/{id}/resume", s.requireAuth(s.handleJobResume))
	mux.Handle("DELETE /api/jobs/{id}", s.requireAuth(s.handleJobRemove))

	mux.Handle("GET /api/config", s.requireAuth(s.handleGetConfig))
	mux.Handle("PATCH /api/config", s.requireAuth(s.handlePatchConfig))
	mux.Handle("POST /api/config/password", s.requireAuth(s.handleSetPassword))

	mux.Handle("GET /api/tests", s.requireAuth(s.handleTestsList))
	mux.Handle("POST /api/tests", s.requireAuth(s.handleTestsCreate))
	mux.Handle("PUT /api/tests/{id}", s.requireAuth(s.handleTestsUpdate))
	mux.Handle("DELETE /api/tests/{id}", s.requireAuth(s.handleTestsDelete))
	mux.Handle("POST /api/tests/reorder", s.requireAuth(s.handleTestsReorder))
	mux.Handle("POST /api/test-groups", s.requireAuth(s.handleTestGroupsCreate))
	mux.Handle("PUT /api/test-groups/{id}", s.requireAuth(s.handleTestGroupsUpdate))
	mux.Handle("DELETE /api/test-groups/{id}", s.requireAuth(s.handleTestGroupsDelete))

	mux.Handle("GET /api/tests/agent/sessions", s.requireAuth(s.handleAgentSessionsList))
	mux.Handle("POST /api/tests/agent/sessions", s.requireAuth(s.handleAgentSessionsCreate))
	mux.Handle("GET /api/tests/agent/sessions/{id}", s.requireAuth(s.handleAgentSessionGet))
	mux.Handle("POST /api/tests/agent/sessions/{id}/message", s.requireAuth(s.handleAgentSessionMessage))
	mux.Handle("POST /api/tests/agent/sessions/{id}/tool", s.requireAuth(s.handleAgentSessionTool))
	mux.Handle("POST /api/tests/agent/sessions/{id}/reset", s.requireAuth(s.handleAgentSessionReset))
	mux.Handle("DELETE /api/tests/agent/sessions/{id}", s.requireAuth(s.handleAgentSessionDestroy))
	mux.Handle("GET /api/tests/agent/sessions/{id}/files", s.requireAuth(s.handleAgentSessionFiles))

	mux.Handle("GET /api/runner/sys-info", s.requireAuth(s.handleSysInfo))
	mux.Handle("POST /api/runner/battery", s.requireAuth(s.handleBatteryRun))
	mux.Handle("GET /api/runner/runs", s.requireAuth(s.handleListRuns))
	mux.Handle("GET /api/runner/runs/{id}", s.requireAuth(s.handleGetRun))
	mux.Handle("GET /api/runner/runs/{id}/progress", s.requireAuth(s.handleBatteryProgress))
	mux.Handle("PUT /api/runner/runs/{id}/rate", s.requireAuth(s.handleRateRun))
	mux.Handle("DELETE /api/runner/runs/{id}", s.requireAuth(s.handleDeleteRun))
	mux.Handle("GET /api/runner/test-history/{id}", s.requireAuth(s.handleGetTestHistory))
	mux.Handle("GET /api/runner/group-history/{id}", s.requireAuth(s.handleGetGroupHistory))

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
