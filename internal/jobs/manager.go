// Package jobs implements a FIFO download queue for Ollama model pulls.
//
// Concurrency is capped at 1 active job: when a job is enqueued the manager
// starts it immediately if nothing is running, otherwise it waits in the
// queue. Cancelling a job either removes it from the queue (if queued) or
// stops the in-flight /api/pull (if running) by cancelling its context.
//
// Because Ollama stores downloaded blobs content-addressably, re-enqueuing
// a cancelled/interrupted job effectively resumes the download: already
// completed layers are not re-downloaded.
//
// Jobs are persisted to a JSON file so queued/finished state survives
// restarts. On load, any job that was "running" is demoted to "queued" so
// the worker picks it up again.
package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gense/ollama-manager/internal/ollama"
)

// Status is the lifecycle state of a Job.
type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusDone      Status = "done"
	StatusError     Status = "error"
	StatusCancelled Status = "cancelled"
)

// IsTerminal reports whether s is a final, non-active state.
func (s Status) IsTerminal() bool {
	return s == StatusDone || s == StatusError || s == StatusCancelled
}

// Job is a single model download tracked by the manager.
type Job struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Status     Status    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	Completed  int64     `json:"completed"`
	Total      int64     `json:"total"`
	Percent    float64   `json:"percent"`
	Digest     string    `json:"digest,omitempty"`
	StatusText string    `json:"status_text,omitempty"`
	Error      string    `json:"error,omitempty"`

	// cancel is set while the job is running so Cancel() can abort the
	// underlying /api/pull stream.
	cancel context.CancelFunc `json:"-"`
}

// clone returns a value copy safe to hand out to callers / SSE subscribers.
func (j *Job) clone() Job {
	cp := *j
	cp.cancel = nil
	return cp
}

// EventKind identifies the kind of broadcast emitted by the manager.
type EventKind string

const (
	EventUpdate EventKind = "update"
	EventRemove EventKind = "remove"
)

// Event is what subscribers receive through the channel returned by Subscribe.
type Event struct {
	Kind EventKind `json:"kind"`
	Job  *Job      `json:"job,omitempty"`
	ID   string    `json:"id,omitempty"`
}

// persistFile is the disk format for jobs.json.
type persistFile struct {
	Jobs []Job `json:"jobs"`
}

// Manager coordinates the queue, execution, persistence and fan-out of
// job events.
type Manager struct {
	mu       sync.Mutex
	jobs     map[string]*Job
	order    []string // insertion order of job ids
	activeID string   // id of the currently running job, or ""
	path     string
	ollama   *ollama.Client
	logger   *log.Logger

	subsMu  sync.Mutex
	subs    map[int64]chan Event
	nextSub int64
}

// New returns an empty manager. Call Load() to restore state from disk, then
// Start() to kick off the first queued job (if any).
func New(path string, client *ollama.Client, logger *log.Logger) *Manager {
	if logger == nil {
		logger = log.Default()
	}
	return &Manager{
		jobs:   make(map[string]*Job),
		path:   path,
		ollama: client,
		logger: logger,
		subs:   make(map[int64]chan Event),
	}
}

// Load reads jobs.json, if present. Any job persisted as "running" is
// demoted to "queued" so the worker picks it up again on the next Start().
func (m *Manager) Load() error {
	data, err := os.ReadFile(m.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", m.path, err)
	}
	var pf persistFile
	if err := json.Unmarshal(data, &pf); err != nil {
		return fmt.Errorf("parse %s: %w", m.path, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range pf.Jobs {
		j := pf.Jobs[i]
		if j.ID == "" || j.Name == "" {
			continue
		}
		if j.Status == StatusRunning {
			j.Status = StatusQueued
			j.StartedAt = time.Time{}
			j.Percent = 0
			j.Completed = 0
			j.Total = 0
			j.StatusText = ""
		}
		jj := j
		m.jobs[j.ID] = &jj
		m.order = append(m.order, j.ID)
	}
	return nil
}

// Start kicks the worker: if there is no active job, it promotes the first
// queued job (in insertion order) to running.
func (m *Manager) Start() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tryStartNextLocked()
}

// List returns a snapshot of all jobs in insertion order.
func (m *Manager) List() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Job, 0, len(m.order))
	for _, id := range m.order {
		if j, ok := m.jobs[id]; ok {
			out = append(out, j.clone())
		}
	}
	return out
}

// Enqueue registers a new job and starts it if the worker is idle. Returns
// a snapshot of the created job.
//
// If a job with the same model name already exists:
//   - queued or running -> the existing job is returned unchanged (idempotent).
//   - done / error / cancelled -> the same job slot is reused: its transient
//     fields are reset and it goes back to queued, moved to the end of the
//     queue. This avoids piling up duplicate "cancelled" cards every time the
//     user re-adds (resumes) a model after cancelling.
func (m *Manager) Enqueue(name string) (Job, error) {
	if name == "" {
		return Job{}, errors.New("model name required")
	}
	m.mu.Lock()
	for _, id := range m.order {
		j := m.jobs[id]
		if j == nil || j.Name != name {
			continue
		}
		switch j.Status {
		case StatusQueued, StatusRunning:
			snap := j.clone()
			m.mu.Unlock()
			return snap, nil
		case StatusDone, StatusError, StatusCancelled:
			// Reuse this slot: reset and re-queue at the end.
			j.Status = StatusQueued
			j.CreatedAt = time.Now().UTC()
			j.StartedAt = time.Time{}
			j.FinishedAt = time.Time{}
			j.Completed = 0
			j.Total = 0
			j.Percent = 0
			j.StatusText = ""
			j.Error = ""
			j.Digest = ""
			m.order = removeString(m.order, id)
			m.order = append(m.order, id)
			snap := j.clone()
			m.tryStartNextLocked()
			if err := m.saveLocked(); err != nil {
				m.logger.Printf("jobs: save failed: %v", err)
			}
			m.mu.Unlock()
			m.broadcast(Event{Kind: EventUpdate, Job: &snap})
			return snap, nil
		}
	}
	id, err := newID()
	if err != nil {
		m.mu.Unlock()
		return Job{}, err
	}
	j := &Job{
		ID:        id,
		Name:      name,
		Status:    StatusQueued,
		CreatedAt: time.Now().UTC(),
	}
	m.jobs[id] = j
	m.order = append(m.order, id)
	snap := j.clone()
	m.tryStartNextLocked()
	if err := m.saveLocked(); err != nil {
		m.logger.Printf("jobs: save failed: %v", err)
	}
	m.mu.Unlock()
	m.broadcast(Event{Kind: EventUpdate, Job: &snap})
	return snap, nil
}

// Cancel aborts a running job or removes a queued one from the queue. The
// job is not deleted from history: it keeps the "cancelled" state so the UI
// can show it (and the user can choose to remove it).
func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	j, ok := m.jobs[id]
	if !ok {
		m.mu.Unlock()
		return errors.New("job not found")
	}
	switch j.Status {
	case StatusQueued:
		j.Status = StatusCancelled
		j.FinishedAt = time.Now().UTC()
		snap := j.clone()
		if err := m.saveLocked(); err != nil {
			m.logger.Printf("jobs: save failed: %v", err)
		}
		m.mu.Unlock()
		m.broadcast(Event{Kind: EventUpdate, Job: &snap})
		return nil
	case StatusRunning:
		if j.cancel != nil {
			j.cancel()
		}
		// The runner goroutine will set the terminal state and broadcast.
		m.mu.Unlock()
		return nil
	default:
		m.mu.Unlock()
		return fmt.Errorf("job already %s", j.Status)
	}
}

// Remove deletes a job from history. Only terminal jobs can be removed.
func (m *Manager) Remove(id string) error {
	m.mu.Lock()
	j, ok := m.jobs[id]
	if !ok {
		m.mu.Unlock()
		return errors.New("job not found")
	}
	if !j.Status.IsTerminal() {
		m.mu.Unlock()
		return fmt.Errorf("cannot remove job in state %s", j.Status)
	}
	status := j.Status
	name := j.Name
	delete(m.jobs, id)
	m.order = removeString(m.order, id)
	if err := m.saveLocked(); err != nil {
		m.logger.Printf("jobs: save failed: %v", err)
	}
	m.mu.Unlock()

	// Only on manual remove of failed/cancelled jobs, attempt Ollama cleanup.
	// Ignore cleanup errors so removing from history always succeeds.
	if (status == StatusError || status == StatusCancelled) && name != "" && m.ollama != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := m.ollama.Delete(ctx, name)
		cancel()
		if err != nil {
			m.logger.Printf("jobs: cleanup failed for %q: %v", name, err)
		}
	}

	m.broadcast(Event{Kind: EventRemove, ID: id})
	return nil
}

// ClearFinished removes all terminal jobs and returns the number removed.
func (m *Manager) ClearFinished() int {
	m.mu.Lock()
	removed := make([]string, 0)
	kept := make([]string, 0, len(m.order))
	for _, id := range m.order {
		j := m.jobs[id]
		if j == nil {
			continue
		}
		if j.Status.IsTerminal() {
			delete(m.jobs, id)
			removed = append(removed, id)
		} else {
			kept = append(kept, id)
		}
	}
	m.order = kept
	if len(removed) > 0 {
		if err := m.saveLocked(); err != nil {
			m.logger.Printf("jobs: save failed: %v", err)
		}
	}
	m.mu.Unlock()
	for _, id := range removed {
		m.broadcast(Event{Kind: EventRemove, ID: id})
	}
	return len(removed)
}

// Subscribe returns a channel that receives every update/remove event. The
// returned cancel function closes the channel and removes the subscription.
// The channel is buffered and slow consumers may miss intermediate events;
// clients are expected to re-hydrate via List() when reconnecting.
func (m *Manager) Subscribe() (<-chan Event, func()) {
	m.subsMu.Lock()
	id := atomic.AddInt64(&m.nextSub, 1)
	ch := make(chan Event, 64)
	m.subs[id] = ch
	m.subsMu.Unlock()
	cancel := func() {
		m.subsMu.Lock()
		if sub, ok := m.subs[id]; ok {
			delete(m.subs, id)
			close(sub)
		}
		m.subsMu.Unlock()
	}
	return ch, cancel
}

// broadcast fans out an event to every live subscriber, dropping events for
// subscribers whose buffer is full (to protect the runner from blocking).
func (m *Manager) broadcast(ev Event) {
	m.subsMu.Lock()
	defer m.subsMu.Unlock()
	for id, ch := range m.subs {
		select {
		case ch <- ev:
		default:
			m.logger.Printf("jobs: subscriber %d is slow, dropping event", id)
		}
	}
}

// tryStartNextLocked must be called with m.mu held. If no job is currently
// running it promotes the first queued job and launches the runner.
func (m *Manager) tryStartNextLocked() {
	if m.activeID != "" {
		return
	}
	for _, id := range m.order {
		j := m.jobs[id]
		if j == nil {
			continue
		}
		if j.Status == StatusQueued {
			ctx, cancel := context.WithCancel(context.Background())
			j.Status = StatusRunning
			j.StartedAt = time.Now().UTC()
			j.cancel = cancel
			m.activeID = id
			go m.run(ctx, id)
			return
		}
	}
}

// run executes one job in its own goroutine. It streams progress events
// from Ollama, updates the Job in place (under lock), and broadcasts a
// throttled update. On exit it marks the terminal state, persists, and
// starts the next queued job if any.
func (m *Manager) run(ctx context.Context, id string) {
	// Snapshot for broadcasting that the job just went running.
	m.mu.Lock()
	startJob := m.jobs[id]
	if startJob == nil {
		m.activeID = ""
		m.mu.Unlock()
		return
	}
	name := startJob.Name
	snap := startJob.clone()
	// Persist the queued->running transition.
	if err := m.saveLocked(); err != nil {
		m.logger.Printf("jobs: save failed: %v", err)
	}
	m.mu.Unlock()
	m.broadcast(Event{Kind: EventUpdate, Job: &snap})

	var lastEmit time.Time
	const emitEvery = 250 * time.Millisecond

	err := m.ollama.Pull(ctx, name, func(ev ollama.PullProgress) error {
		m.mu.Lock()
		j := m.jobs[id]
		if j == nil {
			m.mu.Unlock()
			return errors.New("job gone")
		}
		j.StatusText = ev.Status
		if ev.Digest != "" {
			j.Digest = ev.Digest
		}
		if ev.Total > 0 {
			j.Total = ev.Total
			j.Completed = ev.Completed
			pct := float64(ev.Completed) / float64(ev.Total) * 100
			if pct > 100 {
				pct = 100
			}
			j.Percent = pct
		}
		// Throttle broadcasts to avoid overwhelming SSE subscribers.
		if time.Since(lastEmit) < emitEvery {
			m.mu.Unlock()
			return nil
		}
		lastEmit = time.Now()
		cp := j.clone()
		m.mu.Unlock()
		m.broadcast(Event{Kind: EventUpdate, Job: &cp})
		return nil
	})

	// Finalize state.
	m.mu.Lock()
	j := m.jobs[id]
	if j != nil {
		j.cancel = nil
		j.FinishedAt = time.Now().UTC()
		switch {
		case err == nil:
			j.Status = StatusDone
			j.Percent = 100
			j.StatusText = "success"
			j.Error = ""
		case errors.Is(err, context.Canceled) || ctx.Err() != nil:
			j.Status = StatusCancelled
			j.Error = ""
		default:
			j.Status = StatusError
			j.Error = err.Error()
		}
	}
	m.activeID = ""
	var finalSnap Job
	if j != nil {
		finalSnap = j.clone()
	}
	if saveErr := m.saveLocked(); saveErr != nil {
		m.logger.Printf("jobs: save failed: %v", saveErr)
	}
	m.tryStartNextLocked()
	m.mu.Unlock()
	if j != nil {
		m.broadcast(Event{Kind: EventUpdate, Job: &finalSnap})
	}
}

// saveLocked persists the current jobs list to disk. Must be called with
// m.mu held.
func (m *Manager) saveLocked() error {
	if m.path == "" {
		return nil
	}
	pf := persistFile{Jobs: make([]Job, 0, len(m.order))}
	for _, id := range m.order {
		if j, ok := m.jobs[id]; ok {
			pf.Jobs = append(pf.Jobs, j.clone())
		}
	}
	// Stable order is the insertion order (m.order). Keep it.
	data, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

// Shutdown cancels the active job (if any) without changing state on disk
// beyond what the runner persists. Useful for tests.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	if m.activeID != "" {
		if j := m.jobs[m.activeID]; j != nil && j.cancel != nil {
			j.cancel()
		}
	}
	m.mu.Unlock()
}

// newID returns a short random hex id.
func newID() (string, error) {
	buf := make([]byte, 10)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func removeString(s []string, v string) []string {
	for i, x := range s {
		if x == v {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}
