package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ChatDefaults defines global fallback parameters for chat sessions.
type ChatDefaults struct {
	SystemPrompt string   `json:"system_prompt"`
	Temperature  *float64 `json:"temperature,omitempty"`
	TopK         *int     `json:"top_k,omitempty"`
	TopP         *float64 `json:"top_p,omitempty"`
	// NumCtx is the default context window (num_ctx) in tokens. nil/0 means
	// "use the model's own default". Lowering it reduces VRAM/RAM usage.
	NumCtx *int `json:"num_ctx,omitempty"`
	// ThinkLevel is the default reasoning effort: "auto", "off", "low",
	// "medium", "high" or "max". It replaces the old no_think boolean.
	ThinkLevel string `json:"think_level,omitempty"`
	// NoThink is deprecated and kept only to read older config files.
	NoThink   *bool `json:"no_think,omitempty"`
	WebTools  *bool `json:"web_tools,omitempty"`
	Artifacts *bool `json:"artifacts,omitempty"`
}

// Config holds the runtime configuration for ollama-manager.
type Config struct {
	Port          int          `json:"port"`
	ExposeNetwork bool         `json:"expose_network"`
	PasswordHash  string       `json:"password_hash"`
	SessionSecret string       `json:"session_secret"`
	OllamaURL     string       `json:"ollama_url"`
	Language      string       `json:"language"`
	ChatDefaults  ChatDefaults `json:"chat_defaults"`

	path string `json:"-"`
}

// Defaults returns a Config populated with sensible default values.
func Defaults() *Config {
	defaultTemp := 0.7
	defaultTopK := 40
	defaultTopP := 0.9
	defaultWebTools := false
	defaultArtifacts := false

	return &Config{
		Port:          7860,
		ExposeNetwork: false,
		PasswordHash:  "",
		SessionSecret: "",
		OllamaURL:     "http://localhost:11434",
		Language:      "en",
		ChatDefaults: ChatDefaults{
			SystemPrompt: "",
			Temperature:  &defaultTemp,
			TopK:         &defaultTopK,
			TopP:         &defaultTopP,
			ThinkLevel:   "auto",
			WebTools:     &defaultWebTools,
			Artifacts:    &defaultArtifacts,
		},
	}
}

// validLanguages is the set of supported UI languages.
var validLanguages = map[string]bool{"en": true, "es": true}

// validThinkLevels is the set of supported default reasoning levels.
var validThinkLevels = map[string]bool{
	"auto": true, "off": true, "low": true, "medium": true, "high": true, "max": true,
}

// IsValidThinkLevel reports whether lvl is a supported thinking level.
func IsValidThinkLevel(lvl string) bool { return validThinkLevels[lvl] }

// NormalizeThinkLevel returns lvl if valid, otherwise "auto".
func NormalizeThinkLevel(lvl string) string {
	if IsValidThinkLevel(lvl) {
		return lvl
	}
	return "auto"
}

// IsValidLanguage reports whether lang is a supported UI language.
func IsValidLanguage(lang string) bool { return validLanguages[lang] }

// Load reads the config file at path. If the file does not exist a new one
// is created with default values. If session_secret is empty a random one is
// generated and persisted.
func Load(path string) (*Config, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	cfg := Defaults()
	cfg.path = abs

	data, err := os.ReadFile(abs)
	if errors.Is(err, os.ErrNotExist) {
		if err := cfg.ensureSecret(); err != nil {
			return nil, err
		}
		if err := cfg.Save(); err != nil {
			return nil, fmt.Errorf("could not create default config at %s: %w", abs, err)
		}
		fmt.Printf("[ollama-manager] created default config at %s\n", abs)
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not read config %s: %w", abs, err)
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("invalid config json: %w", err)
	}
	cfg.path = abs

	if cfg.Port <= 0 || cfg.Port > 65535 {
		return nil, fmt.Errorf("invalid port %d in config", cfg.Port)
	}
	if cfg.OllamaURL == "" {
		cfg.OllamaURL = Defaults().OllamaURL
	}

	def := Defaults().ChatDefaults
	if cfg.ChatDefaults.Temperature == nil {
		cfg.ChatDefaults.Temperature = def.Temperature
	}
	if cfg.ChatDefaults.TopK == nil {
		cfg.ChatDefaults.TopK = def.TopK
	}
	if cfg.ChatDefaults.TopP == nil {
		cfg.ChatDefaults.TopP = def.TopP
	}
	if cfg.ChatDefaults.NumCtx == nil {
		cfg.ChatDefaults.NumCtx = def.NumCtx
	}
	// Migrate the legacy no_think boolean into think_level.
	cfg.ChatDefaults.ThinkLevel = NormalizeThinkLevel(cfg.ChatDefaults.ThinkLevel)
	if cfg.ChatDefaults.NoThink != nil && *cfg.ChatDefaults.NoThink {
		cfg.ChatDefaults.ThinkLevel = "off"
	}
	cfg.ChatDefaults.NoThink = nil
	if cfg.ChatDefaults.WebTools == nil {
		cfg.ChatDefaults.WebTools = def.WebTools
	}
	if cfg.ChatDefaults.Artifacts == nil {
		cfg.ChatDefaults.Artifacts = def.Artifacts
	}

	dirty := false
	if cfg.SessionSecret == "" {
		if err := cfg.ensureSecret(); err != nil {
			return nil, err
		}
		dirty = true
	}
	if !IsValidLanguage(cfg.Language) {
		cfg.Language = Defaults().Language
		dirty = true
	}
	if dirty {
		if err := cfg.Save(); err != nil {
			return nil, err
		}
	}

	return cfg, nil
}

// Save writes the current config back to disk in pretty JSON.
func (c *Config) Save() error {
	if c.path == "" {
		return errors.New("config has no path")
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// Path returns the absolute path of the loaded config file.
func (c *Config) Path() string { return c.path }

// HasPassword reports whether a password is configured.
func (c *Config) HasPassword() bool { return c.PasswordHash != "" }

// BindAddress returns the host:port the HTTP server should listen on.
func (c *Config) BindAddress() string {
	host := "127.0.0.1"
	if c.ExposeNetwork {
		host = "0.0.0.0"
	}
	return fmt.Sprintf("%s:%d", host, c.Port)
}

func (c *Config) ensureSecret() error {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Errorf("could not generate session secret: %w", err)
	}
	c.SessionSecret = hex.EncodeToString(buf)
	return nil
}
