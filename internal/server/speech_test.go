package server

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gense/ollama-manager/internal/ollama"
)

func TestRecordSpeechMetrics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model_usage.json")
	store := newModelUsageStore(path)
	if err := store.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	at := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	// A cold call pays for the model load, so its latency is the cold load time
	// and it must never become the throughput record.
	if err := store.RecordSpeech("phonon:40m-es", 100, 48000, 4000, true, at); err != nil {
		t.Fatalf("RecordSpeech cold failed: %v", err)
	}
	rec, ok := store.Get("phonon:40m-es")
	if !ok {
		t.Fatalf("expected a record for phonon:40m-es")
	}
	if rec.MinColdLoadMs != 4000 {
		t.Errorf("expected MinColdLoadMs 4000, got %d", rec.MinColdLoadMs)
	}
	if rec.MinColdLoadAt == nil || !rec.MinColdLoadAt.Equal(at) {
		t.Errorf("expected MinColdLoadAt %v, got %v", at, rec.MinColdLoadAt)
	}
	if rec.SpeechColdMs != 4000 {
		t.Errorf("expected SpeechColdMs 4000, got %d", rec.SpeechColdMs)
	}
	if rec.RecordCharsPerSec != 0 {
		t.Errorf("a cold call must not set the record, got %f", rec.RecordCharsPerSec)
	}

	// Warm call: 100 chars in 500 ms = 200 chars/s.
	warm := at.Add(time.Minute)
	if err := store.RecordSpeech("phonon:40m-es", 100, 48000, 500, false, warm); err != nil {
		t.Fatalf("RecordSpeech warm failed: %v", err)
	}
	rec, _ = store.Get("phonon:40m-es")
	if math.Abs(rec.RecordCharsPerSec-200) > 0.001 {
		t.Errorf("expected RecordCharsPerSec 200, got %f", rec.RecordCharsPerSec)
	}
	if rec.RecordCharsPerSecAt == nil || !rec.RecordCharsPerSecAt.Equal(warm) {
		t.Errorf("expected RecordCharsPerSecAt %v, got %v", warm, rec.RecordCharsPerSecAt)
	}
	if rec.MinColdLoadMs != 4000 {
		t.Errorf("a warm call must not touch the cold load time, got %d", rec.MinColdLoadMs)
	}
	if rec.SpeechCalls != 2 || rec.SpeechInputChars != 200 || rec.SpeechAudioBytes != 96000 {
		t.Errorf("unexpected speech counters: %+v", rec)
	}

	// A slower warm call keeps the record.
	if err := store.RecordSpeech("phonon:40m-es", 100, 48000, 1000, false, warm.Add(time.Minute)); err != nil {
		t.Fatalf("RecordSpeech slow failed: %v", err)
	}
	rec, _ = store.Get("phonon:40m-es")
	if math.Abs(rec.RecordCharsPerSec-200) > 0.001 {
		t.Errorf("expected the record to stay at 200, got %f", rec.RecordCharsPerSec)
	}
	if rec.SpeechMinMs != 500 || rec.SpeechMaxMs != 4000 {
		t.Errorf("unexpected speech latency range: min=%d max=%d", rec.SpeechMinMs, rec.SpeechMaxMs)
	}

	// The metrics survive a reload, since the panel reads them from disk.
	reloaded := newModelUsageStore(path)
	if err := reloaded.Load(); err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	rec, ok = reloaded.Get("phonon:40m-es")
	if !ok {
		t.Fatalf("expected the record after reload")
	}
	if math.Abs(rec.RecordCharsPerSec-200) > 0.001 || rec.MinColdLoadMs != 4000 {
		t.Errorf("reloaded metrics lost: chars/s=%f cold=%d", rec.RecordCharsPerSec, rec.MinColdLoadMs)
	}
}

func TestSpeechVoiceList(t *testing.T) {
	got := speechVoiceList([]string{"Zoey", "default", "FREYA", "zoey", "  "})
	want := []string{"default", "Zoey", "FREYA"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("expected %v, got %v", want, got)
	}
	if only := speechVoiceList(nil); len(only) != 1 || only[0] != "default" {
		t.Errorf("expected [default] for a model without voices, got %v", only)
	}
}

func TestHandleSpeechVoices(t *testing.T) {
	ollamaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/show" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"capabilities": []string{"speech"},
			"voices":       []string{"Zoey", "Easton"},
		})
	}))
	defer ollamaSrv.Close()

	s := &Server{ollama: ollama.New(ollamaSrv.URL)}

	req := httptest.NewRequest(http.MethodGet, "/api/speech/voices?model=phonon", nil)
	rr := httptest.NewRecorder()
	s.handleSpeechVoices(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Model    string   `json:"model"`
		Voices   []string `json:"voices"`
		Reported int      `json:"reported"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if out.Model != "phonon" {
		t.Errorf("expected model phonon, got %q", out.Model)
	}
	if strings.Join(out.Voices, ",") != "default,Zoey,Easton" {
		t.Errorf("unexpected voices: %v", out.Voices)
	}
	if out.Reported != 2 {
		t.Errorf("expected 2 reported voices, got %d", out.Reported)
	}

	// Without a model there is nothing to ask the engine about.
	rr = httptest.NewRecorder()
	s.handleSpeechVoices(rr, httptest.NewRequest(http.MethodGet, "/api/speech/voices", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 without model, got %d", rr.Code)
	}
}

// An engine that does not report voices (Ollama before 0.40.11) leaves the
// field out of /api/show, which must not look like a model with one named
// voice: the answer carries reported=0 so the UI can say why.
func TestHandleSpeechVoicesWithoutEngineList(t *testing.T) {
	ollamaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"capabilities": []string{"speech"},
		})
	}))
	defer ollamaSrv.Close()

	s := &Server{ollama: ollama.New(ollamaSrv.URL)}
	rr := httptest.NewRecorder()
	s.handleSpeechVoices(rr, httptest.NewRequest(http.MethodGet, "/api/speech/voices?model=phonon:40m-es", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Voices   []string `json:"voices"`
		Reported int      `json:"reported"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if strings.Join(out.Voices, ",") != "default" || out.Reported != 0 {
		t.Errorf("expected [default] with reported=0, got %v / %d", out.Voices, out.Reported)
	}
}
