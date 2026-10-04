package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Chat session statuses. A session is "running" while the model works, "queued"
// while it waits for a free slot, and "idle" once the turn finished.
const (
	chatSessionRunning   = "running"
	chatSessionQueued    = "queued"
	chatSessionIdle      = "idle"
	chatSessionError     = "error"
	chatSessionCancelled = "cancelled"
)

// maxConcurrentChatSessions bounds how many detached turns may hit Ollama at
// once. Anything above that waits in a FIFO queue so a few background sessions
// cannot exhaust VRAM on a local machine.
const maxConcurrentChatSessions = 2

// maxSessionEvents bounds the replay log kept per session. It only exists so a
// browser can tail a live run; older entries are dropped as new ones arrive.
const maxSessionEvents = 4000

// chatSessionSaveDebounce is how long a burst of stream events may rewrite a
// session file before we actually hit the disk.
const chatSessionSaveDebounce = 1500 * time.Millisecond

// SessionToolEntry mirrors one entry of the browser's toolLog, so a restored
// session shows the same tool timeline a live chat shows.
type SessionToolEntry struct {
	Name          string `json:"name"`
	Status        string `json:"status,omitempty"`
	Phase         string `json:"phase,omitempty"`
	Query         string `json:"query,omitempty"`
	URL           string `json:"url,omitempty"`
	MaxResults    int    `json:"max_results,omitempty"`
	Path          string `json:"path,omitempty"`
	Command       string `json:"command,omitempty"`
	Code          string `json:"code,omitempty"`
	ArtifactName  string `json:"artifact_name,omitempty"`
	Description   string `json:"description,omitempty"`
	Error         string `json:"error,omitempty"`
	ResultPreview string `json:"result_preview,omitempty"`
	ResultRunes   int    `json:"result_runes,omitempty"`
	Image         string `json:"image,omitempty"`
}

// ChatAttach is a file pasted into a chat message. Data holds a data URL for
// images and audio, Text the extracted body for documents.
type ChatAttach struct {
	Kind     string `json:"kind"`
	Name     string `json:"name,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
}

// SessionMessage is one persisted chat turn. The assistant turn that is still
// streaming is stored with Pending true and rewritten on every event, so a page
// opened mid-run renders everything produced so far. Think/Raw hold the raw
// stream text: the browser runs its own splitThink on it when restoring, which
// keeps this format identical to a live message.
type SessionMessage struct {
	Role               string             `json:"role"`
	Content            string             `json:"content,omitempty"`
	Think              string             `json:"think,omitempty"`
	Raw                string             `json:"raw,omitempty"`
	Model              string             `json:"model,omitempty"`
	ToolLog            []SessionToolEntry `json:"tool_log,omitempty"`
	Attach             []ChatAttach       `json:"attachments,omitempty"`
	ArtifactTS         string             `json:"artifact_ts,omitempty"`
	ArtifactNm         string             `json:"artifact_name,omitempty"`
	ArtifactURL        string             `json:"artifact_url,omitempty"`
	ArtifactDesc       string             `json:"artifact_desc,omitempty"`
	ArtifactGenerating bool               `json:"artifact_generating,omitempty"`
	ElapsedMs          int64              `json:"elapsed_ms,omitempty"`
	PromptTokens       int                `json:"prompt_tokens,omitempty"`
	CompletionTokens   int                `json:"completion_tokens,omitempty"`
	EvalNs             int64              `json:"eval_duration_ns,omitempty"`
	DoneReason         string             `json:"done_reason,omitempty"`
	CreatedAt          time.Time          `json:"created_at"`
	Pending            bool               `json:"pending,omitempty"`
	Error              string             `json:"error,omitempty"`
	// StreamStartedAt is the client clock the browser uses to draw the elapsed
	// timer; it is only a hint and the server recomputes on restore.
	StreamStartedAt int64 `json:"stream_started_at,omitempty"`
}

// SessionSettings is the snapshot of the chat options panel taken at the moment
// the last message was sent. Restoring a session puts every control back where
// the user left it, so a continued session behaves exactly like the original.
type SessionSettings struct {
	System      string  `json:"system,omitempty"`
	Temperature float64 `json:"temperature"`
	TopK        int     `json:"top_k"`
	TopP        float64 `json:"top_p"`
	NumCtxPct   int     `json:"num_ctx"`
	ThinkLevel  string  `json:"think_level,omitempty"`
	WebTools    bool    `json:"web_tools"`
	Artifacts   bool    `json:"artifacts"`
	ImageWidth  int     `json:"image_width,omitempty"`
	ImageHeight int     `json:"image_height,omitempty"`
	ImageSteps  int     `json:"image_steps,omitempty"`
	ImageSeed   int     `json:"image_seed,omitempty"`
}

// SessionEvent is one entry of the per-session replay log. Seq is monotonic per
// session so a browser can resume with ?from=<seq>.
type SessionEvent struct {
	Seq   int    `json:"seq"`
	Event string `json:"event"`
	Data  any    `json:"data,omitempty"`
}

// ChatSession is a persistent chat that keeps working after the browser closes.
type ChatSession struct {
	ID         string           `json:"id"`
	Title      string           `json:"title"`
	Model      string           `json:"model"`
	Status     string           `json:"status"`
	Unseen     bool             `json:"unseen,omitempty"`
	Cancelled  bool             `json:"cancelled,omitempty"`
	Error      string           `json:"error,omitempty"`
	Settings   SessionSettings  `json:"settings"`
	Messages   []SessionMessage `json:"messages"`
	Events     []SessionEvent   `json:"events,omitempty"`
	Seq        int              `json:"seq"`
	QueuedAt   time.Time        `json:"queued_at,omitempty"`
	CreatedAt  time.Time        `json:"created_at"`
	UpdatedAt  time.Time        `json:"updated_at"`
	LastActive time.Time        `json:"last_active_at"`
}

// SessionSummary is the lightweight row used by the session list and by the
// badges shown next to each model.
type SessionSummary struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Model      string    `json:"model"`
	Status     string    `json:"status"`
	Unseen     bool      `json:"unseen,omitempty"`
	Error      string    `json:"error,omitempty"`
	Messages   int       `json:"messages"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	LastActive time.Time `json:"last_active_at"`
}

const (
	chatSessionUpdate = "update"
	chatSessionRemove = "remove"
)

// ChatSessionEvent is broadcast to every connected browser: session state
// changes always, live stream events only for subscribers of that session.
type ChatSessionEvent struct {
	Kind    string          `json:"kind"`
	Session *SessionSummary `json:"session,omitempty"`
	ID      string          `json:"id,omitempty"`
	Seq     int             `json:"seq,omitempty"`
	Event   string          `json:"event,omitempty"`
	Data    any             `json:"data,omitempty"`
}

type chatSessionStore struct {
	mu       sync.Mutex
	dir      string
	sessions map[string]*ChatSession
	order    []string

	running map[string]context.CancelFunc
	queue   []queuedTurn
	// watchers counts the browsers currently tailing each session's stream.
	// A turn that ends while nobody watches is what turns the badge white.
	watchers map[string]int

	subs   map[chan ChatSessionEvent]struct{}
	closed bool

	// saves holds the pending debounced write of each session file.
	saves map[string]*time.Timer
}

type queuedTurn struct {
	id    string
	start func()
}

var chatSessionSeq atomic.Uint64

func newChatSessionStore(dir string) *chatSessionStore {
	return &chatSessionStore{
		dir:      dir,
		sessions: map[string]*ChatSession{},
		running:  map[string]context.CancelFunc{},
		watchers: map[string]int{},
		subs:     map[chan ChatSessionEvent]struct{}{},
		saves:    map[string]*time.Timer{},
	}
}

func (st *chatSessionStore) pathFor(id string) string {
	return filepath.Join(st.dir, id+".json")
}

// ensureDir creates the session directory on first write.
func (st *chatSessionStore) ensureDir() {
	if err := os.MkdirAll(st.dir, 0o700); err != nil {
		log.Printf("[chat-sessions] mkdir %s: %v", st.dir, err)
	}
}

// Load reads every session file. A run that was in flight when the process died
// cannot be resumed, so it is rewritten as an error the user can see.
func (st *chatSessionStore) Load() {
	st.mu.Lock()
	defer st.mu.Unlock()
	entries, err := os.ReadDir(st.dir)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[chat-sessions] read dir: %v", err)
		}
		return
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		path := filepath.Join(st.dir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			log.Printf("[chat-sessions] read %s: %v", name, err)
			continue
		}
		var sess ChatSession
		if err := json.Unmarshal(raw, &sess); err != nil {
			log.Printf("[chat-sessions] corrupt %s, quarantining: %v", name, err)
			quarantineCorrupt(path)
			continue
		}
		if sess.ID == "" {
			sess.ID = strings.TrimSuffix(name, ".json")
		}
		if sess.Status == chatSessionRunning || sess.Status == chatSessionQueued {
			for i := range sess.Messages {
				if sess.Messages[i].Pending {
					sess.Messages[i].Pending = false
				}
			}
			sess.Status = chatSessionError
			sess.Error = "interrupted by restart"
		}
		if _, dup := st.sessions[sess.ID]; !dup {
			st.order = append(st.order, sess.ID)
		}
		st.sessions[sess.ID] = &sess
	}
	log.Printf("[chat-sessions] loaded %d session(s) from %s", len(st.order), st.dir)
}

// Create registers a brand new session and persists it immediately.
func (st *chatSessionStore) Create(model string, settings SessionSettings) *ChatSession {
	now := time.Now()
	sess := &ChatSession{
		ID:         newChatSessionID(),
		Title:      "",
		Model:      model,
		Status:     chatSessionIdle,
		Settings:   settings,
		Messages:   []SessionMessage{},
		CreatedAt:  now,
		UpdatedAt:  now,
		LastActive: now,
	}
	st.mu.Lock()
	st.sessions[sess.ID] = sess
	st.order = append(st.order, sess.ID)
	st.touchLocked(sess)
	st.saveLocked(sess)
	sum := summaryOf(sess)
	st.mu.Unlock()
	st.broadcast(ChatSessionEvent{Kind: chatSessionUpdate, Session: &sum})
	return sess
}

func newChatSessionID() string {
	return fmt.Sprintf("cs-%d-%d", time.Now().UnixMilli(), chatSessionSeq.Add(1))
}

// List returns every session, most recently active first.
func (st *chatSessionStore) List() []SessionSummary {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]SessionSummary, 0, len(st.order))
	for _, id := range st.order {
		if sess := st.sessions[id]; sess != nil {
			out = append(out, summaryOf(sess))
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastActive.After(out[j].LastActive) })
	return out
}

func summaryOf(sess *ChatSession) SessionSummary {
	return SessionSummary{
		ID:         sess.ID,
		Title:      sess.Title,
		Model:      sess.Model,
		Status:     sess.Status,
		Unseen:     sess.Unseen,
		Error:      sess.Error,
		Messages:   len(sess.Messages),
		CreatedAt:  sess.CreatedAt,
		UpdatedAt:  sess.UpdatedAt,
		LastActive: sess.LastActive,
	}
}

func (st *chatSessionStore) Get(id string) *ChatSession {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.sessions[id]
}

func (st *chatSessionStore) Summary(id string) *SessionSummary {
	st.mu.Lock()
	defer st.mu.Unlock()
	sess := st.sessions[id]
	if sess == nil {
		return nil
	}
	sum := summaryOf(sess)
	return &sum
}

// Delete cancels any in-flight run, forgets the session and removes its file.
func (st *chatSessionStore) Delete(id string) bool {
	st.mu.Lock()
	cancel := st.running[id]
	delete(st.running, id)
	sess := st.sessions[id]
	delete(st.sessions, id)
	st.dropFromQueueLocked(id)
	if t := st.saves[id]; t != nil {
		t.Stop()
		delete(st.saves, id)
	}
	st.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	removed := false
	if err := os.Remove(st.pathFor(id)); err == nil {
		removed = true
	} else if !os.IsNotExist(err) {
		log.Printf("[chat-sessions] remove %s: %v", id, err)
	}
	if sess != nil {
		st.broadcast(ChatSessionEvent{Kind: chatSessionRemove, ID: id})
	}
	return sess != nil || removed
}

func (st *chatSessionStore) dropFromQueueLocked(id string) {
	out := st.queue[:0]
	for _, q := range st.queue {
		if q.id != id {
			out = append(out, q)
		}
	}
	st.queue = out
}

// MarkSeen clears the "it finished while you were away" flag.
func (st *chatSessionStore) MarkSeen(id string) bool {
	st.mu.Lock()
	sess := st.sessions[id]
	if sess == nil || !sess.Unseen {
		st.mu.Unlock()
		return false
	}
	sess.Unseen = false
	sess.UpdatedAt = time.Now()
	st.saveLocked(sess)
	sum := summaryOf(sess)
	st.mu.Unlock()
	st.broadcast(ChatSessionEvent{Kind: chatSessionUpdate, Session: &sum})
	return true
}

// Cancel stops the turn in flight for a session, without deleting it.
func (st *chatSessionStore) Cancel(id string) bool {
	st.mu.Lock()
	cancel, running := st.running[id]
	wasQueued := false
	for _, q := range st.queue {
		if q.id == id {
			wasQueued = true
			break
		}
	}
	st.dropFromQueueLocked(id)
	sess := st.sessions[id]
	if (running || wasQueued) && sess != nil {
		// Mark it even when the cancel func is not bound yet, so the turn
		// settles as cancelled instead of idle when it finally starts.
		sess.Cancelled = true
	}
	st.mu.Unlock()
	if cancel != nil {
		cancel()
		return true
	}
	if wasQueued && sess != nil {
		// Nothing was running, so no turn will settle the session for us.
		st.mu.Lock()
		sess.Status = chatSessionCancelled
		sess.Error = ""
		st.touchLocked(sess)
		st.flushLocked(sess)
		sum := summaryOf(sess)
		st.mu.Unlock()
		st.broadcast(ChatSessionEvent{Kind: chatSessionUpdate, Session: &sum})
		return true
	}
	return running
}

// IsBusy reports whether a session already has a turn in flight or queued.
func (st *chatSessionStore) IsBusy(id string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if _, ok := st.running[id]; ok {
		return true
	}
	for _, q := range st.queue {
		if q.id == id {
			return true
		}
	}
	return false
}

// Subscribe registers a listener for session state changes and live stream
// events. Callers filter by session id when they care about one transcript.
func (st *chatSessionStore) Subscribe() (<-chan ChatSessionEvent, func()) {
	ch := make(chan ChatSessionEvent, 512)
	st.mu.Lock()
	if st.closed {
		st.mu.Unlock()
		close(ch)
		return ch, func() {}
	}
	st.subs[ch] = struct{}{}
	st.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			st.mu.Lock()
			if _, ok := st.subs[ch]; ok {
				delete(st.subs, ch)
				close(ch)
			}
			st.mu.Unlock()
		})
	}
}

// AddWatcher marks a browser as tailing a session's live stream.
func (st *chatSessionStore) AddWatcher(id string) {
	st.mu.Lock()
	st.watchers[id]++
	st.mu.Unlock()
}

// RemoveWatcher drops one browser from a session's live stream audience.
func (st *chatSessionStore) RemoveWatcher(id string) {
	st.mu.Lock()
	if n := st.watchers[id] - 1; n > 0 {
		st.watchers[id] = n
	} else {
		delete(st.watchers, id)
	}
	st.mu.Unlock()
}

func (st *chatSessionStore) broadcast(ev ChatSessionEvent) {
	st.mu.Lock()
	for ch := range st.subs {
		select {
		case ch <- ev:
		default:
			// A browser that cannot keep up resyncs on the next snapshot.
		}
	}
	st.mu.Unlock()
}

// Start begins a detached turn, either right away or once a slot frees up.
// The turn always runs in its own goroutine so the HTTP handler that queued it
// returns immediately and the page can be closed without stopping the model.
func (st *chatSessionStore) Start(id string, run func()) {
	st.mu.Lock()
	// One turn per session. A second request while this session is already
	// working (or waiting for a slot) would interleave two transcripts and
	// overwrite the reserved slot, so it is simply ignored.
	if _, busy := st.running[id]; busy {
		st.mu.Unlock()
		return
	}
	for _, q := range st.queue {
		if q.id == id {
			st.mu.Unlock()
			return
		}
	}
	if len(st.running) >= maxConcurrentChatSessions {
		st.queue = append(st.queue, queuedTurn{id: id, start: run})
		if sess := st.sessions[id]; sess != nil && sess.Status != chatSessionRunning {
			sess.Status = chatSessionQueued
			sess.QueuedAt = time.Now()
			sess.UpdatedAt = sess.QueuedAt
			st.saveLocked(sess)
		}
		sum := summaryOf(st.sessions[id])
		st.mu.Unlock()
		if sum.ID != "" {
			st.broadcast(ChatSessionEvent{Kind: chatSessionUpdate, Session: &sum})
		}
		return
	}
	// Reserve the slot before the goroutine starts, otherwise two back-to-back
	// Start calls would both see a free slot and blow past the limit. The
	// placeholder is replaced by the real cancel func in bindRunning.
	st.running[id] = nil
	st.mu.Unlock()
	go run()
}

// bindRunning records the cancel func of a turn that just claimed a slot.
func (st *chatSessionStore) bindRunning(id string, cancel context.CancelFunc) {
	st.mu.Lock()
	st.running[id] = cancel
	st.mu.Unlock()
}

func (st *chatSessionStore) releaseRunning(id string) {
	st.mu.Lock()
	delete(st.running, id)
	st.mu.Unlock()
	st.pump()
}

func (st *chatSessionStore) pump() {
	for {
		st.mu.Lock()
		if len(st.running) >= maxConcurrentChatSessions || len(st.queue) == 0 {
			st.mu.Unlock()
			return
		}
		next := st.queue[0]
		st.queue = st.queue[1:]
		st.mu.Unlock()
		next.start()
	}
}

// Shutdown cancels every run, drops pending writes and closes all subscribers.
func (st *chatSessionStore) Shutdown() {
	st.mu.Lock()
	st.closed = true
	cancels := make([]context.CancelFunc, 0, len(st.running))
	for _, cancel := range st.running {
		cancels = append(cancels, cancel)
	}
	pending := make([]*ChatSession, 0, len(st.saves))
	for id, t := range st.saves {
		t.Stop()
		if sess := st.sessions[id]; sess != nil {
			pending = append(pending, sess)
		}
	}
	st.saves = map[string]*time.Timer{}
	for ch := range st.subs {
		close(ch)
	}
	st.subs = map[chan ChatSessionEvent]struct{}{}
	st.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
	for _, sess := range pending {
		snap := *sess
		if err := writeJSONFileAtomic(st.pathFor(snap.ID), &snap, 0o600); err != nil {
			log.Printf("[chat-sessions] final save %s: %v", snap.ID, err)
		}
	}
}

// --- persistence ---------------------------------------------------------

// touchLocked refreshes the timestamps and derives a title from the first user
// message. Callers must hold st.mu.
func (st *chatSessionStore) touchLocked(sess *ChatSession) {
	now := time.Now()
	sess.UpdatedAt = now
	sess.LastActive = now
	if sess.Title == "" {
		for _, m := range sess.Messages {
			if m.Role == "user" && strings.TrimSpace(m.Content) != "" {
				sess.Title = chatSessionTitle(strings.TrimSpace(m.Content))
				break
			}
		}
	}
}

func chatSessionTitle(s string) string {
	r := []rune(s)
	if len(r) <= 60 {
		return s
	}
	return strings.TrimSpace(string(r[:60])) + "…"
}

// saveLocked schedules a debounced write of the session file. Callers must hold
// st.mu.
func (st *chatSessionStore) saveLocked(sess *ChatSession) {
	if sess == nil {
		return
	}
	id := sess.ID
	if t := st.saves[id]; t != nil {
		t.Stop()
	}
	st.saves[id] = time.AfterFunc(chatSessionSaveDebounce, func() { st.flush(id) })
}

// flushLocked writes a session file right away. Callers must hold st.mu.
func (st *chatSessionStore) flushLocked(sess *ChatSession) {
	if sess == nil {
		return
	}
	id := sess.ID
	if t := st.saves[id]; t != nil {
		t.Stop()
		delete(st.saves, id)
	}
	st.ensureDir()
	snap := *sess
	if err := writeJSONFileAtomic(st.pathFor(id), &snap, 0o600); err != nil {
		log.Printf("[chat-sessions] save %s: %v", id, err)
	}
}

func (st *chatSessionStore) flush(id string) {
	st.mu.Lock()
	sess := st.sessions[id]
	if sess == nil {
		delete(st.saves, id)
		st.mu.Unlock()
		return
	}
	st.flushLocked(sess)
	st.mu.Unlock()
}
