package server

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/gense/ollama-manager/internal/opencode"
)

// opencodeProviderView is the local Ollama provider summary sent to the UI.
type opencodeProviderView struct {
	Key     string `json:"key"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
}

// opencodeModelView is one installed model with its opencode visibility state.
type opencodeModelView struct {
	Name          string   `json:"name"`
	DisplayName   string   `json:"display_name"`
	CustomName    bool     `json:"custom_name"`
	RecordTPS     float64  `json:"record_tps,omitempty"`
	Enabled       bool     `json:"enabled"`
	Size          int64    `json:"size"`
	ContextLength int64    `json:"context_length,omitempty"`
	HasVision     bool     `json:"has_vision"`
	Capabilities  []string `json:"capabilities,omitempty"`
}

// opencodeStateView is the full state of the OpenCode settings section.
type opencodeStateView struct {
	ConfigPath     string                `json:"config_path"`
	Exists         bool                  `json:"exists"`
	DefaultBaseURL string                `json:"default_base_url"`
	Remote         bool                  `json:"remote"`
	Provider       *opencodeProviderView `json:"provider"`
	Models         []opencodeModelView   `json:"models"`
}

// handleOpenCodeGet reports the opencode integration state: config path,
// detected local provider and every installed model's visibility flag.
func (s *Server) handleOpenCodeGet(w http.ResponseWriter, r *http.Request) {
	view, err := s.buildOpenCodeView(r.Context(), !isLoopbackRequest(r))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleOpenCodeEnsureProvider creates the local Ollama provider in the
// opencode config when none exists. Idempotent.
func (s *Server) handleOpenCodeEnsureProvider(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BaseURL string `json:"base_url"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}

	s.opencodeMu.Lock()
	path := opencode.Resolve()
	doc, err := opencode.Load(path)
	if err == nil {
		baseURL := strings.TrimSpace(body.BaseURL)
		if baseURL == "" {
			baseURL = s.ollamaOpenAIBaseURL()
		}
		doc.EnsureLocalProvider(baseURL)
		err = doc.Save()
	}
	s.opencodeMu.Unlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	view, err := s.buildOpenCodeView(r.Context(), !isLoopbackRequest(r))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": view})
}

// handleOpenCodeSetModels stores the exact set of models to expose in the
// local Ollama provider, along with optional custom display names.
// Requires a local provider to already exist.
func (s *Server) handleOpenCodeSetModels(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled []string                  `json:"enabled"`
		Names   map[string]string         `json:"names"`
		Limits  map[string]map[string]any `json:"limits,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid body"))
		return
	}

	s.opencodeMu.Lock()
	path := opencode.Resolve()
	doc, err := opencode.Load(path)
	if err == nil {
		provider := doc.LocalOllamaProvider(s.ollamaPort())
		if provider == nil {
			err = errors.New("no local Ollama provider configured in opencode; create one first")
		} else {
			doc.SetEnabledModels(provider.Key, sanitizeTags(body.Enabled), body.Names, body.Limits)
			err = doc.Save()
		}
	}
	s.opencodeMu.Unlock()
	if err != nil {
		status := http.StatusInternalServerError
		if strings.Contains(err.Error(), "no local Ollama provider") {
			status = http.StatusConflict
		}
		writeError(w, status, err)
		return
	}

	view, err := s.buildOpenCodeView(r.Context(), !isLoopbackRequest(r))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": view})
}

func sanitizeTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := make(map[string]bool, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	return out
}

func (s *Server) buildOpenCodeView(ctx context.Context, remote bool) (opencodeStateView, error) {
	path := opencode.Resolve()
	doc, err := opencode.Load(path)
	if err != nil {
		return opencodeStateView{}, err
	}
	view := opencodeStateView{
		ConfigPath: path,
		Exists:     configFileExists(path),
		Remote:     remote,
	}
	if p := doc.LocalOllamaProvider(s.ollamaPort()); p != nil {
		view.Provider = &opencodeProviderView{Key: p.Key, Name: p.Name, BaseURL: p.BaseURL}
		view.DefaultBaseURL = p.BaseURL
	} else {
		view.DefaultBaseURL = s.ollamaOpenAIBaseURL()
	}

	providerKey := ""
	if view.Provider != nil {
		providerKey = view.Provider.Key
	}
	enabled := doc.EnabledModels(providerKey)

	models, err := s.ollama.List(ctx)
	if err != nil {
		// Ollama unreachable: still report provider status with an empty list.
		models = nil
	}
	meta := s.fetchModelMeta(ctx, models)
	view.Models = make([]opencodeModelView, 0, len(models))
	for _, m := range models {
		tag := m.Name
		if tag == "" {
			tag = m.Model
		}
		tps := 0.0
		if s.usage != nil {
			if rec, ok := s.getModelUsage(tag); ok {
				tps = rec.RecordTokensPerSec
			}
		}
		mMeta := meta[m.Digest]
		hasVision := false
		for _, c := range mMeta.Capabilities {
			if strings.EqualFold(c, "vision") {
				hasVision = true
				break
			}
		}
		ctxLen := mMeta.ContextLength
		if ctxLen == 0 && s.usage != nil {
			if rec, ok := s.getModelUsage(tag); ok {
				ctxLen = rec.ContextLength
			}
		}
		view.Models = append(view.Models, opencodeModelView{
			Name:          tag,
			DisplayName:   doc.ModelDisplayName(providerKey, tag),
			CustomName:    doc.HasCustomName(providerKey, tag),
			RecordTPS:     tps,
			Enabled:       enabled[tag],
			Size:          m.Size,
			ContextLength: ctxLen,
			HasVision:     hasVision,
			Capabilities:  mMeta.Capabilities,
		})
	}
	return view, nil
}

func configFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// isLoopbackRequest reports whether the client connected from the same
// machine (localhost / loopback) or from a different device on the network.
func isLoopbackRequest(r *http.Request) bool {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}
	if host == "" || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ollamaOpenAIBaseURL returns the Ollama OpenAI-compatible base URL (with a
// /v1 suffix) derived from the manager's configured Ollama URL.
func (s *Server) ollamaOpenAIBaseURL() string {
	s.cfgMu.RLock()
	base := strings.TrimSpace(s.cfg.OllamaURL)
	s.cfgMu.RUnlock()
	if base == "" {
		return "http://localhost:11434/v1"
	}
	if strings.HasSuffix(base, "/v1") {
		return base
	}
	return strings.TrimSuffix(base, "/") + "/v1"
}

// ollamaPort returns the port configured for Ollama, defaulting to "11434".
func (s *Server) ollamaPort() string {
	s.cfgMu.RLock()
	base := strings.TrimSpace(s.cfg.OllamaURL)
	s.cfgMu.RUnlock()
	if base == "" {
		return "11434"
	}
	if !strings.Contains(base, "://") {
		base = "http://" + base
	}
	u, err := url.Parse(base)
	if err != nil || u.Port() == "" {
		return "11434"
	}
	return u.Port()
}
