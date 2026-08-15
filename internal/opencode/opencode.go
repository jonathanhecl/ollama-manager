// Package opencode reads and edits the global opencode configuration file
// (~/.config/opencode/opencode.json) so the manager can expose which local
// Ollama models appear in opencode.
package opencode

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Resolve returns the path of the opencode config to read and write.
// It honors the OPENCODE_CONFIG environment variable when set; otherwise it
// falls back to ~/.config/opencode/opencode.json, or the .jsonc variant when
// that is the only file that exists on disk.
func Resolve() string {
	if p := strings.TrimSpace(os.Getenv("OPENCODE_CONFIG")); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	json := filepath.Join(home, ".config", "opencode", "opencode.json")
	if _, err := os.Stat(json); err != nil {
		jsonc := strings.TrimSuffix(json, ".json") + ".jsonc"
		if _, err := os.Stat(jsonc); err == nil {
			return jsonc
		}
	}
	return json
}

// Document is a loaded opencode config. The whole document is kept as a map
// so unrelated sections (google, mcp, plugin, ...) are preserved verbatim.
// Save() applies changes surgically to the original bytes so anything not
// related to the local Ollama provider is left byte-for-byte untouched.
type Document struct {
	Path  string
	Raw   map[string]any
	orig  []byte
	edits []editOp
}

// editOp describes a single surgical change to the on-disk config.
type editOp struct {
	kind   string // "models" | "provider"
	key    string
	models map[string]any // kind == "models"
	entry  map[string]any // kind == "provider"
}

// Load reads the config at path. A missing file yields an empty document
// (created on the next Save). JSONC files have their comments stripped.
func Load(path string) (*Document, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Document{Path: path, Raw: map[string]any{}}, nil
	}
	if err != nil {
		return nil, err
	}
	// Parse a JSONC-stripped copy, but keep the original bytes for Save so
	// comments and formatting are preserved byte-for-byte.
	parsed := data
	if strings.HasSuffix(strings.ToLower(path), ".jsonc") {
		parsed = StripJSONC(data)
	}
	var raw map[string]any
	if err := json.Unmarshal(parsed, &raw); err != nil {
		return nil, fmt.Errorf("invalid opencode config: %w", err)
	}
	if raw == nil {
		raw = map[string]any{}
	}
	return &Document{Path: path, Raw: raw, orig: data}, nil
}

// StripJSONC removes // and /* */ comments from JSONC bytes while leaving
// comment markers inside string literals untouched.
func StripJSONC(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inStr := false
	inLine := false
	inBlock := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		switch {
		case inLine:
			if c == '\n' {
				inLine = false
				out = append(out, c)
			} else {
				out = append(out, ' ')
			}
		case inBlock:
			if c == '*' && i+1 < len(data) && data[i+1] == '/' {
				inBlock = false
				i++
				out = append(out, ' ', ' ')
			} else {
				out = append(out, ' ')
			}
		case inStr:
			out = append(out, c)
			if c == '\\' && i+1 < len(data) {
				i++
				out = append(out, data[i])
			} else if c == '"' {
				inStr = false
			}
		case c == '"':
			inStr = true
			out = append(out, c)
		case c == '/' && i+1 < len(data) && data[i+1] == '/':
			inLine = true
			i++
			out = append(out, ' ', ' ')
		case c == '/' && i+1 < len(data) && data[i+1] == '*':
			inBlock = true
			i++
			out = append(out, ' ', ' ')
		default:
			out = append(out, c)
		}
	}
	return out
}

// Provider is a detected provider entry.
type Provider struct {
	Key     string
	Name    string
	BaseURL string
}

// LocalOllamaProvider returns the provider whose options.baseURL points at a
// local Ollama server (localhost or 127.0.0.1), or nil when none matches.
func (d *Document) LocalOllamaProvider() *Provider {
	providers, _ := d.Raw["provider"].(map[string]any)
	for key, raw := range providers {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		opts, _ := entry["options"].(map[string]any)
		baseURL, _ := opts["baseURL"].(string)
		if isLocalBaseURL(baseURL) {
			return &Provider{
				Key:     key,
				Name:    firstNonEmpty(entry["name"], key),
				BaseURL: baseURL,
			}
		}
	}
	return nil
}

// EnsureLocalProvider adds a local Ollama provider when none exists.
// baseURL becomes options.baseURL; when empty it defaults to
// http://localhost:11434/v1. It returns the provider key and whether the
// provider was created.
func (d *Document) EnsureLocalProvider(baseURL string) (string, bool) {
	if p := d.LocalOllamaProvider(); p != nil {
		return p.Key, false
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "http://localhost:11434/v1"
	}
	providers, _ := d.Raw["provider"].(map[string]any)
	if providers == nil {
		providers = map[string]any{}
	}
	const key = "ollama-local"
	entry := map[string]any{
		"npm":   "@ai-sdk/openai-compatible",
		"name":  "Ollama (local)",
		"options": map[string]any{
			"baseURL": baseURL,
		},
		"models": map[string]any{},
	}
	providers[key] = entry
	d.Raw["provider"] = providers
	d.edits = append(d.edits, editOp{kind: "provider", key: key, entry: entry})
	return key, true
}

// SetEnabledModels replaces the models map of the given provider with exactly
// the enabled tags. Metadata of entries that stay enabled (display name,
// limits, modalities) is preserved. names maps a tag to a custom display name:
// a non-empty value overrides it, an empty value resets it to the tag.
func (d *Document) SetEnabledModels(providerKey string, enabled []string, names map[string]string) {
	providers, _ := d.Raw["provider"].(map[string]any)
	entry, ok := providers[providerKey].(map[string]any)
	if !ok {
		return
	}
	old, _ := entry["models"].(map[string]any)
	models := make(map[string]any, len(enabled))
	for _, tag := range enabled {
		model, _ := old[tag].(map[string]any)
		if model == nil {
			model = map[string]any{}
		}
		if name, ok := names[tag]; ok {
			if name = strings.TrimSpace(name); name != "" {
				model["name"] = name
			} else {
				delete(model, "name")
			}
		}
		if _, has := model["name"]; !has {
			model["name"] = tag
		}
		models[tag] = model
	}
	entry["models"] = models
	providers[providerKey] = entry
	d.Raw["provider"] = providers
	d.edits = append(d.edits, editOp{kind: "models", key: providerKey, models: models})
}

// EnabledModels returns the set of tags present in the provider's models map.
func (d *Document) EnabledModels(providerKey string) map[string]bool {
	providers, _ := d.Raw["provider"].(map[string]any)
	entry, _ := providers[providerKey].(map[string]any)
	models, _ := entry["models"].(map[string]any)
	out := make(map[string]bool, len(models))
	for tag := range models {
		out[tag] = true
	}
	return out
}

// ModelDisplayName returns the friendly name stored for a tag, falling back
// to the tag with its namespace prefix stripped (e.g. hf.co/x/name:quant →
// name:quant).
func (d *Document) ModelDisplayName(providerKey, tag string) string {
	providers, _ := d.Raw["provider"].(map[string]any)
	entry, _ := providers[providerKey].(map[string]any)
	models, _ := entry["models"].(map[string]any)
	m, _ := models[tag].(map[string]any)
	if name, _ := m["name"].(string); strings.TrimSpace(name) != "" {
		return name
	}
	return shortName(tag)
}

// shortName strips the namespace prefix from an Ollama tag, leaving just
// name:quant (e.g. hf.co/bartowski/Laguna-XS-2.1-GGUF:Q2_K_L → "Laguna-XS-2.1-GGUF:Q2_K_L").
func shortName(tag string) string {
	if i := strings.LastIndexByte(tag, '/'); i >= 0 && i+1 < len(tag) {
		return tag[i+1:]
	}
	return tag
}

// Save writes the applied edits back to disk. When the document was loaded
// from an existing file the change is a minimal byte splice that leaves every
// other byte (formatting, comments, unrelated sections) untouched; when no
// edit was recorded nothing is written at all. New files are created with
// tab-indented JSON. Writes are atomic (tmp + rename).
func (d *Document) Save() error {
	if d.Path == "" {
		return errors.New("opencode config has no path")
	}
	if len(d.edits) == 0 {
		return nil
	}

	var data []byte
	if d.orig == nil {
		b, err := json.MarshalIndent(d.Raw, "", "\t")
		if err != nil {
			return err
		}
		data = append(b, '\n')
	} else {
		data = d.orig
		for _, e := range d.edits {
			var err error
			data, err = applyEdit(data, e)
			if err != nil {
				return err
			}
		}
	}

	if dir := filepath.Dir(d.Path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := d.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, d.Path)
}

func isLocalBaseURL(baseURL string) bool {
	host := baseURLHost(baseURL)
	if host == "" {
		return false
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == "localhost" || host == "127.0.0.1"
}

func baseURLHost(baseURL string) string {
	if !strings.Contains(baseURL, "://") {
		baseURL = "http://" + baseURL
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func firstNonEmpty(v any, fallback string) string {
	if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
		return s
	}
	return fallback
}
