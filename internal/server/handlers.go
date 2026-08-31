package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
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
	"unicode/utf8"

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
		r.URL.Path == "/chat" ||
		strings.HasPrefix(r.URL.Path, "/chat/") ||
		r.URL.Path == "/tests" ||
		r.URL.Path == "/tests/new" ||
		strings.HasPrefix(r.URL.Path, "/tests/edit/") ||
		strings.HasPrefix(r.URL.Path, "/tests/group/") ||
		strings.HasPrefix(r.URL.Path, "/tests/agent/") ||
		strings.HasPrefix(r.URL.Path, "/tests/battery/") ||
		r.URL.Path == "/analytics" ||
		r.URL.Path == "/settings" ||
		strings.HasPrefix(r.URL.Path, "/settings/") ||
		r.URL.Path == "/opencode" ||
		r.URL.Path == "/archived" ||
		r.URL.Path == "/modelfile" ||
		strings.HasPrefix(r.URL.Path, "/modelfile/") ||
		r.URL.Path == "/hf" ||
		strings.HasPrefix(r.URL.Path, "/hf/") ||
		r.URL.Path == "/huggingface" ||
		strings.HasPrefix(r.URL.Path, "/huggingface/")
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

func (s *Server) handleAPILogin(w http.ResponseWriter, r *http.Request) {
	s.cfgMu.RLock()
	hasPwd := s.cfg.HasPassword()
	hash := s.cfg.PasswordHash
	s.cfgMu.RUnlock()

	if !hasPwd {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	var body struct {
		Password string `json:"password"`
	}
	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, errors.New("invalid request body"))
			return
		}
	} else {
		if err := r.ParseForm(); err != nil {
			writeError(w, http.StatusBadRequest, errors.New("bad form"))
			return
		}
		body.Password = r.FormValue("password")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(body.Password)); err != nil {
		writeError(w, http.StatusUnauthorized, errors.New("incorrect password"))
		return
	}

	s.cfgMu.RLock()
	s.setSessionCookie(w)
	s.cfgMu.RUnlock()

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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

	running, err := s.ollama.PS(ctx)
	if err != nil {
		running = nil
	}
	runningViews := make([]runningView, 0, len(running))
	var ramBytes, vramBytes, totalBytes uint64
	for _, rm := range running {
		total, vram := normalizeRunningModelSizes(rm.Size, rm.SizeVRAM)
		ram := total - vram
		totalBytes += uint64(total)
		ramBytes += uint64(ram)
		vramBytes += uint64(vram)

		rv := runningView{
			Name:     rm.Name,
			SizeVRAM: vram,
		}
		if !rm.ExpiresAt.IsZero() {
			exp := rm.ExpiresAt
			rv.ExpiresAt = &exp
		}
		runningViews = append(runningViews, rv)
	}

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
		"cpu_model":                sys.CPUModel,
		"cpu_used_pct":             sys.CPUUsedPercent,
		"memory_total":             sys.MemoryTotal,
		"memory_free":              sys.MemoryFree,
		"memory_used":              sys.MemoryUsed,
		"memory_used_pct":          sys.MemoryUsedPct,
		"vram_total":               sys.VramTotal,
		"vram_used":                sys.VramUsed,
		"vram_used_pct":            sys.VramUsedPct,
		"models_ram_loaded_bytes":  ramBytes,
		"models_vram_loaded_bytes": vramBytes,
		"models_loaded_bytes":      totalBytes,
		"running":                  runningViews,
	}
	writeJSON(w, http.StatusOK, resp)
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
		"version":        s.versionInfo,
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
	Name                 string     `json:"name"`
	Size                 int64      `json:"size"`
	ModifiedAt           time.Time  `json:"modified_at"`
	LastUsedAt           *time.Time `json:"last_used_at,omitempty"`
	RecordTokensPerSec   float64    `json:"record_tokens_per_sec,omitempty"`
	RecordTokensPerSecAt *time.Time `json:"record_tokens_per_sec_at,omitempty"`
	MinColdLoadMs        int64      `json:"min_cold_load_ms,omitempty"`
	MinColdLoadAt        *time.Time `json:"min_cold_load_at,omitempty"`
	TotalTokens          int64      `json:"total_tokens,omitempty"`
	TotalCalls           int64      `json:"total_calls,omitempty"`
	Digest               string     `json:"digest"`
	Family               string     `json:"family"`
	Families             []string   `json:"families"`
	Format               string     `json:"format"`
	ParameterSize        string     `json:"parameter_size"`
	Quantization         string     `json:"quantization"`
	ContextLength        int64      `json:"context_length,omitempty"`
	Capabilities         []string   `json:"capabilities,omitempty"`
	ParameterCount       int64      `json:"parameter_count,omitempty"`
	Architecture         string     `json:"architecture,omitempty"`
	FileType             int64      `json:"file_type,omitempty"`
	SizeLabel            string     `json:"size_label,omitempty"`
	IsMOE                bool       `json:"is_moe,omitempty"`
	Loaded               bool       `json:"loaded"`
	SizeVRAM             int64      `json:"size_vram,omitempty"`
	ExpiresAt            *time.Time `json:"expires_at,omitempty"`
	Archived             bool       `json:"archived"`
	IsGhost              bool       `json:"is_ghost,omitempty"`
	UninstallReason      string     `json:"uninstall_reason,omitempty"`
	UninstallAt          *time.Time `json:"uninstall_at,omitempty"`
	IsCustom             bool       `json:"is_custom,omitempty"`
	IsExternal           bool       `json:"is_external,omitempty"`
	Disabled             bool       `json:"disabled,omitempty"`
	URL                  string     `json:"url,omitempty"`
	BaseModel            string     `json:"base_model,omitempty"`
}

func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	models, err := s.ollama.List(ctx)
	if err != nil {
		log.Printf("list failed: %v", err)
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

	s.syncAllModelUsageFamilies()
	out := make([]modelView, 0, len(models))
	for _, m := range models {
		isCustom := false
		baseModel := ""
		if s.customModels != nil && s.customModels.IsCustom(m.Name) {
			isCustom = true
			baseModel = s.customModels.GetBase(m.Name)
		} else if isFixedModelName(m.Name) {
			isCustom = true
			baseModel = fixedBaseName(m.Name)
		}

		if isCustom {
			if baseModel == "" || !strings.Contains(baseModel, ":") || strings.HasSuffix(baseModel, ":latest") {
				prefix := baseModel
				if prefix == "" && isFixedModelName(m.Name) {
					prefix = fixedBaseName(m.Name)
				}
				if prefix != "" {
					cleanPrefix := strings.TrimSuffix(prefix, ":latest")
					for _, other := range models {
						if other.Name != m.Name {
							if other.Name == cleanPrefix || other.Name == cleanPrefix+":latest" {
								baseModel = other.Name
								if s.customModels != nil {
									_ = s.customModels.Register(m.Name, baseModel)
								}
								break
							}
						}
					}
				}
			}
		}

		v := modelView{
			Name:           m.Name,
			Size:           m.Size,
			ModifiedAt:     m.ModifiedAt,
			Digest:         m.Digest,
			Family:         m.Details.Family,
			Families:       m.Details.Families,
			Format:         m.Details.Format,
			ParameterSize:  m.Details.ParameterSize,
			Quantization:   m.Details.QuantizationLevel,
			ContextLength:  modelMeta[m.Digest].ContextLength,
			Capabilities:   modelMeta[m.Digest].Capabilities,
			ParameterCount: modelMeta[m.Digest].ParameterCount,
			Architecture:   modelMeta[m.Digest].Architecture,
			FileType:       modelMeta[m.Digest].FileType,
			SizeLabel:      modelMeta[m.Digest].SizeLabel,
			IsMOE:          modelMeta[m.Digest].IsMOE,
			Archived:       s.archived.IsArchived(m.Name),
			IsCustom:       isCustom,
			BaseModel:      baseModel,
		}
		if s.usage != nil {
			if rec, ok := s.getModelUsage(m.Name); ok {
				v.LastUsedAt = rec.LastUsedAt
				v.RecordTokensPerSec = rec.RecordTokensPerSec
				v.RecordTokensPerSecAt = rec.RecordTokensPerSecAt
				v.MinColdLoadMs = rec.MinColdLoadMs
				v.MinColdLoadAt = rec.MinColdLoadAt
				v.TotalTokens = rec.TotalTokens
				v.TotalCalls = rec.TotalCalls
			}
		}
		if rm, ok := loaded[m.Name]; ok {
			_, vram := normalizeRunningModelSizes(rm.Size, rm.SizeVRAM)
			v.Loaded = true
			v.SizeVRAM = vram
			exp := rm.ExpiresAt
			v.ExpiresAt = &exp
		}
		if s.usage != nil {
			meta := modelMeta[m.Digest]
			if err := s.usage.SetMeta(m.Name, modelUsageMeta{
				ParameterSize:  m.Details.ParameterSize,
				Size:           m.Size,
				Quantization:   m.Details.QuantizationLevel,
				Family:         m.Details.Family,
				ParameterCount: meta.ParameterCount,
				Architecture:   meta.Architecture,
				FileType:       meta.FileType,
				SizeLabel:      meta.SizeLabel,
				IsMOE:          meta.IsMOE,
				ContextLength:  meta.ContextLength,
			}); err != nil {
				log.Printf("usage: SetMeta failed for %q: %v", m.Name, err)
			}
		}
		out = append(out, v)
	}

	if s.externalModels != nil {
		for _, ext := range s.externalModels.All() {
			if ext.Disabled {
				continue
			}
			caps := ext.Capabilities
			if len(caps) == 0 {
				caps = []string{"completion", "tools", "thinking", "vision"}
			}
			ev := modelView{
				Name:         ext.Name,
				Family:       "external",
				Format:       "external",
				Capabilities: caps,
				IsExternal:   true,
				Disabled:     ext.Disabled,
				URL:          cleanURLDisplay(ext.URL),
				ModifiedAt:   ext.CreatedAt,
			}
			if s.usage != nil {
				if rec, ok := s.usage.Get(ext.Name); ok {
					ev.LastUsedAt = rec.LastUsedAt
					ev.RecordTokensPerSec = rec.RecordTokensPerSec
					ev.RecordTokensPerSecAt = rec.RecordTokensPerSecAt
					ev.TotalTokens = rec.TotalTokens
					ev.TotalCalls = rec.TotalCalls
				}
			}
			out = append(out, ev)
		}
	}

	var ghostOut []modelView
	if s.usage != nil {
		installedSet := make(map[string]struct{}, len(models)*2)
		for _, m := range models {
			installedSet[m.Name] = struct{}{}
			installedSet[strings.TrimSuffix(m.Name, ":latest")] = struct{}{}
		}
		for name, rec := range s.usage.All() {
			if s.externalModels != nil && s.externalModels.IsExternal(name) {
				continue
			}
			if s.customModels != nil && s.customModels.IsCustom(name) {
				continue
			}
			if isFixedModelName(name) {
				continue
			}
			if _, ok := installedSet[name]; ok {
				continue
			}
			if _, ok := installedSet[strings.TrimSuffix(name, ":latest")]; ok {
				continue
			}
			gv := modelView{
				Name:                 name,
				IsGhost:              true,
				RecordTokensPerSec:   rec.RecordTokensPerSec,
				RecordTokensPerSecAt: rec.RecordTokensPerSecAt,
				MinColdLoadMs:        rec.MinColdLoadMs,
				MinColdLoadAt:        rec.MinColdLoadAt,
				LastUsedAt:           rec.LastUsedAt,
				TotalTokens:          rec.TotalTokens,
				TotalCalls:           rec.TotalCalls,
				ParameterSize:        rec.ParameterSize,
				Size:                 rec.Size,
				Quantization:         rec.Quantization,
				Family:               rec.Family,
				ParameterCount:       rec.ParameterCount,
				Architecture:         rec.Architecture,
				FileType:             rec.FileType,
				SizeLabel:            rec.SizeLabel,
				IsMOE:                rec.IsMOE,
				ContextLength:        rec.ContextLength,
			}
			if rec.LastUsedAt != nil {
				gv.ModifiedAt = *rec.LastUsedAt
			}
			// Why the model was removed is often the only thing worth showing
			// for a ghost that never ran well enough to record any usage.
			if s.uninst != nil {
				if u, ok := s.uninst.Get(name); ok && u.LastReason != "" {
					gv.UninstallReason = u.LastReason
					if !u.LastUninstallAt.IsZero() {
						at := u.LastUninstallAt
						gv.UninstallAt = &at
					}
				}
			}
			ghostOut = append(ghostOut, gv)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"models":       out,
		"ghost_models": ghostOut,
	})
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
	if s.externalModels != nil && s.externalModels.IsExternal(name) {
		writeError(w, http.StatusBadRequest, errors.New("cannot archive external model"))
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
	ContextLength  int64
	Capabilities   []string
	ParameterCount int64
	Architecture   string
	FileType       int64
	SizeLabel      string
	IsMOE          bool
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
		meta, okMeta := s.metaCache[m.Digest]
		if okCtx && okCaps && okMeta {
			result[m.Digest] = modelMetaCache{
				ContextLength:  ctxLen,
				Capabilities:   append([]string(nil), caps...),
				ParameterCount: meta.ParameterCount,
				Architecture:   meta.Architecture,
				FileType:       meta.FileType,
				SizeLabel:      meta.SizeLabel,
				IsMOE:          meta.IsMOE,
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
		digest         string
		contextLen     int64
		capabilities   []string
		parameterCount int64
		architecture   string
		fileType       int64
		sizeLabel      string
		isMOE          bool
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
			if show != nil && show.Modelfile != "" && s.customModels != nil {
				from := extractLineDirective(show.Modelfile, "FROM")
				if from != "" && !isLocalFilePathOrDigest(from) && from != m.Name {
					_ = s.customModels.Register(m.Name, from)
				}
			}
			out <- item{
				digest:         m.Digest,
				contextLen:     extractContextLength(show),
				capabilities:   append([]string(nil), show.Capabilities...),
				parameterCount: extractParameterCount(show),
				architecture:   extractArchitecture(show),
				fileType:       extractFileType(show),
				sizeLabel:      extractSizeLabel(show),
				isMOE:          extractIsMOE(show),
			}
		}(m)
	}
	wg.Wait()
	close(out)

	s.ctxMu.Lock()
	if s.ctxCache == nil {
		s.ctxCache = make(map[string]int64)
	}
	if s.capsCache == nil {
		s.capsCache = make(map[string][]string)
	}
	if s.metaCache == nil {
		s.metaCache = make(map[string]modelMetaCache)
	}
	for it := range out {
		s.ctxCache[it.digest] = it.contextLen
		s.capsCache[it.digest] = append([]string(nil), it.capabilities...)
		s.metaCache[it.digest] = modelMetaCache{
			ParameterCount: it.parameterCount,
			Architecture:   it.architecture,
			FileType:       it.fileType,
			SizeLabel:      it.sizeLabel,
			IsMOE:          it.isMOE,
		}
		result[it.digest] = modelMetaCache{
			ContextLength:  it.contextLen,
			Capabilities:   append([]string(nil), it.capabilities...),
			ParameterCount: it.parameterCount,
			Architecture:   it.architecture,
			FileType:       it.fileType,
			SizeLabel:      it.sizeLabel,
			IsMOE:          it.isMOE,
		}
	}
	s.ctxMu.Unlock()
	return result
}

// extractModelInfoString reads a string-valued key from a ShowResponse model_info.
func extractModelInfoString(show *ollama.ShowResponse, key string) string {
	if show == nil || show.ModelInfo == nil {
		return ""
	}
	raw, ok := show.ModelInfo[key]
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

// extractModelInfoInt reads an int-valued key from a ShowResponse model_info.
func extractModelInfoInt(show *ollama.ShowResponse, key string) int64 {
	if show == nil || show.ModelInfo == nil {
		return 0
	}
	raw, ok := show.ModelInfo[key]
	if !ok {
		return 0
	}
	var n float64
	if json.Unmarshal(raw, &n) == nil && n > 0 {
		return int64(n)
	}
	return 0
}

// extractArchitecture reads the architecture from a ShowResponse model_info.
// (Defined in model_repair.go; kept as a thin alias for clarity.)

func extractParameterCount(show *ollama.ShowResponse) int64 {
	return extractModelInfoInt(show, "general.parameter_count")
}

func extractFileType(show *ollama.ShowResponse) int64 {
	return extractModelInfoInt(show, "general.file_type")
}

func extractSizeLabel(show *ollama.ShowResponse) string {
	return extractModelInfoString(show, "general.size_label")
}

// extractIsMOE reports whether a model is a Mixture-of-Experts (MoE) by checking
// for a positive "<arch>.expert_count" in its GGUF model_info.
func extractIsMOE(show *ollama.ShowResponse) bool {
	if show == nil || show.ModelInfo == nil {
		return false
	}
	if arch := extractArchitecture(show); arch != "" {
		if n := extractModelInfoInt(show, arch+".expert_count"); n > 0 {
			return true
		}
	}
	for k, raw := range show.ModelInfo {
		if strings.HasSuffix(k, ".expert_count") {
			var n float64
			if json.Unmarshal(raw, &n) == nil && n > 0 {
				return true
			}
		}
	}
	return false
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
	Name                 string              `json:"name"`
	License              string              `json:"license,omitempty"`
	Modelfile            string              `json:"modelfile,omitempty"`
	Parameters           string              `json:"parameters,omitempty"`
	Template             string              `json:"template,omitempty"`
	System               string              `json:"system,omitempty"`
	Details              ollama.ModelDetails `json:"details"`
	Capabilities         []string            `json:"capabilities,omitempty"`
	ContextLength        int64               `json:"context_length,omitempty"`
	Architecture         string              `json:"architecture,omitempty"`
	ParameterCount       int64               `json:"parameter_count,omitempty"`
	ModelInfo            map[string]any      `json:"model_info,omitempty"`
	ArtifactCount        int                 `json:"artifact_count,omitempty"`
	ArtifactBytes        int64               `json:"artifact_bytes,omitempty"`
	ModifiedAt           time.Time           `json:"modified_at"`
	LastUsedAt           *time.Time          `json:"last_used_at,omitempty"`
	RecordTokensPerSec   float64             `json:"record_tokens_per_sec,omitempty"`
	RecordTokensPerSecAt *time.Time          `json:"record_tokens_per_sec_at,omitempty"`
	MinColdLoadMs        int64               `json:"min_cold_load_ms,omitempty"`
	MinColdLoadAt        *time.Time          `json:"min_cold_load_at,omitempty"`
	IsCustom             bool                `json:"is_custom,omitempty"`
	IsExternal           bool                `json:"is_external,omitempty"`
	Disabled             bool                `json:"disabled,omitempty"`
	URL                  string              `json:"url,omitempty"`
	BaseModel            string              `json:"base_model,omitempty"`
}

func (s *Server) handleShowModel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing model name"))
		return
	}
	if s.externalModels != nil && s.externalModels.IsExternal(name) {
		rec, _ := s.externalModels.Get(name)
		caps := rec.Capabilities
		if len(caps) == 0 {
			caps = []string{"completion", "tools", "thinking", "vision"}
		}
		detail := modelDetail{
			Name:         rec.Name,
			IsExternal:   true,
			Disabled:     rec.Disabled,
			URL:          cleanURLDisplay(rec.URL),
			Capabilities: caps,
			Details: ollama.ModelDetails{
				Format: "external",
				Family: "external",
			},
			ModifiedAt: rec.CreatedAt,
		}
		if s.usage != nil {
			if urec, ok := s.getModelUsage(name); ok {
				detail.LastUsedAt = urec.LastUsedAt
				detail.RecordTokensPerSec = urec.RecordTokensPerSec
				detail.RecordTokensPerSecAt = urec.RecordTokensPerSecAt
			}
		}
		detail.ArtifactCount, detail.ArtifactBytes = s.artifactInfoForModel(r.Context(), name)
		writeJSON(w, http.StatusOK, detail)
		return
	}
	show, err := s.ollama.Show(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}

	isCustom := false
	baseModel := ""
	if s.customModels != nil && s.customModels.IsCustom(name) {
		isCustom = true
		baseModel = s.customModels.GetBase(name)
	} else if isFixedModelName(name) {
		isCustom = true
		baseModel = fixedBaseName(name)
	}
	if show.Modelfile != "" {
		from := extractLineDirective(show.Modelfile, "FROM")
		if from != "" && !isLocalFilePathOrDigest(from) && from != name {
			isCustom = true
			baseModel = from
			if s.customModels != nil {
				_ = s.customModels.Register(name, baseModel)
			}
		}
	}

	detail := modelDetail{
		Name:         name,
		License:      show.License,
		Modelfile:    show.Modelfile,
		Parameters:   show.Parameters,
		Template:     show.Template,
		System:       show.System,
		Details:      show.Details,
		Capabilities: show.Capabilities,
		ModifiedAt:   show.ModifiedAt,
		IsCustom:     isCustom,
		BaseModel:    baseModel,
	}
	if detail.System == "" {
		// Older Ollama versions do not return a resolved system prompt in
		// /api/show; fall back to the SYSTEM directive in the modelfile,
		// mirroring "ollama show --system".
		detail.System = extractModelfileSystem(show.Modelfile)
	}
	if s.usage != nil {
		if rec, ok := s.getModelUsage(name); ok {
			detail.LastUsedAt = rec.LastUsedAt
			detail.RecordTokensPerSec = rec.RecordTokensPerSec
			detail.RecordTokensPerSecAt = rec.RecordTokensPerSecAt
			detail.MinColdLoadMs = rec.MinColdLoadMs
			detail.MinColdLoadAt = rec.MinColdLoadAt
		}
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
	if s.externalModels != nil && s.externalModels.IsExternal(name) {
		_ = s.externalModels.Unregister(name)
		writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "external": true})
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
	// Capture the model's metadata before deletion so the "ghost" record keeps
	// enough context for analytics charts (params, size, quant, family, etc.).
	if s.usage != nil {
		models, _ := s.ollama.List(r.Context())
		for _, m := range models {
			if m.Name == name {
				meta := s.fetchModelMeta(r.Context(), []ollama.Model{m})[m.Digest]
				_ = s.usage.SetMeta(m.Name, modelUsageMeta{
					ParameterSize:  m.Details.ParameterSize,
					Size:           m.Size,
					Quantization:   m.Details.QuantizationLevel,
					Family:         m.Details.Family,
					ParameterCount: meta.ParameterCount,
					Architecture:   meta.Architecture,
					FileType:       meta.FileType,
					SizeLabel:      meta.SizeLabel,
					IsMOE:          meta.IsMOE,
				})
				break
			}
		}
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
	if s.customModels != nil {
		_ = s.customModels.Unregister(name)
	}
	resp := map[string]any{"deleted": name}
	deletedArtifacts := s.deleteArtifactsForModel(r.Context(), name)
	if !isFixedModelName(name) {
		fixed := fixedModelName(name)
		isLinkedFixed := false
		if s.customModels != nil {
			base := s.customModels.GetBase(fixed)
			if base == name || (strings.HasSuffix(name, ":latest") && base == strings.TrimSuffix(name, ":latest")) || (!strings.Contains(name, ":") && base == name+":latest") {
				isLinkedFixed = true
			}
		}
		if isLinkedFixed && s.modelExists(r.Context(), fixed) {
			if err := s.ollama.Delete(r.Context(), fixed); err != nil {
				resp["warning"] = "base model deleted, but fixed model could not be deleted: " + err.Error()
			} else {
				resp["deleted_fixed"] = fixed
			}
		}
		if isLinkedFixed && s.customModels != nil {
			_ = s.customModels.Unregister(fixed)
		}
		if isLinkedFixed {
			deletedArtifacts += s.deleteArtifactsForModel(r.Context(), fixed)
		}
	}
	if deletedArtifacts > 0 {
		resp["deleted_artifacts"] = deletedArtifacts
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleDeleteGhost removes a model's persistent usage/metadata record so it
// no longer appears as a ghost in stats/charts. The uninstall-history record
// (deletion reason) is preserved.
func (s *Server) handleDeleteGhost(w http.ResponseWriter, r *http.Request) {
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
	if s.usage == nil {
		writeError(w, http.StatusInternalServerError, errors.New("usage store unavailable"))
		return
	}
	// Guard: refuse to remove a model that is still installed; use the normal
	// delete flow for those instead.
	installed, _ := s.ollama.List(r.Context())
	for _, m := range installed {
		if m.Name == name || m.Name == strings.TrimSuffix(name, ":latest") || name == strings.TrimSuffix(m.Name, ":latest") {
			writeError(w, http.StatusBadRequest, errors.New("model is installed; delete it from the models list instead"))
			return
		}
	}
	removed, err := s.usage.Delete(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": removed, "name": name})
}

func (s *Server) handleGetModelUsage(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing model name"))
		return
	}
	var rec ModelUsageRecord
	var found bool
	if s.usage != nil {
		rec, found = s.usage.Get(name)
		if !found {
			if strings.HasSuffix(name, ":latest") {
				rec, found = s.usage.Get(strings.TrimSuffix(name, ":latest"))
			} else {
				rec, found = s.usage.Get(name + ":latest")
			}
		}
	}
	var uninstRec any
	if s.uninst != nil {
		if u, uok := s.uninst.Get(name); uok {
			uninstRec = u
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":      name,
		"found":     found,
		"usage":     rec,
		"uninstall": uninstRec,
	})
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
	if preview.Projector != "" {
		s.checkRepairProjectorDisk(r.Context(), preview)
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
	replacing := s.modelExists(r.Context(), preview.TargetName)
	if isStream {
		sendSSE("progress", map[string]any{
			"stage":   "creating_model",
			"percent": 0,
		})
		err = s.ollama.CreateStream(r.Context(), createReq, func(ev ollama.CreateProgress) error {
			pct := float64(0)
			if ev.Total > 0 {
				pct = float64(ev.Completed) / float64(ev.Total) * 100
			}
			sendSSE("progress", map[string]any{
				"stage":     "creating_model",
				"status":    ev.Status,
				"completed": ev.Completed,
				"total":     ev.Total,
				"percent":   pct,
			})
			return nil
		})
	} else {
		err = s.ollama.Create(r.Context(), createReq)
	}
	if err != nil {
		if isStream {
			sendSSE("error", map[string]any{"error": err.Error()})
		} else {
			writeError(w, http.StatusBadGateway, err)
		}
		return
	}
	if s.customModels != nil {
		_ = s.customModels.Register(preview.TargetName, preview.BaseName)
	}
	if s.usage != nil {
		_ = s.usage.InheritUsage(preview.BaseName, preview.TargetName)
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

func formatBytesInt64(b int64) string {
	if b <= 0 {
		return "0 B"
	}
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func (s *Server) fetchProjectorSize(ctx context.Context, ref string) (int64, error) {
	u, err := resolveProjectorURL(ref)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "ollama-manager")
	resp, err := projectorHTTPClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return resp.ContentLength, nil
}

func (s *Server) checkRepairProjectorDisk(ctx context.Context, preview *modelRepairPreview) {
	if preview == nil || preview.Projector == "" {
		return
	}
	headCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	projBytes, err := s.fetchProjectorSize(headCtx, preview.Projector)
	if err != nil || projBytes <= 0 {
		projBytes = 850 * 1024 * 1024 // fallback estimate ~850MB
	}
	preview.ProjectorBytes = projBytes

	s.cfgMu.RLock()
	cfgPath := s.cfg.Path()
	lang := s.cfg.Language
	s.cfgMu.RUnlock()

	diskPath := resolveDiskProbePath(cfgPath)
	sys := sysmetrics.Collect(ctx, diskPath)
	if sys.DiskFree > 0 {
		preview.FreeDiskBytes = sys.DiskFree
		// Required space: projector download in temp (%TEMP%) + blob in Ollama (~/.ollama/models/blobs) + 512 MB safety buffer
		required := uint64(projBytes)*2 + 512*1024*1024
		preview.RequiredDiskBytes = required
		if sys.DiskFree < required {
			preview.DiskSpaceWarning = true
			if lang == "es" {
				preview.Warnings = append(preview.Warnings, fmt.Sprintf(
					"⚠️ Advertencia de espacio en disco: Instalar este proyector de visión requiere aproximadamente %s libres en disco (%s de descarga + almacenamiento temporal), pero solo dispones de %s libres.",
					formatBytesInt64(int64(required)),
					formatBytesInt64(projBytes),
					formatBytesInt64(int64(sys.DiskFree)),
				))
			} else {
				preview.Warnings = append(preview.Warnings, fmt.Sprintf(
					"⚠️ Low disk space warning: Installing this vision projector requires approximately %s of free disk space (%s download + temporary storage), but only %s is available.",
					formatBytesInt64(int64(required)),
					formatBytesInt64(projBytes),
					formatBytesInt64(int64(sys.DiskFree)),
				))
			}
		}
	}
}

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
	s.projectorCacheMu.RLock()
	cachedHex := s.projectorCache[u]
	s.projectorCacheMu.RUnlock()

	if cachedHex != "" {
		digest := "sha256:" + cachedHex
		if exists, err := s.ollama.HeadBlob(ctx, digest); err == nil && exists {
			if onProgress != nil {
				onProgress(1, 1)
			}
			return cachedHex, nil
		}
	}

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

	s.projectorCacheMu.Lock()
	if s.projectorCache == nil {
		s.projectorCache = make(map[string]string)
	}
	s.projectorCache[u] = hexSum
	s.projectorCacheMu.Unlock()

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

// ---------- model create (modelfile studio) ----------

type modelCreateRequestBody struct {
	Name       string            `json:"name"`
	From       string            `json:"from,omitempty"`
	Modelfile  string            `json:"modelfile,omitempty"`
	System     string            `json:"system,omitempty"`
	Template   string            `json:"template,omitempty"`
	Parameters map[string]any    `json:"parameters,omitempty"`
	Files      map[string]string `json:"files,omitempty"`
}

func (s *Server) handleCreateModel(w http.ResponseWriter, r *http.Request) {
	var body modelCreateRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing model name"))
		return
	}

	createReq := ollama.CreateRequest{
		Model:      name,
		From:       strings.TrimSpace(body.From),
		System:     strings.TrimSpace(body.System),
		Template:   strings.TrimSpace(body.Template),
		Parameters: body.Parameters,
		Modelfile:  strings.TrimSpace(body.Modelfile),
		Files:      body.Files,
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

	onSuccess := func() {
		baseModel := strings.TrimSpace(body.From)
		if baseModel == "" && body.Modelfile != "" {
			baseModel = extractLineDirective(body.Modelfile, "FROM")
		}
		if baseModel != "" && isLocalFilePathOrDigest(baseModel) {
			baseModel = ""
		}
		if s.customModels != nil {
			_ = s.customModels.Register(name, baseModel)
		}
		if s.usage != nil && baseModel != "" {
			_ = s.usage.InheritUsage(baseModel, name)
		}
	}

	if isStream {
		sendSSE("status", map[string]any{
			"status": "starting",
			"model":  name,
		})
		err := s.ollama.CreateStream(r.Context(), createReq, func(ev ollama.CreateProgress) error {
			sendSSE("progress", ev)
			return nil
		})
		if err != nil {
			sendSSE("error", map[string]any{"error": err.Error()})
			return
		}
		onSuccess()
		sendSSE("done", map[string]any{"model": name, "success": true})
		return
	}

	if err := s.ollama.Create(r.Context(), createReq); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	onSuccess()
	writeJSON(w, http.StatusOK, map[string]any{"model": name, "success": true})
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

// estimateTextTokens is a rough fallback (~4 chars per token) used when Ollama
// does not report token counts, e.g. for some vision/OCR responses.
func estimateTextTokens(s string) int {
	if s == "" {
		return 0
	}
	n := utf8.RuneCountInString(s)
	if n < 4 {
		return 1
	}
	return n / 4
}

// modelFamily returns all model names in the same custom/base lineage as name:
// name itself, its base model (if name is custom), and any custom models derived
// from name or from its base model.
func (s *Server) modelFamily(name string) []string {
	if name == "" {
		return nil
	}
	name = strings.TrimSpace(name)
	seen := map[string]bool{name: true}
	family := []string{name}

	add := func(n string) {
		n = strings.TrimSpace(n)
		if n != "" && !seen[n] {
			seen[n] = true
			family = append(family, n)
		}
	}

	// Support :latest tag equivalence (e.g. "model" <-> "model:latest")
	if strings.HasSuffix(name, ":latest") {
		add(strings.TrimSuffix(name, ":latest"))
	} else if !strings.Contains(name, ":") {
		add(name + ":latest")
	}

	base := ""
	if s.customModels != nil {
		base = s.customModels.GetBase(name)
	}
	if base != "" {
		add(base)
		if strings.HasSuffix(base, ":latest") {
			add(strings.TrimSuffix(base, ":latest"))
		} else if !strings.Contains(base, ":") {
			add(base + ":latest")
		}
	}

	if s.customModels != nil {
		for custom, rec := range s.customModels.All() {
			if rec.BaseModel == name || (base != "" && rec.BaseModel == base) {
				add(custom)
			}
			if custom == name && rec.BaseModel != "" {
				add(rec.BaseModel)
			}
		}
	}

	return family
}

// getModelUsage retrieves the usage record for name, unifying velocity (record tokens/sec),
// load times, and last used across custom models and their base parent model.
func (s *Server) getModelUsage(name string) (ModelUsageRecord, bool) {
	if s.usage == nil || name == "" {
		return ModelUsageRecord{}, false
	}
	family := s.modelFamily(name)

	var targetRec ModelUsageRecord
	var targetFound bool
	if rec, ok := s.usage.Get(name); ok {
		targetRec = rec
		targetFound = true
	}

	var bestTelemetry ModelUsageRecord
	var hasTelemetry bool

	for _, member := range family {
		if rec, ok := s.usage.Get(member); ok {
			if rec.RecordTokensPerSec > 0 || rec.LastUsedAt != nil || rec.MinColdLoadMs > 0 || rec.TotalCalls > 0 {
				if !hasTelemetry {
					bestTelemetry = rec
					hasTelemetry = true
				} else {
					bestTelemetry = mergeBaseUsage(bestTelemetry, rec)
				}
			}
		}
	}

	if !targetFound && !hasTelemetry {
		return ModelUsageRecord{}, false
	}
	if !hasTelemetry {
		return targetRec, true
	}
	merged := mergeBaseUsage(targetRec, bestTelemetry)
	return merged, true
}

func (s *Server) syncAllModelUsageFamilies() {
	if s.usage == nil {
		return
	}
	if s.customModels != nil {
		for custom, rec := range s.customModels.All() {
			if rec.BaseModel != "" {
				_ = s.usage.InheritUsage(rec.BaseModel, custom)
			}
		}
	}
}

// recordModelUsage records token telemetry across the entire family (custom and base model).
func (s *Server) recordModelUsage(name string, evalCount int, evalDurationNs int64, promptEvalCount int, usedAt time.Time) {
	if s.usage == nil || name == "" {
		return
	}
	for _, member := range s.modelFamily(name) {
		_ = s.usage.Record(member, evalCount, evalDurationNs, promptEvalCount, usedAt)
	}
}

func (s *Server) recordModelTPS(name string, tps float64, usedAt time.Time) {
	if s.usage == nil || name == "" {
		return
	}
	for _, member := range s.modelFamily(name) {
		_ = s.usage.RecordTPS(member, tps, usedAt)
	}
}

func (s *Server) recordModelColdLoad(name string, durationMs int64, at time.Time) {
	if s.usage == nil || name == "" {
		return
	}
	for _, member := range s.modelFamily(name) {
		_ = s.usage.RecordColdLoad(member, durationMs, at)
	}
}

// recordCancelUsage records a cancelled streaming response as used when it was
// progressing too slowly to wait for completion (< cancelRecordThreshold). Rates
// below minRecordTPS are indexed at that floor so the model is registered as
// functional but extremely slow. At or above the threshold nothing is saved;
// the response must complete to count. The cancellation is only registered when
// the model has no record yet: once it has a recorded tokens-per-second, a
// cancelled response is never saved.
func (s *Server) recordCancelUsage(model, content string, startedAt time.Time) {
	if s.usage == nil {
		return
	}
	if rec, ok := s.usage.Get(model); ok && rec.RecordTokensPerSec > 0 {
		return
	}
	est := estimateTextTokens(content)
	if est <= 0 {
		return
	}
	elapsed := time.Since(startedAt)
	if elapsed <= 0 {
		return
	}
	tps := float64(est) / elapsed.Seconds()
	switch {
	case tps < minRecordTPS:
		s.recordModelTPS(model, minRecordTPS, time.Now())
	case tps < cancelRecordThreshold:
		s.recordModelTPS(model, tps, time.Now())
	}
}

// estimatePromptTokens approximates prompt token usage from the request when
// Ollama reports a zero prompt_eval_count (vision/OCR requests). Text counts
// roughly one token per 4 characters plus a nominal allowance per image.
func estimatePromptTokens(body chatRequestBody) int {
	var b strings.Builder
	for _, m := range body.Messages {
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	tokens := estimateTextTokens(b.String())
	for _, m := range body.Messages {
		tokens += len(m.Images) * 256
	}
	return tokens
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

	var wasCold bool = true
	isSameModelName := func(a, b string) bool {
		a = strings.TrimSpace(a)
		b = strings.TrimSpace(b)
		if a == b {
			return true
		}
		if strings.TrimSuffix(a, ":latest") == strings.TrimSuffix(b, ":latest") {
			return true
		}
		aBase := a[strings.LastIndex(a, "/")+1:]
		bBase := b[strings.LastIndex(b, "/")+1:]
		return strings.TrimSuffix(aBase, ":latest") == strings.TrimSuffix(bBase, ":latest")
	}
	if running, err := s.ollama.PS(r.Context()); err == nil {
		for _, rm := range running {
			if isSameModelName(rm.Name, body.Model) || isSameModelName(rm.Model, body.Model) {
				wasCold = false
				break
			}
		}
	}

	out, err := s.ollama.Embed(r.Context(), body.Model, body.Input)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}

	evalCount := out.EvalCount
	evalDuration := out.EvalDuration
	if evalCount <= 0 || evalDuration <= 0 {
		evalCount = out.PromptEvalCount
		evalDuration = out.PromptEvalDuration
	}
	s.recordModelUsage(body.Model, evalCount, evalDuration, out.PromptEvalCount, time.Now())
	if wasCold && out.LoadDuration > 0 {
		s.recordModelColdLoad(body.Model, out.LoadDuration/1e6, time.Now())
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

	isSameModelName := func(a, b string) bool {
		a = strings.TrimSpace(a)
		b = strings.TrimSpace(b)
		if a == b {
			return true
		}
		if strings.TrimSuffix(a, ":latest") == strings.TrimSuffix(b, ":latest") {
			return true
		}
		aBase := a[strings.LastIndex(a, "/")+1:]
		bBase := b[strings.LastIndex(b, "/")+1:]
		return strings.TrimSuffix(aBase, ":latest") == strings.TrimSuffix(bBase, ":latest")
	}

	isComputeOrOOMError := func(err error) bool {
		if err == nil {
			return false
		}
		s := strings.ToLower(err.Error())
		return strings.Contains(s, "compute error") ||
			strings.Contains(s, "out of memory") ||
			strings.Contains(s, "cuda error") ||
			strings.Contains(s, "cuda out of memory") ||
			strings.Contains(s, "metal: command buffer") ||
			strings.Contains(s, "failed to allocate") ||
			strings.Contains(s, "not enough memory") ||
			strings.Contains(s, "llama-server chat error")
	}

	isExternal := s.externalModels != nil && s.externalModels.IsExternal(body.Model)
	var wasCold bool = !isExternal
	if !isExternal {
		if running, err := s.ollama.PS(r.Context()); err == nil {
			for _, rm := range running {
				if isSameModelName(rm.Name, body.Model) || isSameModelName(rm.Model, body.Model) {
					wasCold = false
					break
				}
			}
		}
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
		var firstTokenTime time.Duration
		var final ollama.GenerateChunk
		var accContent strings.Builder
		err := s.ollama.Generate(r.Context(), genReq, func(chunk ollama.GenerateChunk) error {
			if wasCold && firstTokenTime == 0 && (chunk.Response != "" || chunk.Image != "") {
				firstTokenTime = time.Since(startedAt)
				s.recordModelColdLoad(body.Model, firstTokenTime.Milliseconds(), time.Now())
			}
			if chunk.Response != "" {
				accContent.WriteString(chunk.Response)
			}
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
		if err != nil && isComputeOrOOMError(err) && r.Context().Err() == nil {
			log.Printf("[image-gen] compute/memory error detected for %s (%v), unloading models to free GPU/RAM and retrying once...", body.Model, err)
			if running, psErr := s.ollama.PS(r.Context()); psErr == nil {
				for _, rm := range running {
					_ = s.ollama.Unload(r.Context(), rm.Name)
				}
			}
			time.Sleep(600 * time.Millisecond)

			firstTokenTime = 0
			final = ollama.GenerateChunk{}
			accContent.Reset()
			err = s.ollama.Generate(r.Context(), genReq, func(chunk ollama.GenerateChunk) error {
				if firstTokenTime == 0 && (chunk.Response != "" || chunk.Image != "") {
					firstTokenTime = time.Since(startedAt)
				}
				if chunk.Response != "" {
					accContent.WriteString(chunk.Response)
				}
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
		}
		if err != nil {
			if r.Context().Err() != nil {
				s.recordCancelUsage(body.Model, accContent.String(), startedAt)
				return
			}
			errMsg := err.Error()
			if strings.Contains(errMsg, "mlx runner failed") || strings.Contains(errMsg, "failed to initialize MLX") || strings.Contains(errMsg, "failed to load MLX") {
				if s.cfg.Language == "es" {
					errMsg = "El modelo de generación de imágenes no está soportado en este sistema operativo (Windows/Linux). Los modelos basados en MLX solo funcionan de forma nativa en dispositivos Apple Silicon (macOS)."
				} else {
					errMsg = "This image generation model is not supported on this operating system (Windows/Linux). MLX-based models only run natively on Apple Silicon (macOS) devices."
				}
			} else if isComputeOrOOMError(err) {
				if s.cfg.Language == "es" {
					errMsg = "El modelo se quedó sin memoria suficiente (VRAM / RAM) para procesar esta solicitud. Se intentó liberar memoria descargando procesos de modelos en segundo plano, pero la GPU/sistema no pudo procesar el contexto. Prueba reduciendo el tamaño del contexto (num_ctx), cerrando aplicaciones pesadas o usando una cuantización menor."
				} else {
					errMsg = "The model ran out of memory (VRAM / RAM) to process this request. An automatic attempt was made to free memory by unloading running models, but the GPU/system could not allocate sufficient memory. Try reducing the context length (num_ctx), closing heavy applications, or using a smaller quantization."
				}
			}
			send("error", map[string]any{"error": errMsg})
			return
		}

		evalCount := final.EvalCount
		evalDuration := final.EvalDuration
		promptEvalCount := final.PromptEvalCount
		if evalCount <= 0 {
			if est := estimateTextTokens(accContent.String()); est > 0 {
				evalCount = est
			}
		}
		if promptEvalCount <= 0 {
			if est := estimatePromptTokens(body); est > 0 {
				promptEvalCount = est
			}
		}
		if evalCount > 0 && evalDuration <= 0 {
			evalDuration = int64(time.Since(startedAt))
		}
		totalTokens := promptEvalCount + evalCount
		s.recordModelUsage(body.Model, evalCount, evalDuration, promptEvalCount, time.Now())
		send("done", map[string]any{
			"elapsed_ms":         time.Since(startedAt).Milliseconds(),
			"prompt_tokens":      promptEvalCount,
			"completion_tokens":  evalCount,
			"total_tokens":       totalTokens,
			"prompt_duration_ns": final.PromptEvalDuration,
			"eval_duration_ns":   evalDuration,
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
	var firstTokenTime time.Duration
	var final ollama.ChatChunk
	var accContent strings.Builder
	var accThinking strings.Builder
	err := s.chatWithModel(r.Context(), chatReq, func(chunk ollama.ChatChunk) error {
		if wasCold && firstTokenTime == 0 && (chunk.Message.Content != "" || chunk.Message.Thinking != "") {
			firstTokenTime = time.Since(startedAt)
			s.recordModelColdLoad(body.Model, firstTokenTime.Milliseconds(), time.Now())
		}
		if chunk.Message.Content != "" {
			accContent.WriteString(chunk.Message.Content)
		}
		if chunk.Message.Thinking != "" {
			accThinking.WriteString(chunk.Message.Thinking)
		}
		send("chunk", chunk)
		if chunk.Done {
			final = chunk
		}
		return nil
	})

	if err != nil && isComputeOrOOMError(err) && r.Context().Err() == nil {
		log.Printf("[chat] compute/memory error detected for %s (%v), unloading models to free GPU/RAM and retrying once...", body.Model, err)
		if running, psErr := s.ollama.PS(r.Context()); psErr == nil {
			for _, rm := range running {
				_ = s.ollama.Unload(r.Context(), rm.Name)
			}
		}
		time.Sleep(600 * time.Millisecond)

		firstTokenTime = 0
		final = ollama.ChatChunk{}
		accContent.Reset()
		accThinking.Reset()
		err = s.chatWithModel(r.Context(), chatReq, func(chunk ollama.ChatChunk) error {
			if wasCold && firstTokenTime == 0 && (chunk.Message.Content != "" || chunk.Message.Thinking != "") {
				firstTokenTime = time.Since(startedAt)
				s.recordModelColdLoad(body.Model, firstTokenTime.Milliseconds(), time.Now())
			}
			if chunk.Message.Content != "" {
				accContent.WriteString(chunk.Message.Content)
			}
			if chunk.Message.Thinking != "" {
				accThinking.WriteString(chunk.Message.Thinking)
			}
			send("chunk", chunk)
			if chunk.Done {
				final = chunk
			}
			return nil
		})
	}

	if err != nil {
		if r.Context().Err() != nil {
			s.recordCancelUsage(body.Model, accContent.String()+"\n"+accThinking.String(), startedAt)
			return
		}
		errMsg := err.Error()
		if strings.Contains(errMsg, "mlx runner failed") || strings.Contains(errMsg, "failed to initialize MLX") || strings.Contains(errMsg, "failed to load MLX") {
			if s.cfg.Language == "es" {
				errMsg = "El modelo de generación de imágenes no está soportado en este sistema operativo (Windows/Linux). Los modelos basados en MLX solo funcionan de forma nativa en dispositivos Apple Silicon (macOS)."
			} else {
				errMsg = "This image generation model is not supported on this operating system (Windows/Linux). MLX-based models only run natively on Apple Silicon (macOS) devices."
			}
		} else if isComputeOrOOMError(err) {
			if s.cfg.Language == "es" {
				errMsg = "El modelo se quedó sin memoria suficiente (VRAM / RAM) para procesar esta solicitud. Se intentó liberar memoria descargando procesos de modelos en segundo plano, pero la GPU/sistema no pudo procesar el contexto. Prueba reduciendo el tamaño del contexto (num_ctx), cerrando aplicaciones pesadas o usando una cuantización menor."
			} else {
				errMsg = "The model ran out of memory (VRAM / RAM) to process this request. An automatic attempt was made to free memory by unloading running models, but the GPU/system could not allocate sufficient memory. Try reducing the context length (num_ctx), closing heavy applications, or using a smaller quantization."
			}
		}
		send("error", map[string]any{"error": errMsg})
		return
	}

	evalCount := final.EvalCount
	evalDuration := final.EvalDuration
	promptEvalCount := final.PromptEvalCount
	if evalCount <= 0 {
		if est := estimateTextTokens(accContent.String() + "\n" + accThinking.String()); est > 0 {
			evalCount = est
		}
	}
	if promptEvalCount <= 0 {
		if est := estimatePromptTokens(body); est > 0 {
			promptEvalCount = est
		}
	}
	if evalCount > 0 && evalDuration <= 0 {
		evalDuration = int64(time.Since(startedAt))
	}
	totalTokens := promptEvalCount + evalCount
	s.recordModelUsage(body.Model, evalCount, evalDuration, promptEvalCount, time.Now())
	send("done", map[string]any{
		"elapsed_ms":         time.Since(startedAt).Milliseconds(),
		"prompt_tokens":      promptEvalCount,
		"completion_tokens":  evalCount,
		"total_tokens":       totalTokens,
		"prompt_duration_ns": final.PromptEvalDuration,
		"eval_duration_ns":   evalDuration,
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

func modelRepoBase(name string) string {
	s := strings.TrimSpace(name)
	s = strings.ToLower(s)
	for _, p := range []string{"https://", "http://"} {
		if strings.HasPrefix(s, p) {
			s = strings.TrimPrefix(s, p)
		}
	}
	s = strings.TrimPrefix(s, "ollama.com/library/")
	s = strings.TrimPrefix(s, "ollama.com/")
	idx := strings.Index(s, ":")
	if idx != -1 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

func (s *Server) handleDownloadHistory(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing name"))
		return
	}
	repoBase := modelRepoBase(name)
	resp := map[string]any{
		"name":      name,
		"repo_base": repoBase,
		"exists":    false,
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
	if s.usage != nil {
		if u, ok := s.usage.Get(name); ok {
			resp["usage"] = map[string]any{
				"last_used_at":             u.LastUsedAt,
				"record_tokens_per_sec":    u.RecordTokensPerSec,
				"record_tokens_per_sec_at": u.RecordTokensPerSecAt,
				"min_cold_load_ms":         u.MinColdLoadMs,
				"min_cold_load_at":         u.MinColdLoadAt,
				"total_tokens":             u.TotalTokens,
				"total_calls":              u.TotalCalls,
			}
		}
	}

	// Collect related models from the same repository
	type relatedItem struct {
		Name        string         `json:"name"`
		IsInstalled bool           `json:"is_installed"`
		History     any            `json:"history,omitempty"`
		Uninstall   map[string]any `json:"uninstall,omitempty"`
		Usage       map[string]any `json:"usage,omitempty"`
	}

	candidates := make(map[string]bool)
	installedMap := make(map[string]bool)

	if s.ollama != nil {
		if installedList, err := s.ollama.List(r.Context()); err == nil {
			for _, m := range installedList {
				installedMap[m.Name] = true
				installedMap[strings.ToLower(m.Name)] = true
				candidates[m.Name] = true
			}
		}
	}

	if s.jobs != nil {
		for cand := range s.jobs.AllHistory() {
			candidates[cand] = true
		}
	}
	if s.uninst != nil {
		for cand := range s.uninst.All() {
			candidates[cand] = true
		}
	}
	if s.usage != nil {
		for cand := range s.usage.All() {
			candidates[cand] = true
		}
	}

	var related []relatedItem
	for cand := range candidates {
		if cand == "" {
			continue
		}
		candRepo := modelRepoBase(cand)
		if repoBase != "" && candRepo == repoBase {
			item := relatedItem{
				Name:        cand,
				IsInstalled: installedMap[cand] || installedMap[strings.ToLower(cand)],
			}
			if s.jobs != nil {
				if h, ok := s.jobs.History(cand); ok {
					item.History = h
				}
			}
			if s.uninst != nil {
				if u, ok := s.uninst.Get(cand); ok && u.LastReason != "" {
					item.Uninstall = map[string]any{
						"reason": u.LastReason,
						"at":     u.LastUninstallAt,
					}
				}
			}
			if s.usage != nil {
				if u, ok := s.usage.Get(cand); ok && (u.TotalCalls > 0 || u.RecordTokensPerSec > 0 || u.MinColdLoadMs > 0 || u.LastUsedAt != nil) {
					item.Usage = map[string]any{
						"last_used_at":             u.LastUsedAt,
						"record_tokens_per_sec":    u.RecordTokensPerSec,
						"record_tokens_per_sec_at": u.RecordTokensPerSecAt,
						"min_cold_load_ms":         u.MinColdLoadMs,
						"min_cold_load_at":         u.MinColdLoadAt,
						"total_tokens":             u.TotalTokens,
						"total_calls":              u.TotalCalls,
					}
				}
			}
			related = append(related, item)
		}
	}

	// Sort related: installed first, then by name
	sort.Slice(related, func(i, j int) bool {
		if related[i].IsInstalled != related[j].IsInstalled {
			return related[i].IsInstalled
		}
		return related[i].Name < related[j].Name
	})

	resp["related_models"] = related
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

func (s *Server) handleJobPromote(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing id"))
		return
	}
	if err := s.jobs.Promote(id); err != nil {
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
					ID:          digest + "/" + e.Name(),
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

// handleDeleteArtifact removes an artifact project from disk.
// Path: DELETE /api/artifacts/{rest...}
func (s *Server) handleDeleteArtifact(w http.ResponseWriter, r *http.Request) {
	rest := r.PathValue("rest")
	if rest == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing artifact id"))
		return
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid artifact id"))
		return
	}

	var targetDir string
	if len(parts) >= 2 {
		targetDir = filepath.Join("artifacts", parts[0], parts[1])
	} else {
		targetDir = filepath.Join("artifacts", parts[0])
	}

	cleanTarget := filepath.Clean(targetDir)
	cleanArtifacts, _ := filepath.Abs("artifacts")
	absTarget, err := filepath.Abs(cleanTarget)
	if err != nil || !strings.HasPrefix(absTarget, cleanArtifacts+string(filepath.Separator)) {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid artifact path"))
		return
	}

	if info, err := os.Stat(cleanTarget); err != nil || !info.IsDir() {
		writeError(w, http.StatusNotFound, fmt.Errorf("artifact not found"))
		return
	}

	if err := os.RemoveAll(cleanTarget); err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("failed to delete artifact: %w", err))
		return
	}

	// Clean up empty parent digest folder if no more artifacts remain
	if len(parts) >= 2 {
		parentDir := filepath.Join("artifacts", parts[0])
		if entries, err := os.ReadDir(parentDir); err == nil && len(entries) == 0 {
			_ = os.Remove(parentDir)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": rest})
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

		// When requesting the artifact root or an HTML entry point and index.html is missing,
		// serve a clean, diagnostic "Missing index.html" page instead of a raw 404.
		if subpath == "" || strings.HasSuffix(strings.ToLower(subpath), ".html") {
			missingHTML := renderMissingIndexPage(baseDir)
			injected := injectConsoleCaptureScript(missingHTML)
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

type artifactEvalResponse struct {
	Result string `json:"result"`
	Error  string `json:"error"`
}

func (s *Server) handleArtifactEval(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RequestID string `json:"request_id"`
		Result    string `json:"result"`
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

	s.artifactEvalMu.Lock()
	ch, ok := s.artifactEvalCh[body.RequestID]
	s.artifactEvalMu.Unlock()

	if ok && ch != nil {
		select {
		case ch <- artifactEvalResponse{Result: body.Result, Error: body.Error}:
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

func renderMissingIndexPage(baseDir string) []byte {
	var fileListItems []string
	if entries, err := os.ReadDir(baseDir); err == nil {
		for _, e := range entries {
			if e.Name() == "prompt.txt" || e.Name() == ".artifact.json" {
				continue
			}
			icon := "📄"
			if e.IsDir() {
				icon = "📁"
			}
			info, _ := e.Info()
			sizeStr := ""
			if info != nil && !e.IsDir() {
				sizeStr = fmt.Sprintf(" · %d bytes", info.Size())
			}
			fileListItems = append(fileListItems, fmt.Sprintf(`<li><span class="file-icon">%s</span><span class="file-name">%s</span><span class="file-size">%s</span></li>`, icon, html.EscapeString(e.Name()), sizeStr))
		}
	}

	filesHtml := `<div class="empty-state">(No hay archivos en el workspace todavía)</div>`
	if len(fileListItems) > 0 {
		filesHtml = `<ul class="file-list">` + strings.Join(fileListItems, "") + `</ul>`
	}

	html := fmt.Sprintf(`<!DOCTYPE html>
<html lang="es">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>index.html no encontrado</title>
<style>
* { box-sizing: border-box; margin: 0; padding: 0; }
html, body {
  width: 100%%;
  height: 100%%;
  background: #090a0f;
  background-image: 
    radial-gradient(circle at 50%% 40%%, rgba(245, 158, 11, 0.12) 0%%, rgba(139, 92, 246, 0.08) 35%%, transparent 70%%),
    linear-gradient(rgba(255, 255, 255, 0.02) 1px, transparent 1px),
    linear-gradient(90deg, rgba(255, 255, 255, 0.02) 1px, transparent 1px);
  background-size: 100%% 100%%, 32px 32px, 32px 32px;
  display: flex;
  align-items: center;
  justify-content: center;
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Inter, sans-serif;
  overflow: hidden;
  color: #f1f5f9;
  padding: 1.5rem;
}
.card {
  max-width: 520px;
  width: 100%%;
  background: rgba(18, 20, 29, 0.85);
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 20px;
  padding: 2.2rem 2rem;
  display: flex;
  flex-direction: column;
  align-items: center;
  text-align: center;
  backdrop-filter: blur(16px);
  box-shadow: 0 20px 50px rgba(0, 0, 0, 0.5), 0 0 40px rgba(245, 158, 11, 0.1);
  animation: fadeIn 0.4s ease-out;
}
@keyframes fadeIn {
  from { opacity: 0; transform: translateY(10px) scale(0.98); }
  to { opacity: 1; transform: translateY(0) scale(1); }
}
.icon-badge {
  width: 68px;
  height: 68px;
  border-radius: 18px;
  background: linear-gradient(135deg, rgba(245, 158, 11, 0.2), rgba(239, 68, 68, 0.1));
  border: 1px solid rgba(245, 158, 11, 0.4);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 2rem;
  margin-bottom: 1.25rem;
  box-shadow: 0 0 25px rgba(245, 158, 11, 0.25);
}
.title {
  font-size: 1.35rem;
  font-weight: 700;
  letter-spacing: -0.02em;
  color: #f8fafc;
  margin-bottom: 0.5rem;
}
.desc {
  font-size: 0.9rem;
  line-height: 1.5;
  color: #94a3b8;
  margin-bottom: 1.5rem;
}
.hint-box {
  width: 100%%;
  background: rgba(0, 0, 0, 0.4);
  border: 1px solid rgba(245, 158, 11, 0.25);
  border-radius: 12px;
  padding: 0.85rem 1rem;
  margin-bottom: 1.5rem;
  text-align: left;
}
.hint-label {
  font-size: 0.75rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: #f59e0b;
  margin-bottom: 0.35rem;
  display: flex;
  align-items: center;
  gap: 0.4rem;
}
.hint-code {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 0.82rem;
  color: #e2e8f0;
  word-break: break-all;
}
.files-section {
  width: 100%%;
  text-align: left;
  border-top: 1px solid rgba(255, 255, 255, 0.08);
  padding-top: 1rem;
}
.files-heading {
  font-size: 0.78rem;
  font-weight: 600;
  color: #64748b;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  margin-bottom: 0.6rem;
}
.file-list {
  list-style: none;
  max-height: 120px;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}
.file-list li {
  display: flex;
  align-items: center;
  font-size: 0.82rem;
  color: #cbd5e1;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  background: rgba(255, 255, 255, 0.03);
  padding: 0.3rem 0.6rem;
  border-radius: 6px;
}
.file-icon { margin-right: 0.45rem; }
.file-name { font-weight: 500; }
.file-size { color: #64748b; font-size: 0.75rem; margin-left: 0.4rem; }
.empty-state {
  font-size: 0.82rem;
  color: #64748b;
  font-style: italic;
}
</style>
</head>
<body>
  <div class="card">
    <div class="icon-badge">⚠️</div>
    <h1 class="title">index.html no encontrado</h1>
    <p class="desc">Este artifact no contiene un archivo <code>index.html</code> de entrada. Para renderizar la vista previa interactiva, es necesario crear <code>index.html</code>.</p>
    <div class="hint-box">
      <div class="hint-label">✦ Instrucción para el Asistente / Modelo</div>
      <div class="hint-code">write_file(path="index.html", content="...")</div>
    </div>
    <div class="files-section">
      <div class="files-heading">Archivos en este artifact</div>
      %s
    </div>
  </div>
</body>
</html>`, filesHtml)
	return []byte(html)
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

// ---------- external models API ----------

func (s *Server) handleListExternalModels(w http.ResponseWriter, r *http.Request) {
	if s.externalModels == nil {
		writeJSON(w, http.StatusOK, map[string]any{"models": []any{}})
		return
	}
	all := s.externalModels.All()
	list := make([]ExternalModelRecord, 0, len(all))
	for _, m := range all {
		safeM := m
		if safeM.APIKey != "" {
			safeM.APIKey = "••••••••"
		}
		list = append(list, safeM)
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": list})
}

func (s *Server) handleCreateExternalModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name         string   `json:"name"`
		OldName      string   `json:"old_name"`
		URL          string   `json:"url"`
		APIKey       string   `json:"api_key"`
		Capabilities []string `json:"capabilities"`
		Disabled     bool     `json:"disabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
		return
	}
	name := strings.TrimSpace(body.Name)
	oldName := strings.TrimSpace(body.OldName)
	targetURL := strings.TrimSpace(body.URL)
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("nombre del modelo requerido"))
		return
	}
	if targetURL == "" {
		writeError(w, http.StatusBadRequest, errors.New("URL requerida"))
		return
	}
	if s.externalModels == nil {
		writeError(w, http.StatusInternalServerError, errors.New("external models store unavailable"))
		return
	}

	if oldName != "" && oldName != name {
		_ = s.externalModels.Unregister(oldName)
		_ = s.deleteArtifactsForModel(r.Context(), oldName)
		if s.usage != nil {
			_, _ = s.usage.Delete(oldName)
			if strings.HasSuffix(oldName, ":latest") {
				_, _ = s.usage.Delete(strings.TrimSuffix(oldName, ":latest"))
			} else {
				_, _ = s.usage.Delete(oldName + ":latest")
			}
		}
	}

	if err := s.externalModels.Register(name, targetURL, body.APIKey, body.Capabilities, body.Disabled); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "name": name, "disabled": body.Disabled})
}

func (s *Server) handleToggleExternalModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = strings.TrimSpace(r.URL.Query().Get("name"))
	}
	if name == "" {
		name = strings.TrimSpace(r.PathValue("name"))
	}
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing model name"))
		return
	}
	if s.externalModels == nil {
		writeError(w, http.StatusInternalServerError, errors.New("external models store unavailable"))
		return
	}
	newDisabled, err := s.externalModels.ToggleDisabled(name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"name":     name,
		"disabled": newDisabled,
	})
}

func (s *Server) handleTestExternalModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name   string `json:"name"`
		URL    string `json:"url"`
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
		return
	}
	name := strings.TrimSpace(body.Name)
	targetURL := strings.TrimSpace(body.URL)
	if name == "" || targetURL == "" {
		writeError(w, http.StatusBadRequest, errors.New("nombre y URL requeridos para el test"))
		return
	}
	res, err := ProbeExternalModel(r.Context(), targetURL, body.APIKey, name)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"connected":    res.Connected,
		"vision":       res.Vision,
		"tools":        res.Tools,
		"thinking":     res.Thinking,
		"capabilities": res.Capabilities,
		"latency_ms":   res.LatencyMs,
	})
}

func (s *Server) handleDeleteExternalModel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, errors.New("missing model name"))
		return
	}
	if s.externalModels == nil {
		writeError(w, http.StatusInternalServerError, errors.New("external models store unavailable"))
		return
	}
	deletedArtifacts := s.deleteArtifactsForModel(r.Context(), name)
	if err := s.externalModels.Unregister(name); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if s.usage != nil {
		_, _ = s.usage.Delete(name)
		if strings.HasSuffix(name, ":latest") {
			_, _ = s.usage.Delete(strings.TrimSuffix(name, ":latest"))
		} else {
			_, _ = s.usage.Delete(name + ":latest")
		}
	}
	resp := map[string]any{"status": "ok", "name": name}
	if deletedArtifacts > 0 {
		resp["deleted_artifacts"] = deletedArtifacts
	}
	writeJSON(w, http.StatusOK, resp)
}

func isLocalFilePathOrDigest(pathOrName string) bool {
	s := strings.TrimSpace(pathOrName)
	if s == "" {
		return true
	}
	// Hashes / digests
	if strings.HasPrefix(s, "sha256:") || strings.HasPrefix(s, "sha256-") {
		return true
	}
	// Raw 64-char hex string (sha256 digest)
	if len(s) == 64 {
		isHex := true
		for _, c := range s {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				isHex = false
				break
			}
		}
		if isHex {
			return true
		}
	}
	lower := strings.ToLower(s)
	// Common model/tensor file extensions
	for _, ext := range []string{".gguf", ".bin", ".safetensors", ".pt", ".pth", ".onnx", ".ckpt"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	// Relative and home paths
	if strings.HasPrefix(s, "./") || strings.HasPrefix(s, `.\`) || strings.HasPrefix(s, "../") || strings.HasPrefix(s, `..\`) || strings.HasPrefix(s, "~/") || strings.HasPrefix(s, `~\`) || s == "~" {
		return true
	}
	// Windows drive letters, e.g. C:\ or C:/ or D:\
	if len(s) >= 3 && ((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z')) && s[1] == ':' && (s[2] == '/' || s[2] == '\\') {
		return true
	}
	// Absolute paths starting with / or \
	if strings.HasPrefix(s, "/") || strings.HasPrefix(s, "\\") {
		return true
	}
	// Contains Ollama blobs path structure or sha256 blob
	if strings.Contains(lower, "/blobs/") || strings.Contains(lower, `\blobs\`) || strings.Contains(lower, "/.ollama/") || strings.Contains(lower, `\.ollama\`) || strings.Contains(lower, "sha256-") || strings.Contains(lower, "sha256:") {
		return true
	}
	return false
}

