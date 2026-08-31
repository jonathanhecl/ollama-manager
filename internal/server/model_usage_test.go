package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

func TestSetMetaPersistence(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "model_usage.json")
	store := newModelUsageStore(path)
	_ = store.Load()

	if err := store.SetMeta("llama3:8b", modelUsageMeta{
		ParameterSize:  "8.0B",
		Size:           4832754765,
		Quantization:   "Q4_K_M",
		Family:         "llama",
		ParameterCount: 8030277632,
		Architecture:   "llama",
		FileType:       15,
		SizeLabel:      "8B",
		IsMOE:          true,
		ContextLength:  8192,
	}); err != nil {
		t.Fatalf("SetMeta failed: %v", err)
	}
	rec, ok := store.Get("llama3:8b")
	if !ok {
		t.Fatalf("expected record after SetMeta")
	}
	if rec.ParameterSize != "8.0B" || rec.Size != 4832754765 || rec.Quantization != "Q4_K_M" || rec.Family != "llama" {
		t.Errorf("unexpected meta: %+v", rec)
	}
	if rec.ParameterCount != 8030277632 || rec.Architecture != "llama" || rec.FileType != 15 || rec.SizeLabel != "8B" || !rec.IsMOE {
		t.Errorf("unexpected extended meta: %+v", rec)
	}
	if rec.ContextLength != 8192 {
		t.Errorf("context_length = %d, want 8192", rec.ContextLength)
	}

	// Empty values must NOT overwrite known ones (including IsMOE).
	if err := store.SetMeta("llama3:8b", modelUsageMeta{}); err != nil {
		t.Fatalf("SetMeta(empty) failed: %v", err)
	}
	rec, _ = store.Get("llama3:8b")
	if rec.ParameterSize != "8.0B" || rec.Size != 4832754765 || rec.ParameterCount != 8030277632 || !rec.IsMOE {
		t.Errorf("empty SetMeta overwrote values: %+v", rec)
	}
	if rec.ContextLength != 8192 {
		t.Errorf("empty SetMeta overwrote context_length: %d", rec.ContextLength)
	}

	// Persistence across reload.
	store2 := newModelUsageStore(path)
	_ = store2.Load()
	rec2, _ := store2.Get("llama3:8b")
	if rec2.ParameterSize != "8.0B" || rec2.Family != "llama" || rec2.ParameterCount != 8030277632 || rec2.Architecture != "llama" || !rec2.IsMOE {
		t.Errorf("meta did not persist: %+v", rec2)
	}
	if rec2.ContextLength != 8192 {
		t.Errorf("context_length did not persist: %d", rec2.ContextLength)
	}
}

func TestFixedInheritsMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "model_usage.json")
	store := newModelUsageStore(path)
	_ = store.Load()

	_ = store.SetMeta("qwen3:latest", modelUsageMeta{
		ParameterSize:  "14B",
		Size:           9000000000,
		Quantization:   "Q4_K_M",
		Family:         "qwen",
		ParameterCount: 14600000000,
		Architecture:   "qwen3",
		FileType:       15,
		SizeLabel:      "14B",
		IsMOE:          true,
	})
	_ = store.RecordTPS("qwen3:latest", 45.0, time.Now())
	_ = store.SetMeta("qwen3:fixed", modelUsageMeta{})
	fixedRec, _ := store.Get("qwen3:fixed")
	if fixedRec.ParameterSize != "14B" || fixedRec.Family != "qwen" || fixedRec.ParameterCount != 14600000000 || fixedRec.Architecture != "qwen3" || !fixedRec.IsMOE {
		t.Errorf("fixed model did not inherit metadata: %+v", fixedRec)
	}
}

func TestRecordCancelUsage(t *testing.T) {
	newStore := func() *modelUsageStore {
		store := newModelUsageStore(filepath.Join(t.TempDir(), "model_usage.json"))
		if err := store.Load(); err != nil {
			t.Fatalf("Load on empty store failed: %v", err)
		}
		return store
	}

	t.Run("no record and very slow saves floor", func(t *testing.T) {
		s := &Server{usage: newStore()}
		// 4 chars = 1 token over 20s = 0.05 tok/s -> floor 0.1
		s.recordCancelUsage("slow:1b", "xxxx", time.Now().Add(-20*time.Second))
		rec, ok := s.usage.Get("slow:1b")
		if !ok {
			t.Fatalf("expected a record after cancelled slow response")
		}
		if rec.RecordTokensPerSec != minRecordTPS {
			t.Errorf("expected RecordTokensPerSec %.1f, got %f", minRecordTPS, rec.RecordTokensPerSec)
		}
		if rec.TotalCalls != 1 {
			t.Errorf("expected TotalCalls 1, got %d", rec.TotalCalls)
		}
	})

	t.Run("no record and medium slow saves average", func(t *testing.T) {
		s := &Server{usage: newStore()}
		// 8 chars = 2 tokens over 5s = 0.4 tok/s
		s.recordCancelUsage("mid:1b", "xxxxxxxx", time.Now().Add(-5*time.Second))
		rec, ok := s.usage.Get("mid:1b")
		if !ok {
			t.Fatalf("expected a record after cancelled medium response")
		}
		if rec.RecordTokensPerSec != 0.4 {
			t.Errorf("expected RecordTokensPerSec 0.4, got %f", rec.RecordTokensPerSec)
		}
	})

	t.Run("no record and fast cancel saves nothing", func(t *testing.T) {
		s := &Server{usage: newStore()}
		// 1 token over 0.5s = 2 tok/s >= threshold
		s.recordCancelUsage("fast:1b", "xxxx", time.Now().Add(-500*time.Millisecond))
		if _, ok := s.usage.Get("fast:1b"); ok {
			t.Errorf("expected no record for fast cancelled response")
		}
	})

	t.Run("existing record blocks cancel save", func(t *testing.T) {
		s := &Server{usage: newStore()}
		if err := s.usage.RecordTPS("used:1b", 5.0, time.Now()); err != nil {
			t.Fatalf("RecordTPS failed: %v", err)
		}
		s.recordCancelUsage("used:1b", "xxxx", time.Now().Add(-20*time.Second))
		rec, _ := s.usage.Get("used:1b")
		if rec.RecordTokensPerSec != 5.0 {
			t.Errorf("expected record to stay 5.0, got %f", rec.RecordTokensPerSec)
		}
		if rec.TotalCalls != 1 {
			t.Errorf("expected TotalCalls 1 (no cancel save), got %d", rec.TotalCalls)
		}
	})

	t.Run("cold load alone does not block cancel save", func(t *testing.T) {
		s := &Server{usage: newStore()}
		if err := s.usage.RecordColdLoad("cold:1b", 3000, time.Now()); err != nil {
			t.Fatalf("RecordColdLoad failed: %v", err)
		}
		s.recordCancelUsage("cold:1b", "xxxx", time.Now().Add(-20*time.Second))
		rec, _ := s.usage.Get("cold:1b")
		if rec.RecordTokensPerSec != minRecordTPS {
			t.Errorf("expected RecordTokensPerSec %.1f, got %f", minRecordTPS, rec.RecordTokensPerSec)
		}
	})

	t.Run("no content saves nothing", func(t *testing.T) {
		s := &Server{usage: newStore()}
		s.recordCancelUsage("empty:1b", "", time.Now().Add(-10*time.Second))
		if _, ok := s.usage.Get("empty:1b"); ok {
			t.Errorf("expected no record with no content")
		}
	})
}

func TestDeleteRecord(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "model_usage.json")
	store := newModelUsageStore(path)
	_ = store.Load()

	_ = store.SetMeta("ghost:7b", modelUsageMeta{ParameterSize: "7.0B", Size: 1000, Family: "llama"})
	if _, ok := store.Get("ghost:7b"); !ok {
		t.Fatalf("expected record to exist before delete")
	}
	removed, err := store.Delete("ghost:7b")
	if err != nil || !removed {
		t.Fatalf("Delete failed: removed=%v err=%v", removed, err)
	}
	if _, ok := store.Get("ghost:7b"); ok {
		t.Errorf("record should be gone after delete")
	}
	// Deleting again returns removed=false.
	removed2, _ := store.Delete("ghost:7b")
	if removed2 {
		t.Errorf("expected removed=false on second delete")
	}
	// Persistence: reload should not contain the record.
	store2 := newModelUsageStore(path)
	_ = store2.Load()
	if _, ok := store2.Get("ghost:7b"); ok {
		t.Errorf("deleted record persisted across reload")
	}
}

func TestHandleGetModelUsage(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "model_usage.json")
	store := newModelUsageStore(path)
	_ = store.Load()
	_ = store.RecordTPS("test:model", 55.5, time.Now())
	_ = store.SetMeta("test:model", modelUsageMeta{
		ParameterSize: "8.0B",
		Size:          5000000000,
		Quantization:  "Q4_K_M",
		Family:        "llama",
	})

	srv := &Server{usage: store}
	req := httptest.NewRequest(http.MethodGet, "/api/usage/test:model", nil)
	req.SetPathValue("name", "test:model")
	w := httptest.NewRecorder()
	srv.handleGetModelUsage(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var res struct {
		Name  string           `json:"name"`
		Found bool             `json:"found"`
		Usage ModelUsageRecord `json:"usage"`
	}
	if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if !res.Found || res.Usage.RecordTokensPerSec != 55.5 || res.Usage.ParameterSize != "8.0B" {
		t.Fatalf("unexpected usage payload: %#v", res)
	}
}

func TestCustomAndParentModelUsageSync(t *testing.T) {
	tmpDir := t.TempDir()
	usageStore := newModelUsageStore(filepath.Join(tmpDir, "model_usage.json"))
	_ = usageStore.Load()
	customStore := newCustomModelsStore(filepath.Join(tmpDir, "custom_models.json"))
	_ = customStore.Load()

	parent := "hf.co/user/Model:Q3_K_M"
	custom := "hf.co/user/Model:fixed"

	_ = customStore.Register(custom, parent)

	t1 := time.Date(2026, 8, 29, 3, 0, 0, 0, time.UTC)
	_ = usageStore.RecordTPS(parent, 30.0, t1)
	_ = usageStore.RecordColdLoad(parent, 2500, t1)

	srv := &Server{
		usage:        usageStore,
		customModels: customStore,
	}

	// 1. Custom model retrieves usage from parent
	customUsage, ok := srv.getModelUsage(custom)
	if !ok {
		t.Fatalf("expected custom to have usage from parent")
	}
	if customUsage.RecordTokensPerSec != 30.0 {
		t.Fatalf("expected 30.0 tok/s for custom, got %f", customUsage.RecordTokensPerSec)
	}
	if customUsage.MinColdLoadMs != 2500 {
		t.Fatalf("expected 2500ms cold load for custom, got %d", customUsage.MinColdLoadMs)
	}

	// 2. Record faster TPS on custom model
	t2 := time.Date(2026, 8, 29, 4, 0, 0, 0, time.UTC)
	srv.recordModelTPS(custom, 42.5, t2)

	// 3. Parent model should also reflect the new speed (vice versa)
	parentUsage, ok := srv.getModelUsage(parent)
	if !ok || parentUsage.RecordTokensPerSec != 42.5 {
		t.Fatalf("expected parent to reflect 42.5 tok/s, got %f", parentUsage.RecordTokensPerSec)
	}
	if parentUsage.LastUsedAt == nil || !parentUsage.LastUsedAt.Equal(t2) {
		t.Fatalf("expected parent last used to be %v, got %v", t2, parentUsage.LastUsedAt)
	}

	// 4. Custom model should also reflect 42.5
	customUsage2, ok := srv.getModelUsage(custom)
	if !ok || customUsage2.RecordTokensPerSec != 42.5 {
		t.Fatalf("expected custom to reflect 42.5 tok/s, got %f", customUsage2.RecordTokensPerSec)
	}

	// 5. SyncAllModelUsageFamilies persists to usage store
	srv.syncAllModelUsageFamilies()
	recInStore, ok := usageStore.Get(custom)
	if !ok || recInStore.RecordTokensPerSec != 42.5 {
		t.Fatalf("expected custom in usageStore to have 42.5, got %f", recInStore.RecordTokensPerSec)
	}
}

func TestQuantizationUsageIsolation(t *testing.T) {
	tmpDir := t.TempDir()
	usageStore := newModelUsageStore(filepath.Join(tmpDir, "model_usage.json"))
	_ = usageStore.Load()
	customStore := newCustomModelsStore(filepath.Join(tmpDir, "custom_models.json"))
	_ = customStore.Load()

	srv := &Server{
		usage:        usageStore,
		customModels: customStore,
	}

	quant1 := "hf.co/user/Model:IQ3_M"
	quant2 := "hf.co/user/Model:IQ2_M"
	fixed1 := "hf.co/user/Model:fixed"

	// Register fixed1 as being derived specifically from quant1
	_ = customStore.Register(fixed1, quant1)

	// Record usage on quant1
	t1 := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	srv.recordModelTPS(quant1, 43.4, t1)
	srv.recordModelColdLoad(quant1, 15700, t1)

	// Record completely different usage on quant2
	t2 := time.Date(2026, 8, 30, 11, 0, 0, 0, time.UTC)
	srv.recordModelTPS(quant2, 28.1, t2)
	srv.recordModelColdLoad(quant2, 22400, t2)

	// Verify quant1 retains its own stats
	u1, ok1 := srv.getModelUsage(quant1)
	if !ok1 {
		t.Fatalf("expected usage for quant1")
	}
	if u1.RecordTokensPerSec != 43.4 || u1.MinColdLoadMs != 15700 {
		t.Fatalf("quant1 stats mismatch: got tok/s %f, cold load %d", u1.RecordTokensPerSec, u1.MinColdLoadMs)
	}

	// Verify quant2 retains its own separate stats
	u2, ok2 := srv.getModelUsage(quant2)
	if !ok2 {
		t.Fatalf("expected usage for quant2")
	}
	if u2.RecordTokensPerSec != 28.1 || u2.MinColdLoadMs != 22400 {
		t.Fatalf("quant2 stats mismatch: got tok/s %f, cold load %d", u2.RecordTokensPerSec, u2.MinColdLoadMs)
	}

	// Verify fixed1 inherited from its parent quant1, NOT quant2
	uf, okf := srv.getModelUsage(fixed1)
	if !okf {
		t.Fatalf("expected usage for fixed1")
	}
	if uf.RecordTokensPerSec != 43.4 || uf.MinColdLoadMs != 15700 {
		t.Fatalf("fixed1 stats mismatch: got tok/s %f, cold load %d", uf.RecordTokensPerSec, uf.MinColdLoadMs)
	}

	// Run syncAllModelUsageFamilies
	srv.syncAllModelUsageFamilies()

	// Verify quant2 was NOT contaminated by quant1 or fixed1
	rec2, _ := usageStore.Get(quant2)
	if rec2.RecordTokensPerSec != 28.1 || rec2.MinColdLoadMs != 22400 {
		t.Fatalf("quant2 was contaminated after sync: got tok/s %f, cold load %d", rec2.RecordTokensPerSec, rec2.MinColdLoadMs)
	}

	// Verify quant1 remains 43.4
	rec1, _ := usageStore.Get(quant1)
	if rec1.RecordTokensPerSec != 43.4 || rec1.MinColdLoadMs != 15700 {
		t.Fatalf("quant1 stats changed after sync: got tok/s %f, cold load %d", rec1.RecordTokensPerSec, rec1.MinColdLoadMs)
	}
}

func TestResetModelAnalytics(t *testing.T) {
	tmpDir := t.TempDir()
	usageStore := newModelUsageStore(filepath.Join(tmpDir, "model_usage.json"))
	_ = usageStore.Load()

	srv := &Server{
		usage: usageStore,
	}

	modelName := "hf.co/user/Model:IQ2_M"
	t1 := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	_ = usageStore.Record(modelName, 100, 2000000000, 20, t1)
	_ = usageStore.RecordColdLoad(modelName, 15000, t1)
	_ = usageStore.SetMeta(modelName, modelUsageMeta{
		ParameterSize: "30B",
		Quantization:  "IQ2_M",
		Architecture:  "hy_v3",
	})

	rec, ok := usageStore.Get(modelName)
	if !ok || rec.RecordTokensPerSec == 0 || rec.MinColdLoadMs == 0 {
		t.Fatalf("expected usage to be recorded")
	}

	// Call handleResetModelAnalytics via HTTP
	req := httptest.NewRequest(http.MethodPost, "/api/models/analytics/reset", strings.NewReader(`{"name":"`+modelName+`"}`))
	w := httptest.NewRecorder()
	srv.handleResetModelAnalytics(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify analytics are reset while metadata is preserved
	resetRec, ok := usageStore.Get(modelName)
	if !ok {
		t.Fatalf("expected record to still exist with metadata")
	}
	if resetRec.RecordTokensPerSec != 0 {
		t.Errorf("expected 0 tok/s, got %f", resetRec.RecordTokensPerSec)
	}
	if resetRec.MinColdLoadMs != 0 {
		t.Errorf("expected 0 min cold load, got %d", resetRec.MinColdLoadMs)
	}
	if resetRec.TotalCalls != 0 || resetRec.TotalTokens != 0 {
		t.Errorf("expected 0 calls and tokens, got %d calls, %d tokens", resetRec.TotalCalls, resetRec.TotalTokens)
	}
	if resetRec.LastUsedAt != nil {
		t.Errorf("expected nil LastUsedAt, got %v", resetRec.LastUsedAt)
	}
	if resetRec.ParameterSize != "30B" || resetRec.Quantization != "IQ2_M" || resetRec.Architecture != "hy_v3" {
		t.Errorf("metadata was lost: %+v", resetRec)
	}
}



