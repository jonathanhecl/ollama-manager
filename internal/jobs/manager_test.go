package jobs

import (
	"path/filepath"
	"testing"
	"time"
)

// newTestManager builds an empty manager with a fake active job occupying the
// worker slot, so tryStartNextLocked never launches a pull goroutine. This
// keeps the tests deterministic (no live network) while letting us assert on
// queue ordering.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	m := New(filepath.Join(t.TempDir(), "jobs.json"), filepath.Join(t.TempDir(), "history.json"), nil, nil)
	id, err := newID()
	if err != nil {
		t.Fatalf("newID: %v", err)
	}
	m.mu.Lock()
	m.jobs[id] = &Job{ID: id, Name: "__active__", Status: StatusRunning, CreatedAt: time.Now().UTC()}
	m.order = append(m.order, id)
	m.activeID = id
	m.mu.Unlock()
	return m
}

// enqueue registers a job directly in the manager's state, bypassing the worker.
func enqueue(t *testing.T, m *Manager, name string, status Status) string {
	t.Helper()
	id, err := newID()
	if err != nil {
		t.Fatalf("newID: %v", err)
	}
	m.mu.Lock()
	m.jobs[id] = &Job{ID: id, Name: name, Status: status, CreatedAt: time.Now().UTC()}
	m.order = append(m.order, id)
	m.mu.Unlock()
	return id
}

func TestPromoteMovesJobToFront(t *testing.T) {
	m := newTestManager(t)
	enqueue(t, m, "a", StatusQueued)
	enqueue(t, m, "b", StatusQueued)
	enqueue(t, m, "c", StatusQueued)
	d := enqueue(t, m, "d", StatusQueued)

	if err := m.Promote(d); err != nil {
		t.Fatalf("promote: %v", err)
	}
	// The promoted job must come before every other queued/paused job. (The
	// synthetic "__active__" running job also sits in m.order but is bucketed
	// separately in the UI.)
	if got := jobOrder(t, m); got[0] != "d" || got[2] != "a" || got[3] != "b" || got[4] != "c" {
		t.Fatalf("order = %v, want [d __active__ a b c]", got)
	}
}

func TestPromotePreservesRelativeOrder(t *testing.T) {
	m := newTestManager(t)
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		enqueue(t, m, name, StatusQueued)
	}
	cID := jobID(t, m, "c")
	if err := m.Promote(cID); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if got := jobOrder(t, m); got[0] != "c" || got[2] != "a" || got[3] != "b" || got[4] != "d" || got[5] != "e" {
		t.Fatalf("order = %v, want [c __active__ a b d e]", got)
	}
}

func TestPromoteResumesPausedJob(t *testing.T) {
	m := newTestManager(t)
	enqueue(t, m, "a", StatusQueued)
	bID := enqueue(t, m, "b", StatusPaused)
	enqueue(t, m, "c", StatusQueued)

	if err := m.Promote(bID); err != nil {
		t.Fatalf("promote paused: %v", err)
	}
	j := m.jobs[bID]
	if j == nil || j.Status != StatusQueued {
		t.Fatalf("promoted paused job should be queued at front, got %v", j.Status)
	}
	if got := jobOrder(t, m); got[0] != "b" {
		t.Fatalf("promoted job not at front: %v", got)
	}
}

func TestPromoteRejectsRunningAndTerminal(t *testing.T) {
	m := newTestManager(t)
	done := enqueue(t, m, "done", StatusDone)
	if err := m.Promote(done); err == nil {
		t.Fatal("promoting a terminal job should fail")
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

func jobOrder(t *testing.T, m *Manager) []string {
	t.Helper()
	var out []string
	for _, j := range m.List() {
		out = append(out, j.Name)
	}
	return out
}
