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

func TestLoadKeepsTestingLimits(t *testing.T) {
	path := writeTempConfig(t, `{"port": 7860, "testing": {"max_stage_tokens": 50000, "max_stage_seconds": 600, "mode": "all"}}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Testing.MaxStageTokens != 50000 || cfg.Testing.MaxStageSeconds != 600 || cfg.Testing.Mode != "all" {
		t.Errorf("Testing = %+v, want {50000 600 all}", cfg.Testing)
	}
}

func TestLoadNormalizesTestingMode(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`"all"`, "all"},
		{`"any"`, "any"},
		{`"bogus"`, "any"},
	} {
		path := writeTempConfig(t, `{"port": 7860, "testing": {"mode": `+tc.in+`}}`)
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Testing.Mode != tc.want {
			t.Errorf("mode %s normalized to %q, want %q", tc.in, cfg.Testing.Mode, tc.want)
		}
	}
	// Missing mode defaults to any.
	path := writeTempConfig(t, `{"port": 7860}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Testing.Mode != "any" {
		t.Errorf("missing mode = %q, want any", cfg.Testing.Mode)
	}
}

func TestLoadNormalizesNegativeTestingLimits(t *testing.T) {
	path := writeTempConfig(t, `{"port": 7860, "testing": {"max_stage_tokens": -5, "max_stage_seconds": -10}}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Testing.MaxStageTokens != 0 || cfg.Testing.MaxStageSeconds != 0 {
		t.Errorf("negative Testing limits should normalize to 0, got %+v", cfg.Testing)
	}
}

func TestLoadKeepsSkipRules(t *testing.T) {
	path := writeTempConfig(t, `{"port": 7860, "testing": {"skip_rules": [
    {"min_tps": 100, "max_tps": 200, "max_seconds": 120, "mode": "any"},
    {"min_tps": 0, "max_tokens": 3000, "max_seconds": 180, "mode": "all"}
  ]}}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Testing.SkipRules) != 2 {
		t.Fatalf("SkipRules = %+v, want 2 rules", cfg.Testing.SkipRules)
	}
	r := cfg.Testing.SkipRules[0]
	if r.MinTPS != 100 || r.MaxTPS != 200 || r.MaxSeconds != 120 || r.Mode != "any" {
		t.Errorf("rule 0 = %+v, want {100 200 0 120 any}", r)
	}
}

func TestLoadNormalizesSkipRules(t *testing.T) {
	path := writeTempConfig(t, `{"port": 7860, "testing": {"skip_rules": [
    {"min_tps": -5, "max_tps": -1, "max_tokens": -10, "max_seconds": 60, "mode": "bogus"},
    {"min_tps": 200, "max_tps": 100, "max_seconds": 60},
    {"min_tps": 50}
  ]}}`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Rule 0: negatives clamped, unknown mode -> any. Rule 1: max<=min ->
	// unbounded. Rule 2: no cut condition -> dropped.
	if len(cfg.Testing.SkipRules) != 2 {
		t.Fatalf("SkipRules = %+v, want 2 rules", cfg.Testing.SkipRules)
	}
	r := cfg.Testing.SkipRules[0]
	if r.MinTPS != 0 || r.MaxTPS != 0 || r.MaxTokens != 0 || r.MaxSeconds != 60 || r.Mode != "any" {
		t.Errorf("rule 0 = %+v, want {0 0 0 60 any}", r)
	}
	if cfg.Testing.SkipRules[1].MaxTPS != 0 {
		t.Errorf("rule 1 max should reset to unbounded, got %+v", cfg.Testing.SkipRules[1])
	}
}

func TestNormalizeSkipRuleRequiresCut(t *testing.T) {
	if _, ok := NormalizeSkipRule(SkipRule{MinTPS: 0, MaxTPS: 100}); ok {
		t.Errorf("rule without tokens/seconds should be dropped")
	}
	if _, ok := NormalizeSkipRule(SkipRule{MinTPS: 0, MaxSeconds: 60}); !ok {
		t.Errorf("rule with seconds should be kept")
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
