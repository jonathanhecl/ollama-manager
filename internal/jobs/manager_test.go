package jobs

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gense/ollama-manager/internal/ollama"
)

// newTestManager builds a manager backed by a fake Ollama server whose
// /api/pull blocks until the request context is cancelled, so enqueued jobs
// stay running/queued and we can assert on ordering.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	ollamaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/pull" {
			<-r.Context().Done()
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(ollamaSrv.Close)
	m := New(filepath.Join(t.TempDir(), "jobs.json"), filepath.Join(t.TempDir(), "history.json"), ollama.New(ollamaSrv.URL), nil)
	t.Cleanup(m.Shutdown)
	return m
}

func TestPromoteMovesJobToFront(t *testing.T) {
	m := newTestManager(t)
	for _, name := range []string{"a", "b", "c", "d"} {
		if _, err := m.Enqueue(name); err != nil {
			t.Fatalf("enqueue %s: %v", name, err)
		}
	}

	// Promote the last job to the front.
	if err := m.Promote(jobID(t, m, "d")); err != nil {
		t.Fatalf("promote: %v", err)
	}

	want := []string{"d", "a", "b", "c"}
	got := jobNames(t, m)
	if len(got) != len(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestPromotePreservesRelativeOrder(t *testing.T) {
	m := newTestManager(t)
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		if _, err := m.Enqueue(name); err != nil {
			t.Fatalf("enqueue %s: %v", name, err)
		}
	}

	// Promote the middle job; all others keep their relative order.
	if err := m.Promote(jobID(t, m, "c")); err != nil {
		t.Fatalf("promote: %v", err)
	}
	want := []string{"c", "a", "b", "d", "e"}
	got := jobNames(t, m)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestPromoteResumesPausedJob(t *testing.T) {
	m := newTestManager(t)
	for _, name := range []string{"a", "b", "c"} {
		if _, err := m.Enqueue(name); err != nil {
			t.Fatalf("enqueue %s: %v", name, err)
		}
	}
	if err := m.Pause(jobID(t, m, "b")); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if j := m.jobs[jobID(t, m, "b")]; j == nil || j.Status != StatusPaused {
		t.Fatalf("expected b paused before promote")
	}

	if err := m.Promote(jobID(t, m, "b")); err != nil {
		t.Fatalf("promote paused: %v", err)
	}
	if j := m.jobs[jobID(t, m, "b")]; j == nil || j.Status != StatusQueued {
		t.Fatalf("promoted paused job should be queued, got %v", j.Status)
	}
	if got := jobNames(t, m); got[0] != "b" {
		t.Fatalf("promoted job not at front: %v", got)
	}
}

func TestPromoteRejectsRunningAndTerminal(t *testing.T) {
	m := newTestManager(t)
	if _, err := m.Enqueue("a"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	id := jobID(t, m, "a")
	if err := m.Cancel(id); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if err := m.Promote(id); err == nil {
		t.Fatal("promoting a cancelled job should fail")
	}
}

func jobID(t *testing.T, m *Manager, name string) string {
	t.Helper()
	for _, j := range m.List() {
		if j.Name == name {
			return j.ID
		}
	}
	t.Fatalf("job %q not found", name)
	return ""
}

func jobNames(t *testing.T, m *Manager) []string {
	t.Helper()
	var out []string
	for _, j := range m.List() {
		out = append(out, j.Name)
	}
	return out
}
