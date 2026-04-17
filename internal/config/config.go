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

// Config holds the runtime configuration for ollama-manager.
type Config struct {
	Port           int    `json:"port"`
	ExposeNetwork  bool   `json:"expose_network"`
	PasswordHash   string `json:"password_hash"`
	SessionSecret  string `json:"session_secret"`
	OllamaURL      string `json:"ollama_url"`

	path string `json:"-"`
}

// Defaults returns a Config populated with sensible default values.
func Defaults() *Config {
	return &Config{
		Port:          7860,
		ExposeNetwork: false,
		PasswordHash:  "",
		SessionSecret: "",
		OllamaURL:     "http://localhost:11434",
	}
}

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

	dirty := false
	if cfg.SessionSecret == "" {
		if err := cfg.ensureSecret(); err != nil {
			return nil, err
		}
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
