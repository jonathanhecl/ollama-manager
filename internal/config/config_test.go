package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadMigratesLegacyNoThink(t *testing.T) {
	path := writeTempConfig(t, `{
  "port": 7860,
  "chat_defaults": {
    "no_think": true,
    "web_tools": true,
    "artifacts": true
  }
}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ChatDefaults.ThinkLevel != "off" {
		t.Errorf("ThinkLevel = %q, want %q (no_think=true should migrate to off)", cfg.ChatDefaults.ThinkLevel, "off")
	}
	if cfg.ChatDefaults.NoThink != nil {
		t.Errorf("NoThink should be cleared after migration, got %v", *cfg.ChatDefaults.NoThink)
	}
	if cfg.ChatDefaults.WebTools == nil || !*cfg.ChatDefaults.WebTools {
		t.Errorf("WebTools should be preserved, got %v", cfg.ChatDefaults.WebTools)
	}
}

func TestLoadDefaultsThinkLevelAuto(t *testing.T) {
	path := writeTempConfig(t, `{"port": 7860}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ChatDefaults.ThinkLevel != "auto" {
		t.Errorf("ThinkLevel = %q, want %q", cfg.ChatDefaults.ThinkLevel, "auto")
	}
}

func TestLoadKeepsThinkLevelAndNormalizesInvalid(t *testing.T) {
	path := writeTempConfig(t, `{"port": 7860, "chat_defaults": {"think_level": "high"}}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ChatDefaults.ThinkLevel != "high" {
		t.Errorf("ThinkLevel = %q, want %q", cfg.ChatDefaults.ThinkLevel, "high")
	}

	path2 := writeTempConfig(t, `{"port": 7860, "chat_defaults": {"think_level": "bogus"}}`)
	cfg2, err := Load(path2)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg2.ChatDefaults.ThinkLevel != "auto" {
		t.Errorf("invalid ThinkLevel should normalize to auto, got %q", cfg2.ChatDefaults.ThinkLevel)
	}
}
