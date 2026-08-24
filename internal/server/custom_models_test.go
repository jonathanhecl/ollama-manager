package server

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCustomModelsStore(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "custom_models.json")
	store := newCustomModelsStore(path)

	if err := store.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if store.IsCustom("my-coder") {
		t.Fatalf("expected my-coder not to be custom")
	}

	// :fixed is implicitly custom
	if !store.IsCustom("llama3:fixed") {
		t.Fatalf("expected llama3:fixed to be detected as custom")
	}
	if base := store.GetBase("llama3:fixed"); base != "llama3" {
		t.Fatalf("expected base to be llama3, got %q", base)
	}

	// Register a custom model with base
	if err := store.Register("my-coder", "qwen2.5-coder:7b"); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	if !store.IsCustom("my-coder") {
		t.Fatalf("expected my-coder to be custom")
	}
	if !store.IsCustom("my-coder:latest") {
		t.Fatalf("expected my-coder:latest to be recognized as custom")
	}
	if base := store.GetBase("my-coder"); base != "qwen2.5-coder:7b" {
		t.Fatalf("expected base to be qwen2.5-coder:7b, got %q", base)
	}

	// Reload from disk
	store2 := newCustomModelsStore(path)
	if err := store2.Load(); err != nil {
		t.Fatalf("Load store2 failed: %v", err)
	}
	if !store2.IsCustom("my-coder") {
		t.Fatalf("expected my-coder to persist across reload")
	}
	if base := store2.GetBase("my-coder"); base != "qwen2.5-coder:7b" {
		t.Fatalf("expected base to persist across reload, got %q", base)
	}

	// Unregister
	if err := store2.Unregister("my-coder"); err != nil {
		t.Fatalf("Unregister failed: %v", err)
	}
	if store2.IsCustom("my-coder") {
		t.Fatalf("expected my-coder not to be custom after unregister")
	}
}

func TestServerCustomModelUsageRouting(t *testing.T) {
	tmpDir := t.TempDir()
	customPath := filepath.Join(tmpDir, "custom_models.json")
	usagePath := filepath.Join(tmpDir, "model_usage.json")

	customStore := newCustomModelsStore(customPath)
	_ = customStore.Register("coder-custom", "qwen2.5-coder:7b")

	usageStore := newModelUsageStore(usagePath)

	srv := &Server{
		customModels: customStore,
		usage:        usageStore,
	}

	// Record usage for the custom model
	srv.recordModelUsage("coder-custom", 100, 2e9, 50, time.Now())
	srv.recordModelTPS("coder-custom", 50.0, time.Now())
	srv.recordModelColdLoad("coder-custom", 1500, time.Now())

	// The base model should have received the stats
	baseRec, ok := usageStore.Get("qwen2.5-coder:7b")
	if !ok {
		t.Fatalf("expected base model to receive recorded stats")
	}
	if baseRec.TotalTokens != 150 {
		t.Errorf("expected 150 tokens on base model, got %d", baseRec.TotalTokens)
	}
	if baseRec.RecordTokensPerSec != 50.0 {
		t.Errorf("expected 50.0 TPS on base model, got %f", baseRec.RecordTokensPerSec)
	}
	if baseRec.MinColdLoadMs != 1500 {
		t.Errorf("expected 1500ms cold load on base model, got %d", baseRec.MinColdLoadMs)
	}
}

func TestIsLocalFilePathOrDigest(t *testing.T) {
	cases := []struct {
		input    string
		expected bool
	}{
		{"", true},
		{"   ", true},
		{"/Users/jonathan/.ollama/models/blobs/sha256-415f8f959d807bd4d4da891f01225d7b330416947fb011a8473080ae4fd07885", true},
		{"/home/user/.ollama/models/blobs/sha256-83c18dfba02c75769cdd63f73e37c343400e82d434ff1b14bcc1cb02fcf2f5f2", true},
		{`C:\Users\gense\.ollama\models\blobs\sha256-abc123`, true},
		{"~/.ollama/models/blobs/sha256-abc123", true},
		{"./local-model.gguf", true},
		{"../models/model.bin", true},
		{"sha256:415f8f959d807bd4d4da891f01225d7b330416947fb011a8473080ae4fd07885", true},
		{"sha256-415f8f959d807bd4d4da891f01225d7b330416947fb011a8473080ae4fd07885", true},
		{"415f8f959d807bd4d4da891f01225d7b330416947fb011a8473080ae4fd07885", true},
		{"/tmp/model.safetensors", true},
		{"llama3", false},
		{"llama3:8b", false},
		{"functiongemma:270m", false},
		{"hf.co/LiquidAI/LFM2.5-VL-3B-GGUF:Q4_K_M", false},
		{"hf.co/mradermacher/LFM2.5-8B-A1B-heretic-GGUF:Q4_K_M", false},
		{"registry.ollama.ai/library/llama3:latest", false},
		{"my-user/my-custom:tag", false},
	}

	for _, c := range cases {
		got := isLocalFilePathOrDigest(c.input)
		if got != c.expected {
			t.Errorf("isLocalFilePathOrDigest(%q) = %v; want %v", c.input, got, c.expected)
		}
	}
}

func TestCustomModelsStorePurgeBlobPaths(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "custom_models.json")
	store := newCustomModelsStore(path)

	// Attempt to register with blob path as base model
	_ = store.Register("functiongemma:270m", "/Users/jonathan/.ollama/models/blobs/sha256-415f8f959d807bd4d4da891f01225d7b330416947fb011a8473080ae4fd07885")
	if base := store.GetBase("functiongemma:270m"); base != "" {
		t.Fatalf("expected GetBase to be empty, got %q", base)
	}

	// Also test Load() purging existing corrupt entries
	corruptJSON := `{"models":{"bad_model":{"base_model":"/Users/jonathan/.ollama/models/blobs/sha256-123","created_at":"2026-08-24T00:00:00Z"},"good_model":{"base_model":"llama3","created_at":"2026-08-24T00:00:00Z"}}}`
	if err := os.WriteFile(path, []byte(corruptJSON), 0o600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	store2 := newCustomModelsStore(path)
	if err := store2.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if store2.IsCustom("bad_model") {
		t.Fatalf("expected bad_model with blob base to be purged from custom models")
	}
	if !store2.IsCustom("good_model") {
		t.Fatalf("expected good_model to be retained")
	}
	if base := store2.GetBase("good_model"); base != "llama3" {
		t.Fatalf("expected base llama3, got %q", base)
	}
}


