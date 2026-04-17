package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gense/ollama-manager/internal/ollama"
	"golang.org/x/crypto/bcrypt"
)

// ---------- index / login ----------

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if s.cfg.HasPassword() && !s.isAuthenticated(r) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	data, err := fs.ReadFile(s.web, "index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.HasPassword() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.tmpl.ExecuteTemplate(w, "login.html", map[string]any{"Error": ""})
}

func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.HasPassword() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	pass := r.FormValue("password")
	if err := bcrypt.CompareHashAndPassword([]byte(s.cfg.PasswordHash), []byte(pass)); err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		_ = s.tmpl.ExecuteTemplate(w, "login.html", map[string]any{"Error": "Contraseña incorrecta"})
		return
	}
	s.setSessionCookie(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.clearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// ---------- status ----------

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	resp := map[string]any{
		"ollama_url":      s.cfg.OllamaURL,
		"expose_network":  s.cfg.ExposeNetwork,
		"has_password":    s.cfg.HasPassword(),
		"ollama_reachable": s.ollama.Ping(ctx) == nil,
	}
	writeJSON(w, http.StatusOK, resp)
}

// ---------- models ----------

// modelView is what the frontend consumes.
type modelView struct {
	Name          string     `json:"name"`
	Size          int64      `json:"size"`
	ModifiedAt    time.Time  `json:"modified_at"`
	Digest        string     `json:"digest"`
	Family        string     `json:"family"`
	Families      []string   `json:"families"`
	Format        string     `json:"format"`
	ParameterSize string     `json:"parameter_size"`
	Quantization  string     `json:"quantization"`
	ContextLength int64      `json:"context_length,omitempty"`
	Loaded        bool       `json:"loaded"`
	SizeVRAM      int64      `json:"size_vram,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
}

func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	models, err := s.ollama.List(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	running, err := s.ollama.PS(ctx)
	if err != nil {
		// Non-fatal: just report nothing as loaded.
		log.Printf("ps failed: %v", err)
		running = nil
	}
	loaded := make(map[string]ollama.RunningModel, len(running))
	for _, rm := range running {
		loaded[rm.Name] = rm
	}

	contexts := s.fetchContexts(ctx, models)

	out := make([]modelView, 0, len(models))
	for _, m := range models {
		v := modelView{
			Name:          m.Name,
			Size:          m.Size,
			ModifiedAt:    m.ModifiedAt,
			Digest:        m.Digest,
			Family:        m.Details.Family,
			Families:      m.Details.Families,
			Format:        m.Details.Format,
			ParameterSize: m.Details.ParameterSize,
			Quantization:  m.Details.QuantizationLevel,
			ContextLength: contexts[m.Digest],
		}
		if rm, ok := loaded[m.Name]; ok {
			v.Loaded = true
			v.SizeVRAM = rm.SizeVRAM
			exp := rm.ExpiresAt
			v.ExpiresAt = &exp
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": out})
}

// fetchContexts returns a digest->context_length map for the given models,
// using an in-memory cache. Cache misses are resolved in parallel via
// /api/show. Errors are silently ignored (context just stays at 0).
func (s *Server) fetchContexts(ctx context.Context, models []ollama.Model) map[string]int64 {
	result := make(map[string]int64, len(models))

	// First pass: serve from cache.
	s.ctxMu.RLock()
	missing := make([]ollama.Model, 0)
	for _, m := range models {
		if v, ok := s.ctxCache[m.Digest]; ok {
			result[m.Digest] = v
		} else {
			missing = append(missing, m)
		}
	}
	s.ctxMu.RUnlock()

	if len(missing) == 0 {
		return result
	}

	// Second pass: bounded parallel /api/show.
	type item struct {
		digest string
		ctxLen int64
	}
	out := make(chan item, len(missing))
	const concurrency = 6
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for _, m := range missing {
		wg.Add(1)
		sem <- struct{}{}
		go func(m ollama.Model) {
			defer wg.Done()
			defer func() { <-sem }()
			showCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
			defer cancel()
			show, err := s.ollama.Show(showCtx, m.Name)
			if err != nil {
				out <- item{digest: m.Digest}
				return
			}
			out <- item{digest: m.Digest, ctxLen: extractContextLength(show)}
		}(m)
	}
	wg.Wait()
	close(out)

	s.ctxMu.Lock()
	for it := range out {
		s.ctxCache[it.digest] = it.ctxLen
		result[it.digest] = it.ctxLen
	}
	s.ctxMu.Unlock()
	return result
}

// extractContextLength scans a ShowResponse for a "<arch>.context_length" key.
func extractContextLength(show *ollama.ShowResponse) int64 {
	if show == nil || show.ModelInfo == nil {
		return 0
	}
	var arch string
	if raw, ok := show.ModelInfo["general.architecture"]; ok {
		_ = json.Unmarshal(raw, &arch)
	}
	if arch != "" {
		if raw, ok := show.ModelInfo[arch+".context_length"]; ok {
			var n float64
			if json.Unmarshal(raw, &n) == nil && n > 0 {
				return int64(n)
			}
		}
	}
	for k, raw := range show.ModelInfo {
		if strings.HasSuffix(k, ".context_length") {
			var n float64
			if json.Unmarshal(raw, &n) == nil && n > 0 {
				return int64(n)
			}
		}
	}
	return 0
}

// modelDetail is the response of GET /api/models/{name}.
type modelDetail struct {
	Name          string         `json:"name"`
	License       string         `json:"license,omitempty"`
	Modelfile     string         `json:"modelfile,omitempty"`
	Parameters    string         `json:"parameters,omitempty"`
	Template      string         `json:"template,omitempty"`
	Details       ollama.ModelDetails `json:"details"`
	Capabilities  []string       `json:"capabilities,omitempty"`
	ContextLength int64          `json:"context_length,omitempty"`
	Architecture  string         `json:"architecture,omitempty"`
	ParameterCount int64         `json:"parameter_count,omitempty"`
	ModelInfo     map[string]any `json:"model_info,omitempty"`
	ModifiedAt    time.Time      `json:"modified_at"`
}

func (s *Server) handleShowModel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing model name"))
		return
	}
	show, err := s.ollama.Show(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}

	detail := modelDetail{
		Name:         name,
		License:      show.License,
		Modelfile:    show.Modelfile,
		Parameters:   show.Parameters,
		Template:     show.Template,
		Details:      show.Details,
		Capabilities: show.Capabilities,
		ModifiedAt:   show.ModifiedAt,
	}
	flat := make(map[string]any, len(show.ModelInfo))
	for k, raw := range show.ModelInfo {
		var v any
		_ = json.Unmarshal(raw, &v)
		flat[k] = v
	}
	detail.ModelInfo = flat
	if v, ok := flat["general.architecture"].(string); ok {
		detail.Architecture = v
	}
	if v, ok := flat["general.parameter_count"].(float64); ok {
		detail.ParameterCount = int64(v)
	}
	if detail.Architecture != "" {
		key := detail.Architecture + ".context_length"
		if v, ok := flat[key].(float64); ok {
			detail.ContextLength = int64(v)
		}
	}
	if detail.ContextLength == 0 {
		// Fallback: scan any *.context_length value.
		for k, v := range flat {
			if strings.HasSuffix(k, ".context_length") {
				if f, ok := v.(float64); ok {
					detail.ContextLength = int64(f)
					break
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handleDeleteModel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing model name"))
		return
	}
	if err := s.ollama.Delete(r.Context(), name); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": name})
}

// ---------- pull (SSE) ----------

func (s *Server) handlePull(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing 'name'"))
		return
	}
	name := strings.TrimSpace(body.Name)

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("streaming not supported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	send := func(event string, payload any) {
		buf, _ := json.Marshal(payload)
		if event != "" {
			fmt.Fprintf(w, "event: %s\n", event)
		}
		fmt.Fprintf(w, "data: %s\n\n", buf)
		flusher.Flush()
	}

	send("start", map[string]any{"name": name})

	err := s.ollama.Pull(r.Context(), name, func(ev ollama.PullProgress) error {
		percent := 0.0
		if ev.Total > 0 {
			percent = float64(ev.Completed) / float64(ev.Total) * 100
			if percent > 100 {
				percent = 100
			}
		}
		send("progress", map[string]any{
			"status":    ev.Status,
			"digest":    ev.Digest,
			"total":     ev.Total,
			"completed": ev.Completed,
			"percent":   percent,
		})
		return nil
	})
	if err != nil {
		send("error", map[string]string{"error": err.Error()})
		return
	}
	send("done", map[string]string{"name": name})
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// logging is a tiny request logger.
func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(ww, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, ww.status, time.Since(start).Truncate(time.Millisecond))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Flush forwards to the underlying writer when it supports streaming.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
