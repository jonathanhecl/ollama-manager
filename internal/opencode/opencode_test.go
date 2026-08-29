package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripJSONC(t *testing.T) {
	in := []byte(`{
  // line comment
  "a": "http://localhost/x", // trailing
  "b": "// not a comment",
  "c": "/* nor this */",
  "d": [1, /* block */ 2],
}`)
	got := string(StripJSONC(in))
	for _, want := range []string{
		`"a": "http://localhost/x"`,
		`"b": "// not a comment"`,
		`"c": "/* nor this */"`,
		`"d": [1,`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("StripJSONC lost %q; got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "line comment") {
		t.Errorf("StripJSONC left a line comment behind:\n%s", got)
	}
}

func TestResolve(t *testing.T) {
	t.Run("OPENCODE_CONFIG overrides", func(t *testing.T) {
		want := filepath.Join(t.TempDir(), "custom.json")
		t.Setenv("OPENCODE_CONFIG", want)
		if got := Resolve(); got != want {
			t.Fatalf("Resolve() = %q; want %q", got, want)
		}
	})
	t.Run("falls back to home", func(t *testing.T) {
		t.Setenv("OPENCODE_CONFIG", "")
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		want := filepath.Join(home, ".config", "opencode", "opencode.json")
		if got := Resolve(); got != want {
			t.Fatalf("Resolve() = %q; want %q", got, want)
		}
	})
}

func TestLoadMissingFile(t *testing.T) {
	doc, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if doc.LocalOllamaProvider() != nil {
		t.Fatal("expected no provider for empty document")
	}
}

func TestLocalOllamaProvider(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string // expected provider key, "" = none
	}{
		{
			name: "localhost",
			raw:  `{"provider":{"ollama-local":{"options":{"baseURL":"http://localhost:11434/v1"}}}}`,
			want: "ollama-local",
		},
		{
			name: "127.0.0.1 uppercase",
			raw:  `{"provider":{"x":{"options":{"baseURL":"HTTP://127.0.0.1:11434"}}}}`,
			want: "x",
		},
		{
			name: "remote host not local",
			raw:  `{"provider":{"ollama-remote":{"options":{"baseURL":"http://192.168.0.121:11434/v1"}}}}`,
			want: "",
		},
		{
			name: "hostname not local",
			raw:  `{"provider":{"o":{"options":{"baseURL":"http://mac-mini.local:11434/v1"}}}}`,
			want: "",
		},
		{
			name: "no baseURL",
			raw:  `{"provider":{"o":{"options":{"temperature":0.7}}}}`,
			want: "",
		},
		{
			name: "llamacpp port 8080 must not match",
			raw:  `{"provider":{"llamacpp":{"options":{"baseURL":"http://127.0.0.1:8080/v1"}}}}`,
			want: "",
		},
		{
			name: "llamacpp 8080 and ollama 11434 together picks ollama",
			raw:  `{"provider":{"llamacpp":{"options":{"baseURL":"http://127.0.0.1:8080/v1"}},"ollama":{"options":{"baseURL":"http://localhost:11434/v1"}}}}`,
			want: "ollama",
		},
		{
			name: "localhost without port does not match 11434",
			raw:  `{"provider":{"o":{"options":{"baseURL":"http://localhost/v1"}}}}`,
			want: "",
		},
		{
			name: "ipv6 loopback 11434",
			raw:  `{"provider":{"ollama-ipv6":{"options":{"baseURL":"http://[::1]:11434/v1"}}}}`,
			want: "ollama-ipv6",
		},
		{
			name: "deterministic preference for ollama key over arbitrary key",
			raw:  `{"provider":{"zebra":{"options":{"baseURL":"http://localhost:11434/v1"}},"ollama":{"options":{"baseURL":"http://127.0.0.1:11434/v1"}}}}`,
			want: "ollama",
		},
		{
			name: "empty provider",
			raw:  `{}`,
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			doc, err := Load(filepath.Join(t.TempDir(), "c.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(doc.Path, []byte(c.raw), 0o600); err != nil {
				t.Fatal(err)
			}
			doc, err = Load(doc.Path)
			if err != nil {
				t.Fatal(err)
			}
			p := doc.LocalOllamaProvider()
			if c.want == "" {
				if p != nil {
					t.Fatalf("expected no provider, got %+v", p)
				}
				return
			}
			if p == nil {
				t.Fatal("expected a provider")
			}
			if p.Key != c.want {
				t.Fatalf("provider key = %q; want %q", p.Key, c.want)
			}
		})
	}
}

func TestEnsureLocalProvider(t *testing.T) {
	doc := &Document{Path: filepath.Join(t.TempDir(), "c.json"), Raw: map[string]any{}}

	key, created := doc.EnsureLocalProvider("")
	if !created || key != "ollama-local" {
		t.Fatalf("first ensure: created=%v key=%q", created, key)
	}
	p := doc.LocalOllamaProvider()
	if p == nil {
		t.Fatal("provider missing after ensure")
	}
	if p.BaseURL != "http://localhost:11434/v1" {
		t.Fatalf("default baseURL = %q", p.BaseURL)
	}

	key2, created2 := doc.EnsureLocalProvider("http://localhost:1234/v1")
	if created2 || key2 != key {
		t.Fatalf("second ensure: created=%v key=%q", created2, key2)
	}
	if doc.LocalOllamaProvider().BaseURL != "http://localhost:11434/v1" {
		t.Fatal("idempotent ensure must not overwrite the existing provider")
	}
}

func TestEnsureLocalProviderCustomBaseURL(t *testing.T) {
	doc := &Document{Path: filepath.Join(t.TempDir(), "c.json"), Raw: map[string]any{}}
	_, created := doc.EnsureLocalProvider("http://localhost:11434/custom/v1")
	if !created {
		t.Fatal("expected creation")
	}
	if got := doc.LocalOllamaProvider().BaseURL; got != "http://localhost:11434/custom/v1" {
		t.Fatalf("custom baseURL = %q", got)
	}

	doc2 := &Document{Path: filepath.Join(t.TempDir(), "c2.json"), Raw: map[string]any{}}
	_, created2 := doc2.EnsureLocalProvider("http://localhost:9999/v1")
	if !created2 {
		t.Fatal("expected creation")
	}
	if got := doc2.LocalOllamaProvider("9999").BaseURL; got != "http://localhost:9999/v1" {
		t.Fatalf("custom baseURL with port 9999 = %q", got)
	}
	if p := doc2.LocalOllamaProvider(); p != nil {
		t.Fatalf("port 9999 should not match default port 11434, got %+v", p)
	}
}

func TestSetEnabledModels(t *testing.T) {
	raw := `{
  "provider": {
    "ollama-local": {
      "options": {"baseURL": "http://localhost:11434/v1"},
      "models": {
        "tag-a": {"name": "Friendly A"},
        "tag-b": {"name": "B", "limit": {"context": 1000}},
        "tag-ghost": {"name": "Ghost"}
      }
    },
    "ollama-remote": {"models": {"tag-r": {"name": "R"}}}
  }
}`
	doc, err := Load(filepath.Join(t.TempDir(), "c.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(doc.Path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	doc, err = Load(doc.Path)
	if err != nil {
		t.Fatal(err)
	}

	doc.SetEnabledModels("ollama-local", []string{"tag-a", "tag-c"}, map[string]string{
		"tag-a": "Renamed A",
		"tag-b": "", // not enabled anyway
	})
	models, _ := doc.Raw["provider"].(map[string]any)["ollama-local"].(map[string]any)["models"].(map[string]any)
	if len(models) != 2 {
		t.Fatalf("models len = %d; want 2", len(models))
	}
	if _, ok := models["tag-ghost"]; ok {
		t.Fatal("unchecked tag-ghost should have been removed")
	}
	a, _ := models["tag-a"].(map[string]any)
	if a["name"] != "Renamed A" {
		t.Fatalf("custom name not applied: %v", a["name"])
	}
	c, _ := models["tag-c"].(map[string]any)
	if _, has := c["name"]; has {
		t.Fatalf("new tag without custom name should stay unnamed, got %v", c["name"])
	}

	// An empty name resets the entry back to unnamed (auto-named by the UI).
	doc.SetEnabledModels("ollama-local", []string{"tag-a"}, map[string]string{"tag-a": ""})
	models, _ = doc.Raw["provider"].(map[string]any)["ollama-local"].(map[string]any)["models"].(map[string]any)
	a, _ = models["tag-a"].(map[string]any)
	if _, has := a["name"]; has {
		t.Fatalf("empty name should reset to unnamed, got %v", a["name"])
	}
	if doc.ModelDisplayName("ollama-local", "tag-a") != "tag-a" {
		t.Fatal("unnamed entry should fall back to the short name")
	}

	// Unrelated provider untouched.
	remote, _ := doc.Raw["provider"].(map[string]any)["ollama-remote"].(map[string]any)["models"].(map[string]any)
	if remote["tag-r"] == nil {
		t.Fatal("unrelated provider was modified")
	}
}

func TestShortName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"smtek/Qwen3.8-27B:Q3_K_M", "Qwen3.8-27B:Q3_K_M"},
		{"hf.co/bartowski/Laguna-XS-2.1-GGUF:Q2_K_L", "Laguna-XS-2.1-GGUF:Q2_K_L"},
		{"plain", "plain"},
		{"name:quant", "name:quant"},
	}
	for _, c := range cases {
		if got := shortName(c.in); got != c.want {
			t.Errorf("shortName(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestModelDisplayNameFallsBackToShort(t *testing.T) {
	doc := &Document{Path: filepath.Join(t.TempDir(), "c.json"), Raw: map[string]any{
		"provider": map[string]any{
			"ollama-local": map[string]any{
				"models": map[string]any{
					"hf.co/x/Name-One:Q4": map[string]any{"name": "Custom"},
				},
			},
		},
	}}
	if got := doc.ModelDisplayName("ollama-local", "hf.co/x/Name-One:Q4"); got != "Custom" {
		t.Fatalf("custom name lost: %q", got)
	}
	if got := doc.ModelDisplayName("ollama-local", "smtek/Qwen:Q3_K_M"); got != "Qwen:Q3_K_M" {
		t.Fatalf("expected short fallback, got %q", got)
	}
}

func TestHasCustomName(t *testing.T) {
	doc := &Document{Path: filepath.Join(t.TempDir(), "c.json"), Raw: map[string]any{
		"provider": map[string]any{
			"ollama-local": map[string]any{
				"models": map[string]any{
					"m1":            map[string]any{"name": "Friendly"},
					"hf.co/x/m2:Q4": map[string]any{"name": "hf.co/x/m2:Q4"},
					"smtek/m3:Q4":   map[string]any{"name": "m3:Q4"},
					"m4":            map[string]any{"name": "  "},
					"m5":            map[string]any{},
				},
			},
		},
	}}
	cases := []struct {
		tag  string
		want bool
	}{
		{"m1", true},
		{"hf.co/x/m2:Q4", false}, // mirrors the full tag
		{"smtek/m3:Q4", false},   // mirrors the short name (legacy saves)
		{"m4", false},            // blank
		{"m5", false},            // missing
	}
	for _, tc := range cases {
		if got := doc.HasCustomName("ollama-local", tc.tag); got != tc.want {
			t.Errorf("HasCustomName(%q) = %v; want %v", tc.tag, got, tc.want)
		}
	}
}

func TestSaveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "opencode.json")
	doc, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	// Seed an unrelated top-level key to ensure it survives the round trip.
	doc.Raw["mcp"] = map[string]any{"foo": map[string]any{"enabled": true}}

	key, created := doc.EnsureLocalProvider("http://localhost:11434/v1")
	if !created {
		t.Fatal("expected creation")
	}
	doc.SetEnabledModels(key, []string{"m1", "m2"}, nil)
	if err := doc.Save(); err != nil {
		t.Fatal(err)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p := reloaded.LocalOllamaProvider()
	if p == nil || p.Key != key {
		t.Fatalf("provider lost on reload: %+v", p)
	}
	if !reloaded.EnabledModels(key)["m2"] {
		t.Fatal("enabled model lost on reload")
	}
	if _, ok := reloaded.Raw["mcp"]; !ok {
		t.Fatal("unrelated top-level section lost on reload")
	}
	if doc.ModelDisplayName(key, "m1") != "m1" {
		t.Fatal("default display name wrong")
	}
}

// richConfig mirrors a realistic opencode.json (tabs, several sections).
const richConfig = `{
	"$schema": "https://opencode.ai/config.json",
	"plugin": [
		"opencode-antigravity-auth@latest"
	],
	"provider": {
		"google": {
			"models": {
				"gemini-2.5-flash": {
					"name": "Gemini 2.5 Flash"
				}
			}
		},
		"ollama-local": {
			"npm": "@ai-sdk/openai-compatible",
			"name": "Ollama (local)",
			"options": {
				"baseURL": "http://localhost:11434/v1"
			},
			"models": {
				"tag-a": {"name": "Friendly A"},
				"tag-b": {"name": "B"}
			}
		},
		"ollama-remote": {
			"options": {"baseURL": "http://192.168.0.121:11434/v1"},
			"models": {"tag-r": {"name": "R"}}
		}
	},
	"mcp": {
		"foo": {"enabled": true}
	}
}
`

func mustLoadSurgical(t *testing.T, raw string) (*Document, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode.json")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	doc, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return doc, path
}

// assertLinesPreserved verifies that every non-empty line of before (except
// the given skip substrings) still appears verbatim in after.
func assertLinesPreserved(t *testing.T, before, after string, skip []string) {
	t.Helper()
	for _, line := range strings.Split(before, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" {
			continue
		}
		if containsAny(trim, skip) {
			continue
		}
		if !strings.Contains(after, line) {
			t.Fatalf("line not preserved byte-for-byte:\n  %q\n--- after ---\n%s", line, after)
		}
	}
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func TestSurgicalSaveModelsPreservesEverythingElse(t *testing.T) {
	doc, path := mustLoadSurgical(t, richConfig)
	doc.SetEnabledModels("ollama-local", []string{"tag-a", "tag-c"}, nil)
	if err := doc.Save(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Unrelated sections and lines must be byte-identical.
	assertLinesPreserved(t, richConfig, string(after), []string{"tag-a", "tag-b"})

	// The models block must now contain exactly the enabled set, keeping the
	// preserved display name for tag-a.
	var raw map[string]any
	if err := json.Unmarshal(after, &raw); err != nil {
		t.Fatalf("result no longer parses: %v\n%s", err, after)
	}
	models, _ := raw["provider"].(map[string]any)["ollama-local"].(map[string]any)["models"].(map[string]any)
	if len(models) != 2 {
		t.Fatalf("models len = %d; want 2\n%s", len(models), after)
	}
	if _, ok := models["tag-b"]; ok {
		t.Fatal("tag-b should be removed")
	}
	if a, _ := models["tag-a"].(map[string]any); a["name"] != "Friendly A" {
		t.Fatalf("display name lost: %v", a["name"])
	}
	if _, ok := models["tag-c"]; !ok {
		t.Fatal("tag-c should be added")
	}
}

func TestSurgicalSaveInsertModelsWhenMissing(t *testing.T) {
	config := strings.Replace(richConfig,
		`			},
			"models": {
				"tag-a": {"name": "Friendly A"},
				"tag-b": {"name": "B"}
			}
`, `			}
`, 1)
	if config == richConfig {
		t.Fatal("fixture failed to strip models block")
	}
	doc, path := mustLoadSurgical(t, config)
	doc.SetEnabledModels("ollama-local", []string{"tag-z"}, nil)
	if err := doc.Save(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertLinesPreserved(t, config, string(after), []string{"ollama-local"})
	if !strings.Contains(string(after), `"models": {`) || !strings.Contains(string(after), `"tag-z": {}`) {
		t.Fatalf("models block not inserted:\n%s", after)
	}
	var raw map[string]any
	if err := json.Unmarshal(after, &raw); err != nil {
		t.Fatalf("result no longer parses: %v\n%s", err, after)
	}
}

func TestSurgicalSaveCreateProviderPreservesOthers(t *testing.T) {
	// No local provider in the fixture.
	config := strings.Replace(richConfig, `		"ollama-local": {
			"npm": "@ai-sdk/openai-compatible",
			"name": "Ollama (local)",
			"options": {
				"baseURL": "http://localhost:11434/v1"
			},
			"models": {
				"tag-a": {"name": "Friendly A"},
				"tag-b": {"name": "B"}
			}
		},
`, "", 1)
	if config == richConfig {
		t.Fatal("fixture failed to strip local provider")
	}
	doc, path := mustLoadSurgical(t, config)
	if _, created := doc.EnsureLocalProvider("http://localhost:11434/v1"); !created {
		t.Fatal("expected creation")
	}
	if err := doc.Save(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertLinesPreserved(t, config, string(after), []string{"ollama-remote"})
	if !strings.Contains(string(after), `"ollama-local": {`) ||
		!strings.Contains(string(after), `"baseURL": "http://localhost:11434/v1"`) {
		t.Fatalf("local provider not created:\n%s", after)
	}
	var raw map[string]any
	if err := json.Unmarshal(after, &raw); err != nil {
		t.Fatalf("result no longer parses: %v\n%s", err, after)
	}
	if _, ok := raw["provider"].(map[string]any)["ollama-local"]; !ok {
		t.Fatal("ollama-local missing after reload")
	}
}

func TestSurgicalSaveNoOpWritesNothing(t *testing.T) {
	doc, path := mustLoadSurgical(t, richConfig)
	before, _ := os.ReadFile(path)
	// Provider already exists: ensure is a no-op, so Save must not rewrite.
	if _, created := doc.EnsureLocalProvider("http://localhost:11434/v1"); created {
		t.Fatal("expected no-op")
	}
	if err := doc.Save(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatalf("no-op Save modified the file:\n%s", after)
	}
}

func TestSurgicalSaveJSONCPreservesComments(t *testing.T) {
	jsonc := `{
	// local ollama models
	"provider": {
		"ollama-local": {
			"options": {"baseURL": "http://localhost:11434/v1"},
			"models": {"old": {"name": "Old"}}
		},
		"other": {"options": {"baseURL": "http://x/y"}}
	}
}
`
	path := filepath.Join(t.TempDir(), "opencode.jsonc")
	if err := os.WriteFile(path, []byte(jsonc), 0o600); err != nil {
		t.Fatal(err)
	}
	doc, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	doc.SetEnabledModels("ollama-local", []string{"new"}, nil)
	if err := doc.Save(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	got := string(after)
	if !strings.Contains(got, "// local ollama models") {
		t.Fatalf("comment not preserved:\n%s", got)
	}
	if !strings.Contains(got, `"new": {}`) {
		t.Fatalf("models not updated:\n%s", got)
	}
	if strings.Contains(got, `"Old"`) {
		t.Fatalf("old model not removed:\n%s", got)
	}
	if !strings.Contains(got, `"baseURL": "http://x/y"`) {
		t.Fatalf("other provider changed:\n%s", got)
	}
}

func TestSetEnabledModelsSavesLimits(t *testing.T) {
	doc, err := Load(filepath.Join(t.TempDir(), "opencode.json"))
	if err != nil {
		t.Fatal(err)
	}
	key, _ := doc.EnsureLocalProvider("http://localhost:11434/v1")
	limits := map[string]map[string]any{
		"minimax-m2.1-reap-30": {
			"context": 65536,
			"output":  16384,
		},
	}
	names := map[string]string{
		"minimax-m2.1-reap-30": "Minimax M2.1 REAP-30",
	}
	doc.SetEnabledModels(key, []string{"minimax-m2.1-reap-30"}, names, limits)
	if err := doc.Save(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(doc.Path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	provider := root["provider"].(map[string]any)[key].(map[string]any)
	models := provider["models"].(map[string]any)
	mm := models["minimax-m2.1-reap-30"].(map[string]any)
	if mm["name"] != "Minimax M2.1 REAP-30" {
		t.Fatalf("name = %v; want 'Minimax M2.1 REAP-30'", mm["name"])
	}
	lim, ok := mm["limit"].(map[string]any)
	if !ok {
		t.Fatalf("limit block missing: %v", mm)
	}
	if lim["context"] != float64(65536) || lim["output"] != float64(16384) {
		t.Fatalf("limit = %v; want context 65536, output 16384", lim)
	}
}

