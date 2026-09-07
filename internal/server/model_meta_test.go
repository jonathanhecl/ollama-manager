package server

import (
	"context"
	"net/http"
	"net/http/httptest"
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
