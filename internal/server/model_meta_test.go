package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/gense/ollama-manager/internal/ollama"
)

// A transient /api/show failure (timeout, Ollama busy) must not stick as
// permanently blank capabilities: the failure is not cached and the next
// list refresh retries detection.
func TestFetchModelMetaRetriesShowFailures(t *testing.T) {
	var calls atomic.Int32
	ollamaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/show" {
			http.NotFound(w, r)
			return
		}
		if calls.Add(1) == 1 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"capabilities": []string{"completion", "tools"},
		})
	}))
	defer ollamaSrv.Close()

	srv := newTestServer(t, ollamaSrv.URL)
	models := []ollama.Model{{Name: "m:latest", Digest: "sha256:abc"}}

	first := srv.fetchModelMeta(context.Background(), models)
	if got := first["sha256:abc"].Capabilities; len(got) != 0 {
		t.Fatalf("first call caps = %v, want empty (show failed)", got)
	}
	srv.ctxMu.RLock()
	_, cached := srv.capsCache["sha256:abc"]
	srv.ctxMu.RUnlock()
	if cached {
		t.Fatalf("failed show was cached; the retry would never happen")
	}

	second := srv.fetchModelMeta(context.Background(), models)
	got := second["sha256:abc"].Capabilities
	if len(got) != 2 || got[0] != "completion" || got[1] != "tools" {
		t.Fatalf("second call caps = %v, want [completion tools]", got)
	}
	if calls.Load() != 2 {
		t.Fatalf("show calls = %d, want 2", calls.Load())
	}
}

// /api/show can list vision/audio for a multimodal-family GGUF imported
// without an mmproj, so text-only models must not be marked vision-capable
// (which would force them through vision tests they cannot run).
func TestFetchModelMetaStripsVisionWithoutProjector(t *testing.T) {
	ollamaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/show" {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"capabilities": []string{"completion", "tools", "thinking", "vision", "audio"},
		})
	}))
	defer ollamaSrv.Close()

	srv := newTestServer(t, ollamaSrv.URL)
	models := []ollama.Model{{Name: "text-only:latest", Digest: "sha256:no-proj"}}
	got := srv.fetchModelMeta(context.Background(), models)["sha256:no-proj"].Capabilities
	want := []string{"completion", "tools", "thinking"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("caps = %v, want %v (vision/audio without projector)", got, want)
	}
}

func TestFetchModelMetaKeepsVisionWithProjector(t *testing.T) {
	ollamaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/show" {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"capabilities":   []string{"completion", "vision"},
			"projector_info": map[string]any{"clip.has_vision_encoder": true},
		})
	}))
	defer ollamaSrv.Close()

	srv := newTestServer(t, ollamaSrv.URL)
	models := []ollama.Model{{Name: "vlm:latest", Digest: "sha256:proj"}}
	got := srv.fetchModelMeta(context.Background(), models)["sha256:proj"].Capabilities
	want := []string{"completion", "vision"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("caps = %v, want %v (projector keeps vision)", got, want)
	}
}
