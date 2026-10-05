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

// maxSessionMessages bounds the persisted transcript. A session left working
// unattended for hours can accumulate a lot of turns, and every token rewrites
// the file, so the oldest messages are dropped past this point. The first user
// message is always kept as the anchor of the conversation.
const maxSessionMessages = 400

// maxSessionMessageRunes drops a single message that grew past this size, which
// in practice means a runaway reply that would otherwise dominate the file.
const maxSessionMessageRunes = 200000

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
	// Workflow, Prompt and Seed label a ComfyUI run so a restored session reads
	// like the live one, and Media holds the stored files. Only Image is kept in
	//lined: the media list stores paths, which is what makes the transcript small
	// enough to rewrite on every token.
	Workflow string `json:"workflow,omitempty"`
	Prompt   string `json:"prompt,omitempty"`
	Seed     int64  `json:"seed,omitempty"`
	// RunStatus is the live progress line ComfyUI reports while a render runs,
	// e.g. "queued as #3". Empty once the run finishes.
	RunStatus string      `json:"run_status,omitempty"`
	Media     []ChatMedia `json:"media,omitempty"`
	// PromptID is the ComfyUI run id, which is what the UI needs to point the
	// user at the job in ComfyUI's own history.
	PromptID string `json:"prompt_id,omitempty"`
}

// ChatAttach is a file pasted into a chat message. Data holds a data URL for
// images and audio, Text the extracted body for documents.
//
// Data is deliberately kept out of the persisted form. It only holds bytes while
// the attachment is in flight or being served to a browser; a session stores the
// Blob reference instead and chat_session_blobs.go keeps the bytes in their own
// file, so pasting a photo does not bloat a file that is rewritten on every
// token. Size is the original byte count, kept for the UI.
type ChatAttach struct {
	Kind     string `json:"kind"`
	Name     string `json:"name,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	Blob     string `json:"blob,omitempty"`
	Size     int    `json:"size,omitempty"`
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
	// ThinkMs is how long the model spent thinking. It is kept so a restored
	// transcript can show the same "thinking 1.2s" the live turn did, instead
	// of an empty "(0ms)" next to a full block of reasoning.
	ThinkMs          int64     `json:"think_ms,omitempty"`
	PromptTokens     int       `json:"prompt_tokens,omitempty"`
	CompletionTokens int       `json:"completion_tokens,omitempty"`
	EvalNs           int64     `json:"eval_duration_ns,omitempty"`
	DoneReason       string    `json:"done_reason,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	Pending          bool      `json:"pending,omitempty"`
	Error            string    `json:"error,omitempty"`
	// StreamStartedAt is the client clock the browser uses to draw the elapsed
	// timer; it is only a hint and the server recomputes on restore.
	StreamStartedAt int64 `json:"stream_started_at,omitempty"`
	// QueueID identifies a queued user turn so the browser can cancel or promote
	// exactly that one. It stays on the message once it reaches the transcript.
	QueueID string `json:"queue_id,omitempty"`
	// Deferred marks a queued turn that was sent while the session was busy. Only
	// those announce themselves when they start; a direct message is already on
	// screen and must not be appended twice.
	Deferred bool `json:"deferred,omitempty"`
	// Truncated marks a message whose body was cut for size, so the UI says the
	// reply was trimmed instead of silently showing one that ends mid-sentence.
	Truncated bool `json:"truncated,omitempty"`
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
	// Comfy turns on the ComfyUI tool for this session and ComfyWorkflow names
	// the workflow a bare call uses. Stored with the rest of the snapshot so a
	// restored session regenerates images with the same settings.
	Comfy         bool   `json:"comfy"`
	ComfyWorkflow string `json:"comfy_workflow,omitempty"`
	ImageWidth    int    `json:"image_width,omitempty"`
	ImageHeight   int    `json:"image_height,omitempty"`
	ImageSteps    int    `json:"image_steps,omitempty"`
	ImageSeed     int    `json:"image_seed,omitempty"`
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
	ID    string `json:"id"`
	Title string `json:"title"`
	// CustomTitle marks a title the user typed. Only a custom title is shown in
	// the session list; an auto-derived one would put the first prompt back on
	// screen, which is exactly what the list must not do.
	CustomTitle bool             `json:"custom_title,omitempty"`
	Model       string           `json:"model"`
	Status      string           `json:"status"`
	Unseen      bool             `json:"unseen,omitempty"`
	Cancelled   bool             `json:"cancelled,omitempty"`
	Error       string           `json:"error,omitempty"`
	Settings    SessionSettings  `json:"settings"`
	Messages    []SessionMessage `json:"messages"`
	// Queue holds user turns sent while the session was busy. They are part of
	// the session (persisted, survive a reload) and become the transcript one by
	// one as their turn starts, so the model never sees a future message.
	Queue      []SessionMessage `json:"queue,omitempty"`
	Events     []SessionEvent   `json:"events,omitempty"`
	Seq        int              `json:"seq"`
	QueuedAt   time.Time        `json:"queued_at,omitempty"`
	CreatedAt  time.Time        `json:"created_at"`
	UpdatedAt  time.Time        `json:"updated_at"`
	LastActive time.Time        `json:"last_active_at"`
	// DroppedMessages counts transcript entries removed by the size cap, so the
	// UI can be honest about a session that no longer shows its whole history.
	DroppedMessages int `json:"dropped_messages,omitempty"`
}

// SessionSummary is the lightweight row used by the session list and by the
// badges shown next to each model.
type SessionSummary struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	CustomTitle bool   `json:"custom_title,omitempty"`
	Model       string `json:"model"`
	Status      string `json:"status"`
	Unseen      bool   `json:"unseen,omitempty"`
	Error       string `json:"error,omitempty"`
	Messages    int    `json:"messages"`
	// Queued is how many user turns are waiting for the running turn to finish.
	Queued     int       `json:"queued,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	LastActive time.Time `json:"last_active_at"`
	// DroppedMessages is the number of trimmed transcript entries, so a row can
	// say "older messages were trimmed".
	DroppedMessages int `json:"dropped_messages,omitempty"`
	// ModelMissing is set by the UI, not the server: a summary cannot know which
	// models are still installed. The row uses it to flag a session that can no
	// longer run.
	ModelMissing bool `json:"model_missing,omitempty"`
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
	// pending holds the turns of a session that is already busy, in order. A
	// queued message owns one entry; when the running turn releases the slot the
	// next entry starts, which is what drains the session's persisted Queue.
	pending map[string][]func()
	// paused is set by an explicit Stop so the finished turn does not start the
	// next queued one. The queue is kept and Resume clears the flag.
	paused map[string]bool
	// defaultRun rebuilds the run for a queued turn that has no parked closure,
	// which is what happens to a queue restored from disk after a restart.
	defaultRun func(id string)
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
		pending:  map[string][]func(){},
		paused:   map[string]bool{},
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
		// Titles used to be derived from the first prompt. That put the prompt
		// back on screen in the session list, so only a user-typed title is kept.
		if !sess.CustomTitle {
			sess.Title = ""
		}
		// A queued message is a turn that never started. The restart sweep below
		// only closes the running assistant turn; the queue is kept as-is so the
		// next message or a resume can still run it.
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
		ID:          sess.ID,
		Title:       sess.Title,
		CustomTitle: sess.CustomTitle,
		Model:       sess.Model,
		Status:      sess.Status,
		Unseen:      sess.Unseen,
		Error:       sess.Error,
		Messages:    len(sess.Messages),
		Queued:      len(sess.Queue),
		CreatedAt:   sess.CreatedAt,
		UpdatedAt:   sess.UpdatedAt,
		LastActive:  sess.LastActive,

		DroppedMessages: sess.DroppedMessages,
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

// LiveIDs returns the sanitized ids of every session still on disk. It exists
// for the startup sweep that drops generated media whose transcript is gone.
func (st *chatSessionStore) LiveIDs() map[string]bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make(map[string]bool, len(st.sessions))
	for id := range st.sessions {
		out[sanitizeMediaSegment(id)] = true
	}
	// Quick-chat media has no session of its own and must survive the sweep.
	out[comfyQuickMediaDir] = true
	return out
}

// Delete cancels any in-flight run, forgets the session and removes its file.
func (st *chatSessionStore) Delete(id string) bool {
	st.mu.Lock()
	cancel := st.running[id]
	delete(st.running, id)
	delete(st.pending, id)
	delete(st.paused, id)
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
	// The attachment bytes live outside the session file, so they have to go with
	// it. Otherwise deleting a chat would leave its images on disk forever.
	st.dropSessionBlobs(id)
	// Same for anything ComfyUI generated for this session.
	removeComfyMediaForSession(id)
	if sess != nil {
		st.broadcast(ChatSessionEvent{Kind: chatSessionRemove, ID: id})
	}
	return sess != nil || removed
}

// DeleteByModel removes every session that runs on the given model and returns
// how many went away. It backs the "uninstall a model" cleanup, which is the
// point where keeping transcripts around is most confusing: they can never run
// again until the exact same model is pulled back.
func (st *chatSessionStore) DeleteByModel(model string) int {
	st.mu.Lock()
	var ids []string
	for _, sess := range st.sessions {
		if sess.Model == model {
			ids = append(ids, sess.ID)
		}
	}
	st.mu.Unlock()
	for _, id := range ids {
		st.Delete(id)
	}
	if len(ids) > 0 {
		log.Printf("[chat-sessions] removed %d session(s) for uninstalled model %q", len(ids), model)
	}
	return len(ids)
}

// DeleteAll wipes every session and returns how many were removed. "Clear all"
// in the UI goes through here.
func (st *chatSessionStore) DeleteAll() int {
	st.mu.Lock()
	ids := make([]string, 0, len(st.sessions))
	for id := range st.sessions {
		ids = append(ids, id)
	}
	st.mu.Unlock()
	n := 0
	for _, id := range ids {
		if st.Delete(id) {
			n++
		}
	}
	// Stray files from an interrupted delete, or from a hand-edited data dir.
	// They are counted apart: a stray is not a session, and the caller reports
	// this number to the user as "N sessions deleted".
	strays := 0
	if entries, err := os.ReadDir(st.dir); err == nil {
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".json") || !strings.HasPrefix(name, "cs-") {
				continue
			}
			path := filepath.Join(st.dir, name)
			if err := os.Remove(path); err == nil {
				strays++
			}
		}
	}
	if n > 0 || strays > 0 {
		log.Printf("[chat-sessions] cleared %d session(s) and %d stray file(s)", n, strays)
	}
	// Whatever Delete could not attribute to a session id still has to go.
	st.clearAllAttachBlobs()
	return n
}

// HasBusy reports whether any session is running or waiting for a slot. A queue
// paused by an explicit Stop does not count: nothing is working on it.
func (st *chatSessionStore) HasBusy() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.running) > 0 || len(st.queue) > 0 {
		return true
	}
	for id, p := range st.pending {
		if len(p) > 0 && !st.paused[id] {
			return true
		}
	}
	return false
}

// CancelAll stops every in-flight turn and settles the sessions as cancelled.
// It runs before the feature is switched off, so nothing is left running
// without a page listening to it.
func (st *chatSessionStore) CancelAll() int {
	st.mu.Lock()
	ids := make([]string, 0, len(st.running)+len(st.queue))
	for id := range st.running {
		ids = append(ids, id)
	}
	for _, q := range st.queue {
		ids = append(ids, q.id)
	}
	st.mu.Unlock()
	n := 0
	for _, id := range ids {
		if st.Cancel(id) {
			n++
		}
	}
	return n
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

// Cancel stops the turn in flight for a session, without deleting it and without
// throwing away what is queued behind it. The queue is paused, so the finished
// turn does not start the next one; Resume is what lets it continue.
func (st *chatSessionStore) Cancel(id string) bool {
	st.mu.Lock()
	cancel, running := st.running[id]
	st.paused[id] = true
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
		// Nothing to cancel directly: no turn will settle the session for us.
		st.mu.Lock()
		if msg := pendingMessageLocked(sess); msg != nil {
			msg.Pending = false
		}
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

// IsBusy reports whether a session already has a turn in flight, parked behind
// one, or waiting for a global slot.
func (st *chatSessionStore) IsBusy(id string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if _, ok := st.running[id]; ok {
		return true
	}
	if len(st.pending[id]) > 0 {
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

// Start begins a detached turn, either right away or once a slot frees up. The
// turn always runs in its own goroutine so the HTTP handler that queued it
// returns immediately and the page can be closed without stopping the model.
//
// When the session is already busy the run is parked in order instead of being
// dropped: that is what lets a message sent mid-turn wait its turn and then run
// on its own, without the browser holding it.
func (st *chatSessionStore) Start(id string, run func()) {
	st.mu.Lock()
	if _, busy := st.running[id]; busy || len(st.pending[id]) > 0 {
		st.pending[id] = append(st.pending[id], run)
		st.mu.Unlock()
		return
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

// Resume starts the next parked turn after an explicit Stop paused the queue.
func (st *chatSessionStore) Resume(id string) {
	st.mu.Lock()
	st.paused[id] = false
	if _, busy := st.running[id]; busy {
		st.mu.Unlock()
		return
	}
	st.startNextLocked(id)
	st.mu.Unlock()
}

// startNextLocked pops the next parked turn and starts it, respecting the global
// concurrency limit. Callers must hold st.mu and must have checked the session is
// not running and not paused. When no parked run is left (for example a queue
// restored after a restart) it rebuilds one from the default runner.
func (st *chatSessionStore) startNextLocked(id string) {
	var next func()
	if len(st.pending[id]) > 0 {
		next = st.pending[id][0]
		st.pending[id] = st.pending[id][1:]
		if len(st.pending[id]) == 0 {
			delete(st.pending, id)
		}
	} else if st.defaultRun != nil {
		if sess := st.sessions[id]; sess != nil && len(sess.Queue) > 0 {
			next = func() { st.defaultRun(id) }
		}
	}
	if next == nil {
		return
	}
	if len(st.running) >= maxConcurrentChatSessions {
		st.queue = append(st.queue, queuedTurn{id: id, start: next})
		return
	}
	st.running[id] = nil
	go next()
}

// hasQueuedWorkLocked reports whether a session has a turn ready to run, either
// parked or still in its persisted queue. Callers must hold st.mu.
func (st *chatSessionStore) hasQueuedWorkLocked(id string) bool {
	if len(st.pending[id]) > 0 {
		return true
	}
	if st.defaultRun == nil {
		return false
	}
	sess := st.sessions[id]
	return sess != nil && len(sess.Queue) > 0
}

// releaseRunning frees the session's slot and starts whatever was parked behind
// it. A Stop leaves the queue paused: the parked runs are held so Resume can run
// them without the messages being lost.
func (st *chatSessionStore) releaseRunning(id string) {
	st.mu.Lock()
	delete(st.running, id)
	if st.paused[id] {
		st.mu.Unlock()
		st.pump()
		return
	}
	if st.hasQueuedWorkLocked(id) {
		st.startNextLocked(id)
		st.mu.Unlock()
		return
	}
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
		if _, busy := st.running[next.id]; busy {
			st.mu.Unlock()
			continue
		}
		if st.paused[next.id] {
			// The session was stopped while its turn waited for a slot. Keep the
			// run parked so Resume can pick it up later.
			st.pending[next.id] = append([]func(){next.start}, st.pending[next.id]...)
			st.mu.Unlock()
			continue
		}
		st.running[next.id] = nil
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

// touchLocked refreshes the timestamps. Callers must hold st.mu. It deliberately
// does not name the session: a title taken from the first prompt is just the
// prompt shown again, and the list shows the relative time instead. Only Rename
// sets a title.
func (st *chatSessionStore) touchLocked(sess *ChatSession) {
	now := time.Now()
	sess.UpdatedAt = now
	sess.LastActive = now
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

// sessionWatcherCheck returns a function the artifact loop can call on each round
// to find out whether a browser is watching this session right now.
//
// The two browser-only tools block on an answer from the artifact preview panel,
// so offering them with nobody listening just burns their 8-10s timeout. A
// detached run therefore hides them at first and picks them up the moment the
// user opens the session, which is why this is a live check and not a flag read
// once when the body was built.
func (st *chatSessionStore) sessionWatcherCheck(id string) func() bool {
	return func() bool {
		st.mu.Lock()
		defer st.mu.Unlock()
		if st.sessions[id] == nil {
			return false
		}
		return st.watchers[id] > 0
	}
}
