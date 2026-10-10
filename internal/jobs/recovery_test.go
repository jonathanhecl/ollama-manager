package jobs

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gense/ollama-manager/internal/ollama"
)

func fakeOllamaPull(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/pull" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"pull failed"}`))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func waitJobStatus(t *testing.T, m *Manager, id string, want ...Status) Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if j, ok := m.Get(id); ok {
			for _, s := range want {
				if j.Status == s {
					return j
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	j, _ := m.Get(id)
	t.Fatalf("job %s never reached %v (now %s)", id, want, j.Status)
	return Job{}
}

func testSpec() HFRecoverySpec {
	return HFRecoverySpec{
		Repo:     "owner/repo",
		Revision: "0123456789abcdef0123456789abcdef01234567",
		Files: []HFRecoveryFile{
			{Filename: "model-Q4_K_M.gguf", Size: 100, Digest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		},
	}
}

func TestRecoverRequeuesFailedJob(t *testing.T) {
	m := newTestManager(t)
	id := enqueue(t, m, "huggingface.co/owner/repo:Q4_K_M", StatusError)
	m.mu.Lock()
	m.jobs[id].Error = "pull exploded"
	m.mu.Unlock()

	j, err := m.Recover(id, testSpec())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if j.Status != StatusQueued || j.Recovery == nil || j.OriginalError != "pull exploded" || j.Error != "" {
		t.Fatalf("unexpected job after Recover: %+v", j)
	}
	order := jobOrder(t, m)
	if order[len(order)-1] != "huggingface.co/owner/repo:Q4_K_M" {
		t.Fatalf("recovered job not at end: %v", order)
	}
}

func TestRecoverRejectsNonErrorJob(t *testing.T) {
	m := newTestManager(t)
	for _, st := range []Status{StatusQueued, StatusPaused, StatusDone, StatusCancelled} {
		id := enqueue(t, m, "huggingface.co/owner/repo:"+string(st), st)
		if _, err := m.Recover(id, testSpec()); err == nil {
			t.Fatalf("Recover on %s job should fail", st)
		}
	}
}

func TestRecoveryRunsThroughRunner(t *testing.T) {
	ollamaSrv := fakeOllamaPull(t)
	m := New(filepath.Join(t.TempDir(), "jobs.json"), "", ollama.New(ollamaSrv.URL), nil)
	t.Cleanup(m.Shutdown)

	var calls int32
	var gotSpec HFRecoverySpec
	var gotName string
	m.SetRecoveryRunner(func(ctx context.Context, name string, spec HFRecoverySpec, onProgress func(ollama.PullProgress) error) error {
		atomic.AddInt32(&calls, 1)
		gotName = name
		gotSpec = spec
		if onProgress != nil {
			_ = onProgress(ollama.PullProgress{Status: "checking local blobs", Total: 100, Completed: 0})
		}
		return nil
	})
	m.Start()

	job, err := m.Enqueue("hf.co/owner/repo:Q4_K_M")
	if err != nil {
		t.Fatal(err)
	}
	j := waitJobStatus(t, m, job.ID, StatusError)
	if j.Error == "" {
		t.Fatal("expected pull error")
	}

	if _, err := m.Recover(job.ID, testSpec()); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	j = waitJobStatus(t, m, job.ID, StatusDone)
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("runner calls = %d, want 1", calls)
	}
	if gotName != "huggingface.co/owner/repo:Q4_K_M" || gotSpec.Repo != "owner/repo" {
		t.Fatalf("runner got name=%q spec=%+v", gotName, gotSpec)
	}
}

func TestRecoveryWithoutRunnerFails(t *testing.T) {
	ollamaSrv := fakeOllamaPull(t)
	m := New(filepath.Join(t.TempDir(), "jobs.json"), "", ollama.New(ollamaSrv.URL), nil)
	t.Cleanup(m.Shutdown)
	m.Start()

	job, err := m.Enqueue("huggingface.co/owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	waitJobStatus(t, m, job.ID, StatusError)
	if _, err := m.Recover(job.ID, testSpec()); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	j := waitJobStatus(t, m, job.ID, StatusError)
	if j.Error == "" || j.OriginalError == "" {
		t.Fatalf("expected recovery error with original pull error, got %+v", j)
	}
}

func TestRecoveryRunnerErrorPreservesOriginal(t *testing.T) {
	ollamaSrv := fakeOllamaPull(t)
	m := New("", "", ollama.New(ollamaSrv.URL), nil)
	t.Cleanup(m.Shutdown)
	m.SetRecoveryRunner(func(ctx context.Context, name string, spec HFRecoverySpec, onProgress func(ollama.PullProgress) error) error {
		return errors.New("download blew up")
	})
	m.Start()

	job, err := m.Enqueue("huggingface.co/owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	waitJobStatus(t, m, job.ID, StatusError)
	if _, err := m.Recover(job.ID, testSpec()); err != nil {
		t.Fatal(err)
	}
	j := waitJobStatus(t, m, job.ID, StatusError)
	if j.Error == "" || j.OriginalError == "" {
		t.Fatalf("expected combined error, got %+v", j)
	}
}

func TestRecoverIsIdempotentAgainstDoubleClick(t *testing.T) {
	m := newTestManager(t)
	id := enqueue(t, m, "huggingface.co/owner/repo", StatusError)
	if _, err := m.Recover(id, testSpec()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Recover(id, testSpec()); err == nil {
		t.Fatal("second Recover while queued should fail")
	}
}

func TestNativeRetryClearsRecovery(t *testing.T) {
	ollamaSrv := fakeOllamaPull(t)
	m := New(filepath.Join(t.TempDir(), "jobs.json"), "", ollama.New(ollamaSrv.URL), nil)
	t.Cleanup(m.Shutdown)
	m.PauseQueue()

	id := enqueue(t, m, "huggingface.co/owner/repo", StatusError)
	m.mu.Lock()
	m.jobs[id].Error = "pull exploded"
	m.jobs[id].Recovery = &HFRecoverySpec{Repo: "owner/repo", Revision: "x"}
	m.jobs[id].OriginalError = "pull exploded"
	m.mu.Unlock()

	if _, err := m.Enqueue("huggingface.co/owner/repo"); err != nil {
		t.Fatal(err)
	}
	j, _ := m.Get(id)
	if j.Recovery != nil || j.OriginalError != "" || j.Error != "" {
		t.Fatalf("native retry must clear recovery mode, got %+v", j)
	}
}

func TestRecoverySpecSurvivesReload(t *testing.T) {
	dir := t.TempDir()
	jobsPath := filepath.Join(dir, "jobs.json")
	m := New(jobsPath, "", nil, nil)
	m.PauseQueue()
	id := enqueue(t, m, "huggingface.co/owner/repo", StatusError)
	if _, err := m.Recover(id, testSpec()); err != nil {
		t.Fatal(err)
	}

	m2 := New(jobsPath, "", nil, nil)
	if err := m2.Load(); err != nil {
		t.Fatal(err)
	}
	j, ok := m2.Get(id)
	if !ok || j.Recovery == nil || j.Recovery.Repo != "owner/repo" || len(j.Recovery.Files) != 1 {
		t.Fatalf("spec not persisted: %+v", j)
	}
	if j.Status != StatusPaused {
		t.Fatalf("paused queue should keep job paused, got %s", j.Status)
	}
}

func TestCancelRunningRecovery(t *testing.T) {
	ollamaSrv := fakeOllamaPull(t)
	m := New("", "", ollama.New(ollamaSrv.URL), nil)
	t.Cleanup(m.Shutdown)
	started := make(chan struct{})
	m.SetRecoveryRunner(func(ctx context.Context, name string, spec HFRecoverySpec, onProgress func(ollama.PullProgress) error) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	m.Start()

	job, err := m.Enqueue("huggingface.co/owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	waitJobStatus(t, m, job.ID, StatusError)
	if _, err := m.Recover(job.ID, testSpec()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("runner never started")
	}
	if err := m.Cancel(job.ID); err != nil {
		t.Fatal(err)
	}
	waitJobStatus(t, m, job.ID, StatusCancelled)
}

func TestPauseResumeRunningRecovery(t *testing.T) {
	ollamaSrv := fakeOllamaPull(t)
	m := New("", "", ollama.New(ollamaSrv.URL), nil)
	t.Cleanup(m.Shutdown)

	started := make(chan struct{}, 4)
	var calls int32
	m.SetRecoveryRunner(func(ctx context.Context, name string, spec HFRecoverySpec, onProgress func(ollama.PullProgress) error) error {
		started <- struct{}{}
		if atomic.AddInt32(&calls, 1) == 1 {
			<-ctx.Done()
			return ctx.Err()
		}
		if spec.Repo != "owner/repo" {
			t.Errorf("resumed run lost spec: %+v", spec)
		}
		return nil
	})
	m.Start()

	job, err := m.Enqueue("huggingface.co/owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	waitJobStatus(t, m, job.ID, StatusError)
	if _, err := m.Recover(job.ID, testSpec()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("runner never started")
	}
	if err := m.Pause(job.ID); err != nil {
		t.Fatal(err)
	}
	waitJobStatus(t, m, job.ID, StatusPaused)
	if err := m.Resume(job.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not restart after resume")
	}
	waitJobStatus(t, m, job.ID, StatusDone)
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("runner calls = %d, want 2", calls)
	}
}

func TestRecoverySuccessRecordsHistory(t *testing.T) {
	ollamaSrv := fakeOllamaPull(t)
	dir := t.TempDir()
	m := New(filepath.Join(dir, "jobs.json"), filepath.Join(dir, "history.json"), ollama.New(ollamaSrv.URL), nil)
	t.Cleanup(m.Shutdown)
	m.SetRecoveryRunner(func(ctx context.Context, name string, spec HFRecoverySpec, onProgress func(ollama.PullProgress) error) error {
		return nil
	})
	m.Start()

	job, err := m.Enqueue("huggingface.co/owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	waitJobStatus(t, m, job.ID, StatusError)
	if _, err := m.Recover(job.ID, testSpec()); err != nil {
		t.Fatal(err)
	}
	waitJobStatus(t, m, job.ID, StatusDone)

	h, ok := m.History("huggingface.co/owner/repo")
	if !ok || h.DoneCount != 1 {
		t.Fatalf("history missing done record: %+v ok=%v", h, ok)
	}
}

func TestRecoverySpecDeepCopy(t *testing.T) {
	m := newTestManager(t)
	id := enqueue(t, m, "huggingface.co/owner/repo", StatusError)
	spec := testSpec()
	spec.Projector = &HFRecoveryFile{Filename: "mmproj.gguf", Size: 10, Digest: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	j, err := m.Recover(id, spec)
	if err != nil {
		t.Fatal(err)
	}
	spec.Files[0].Filename = "mutated.gguf"
	spec.Projector.Filename = "mutated-proj.gguf"
	got, _ := m.Get(id)
	if got.Recovery.Files[0].Filename != "model-Q4_K_M.gguf" || got.Recovery.Projector.Filename != "mmproj.gguf" {
		t.Fatalf("Recover kept caller slices: %+v", got.Recovery)
	}
	got.Recovery.Files[0].Filename = "mutated2.gguf"
	got.Recovery.Projector.Filename = "mutated2.gguf"
	got2, _ := m.Get(id)
	if got2.Recovery.Files[0].Filename != "model-Q4_K_M.gguf" || got2.Recovery.Projector.Filename != "mmproj.gguf" {
		t.Fatalf("Get returned shared spec: %+v", got2.Recovery)
	}
	m.mu.Lock()
	m.jobs[id].Recovery.Files[0].Filename = "inner.gguf"
	m.mu.Unlock()
	if j.Recovery.Files[0].Filename == "inner.gguf" {
		t.Fatal("Recover snapshot shares spec internals")
	}
}
