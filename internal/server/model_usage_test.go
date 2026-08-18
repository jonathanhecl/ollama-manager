package server

import (
	"path/filepath"
	"testing"
	"time"
)

func TestModelUsageStore(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "model_usage.json")

	store := newModelUsageStore(path)
	if err := store.Load(); err != nil {
		t.Fatalf("Load on empty store failed: %v", err)
	}

	t1 := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	// 50 tokens in 2 seconds = 25.0 tok/s
	if err := store.Record("llama3:8b", 50, 2000000000, 10, t1); err != nil {
		t.Fatalf("Record failed: %v", err)
	}

	rec, ok := store.Get("llama3:8b")
	if !ok {
		t.Fatalf("expected record for llama3:8b")
	}
	if rec.TotalCalls != 1 {
		t.Errorf("expected TotalCalls 1, got %d", rec.TotalCalls)
	}
	if rec.TotalTokens != 60 {
		t.Errorf("expected TotalTokens 60, got %d", rec.TotalTokens)
	}
	if rec.RecordTokensPerSec != 25.0 {
		t.Errorf("expected RecordTokensPerSec 25.0, got %f", rec.RecordTokensPerSec)
	}
	if rec.RecordTokensPerSecAt == nil || !rec.RecordTokensPerSecAt.Equal(t1) {
		t.Errorf("expected RecordTokensPerSecAt %v, got %v", t1, rec.RecordTokensPerSecAt)
	}
	if rec.LastUsedAt == nil || !rec.LastUsedAt.Equal(t1) {
		t.Errorf("expected LastUsedAt %v, got %v", t1, rec.LastUsedAt)
	}

	// Record lower speed: should update last used but NOT record tok/s
	t2 := time.Date(2026, 8, 18, 11, 0, 0, 0, time.UTC)
	// 20 tokens in 2 seconds = 10.0 tok/s
	if err := store.Record("llama3:8b", 20, 2000000000, 5, t2); err != nil {
		t.Fatalf("Record 2 failed: %v", err)
	}

	rec, _ = store.Get("llama3:8b")
	if rec.TotalCalls != 2 {
		t.Errorf("expected TotalCalls 2, got %d", rec.TotalCalls)
	}
	if rec.RecordTokensPerSec != 25.0 {
		t.Errorf("expected RecordTokensPerSec to remain 25.0, got %f", rec.RecordTokensPerSec)
	}
	if rec.LastUsedAt == nil || !rec.LastUsedAt.Equal(t2) {
		t.Errorf("expected LastUsedAt %v, got %v", t2, rec.LastUsedAt)
	}

	// Record higher speed: should update both
	t3 := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	// 100 tokens in 2 seconds = 50.0 tok/s
	if err := store.Record("llama3:8b", 100, 2000000000, 20, t3); err != nil {
		t.Fatalf("Record 3 failed: %v", err)
	}

	rec, _ = store.Get("llama3:8b")
	if rec.RecordTokensPerSec != 50.0 {
		t.Errorf("expected RecordTokensPerSec 50.0, got %f", rec.RecordTokensPerSec)
	}
	if rec.RecordTokensPerSecAt == nil || !rec.RecordTokensPerSecAt.Equal(t3) {
		t.Errorf("expected RecordTokensPerSecAt %v, got %v", t3, rec.RecordTokensPerSecAt)
	}

	// Record cold load
	tCold1 := time.Date(2026, 8, 18, 13, 0, 0, 0, time.UTC)
	if err := store.RecordColdLoad("llama3:8b", 22400, tCold1); err != nil {
		t.Fatalf("RecordColdLoad failed: %v", err)
	}
	rec, _ = store.Get("llama3:8b")
	if rec.MinColdLoadMs != 22400 {
		t.Errorf("expected MinColdLoadMs 22400, got %d", rec.MinColdLoadMs)
	}

	// Record higher cold load: should NOT replace min
	tCold2 := time.Date(2026, 8, 18, 14, 0, 0, 0, time.UTC)
	if err := store.RecordColdLoad("llama3:8b", 30000, tCold2); err != nil {
		t.Fatalf("RecordColdLoad 2 failed: %v", err)
	}
	rec, _ = store.Get("llama3:8b")
	if rec.MinColdLoadMs != 22400 {
		t.Errorf("expected MinColdLoadMs to remain 22400, got %d", rec.MinColdLoadMs)
	}

	// Record lower cold load: SHOULD replace min
	tCold3 := time.Date(2026, 8, 18, 15, 0, 0, 0, time.UTC)
	if err := store.RecordColdLoad("llama3:8b", 18500, tCold3); err != nil {
		t.Fatalf("RecordColdLoad 3 failed: %v", err)
	}
	rec, _ = store.Get("llama3:8b")
	if rec.MinColdLoadMs != 18500 {
		t.Errorf("expected MinColdLoadMs 18500, got %d", rec.MinColdLoadMs)
	}

	// Test persistence by reloading in a new store instance
	store2 := newModelUsageStore(path)
	if err := store2.Load(); err != nil {
		t.Fatalf("store2 Load failed: %v", err)
	}
	rec2, ok := store2.Get("llama3:8b")
	if !ok || rec2.RecordTokensPerSec != 50.0 || rec2.TotalCalls != 3 || rec2.MinColdLoadMs != 18500 {
		t.Errorf("persisted store mismatch: %+v", rec2)
	}
}

func TestFixedModelInheritsUsage(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "model_usage.json")

	store := newModelUsageStore(path)
	_ = store.Load()

	t1 := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	_ = store.RecordTPS("qwen3:latest", 45.0, t1)
	_ = store.RecordColdLoad("qwen3:latest", 12000, t1)

	// Automatic fallback for :fixed when not explicitly recorded yet
	fixedRec, ok := store.Get("qwen3:fixed")
	if !ok {
		t.Fatalf("expected fixed model to inherit base stats via fallback")
	}
	if fixedRec.RecordTokensPerSec != 45.0 || fixedRec.MinColdLoadMs != 12000 {
		t.Errorf("unexpected inherited stats: %+v", fixedRec)
	}

	// Explicit inheritance
	if err := store.InheritUsage("qwen3:latest", "qwen3:fixed"); err != nil {
		t.Fatalf("InheritUsage failed: %v", err)
	}

	// Record better stats on fixed model
	t2 := time.Date(2026, 8, 18, 11, 0, 0, 0, time.UTC)
	_ = store.RecordTPS("qwen3:fixed", 55.0, t2)

	fixedRec2, _ := store.Get("qwen3:fixed")
	if fixedRec2.RecordTokensPerSec != 55.0 {
		t.Errorf("expected 55.0, got %f", fixedRec2.RecordTokensPerSec)
	}

	// Base model should still have its own 45.0
	baseRec, _ := store.Get("qwen3:latest")
	if baseRec.RecordTokensPerSec != 45.0 {
		t.Errorf("expected base to keep 45.0, got %f", baseRec.RecordTokensPerSec)
	}
}
