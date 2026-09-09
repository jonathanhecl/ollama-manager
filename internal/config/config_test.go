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

func TestLoadKeepsNumCtx(t *testing.T) {
	path := writeTempConfig(t, `{"port": 7860, "chat_defaults": {"num_ctx": 2048}}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ChatDefaults.NumCtx == nil || *cfg.ChatDefaults.NumCtx != 2048 {
		t.Errorf("NumCtx = %v, want 2048", cfg.ChatDefaults.NumCtx)
	}
}

func TestLoadDefaultsNumCtxNil(t *testing.T) {
	path := writeTempConfig(t, `{"port": 7860}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ChatDefaults.NumCtx != nil {
		t.Errorf("NumCtx = %v, want nil (model default)", cfg.ChatDefaults.NumCtx)
	}
}

func TestLoadKeepsLeaderboardGroupOrder(t *testing.T) {
	path := writeTempConfig(t, `{"port": 7860, "leaderboard_group_order": ["coding", "terminal", "judge"]}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.LeaderboardGroupOrder) != 3 || cfg.LeaderboardGroupOrder[0] != "coding" {
		t.Errorf("unexpected LeaderboardGroupOrder: %v", cfg.LeaderboardGroupOrder)
	}
}

func TestGatewayBindAddressDefaults(t *testing.T) {
	gw := GatewayConfig{}
	if addr := gw.GatewayBindAddress(); addr != "127.0.0.1:7861" {
		t.Fatalf("default bind = %q, want 127.0.0.1:7861", addr)
	}
	gw = GatewayConfig{ExposeNetwork: true, Port: 8080}
	if addr := gw.GatewayBindAddress(); addr != "0.0.0.0:8080" {
		t.Fatalf("exposed bind = %q, want 0.0.0.0:8080", addr)
	}
}

func TestLoadRejectsBadGatewayPort(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.json"
	cfg := Defaults()
	cfg.Gateway.Port = 70000
	cfg.path = path
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatalf("expected Load to reject gateway port 70000")
	}
}
