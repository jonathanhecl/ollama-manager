package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gense/ollama-manager/internal/config"
	"github.com/gense/ollama-manager/internal/jobs"
	"github.com/gense/ollama-manager/internal/ollama"
	"github.com/gense/ollama-manager/internal/sysmetrics"
	"golang.org/x/crypto/bcrypt"
)

// configIsValidLang is a tiny indirection to avoid importing config in tests.
func configIsValidLang(lang string) bool { return config.IsValidLanguage(lang) }

// ---------- index / login ----------

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	isSPAPath := r.URL.Path == "/" ||
		strings.HasPrefix(r.URL.Path, "/chat/") ||
		r.URL.Path == "/tests" ||
		r.URL.Path == "/tests/new" ||
		strings.HasPrefix(r.URL.Path, "/tests/edit/") ||
		strings.HasPrefix(r.URL.Path, "/tests/group/") ||
		strings.HasPrefix(r.URL.Path, "/tests/agent/") ||
		strings.HasPrefix(r.URL.Path, "/tests/battery/")
	if !isSPAPath {
		http.NotFound(w, r)
		return
	}
	s.cfgMu.RLock()
	hasPwd := s.cfg.HasPassword()
	s.cfgMu.RUnlock()
	if hasPwd && !s.isAuthenticated(r) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	data, err := fs.ReadFile(s.web, "index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	s.cfgMu.RLock()
	hasPwd := s.cfg.HasPassword()
	lang := s.cfg.Language
	s.cfgMu.RUnlock()
	if !hasPwd {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.tmpl.ExecuteTemplate(w, "login.html", loginViewData(lang, ""))
}

func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	s.cfgMu.RLock()
	hasPwd := s.cfg.HasPassword()
	hash := s.cfg.PasswordHash
	lang := s.cfg.Language
	s.cfgMu.RUnlock()

	if !hasPwd {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	pass := r.FormValue("password")
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(pass)); err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		errMsg := "Contraseña incorrecta"
		if lang == "en" {
			errMsg = "Incorrect password"
		}
		_ = s.tmpl.ExecuteTemplate(w, "login.html", loginViewData(lang, errMsg))
		return
	}
	s.cfgMu.RLock()
	s.setSessionCookie(w)
	s.cfgMu.RUnlock()
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// loginViewData builds the data map passed to login.html.
func loginViewData(lang, errMsg string) map[string]any {
	t := loginStrings(lang)
	t["Error"] = errMsg
	return t
}

// loginStrings returns translated labels for the login page.
func loginStrings(lang string) map[string]any {
	if lang == "es" {
		return map[string]any{
			"Title":    "Ollama Manager — Acceder",
			"Heading":  "Ollama Manager",
			"Subtitle": "Esta instancia requiere contraseña.",
			"Label":    "Contraseña",
			"Submit":   "Entrar",
		}
	}
	return map[string]any{
		"Title":    "Ollama Manager — Sign in",
		"Heading":  "Ollama Manager",
		"Subtitle": "This instance is password protected.",
		"Label":    "Password",
		"Submit":   "Sign in",
	}
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.clearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// ---------- status ----------

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	s.cfgMu.RLock()
	cfgPath := s.cfg.Path()
	ollamaURL := s.cfg.OllamaURL
	exposeNetwork := s.cfg.ExposeNetwork
	hasPassword := s.cfg.HasPassword()
	language := s.cfg.Language
	s.cfgMu.RUnlock()

	diskPath := resolveDiskProbePath(cfgPath)
	sys := sysmetrics.Collect(ctx, diskPath)
	loadedModelsRAM, loadedModelsVRAM, loadedModelsTotal := s.loadedModelsMemoryUsage(ctx)
	resp := map[string]any{
		"ollama_url":               ollamaURL,
		"expose_network":           exposeNetwork,
		"has_password":             hasPassword,
		"language":                 language,
		"ollama_reachable":         s.ollama.Ping(ctx) == nil,
		"disk_probe_path":          diskPath,
		"disk_total_bytes":         sys.DiskTotal,
		"disk_free_bytes":          sys.DiskFree,
		"disk_used_bytes":          sys.DiskUsed,
		"disk_used_pct":            sys.DiskUsedPct,
		"cpu_used_pct":             sys.CPUUsedPercent,
		"memory_total":             sys.MemoryTotal,
		"memory_free":              sys.MemoryFree,
		"memory_used":              sys.MemoryUsed,
		"memory_used_pct":          sys.MemoryUsedPct,
		"models_ram_loaded_bytes":  loadedModelsRAM,
		"models_vram_loaded_bytes": loadedModelsVRAM,
		"models_loaded_bytes":      loadedModelsTotal,
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) loadedModelsMemoryUsage(ctx context.Context) (ramBytes uint64, vramBytes uint64, totalBytes uint64) {
	running, err := s.ollama.PS(ctx)
	if err != nil {
		return 0, 0, 0
	}
	for _, rm := range running {
		total, vram := normalizeRunningModelSizes(rm.Size, rm.SizeVRAM)
		ram := total - vram
		totalBytes += uint64(total)
		ramBytes += uint64(ram)
		vramBytes += uint64(vram)
	}
	return ramBytes, vramBytes, totalBytes
}

func normalizeRunningModelSizes(size, sizeVRAM int64) (int64, int64) {
	if size < 0 {
		size = 0
	}
	if sizeVRAM < 0 {
		sizeVRAM = 0
	}
	// Some Ollama builds may report size_vram larger than size; clamp for stable UI.
	if sizeVRAM > size {
		sizeVRAM = size
	}
	return size, sizeVRAM
}

func resolveDiskProbePath(cfgPath string) string {
	if p := strings.TrimSpace(os.Getenv("OLLAMA_MODELS")); p != "" {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		return filepath.Join(home, ".ollama", "models")
	}
	return cfgPath
}

// ---------- config ----------

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"port":           s.cfg.Port,
		"expose_network": s.cfg.ExposeNetwork,
		"language":       s.cfg.Language,
		"ollama_url":     s.cfg.OllamaURL,
		"has_password":   s.cfg.HasPassword(),
		"bind_address":   s.cfg.BindAddress(),
		"chat_defaults":  s.cfg.ChatDefaults,
	})
}

// patchConfigBody uses pointers so callers can update only the fields they
// care about (PATCH semantics).
type patchConfigBody struct {
	Port          *int                 `json:"port"`
	ExposeNetwork *bool                `json:"expose_network"`
	Language      *string              `json:"language"`
	OllamaURL     *string              `json:"ollama_url"`
	ChatDefaults  *config.ChatDefaults `json:"chat_defaults"`
}

func (s *Server) handlePatchConfig(w http.ResponseWriter, r *http.Request) {
	var body patchConfigBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
		return
	}

	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()

	needsRestart := false
	if body.Port != nil {
		if *body.Port < 1 || *body.Port > 65535 {
			writeError(w, http.StatusBadRequest, errors.New("port must be 1..65535"))
			return
		}
		if *body.Port != s.cfg.Port {
			s.cfg.Port = *body.Port
			needsRestart = true
		}
	}
	if body.ExposeNetwork != nil && *body.ExposeNetwork != s.cfg.ExposeNetwork {
		s.cfg.ExposeNetwork = *body.ExposeNetwork
		needsRestart = true
	}
	if body.Language != nil {
		if !configIsValidLang(*body.Language) {
			writeError(w, http.StatusBadRequest, errors.New("unsupported language"))
			return
		}
		s.cfg.Language = *body.Language
	}
	if body.OllamaURL != nil {
		u := strings.TrimSpace(*body.OllamaURL)
		if u == "" {
			writeError(w, http.StatusBadRequest, errors.New("ollama_url cannot be empty"))
			return
		}
		s.cfg.OllamaURL = u
		// Note: this won't change the running client; takes effect on restart.
		needsRestart = true
	}
	if body.ChatDefaults != nil {
		if !config.IsValidThinkLevel(body.ChatDefaults.ThinkLevel) {
			writeError(w, http.StatusBadRequest, errors.New("invalid think_level (use auto, off, low, medium, high or max)"))
			return
		}
		s.cfg.ChatDefaults = *body.ChatDefaults
	}

	if err := s.cfg.Save(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"needs_restart":  needsRestart,
		"port":           s.cfg.Port,
		"expose_network": s.cfg.ExposeNetwork,
		"language":       s.cfg.Language,
		"ollama_url":     s.cfg.OllamaURL,
		"chat_defaults":  s.cfg.ChatDefaults,
	})
}

// passwordBody is the payload of POST /api/config/password.
// An empty Password clears authentication.
type passwordBody struct {
	Password string `json:"password"`
}

func (s *Server) handleSetPassword(w http.ResponseWriter, r *http.Request) {
	var body passwordBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
		return
	}
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()

	if body.Password == "" {
		s.cfg.PasswordHash = ""
		s.clearSessionCookie(w)
	} else {
		hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		s.cfg.PasswordHash = string(hash)
		// Issue a fresh session cookie so the caller stays logged in.
		s.setSessionCookie(w)
	}
	if err := s.cfg.Save(); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"has_password": s.cfg.HasPassword(),
	})
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
	Capabilities  []string   `json:"capabilities,omitempty"`
	Loaded        bool       `json:"loaded"`
	SizeVRAM      int64      `json:"size_vram,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	Archived      bool       `json:"archived"`
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

	modelMeta := s.fetchModelMeta(ctx, models)

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
			ContextLength: modelMeta[m.Digest].ContextLength,
			Capabilities:  modelMeta[m.Digest].Capabilities,
			Archived:      s.archived.IsArchived(m.Name),
		}
		if rm, ok := loaded[m.Name]; ok {
			_, vram := normalizeRunningModelSizes(rm.Size, rm.SizeVRAM)
			v.Loaded = true
			v.SizeVRAM = vram
			exp := rm.ExpiresAt
			v.ExpiresAt = &exp
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": out})
}

// runningView is a slim row for GET /api/running (Ollama /api/ps only, no list/show).
type runningView struct {
	Name      string     `json:"name"`
	SizeVRAM  int64      `json:"size_vram"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

func (s *Server) handleListRunning(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	running, err := s.ollama.PS(ctx)
	if err != nil {
		log.Printf("ps failed: %v", err)
		writeJSON(w, http.StatusOK, map[string]any{"running": []runningView{}})
		return
	}
	out := make([]runningView, 0, len(running))
	for _, rm := range running {
		_, vram := normalizeRunningModelSizes(rm.Size, rm.SizeVRAM)
		v := runningView{
			Name:     rm.Name,
			SizeVRAM: vram,
		}
		if !rm.ExpiresAt.IsZero() {
			exp := rm.ExpiresAt
			v.ExpiresAt = &exp
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"running": out})
}

func (s *Server) handleUnloadModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing model name"))
		return
	}
	if err := s.ollama.Unload(r.Context(), name); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"unloaded": name,
	})
}

func (s *Server) handleArchiveModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing model name"))
		return
	}
	if err := s.archived.Archive(name); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"archived": name,
	})
}

func (s *Server) handleUnarchiveModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing model name"))
		return
	}
	if err := s.archived.Unarchive(name); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"unarchived": name,
	})
}

func (s *Server) handleUnloadAllRunning(w http.ResponseWriter, r *http.Request) {
	running, err := s.ollama.PS(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	unloaded := make([]string, 0, len(running))
	failed := make(map[string]string)
	for _, rm := range running {
		name := strings.TrimSpace(rm.Name)
		if name == "" {
			continue
		}
		if err := s.ollama.Unload(r.Context(), name); err != nil {
			failed[name] = err.Error()
			continue
		}
		unloaded = append(unloaded, name)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       len(failed) == 0,
		"unloaded": unloaded,
		"failed":   failed,
	})
}

type modelMetaCache struct {
	ContextLength int64
	Capabilities  []string
}

// fetchModelMeta returns digest-keyed model metadata for list rendering,
// using an in-memory cache. Cache misses are resolved in parallel via
// /api/show. Errors are silently ignored (values stay zero/empty).
func (s *Server) fetchModelMeta(ctx context.Context, models []ollama.Model) map[string]modelMetaCache {
	result := make(map[string]modelMetaCache, len(models))

	// First pass: serve from cache.
	s.ctxMu.RLock()
	missing := make([]ollama.Model, 0)
	for _, m := range models {
		ctxLen, okCtx := s.ctxCache[m.Digest]
		caps, okCaps := s.capsCache[m.Digest]
		if okCtx && okCaps {
			result[m.Digest] = modelMetaCache{
				ContextLength: ctxLen,
				Capabilities:  append([]string(nil), caps...),
			}
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
		digest       string
		contextLen   int64
		capabilities []string
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
			out <- item{
				digest:       m.Digest,
				contextLen:   extractContextLength(show),
				capabilities: append([]string(nil), show.Capabilities...),
			}
		}(m)
	}
	wg.Wait()
	close(out)

	s.ctxMu.Lock()
	for it := range out {
		s.ctxCache[it.digest] = it.contextLen
		s.capsCache[it.digest] = append([]string(nil), it.capabilities...)
		result[it.digest] = modelMetaCache{
			ContextLength: it.contextLen,
			Capabilities:  append([]string(nil), it.capabilities...),
		}
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
	Name           string              `json:"name"`
	License        string              `json:"license,omitempty"`
	Modelfile      string              `json:"modelfile,omitempty"`
	Parameters     string              `json:"parameters,omitempty"`
	Template       string              `json:"template,omitempty"`
	Details        ollama.ModelDetails `json:"details"`
	Capabilities   []string            `json:"capabilities,omitempty"`
	ContextLength  int64               `json:"context_length,omitempty"`
	Architecture   string              `json:"architecture,omitempty"`
	ParameterCount int64               `json:"parameter_count,omitempty"`
	ModelInfo      map[string]any      `json:"model_info,omitempty"`
	ArtifactCount  int                 `json:"artifact_count,omitempty"`
	ArtifactBytes  int64               `json:"artifact_bytes,omitempty"`
	ModifiedAt     time.Time           `json:"modified_at"`
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
	detail.ArtifactCount, detail.ArtifactBytes = s.artifactInfoForModel(r.Context(), name)
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
	var body struct {
		Reason string `json:"reason"`
	}
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
			return
		}
	}
	reason := strings.TrimSpace(body.Reason)
	if reason != "" && !allowedUninstallReasons[reason] {
		writeError(w, http.StatusBadRequest, errors.New("invalid uninstall reason"))
		return
	}
	if err := s.ollama.Delete(r.Context(), name); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if s.uninst != nil {
		if err := s.uninst.Record(name, reason, time.Now().UTC()); err != nil {
			log.Printf("uninstall-history: save failed for %q: %v", name, err)
		}
	}
	resp := map[string]any{"deleted": name}
	deletedArtifacts := s.deleteArtifactsForModel(r.Context(), name)
	if !isFixedModelName(name) {
		fixed := fixedModelName(name)
		if s.modelExists(r.Context(), fixed) {
			if err := s.ollama.Delete(r.Context(), fixed); err != nil {
				resp["warning"] = "base model deleted, but fixed model could not be deleted: " + err.Error()
			} else {
				resp["deleted_fixed"] = fixed
			}
		}
		deletedArtifacts += s.deleteArtifactsForModel(r.Context(), fixed)
	}
	if deletedArtifacts > 0 {
		resp["deleted_artifacts"] = deletedArtifacts
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleRepairPreview(w http.ResponseWriter, r *http.Request) {
	var body modelRepairRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
		return
	}
	name := strings.TrimSpace(body.Model)
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing model name"))
		return
	}
	if isFixedModelName(name) {
		writeError(w, http.StatusBadRequest, errors.New("fixed models cannot be repaired; open the base model and apply a new fix"))
		return
	}
	show, err := s.ollama.Show(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	preview, err := buildModelRepairPreview(name, show, body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) handleRepairApply(w http.ResponseWriter, r *http.Request) {
	var body modelRepairRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
		return
	}
	name := strings.TrimSpace(body.Model)
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing model name"))
		return
	}
	if isFixedModelName(name) {
		writeError(w, http.StatusBadRequest, errors.New("fixed models cannot be repaired; open the base model and apply a new fix"))
		return
	}
	if !body.Confirm {
		writeError(w, http.StatusBadRequest, errors.New("confirmation is required before applying a repair"))
		return
	}
	show, err := s.ollama.Show(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	preview, err := buildModelRepairPreview(name, show, body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	modelfile := strings.TrimSpace(body.Modelfile)
	from, system, template, parameters, err := parseRepairModelfile(modelfile, preview.BaseName, preview)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	renderer := extractLineDirective(modelfile, "RENDERER")
	parser := extractLineDirective(modelfile, "PARSER")
	createReq := ollama.CreateRequest{
		Model:      preview.TargetName,
		From:       from,
		System:     system,
		Template:   template,
		Parameters: parameters,
		Renderer:   renderer,
		Parser:     parser,
		Modelfile:  modelfile,
		Stream:     false,
	}
	if digest := blobDigest(from); digest != "" {
		createReq.From = ""
		createReq.Files = map[string]string{"model.gguf": digest}
	}
	isStream := strings.Contains(r.Header.Get("Accept"), "text/event-stream")
	var flusher http.Flusher
	if isStream {
		if f, ok := w.(http.Flusher); ok {
			flusher = f
		} else {
			isStream = false
		}
	}
	if isStream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
	}

	sendSSE := func(event string, payload any) {
		if !isStream {
			return
		}
		buf, _ := json.Marshal(payload)
		if event != "" {
			fmt.Fprintf(w, "event: %s\n", event)
		}
		fmt.Fprintf(w, "data: %s\n\n", buf)
		flusher.Flush()
	}

	projector := strings.TrimSpace(body.Projector)
	if projector != "" {
		modelDigest := blobDigest(from)
		if modelDigest == "" {
			if isStream {
				sendSSE("error", map[string]any{"error": "cannot attach a vision projector without a GGUF model blob"})
			} else {
				writeError(w, http.StatusBadRequest, errors.New("cannot attach a vision projector without a GGUF model blob"))
			}
			return
		}
		sendSSE("progress", map[string]any{
			"stage":   "downloading_projector",
			"percent": 0,
		})
		projHex, err := s.downloadProjector(r.Context(), projector, func(completed, total int64) {
			pct := float64(0)
			if total > 0 {
				pct = float64(completed) / float64(total) * 100
			}
			sendSSE("progress", map[string]any{
				"stage":     "downloading_projector",
				"completed": completed,
				"total":     total,
				"percent":   pct,
			})
		})
		if err != nil {
			if isStream {
				sendSSE("error", map[string]any{"error": err.Error()})
			} else {
				writeError(w, http.StatusBadGateway, err)
			}
			return
		}
		createReq.From = ""
		createReq.Files = map[string]string{
			"model.gguf":  modelDigest,
			"mmproj.gguf": "sha256:" + projHex,
		}
	}
	sendSSE("progress", map[string]any{
		"stage":   "creating_model",
		"percent": 100,
	})
	replacing := s.modelExists(r.Context(), preview.TargetName)
	err = s.ollama.Create(r.Context(), createReq)
	if err != nil {
		if isStream {
			sendSSE("error", map[string]any{"error": err.Error()})
		} else {
			writeError(w, http.StatusBadGateway, err)
		}
		return
	}
	resPayload := map[string]any{
		"base_name":   preview.BaseName,
		"target_name": preview.TargetName,
		"replaced":    replacing,
		"warnings":    preview.Warnings,
	}
	if isStream {
		sendSSE("done", resPayload)
	} else {
		writeJSON(w, http.StatusOK, resPayload)
	}
}

// maxProjectorBytes caps the size of a downloaded mmproj file (8 GiB) to avoid
// pathological downloads from a user-provided URL.
const maxProjectorBytes = 8 << 30

// projectorHTTPClient is the HTTP client used to download mmproj files. It is a
// package variable so tests can replace it with a stub transport.
var projectorHTTPClient = http.DefaultClient

type progressReader struct {
	r         io.Reader
	total     int64
	completed int64
	onProg    func(completed, total int64)
	lastTime  time.Time
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	if n > 0 {
		pr.completed += int64(n)
		if pr.onProg != nil && (time.Since(pr.lastTime) > 100*time.Millisecond || err != nil) {
			pr.lastTime = time.Now()
			pr.onProg(pr.completed, pr.total)
		}
	}
	return n, err
}

// downloadProjector downloads a vision projector (mmproj) GGUF from Hugging Face,
// computes its SHA-256 digest and pushes the blob into Ollama's store if it is
// not already present. It returns the hex digest (without the "sha256:" prefix).
func (s *Server) downloadProjector(ctx context.Context, ref string, onProgress func(completed, total int64)) (string, error) {
	u, err := resolveProjectorURL(ref)
	if err != nil {
		return "", err
	}
	return s.downloadBlob(ctx, u, onProgress)
}

// downloadBlob fetches a blob from u, hashes it and ensures it is stored in
// Ollama under its content digest.
func (s *Server) downloadBlob(ctx context.Context, u string, onProgress func(completed, total int64)) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "ollama-manager")
	resp, err := projectorHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("downloading projector failed: %s", resp.Status)
	}

	contentLength := resp.ContentLength
	if onProgress != nil && contentLength > 0 {
		onProgress(0, contentLength)
	}

	tmp, err := os.CreateTemp("", "ollama-manager-mmproj-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	defer tmp.Close()

	h := sha256.New()
	var pr io.Reader = resp.Body
	if onProgress != nil {
		pr = &progressReader{
			r:        resp.Body,
			total:    contentLength,
			onProg:   onProgress,
			lastTime: time.Now(),
		}
	}
	n, err := io.Copy(tmp, io.TeeReader(io.LimitReader(pr, maxProjectorBytes+1), h))
	if err != nil {
		return "", err
	}
	if n > maxProjectorBytes {
		return "", errors.New("projector file is too large")
	}
	if onProgress != nil && contentLength > 0 {
		onProgress(n, contentLength)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return "", err
	}

	hexSum := hex.EncodeToString(h.Sum(nil))
	digest := "sha256:" + hexSum

	exists, err := s.ollama.HeadBlob(ctx, digest)
	if err != nil {
		return "", err
	}
	if !exists {
		if err := s.ollama.CreateBlob(ctx, digest, tmp); err != nil {
			return "", err
		}
	}
	return hexSum, nil
}

func (s *Server) modelExists(ctx context.Context, name string) bool {
	models, err := s.ollama.List(ctx)
	if err != nil {
		return false
	}
	for _, m := range models {
		if m.Name == name || m.Model == name {
			return true
		}
	}
	return false
}

// ---------- chat ----------

type chatRequestBody struct {
	Model       string               `json:"model"`
	Messages    []ollama.ChatMessage `json:"messages"`
	Think       *ollama.ThinkLevel   `json:"think,omitempty"`
	Options     map[string]any       `json:"options,omitempty"`
	WebTools    *bool                `json:"web_tools,omitempty"`
	Artifacts   *bool                `json:"artifacts,omitempty"`
	ArtifactDir string               `json:"artifact_dir,omitempty"`
	Width       int                  `json:"width,omitempty"`
	Height      int                  `json:"height,omitempty"`
	Steps       int                  `json:"steps,omitempty"`
}

func (s *Server) handleEmbed(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model string `json:"model"`
		Input string `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
		return
	}
	body.Model = strings.TrimSpace(body.Model)
	body.Input = strings.TrimSpace(body.Input)
	if body.Model == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing 'model'"))
		return
	}
	if body.Input == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing 'input'"))
		return
	}
	out, err := s.ollama.Embed(r.Context(), body.Model, body.Input)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"model":     body.Model,
		"embedding": out.Embedding,
		"dims":      len(out.Embedding),
	})
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("streaming not supported"))
		return
	}

	var body chatRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
		return
	}
	body.Model = strings.TrimSpace(body.Model)
	if body.Model == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing 'model'"))
		return
	}
	if len(body.Messages) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("missing 'messages'"))
		return
	}

	imageCount := 0
	for _, m := range body.Messages {
		imageCount += len(m.Images)
	}
	thinkVal := "auto"
	if body.Think != nil {
		thinkVal = string(*body.Think)
	}
	log.Printf("[chat] model=%s messages=%d images=%d artifacts=%v web_tools=%v think=%s", body.Model, len(body.Messages), imageCount, body.Artifacts != nil && *body.Artifacts, body.WebTools != nil && *body.WebTools, thinkVal)

	if body.Artifacts != nil && *body.Artifacts {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		s.runArtifactAgentLoop(r.Context(), w, flusher, body)
		return
	}

	if body.WebTools != nil && *body.WebTools {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		s.runWebToolAgentLoop(r.Context(), w, flusher, body)
		return
	}

	isImageGenerationModel := false
	if show, err := s.ollama.Show(r.Context(), body.Model); err == nil && show != nil {
		hasImage := false
		hasVision := false
		hasCompletion := false
		for _, cap := range show.Capabilities {
			switch cap {
			case "image":
				hasImage = true
			case "vision":
				hasVision = true
			case "completion":
				hasCompletion = true
			}
		}
		isImageGenerationModel = hasImage && !hasVision && !hasCompletion
	}

	if isImageGenerationModel {
		var prompt string
		var images []string
		for i := len(body.Messages) - 1; i >= 0; i-- {
			msg := body.Messages[i]
			if msg.Role == "user" {
				prompt = msg.Content
				images = msg.Images
				break
			}
		}

		log.Printf("[image-gen] request: model=%s, prompt=%q, images_count=%d", body.Model, prompt, len(images))

		genReq := ollama.GenerateRequest{
			Model:   body.Model,
			Prompt:  prompt,
			Images:  images,
			Stream:  true,
			Options: body.Options,
			Width:   body.Width,
			Height:  body.Height,
			Steps:   body.Steps,
		}
		// Fallback: if root-level fields are zero, try reading from options for backward compatibility
		if genReq.Width == 0 {
			if v, ok := body.Options["width"]; ok {
				if vi, ok2 := v.(float64); ok2 {
					genReq.Width = int(vi)
				} else if vi, ok2 := v.(int); ok2 {
					genReq.Width = vi
				}
			}
		}
		if genReq.Height == 0 {
			if v, ok := body.Options["height"]; ok {
				if vi, ok2 := v.(float64); ok2 {
					genReq.Height = int(vi)
				} else if vi, ok2 := v.(int); ok2 {
					genReq.Height = vi
				}
			}
		}
		if genReq.Steps == 0 {
			if v, ok := body.Options["steps"]; ok {
				if vi, ok2 := v.(float64); ok2 {
					genReq.Steps = int(vi)
				} else if vi, ok2 := v.(int); ok2 {
					genReq.Steps = vi
				}
			}
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

		startedAt := time.Now()
		var final ollama.GenerateChunk
		err := s.ollama.Generate(r.Context(), genReq, func(chunk ollama.GenerateChunk) error {
			chatChunk := ollama.ChatChunk{
				Model:     chunk.Model,
				CreatedAt: chunk.CreatedAt,
				Done:      chunk.Done,
				Completed: chunk.Completed,
				Total:     chunk.Total,
			}
			content := chunk.Response
			if content == "" && chunk.Image != "" {
				content = chunk.Image
			}
			if content != "" {
				chatChunk.Message = ollama.ChatMessage{
					Role:    "assistant",
					Content: content,
				}
			}
			send("chunk", chatChunk)
			if chunk.Done {
				final = chunk
			}
			return nil
		})
		if err != nil {
			if r.Context().Err() != nil {
				return
			}
			errMsg := err.Error()
			if strings.Contains(errMsg, "mlx runner failed") || strings.Contains(errMsg, "failed to initialize MLX") || strings.Contains(errMsg, "failed to load MLX") {
				if s.cfg.Language == "es" {
					errMsg = "El modelo de generación de imágenes no está soportado en este sistema operativo (Windows/Linux). Los modelos basados en MLX solo funcionan de forma nativa en dispositivos Apple Silicon (macOS)."
				} else {
					errMsg = "This image generation model is not supported on this operating system (Windows/Linux). MLX-based models only run natively on Apple Silicon (macOS) devices."
				}
			}
			send("error", map[string]any{"error": errMsg})
			return
		}

		totalTokens := final.PromptEvalCount + final.EvalCount
		send("done", map[string]any{
			"elapsed_ms":         time.Since(startedAt).Milliseconds(),
			"prompt_tokens":      final.PromptEvalCount,
			"completion_tokens":  final.EvalCount,
			"total_tokens":       totalTokens,
			"prompt_duration_ns": final.PromptEvalDuration,
			"eval_duration_ns":   final.EvalDuration,
			"total_duration_ns":  final.TotalDuration,
			"done_reason":        final.DoneReason,
		})
		return
	}

	chatReq := ollama.ChatRequest{
		Model:    body.Model,
		Messages: body.Messages,
		Stream:   true,
		Think:    body.Think,
		Options:  body.Options,
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

	startedAt := time.Now()
	var final ollama.ChatChunk
	err := s.ollama.Chat(r.Context(), chatReq, func(chunk ollama.ChatChunk) error {
		send("chunk", chunk)
		if chunk.Done {
			final = chunk
		}
		return nil
	})
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		errMsg := err.Error()
		if strings.Contains(errMsg, "mlx runner failed") || strings.Contains(errMsg, "failed to initialize MLX") || strings.Contains(errMsg, "failed to load MLX") {
			if s.cfg.Language == "es" {
				errMsg = "El modelo de generación de imágenes no está soportado en este sistema operativo (Windows/Linux). Los modelos basados en MLX solo funcionan de forma nativa en dispositivos Apple Silicon (macOS)."
			} else {
				errMsg = "This image generation model is not supported on this operating system (Windows/Linux). MLX-based models only run natively on Apple Silicon (macOS) devices."
			}
		}
		send("error", map[string]any{"error": errMsg})
		return
	}

	totalTokens := final.PromptEvalCount + final.EvalCount
	send("done", map[string]any{
		"elapsed_ms":         time.Since(startedAt).Milliseconds(),
		"prompt_tokens":      final.PromptEvalCount,
		"completion_tokens":  final.EvalCount,
		"total_tokens":       totalTokens,
		"prompt_duration_ns": final.PromptEvalDuration,
		"eval_duration_ns":   final.EvalDuration,
		"total_duration_ns":  final.TotalDuration,
		"done_reason":        final.DoneReason,
	})
}

// ---------- pull (enqueue) ----------

// handlePull enqueues a new download. The job runs asynchronously; clients
// should subscribe to /api/jobs/events for progress.
func (s *Server) handlePull(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing 'name'"))
		return
	}
	name := strings.TrimSpace(body.Name)

	job, err := s.jobs.Enqueue(name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"job_id": job.ID,
		"status": job.Status,
		"name":   job.Name,
	})
}

// ---------- jobs ----------

func (s *Server) handleJobsList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"jobs": s.jobs.List()})
}

func (s *Server) handleDownloadHistory(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing name"))
		return
	}
	resp := map[string]any{
		"name":   name,
		"exists": false,
	}
	if h, ok := s.jobs.History(name); ok {
		resp["exists"] = true
		resp["history"] = h
	}
	if s.uninst != nil {
		if u, ok := s.uninst.Get(name); ok {
			resp["uninstall"] = map[string]any{
				"reason": u.LastReason,
				"at":     u.LastUninstallAt,
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleJobsEvents streams job lifecycle updates as Server-Sent Events.
// On connect it first emits a "snapshot" event with the current list, then
// an "update" or "remove" event for every change until the client disconnects.
func (s *Server) handleJobsEvents(w http.ResponseWriter, r *http.Request) {
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

	send("snapshot", map[string]any{"jobs": s.jobs.List(), "queue_paused": s.jobs.IsQueuePaused()})

	ch, cancel := s.jobs.Subscribe()
	defer cancel()

	// Heartbeat to keep proxies from closing idle connections.
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			switch ev.Kind {
			case jobs.EventUpdate:
				send("update", map[string]any{"job": ev.Job, "queue_paused": s.jobs.IsQueuePaused()})
			case jobs.EventRemove:
				send("remove", map[string]any{"id": ev.ID, "queue_paused": s.jobs.IsQueuePaused()})
			}
		case <-ticker.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func (s *Server) handleJobCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing id"))
		return
	}
	if err := s.jobs.Cancel(id); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleJobRemove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing id"))
		return
	}
	if err := s.jobs.Remove(id); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleJobsClear(w http.ResponseWriter, r *http.Request) {
	removed := s.jobs.ClearFinished()
	writeJSON(w, http.StatusOK, map[string]any{"removed": removed})
}

func (s *Server) handleJobPause(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing id"))
		return
	}
	if err := s.jobs.Pause(id); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleJobResume(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing id"))
		return
	}
	if err := s.jobs.Resume(id); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleJobsPauseQueue(w http.ResponseWriter, r *http.Request) {
	s.jobs.PauseQueue()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "queue_paused": true})
}

func (s *Server) handleJobsResumeQueue(w http.ResponseWriter, r *http.Request) {
	s.jobs.ResumeQueue()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "queue_paused": false})
}

// ---------- artifact files ----------

var artifactTitleTagRe = regexp.MustCompile(`(?i)<title[^>]*>([^<]+)</title>`)

// artifactEntry describes one saved artifact project for a model.
type artifactEntry struct {
	ID          string `json:"id"` // "<digest>/<date>", used as artifact_dir
	Digest      string `json:"digest"`
	Date        string `json:"date"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	FileCount   int    `json:"file_count"`
	Size        int64  `json:"size"`
}

// handleListModelArtifacts lists every artifact project a model has created.
// Path: GET /api/models/{name}/artifacts
func (s *Server) handleListModelArtifacts(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	artifacts := []artifactEntry{}
	if digest := s.artifactModelDigest(r.Context(), name); digest != "" {
		root := filepath.Join("artifacts", digest)
		if entries, err := os.ReadDir(root); err == nil {
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				dir := filepath.Join(root, e.Name())
				fileCount, size := dirInfo(dir)
				var artName, artDesc string
				for _, metaFilename := range []string{".artifact.json", "artifact.json"} {
					if metaBytes, err := os.ReadFile(filepath.Join(dir, metaFilename)); err == nil {
						var meta struct {
							Name        string `json:"name"`
							Description string `json:"description"`
						}
						if json.Unmarshal(metaBytes, &meta) == nil {
							artName = strings.TrimSpace(meta.Name)
							artDesc = strings.TrimSpace(meta.Description)
							break
						}
					}
				}
				if artName == "" {
					if indexBytes, err := os.ReadFile(filepath.Join(dir, "index.html")); err == nil {
						if match := artifactTitleTagRe.FindSubmatch(indexBytes); len(match) > 1 {
							artName = strings.TrimSpace(string(match[1]))
						}
					}
				}
				artifacts = append(artifacts, artifactEntry{
					ID:          filepath.Join(digest, e.Name()),
					Digest:      digest,
					Date:        e.Name(),
					Name:        artName,
					Description: artDesc,
					FileCount:   fileCount,
					Size:        size,
				})
			}
			sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Date > artifacts[j].Date })
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"model": name, "artifacts": artifacts})
}

func (s *Server) handleArtifactFiles(w http.ResponseWriter, r *http.Request) {
	// Path: /api/artifacts/{rest...}
	// Current layout: artifacts/<model-digest>/<timestamp>/<files...>
	// Legacy layout:  artifacts/<timestamp>/<files...>
	rest := r.PathValue("rest")
	if rest == "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}

	// Resolve the artifact folder (2-segment <digest>/<date> when present,
	// otherwise the legacy single-segment folder) and the remaining subpath.
	var baseDir string
	subpathStart := 1
	if len(parts) >= 2 {
		nested := filepath.Join("artifacts", parts[0], parts[1])
		if info, err := os.Stat(nested); err == nil && info.IsDir() {
			baseDir = nested
			subpathStart = 2
		} else {
			baseDir = filepath.Join("artifacts", parts[0])
		}
	} else {
		baseDir = filepath.Join("artifacts", parts[0])
	}
	indexPath := filepath.Join(baseDir, "index.html")

	subpath := strings.Join(parts[subpathStart:], "/")
	var cleanPath string
	if subpath == "" {
		cleanPath = indexPath
	} else {
		cleanPath = filepath.Clean(filepath.Join(baseDir, subpath))
		// Prevent path traversal outside the artifact folder.
		if !strings.HasPrefix(cleanPath, baseDir+string(filepath.Separator)) && cleanPath != baseDir {
			http.NotFound(w, r)
			return
		}
	}

	// Check if requested cleanPath exists as a regular file
	info, err := os.Stat(cleanPath)
	if err != nil || info.IsDir() {
		// SPA Fallback: if requesting a client-side route (e.g. /dashboard) that isn't a file on disk,
		// serve index.html if it exists.
		if indexContent, indexErr := os.ReadFile(indexPath); indexErr == nil {
			injected := injectConsoleCaptureScript(indexContent)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(injected)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(injected)
			return
		}
		http.NotFound(w, r)
		return
	}

	// Serve the file, inject script if it's index.html or ends with .html
	if strings.HasSuffix(strings.ToLower(cleanPath), ".html") {
		content, err := os.ReadFile(cleanPath)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		injected := injectConsoleCaptureScript(content)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(injected)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(injected)
		return
	}

	http.ServeFile(w, r, cleanPath)
}

func (s *Server) handleArtifactConsoleLogs(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Timestamp string `json:"timestamp"`
		Log       string `json:"log"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if body.Timestamp == "" || body.Log == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing timestamp or log"})
		return
	}

	s.artifactConsoleMu.Lock()
	if s.artifactConsoleLogs == nil {
		s.artifactConsoleLogs = make(map[string][]string)
	}
	s.artifactConsoleLogs[body.Timestamp] = append(s.artifactConsoleLogs[body.Timestamp], body.Log)
	if len(s.artifactConsoleLogs[body.Timestamp]) > 200 {
		s.artifactConsoleLogs[body.Timestamp] = s.artifactConsoleLogs[body.Timestamp][len(s.artifactConsoleLogs[body.Timestamp])-200:]
	}
	s.artifactConsoleMu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type artifactScreenshotResponse struct {
	Image string `json:"image"`
	Error string `json:"error"`
}

func (s *Server) handleArtifactScreenshot(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RequestID string `json:"request_id"`
		Image     string `json:"image"`
		Error     string `json:"error"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if body.RequestID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing request_id"})
		return
	}

	s.artifactScreenshotMu.Lock()
	ch, ok := s.artifactScreenshotCh[body.RequestID]
	s.artifactScreenshotMu.Unlock()

	if ok && ch != nil {
		select {
		case ch <- artifactScreenshotResponse{Image: body.Image, Error: body.Error}:
		default:
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func injectConsoleCaptureScript(htmlContent []byte) []byte {
	script := []byte(`
<script>
(function() {
  const sendLog = (type, msg) => {
    window.parent.postMessage({ type: 'artifact-console', logType: type, message: msg }, '*');
  };
  const originalLog = console.log;
  const originalError = console.error;
  console.log = function() {
    sendLog('log', Array.from(arguments).map(arg => {
      try { return typeof arg === 'object' ? JSON.stringify(arg) : String(arg); } catch(e) { return String(arg); }
    }).join(' '));
    originalLog.apply(console, arguments);
  };
  console.error = function() {
    sendLog('error', Array.from(arguments).map(arg => {
      try { return typeof arg === 'object' ? JSON.stringify(arg) : String(arg); } catch(e) { return String(arg); }
    }).join(' '));
    originalError.apply(console, arguments);
  };
  window.onerror = function(message, source, lineno, colno, error) {
    sendLog('error', message + ' (' + source + ':' + lineno + ':' + colno + ')');
  };
  window.addEventListener('unhandledrejection', function(event) {
    sendLog('error', 'Unhandled Promise Rejection: ' + event.reason);
  });
})();
</script>`)

	lower := bytes.ToLower(htmlContent)
	headIdx := bytes.Index(lower, []byte("<head>"))
	if headIdx >= 0 {
		insertPos := headIdx + len("<head>")
		res := make([]byte, 0, len(htmlContent)+len(script))
		res = append(res, htmlContent[:insertPos]...)
		res = append(res, script...)
		res = append(res, htmlContent[insertPos:]...)
		return res
	}
	res := make([]byte, 0, len(htmlContent)+len(script))
	res = append(res, script...)
	res = append(res, htmlContent...)
	return res
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
