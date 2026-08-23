package server

import (
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

