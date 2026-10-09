package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strings"
	"time"

	"github.com/gense/ollama-manager/internal/ollama"
)

// sessionAttachedTextHeader is the header the browser puts above pasted
// documents before re-sending them to the model.
const sessionAttachedTextHeader = "Attached text files"

// These bound how much of a restored conversation is re-sent as images. Replaying
// every generated image is what makes "make it bluer" mean anything, but a long
// iteration session would otherwise push dozens of pictures into the context and
// blow both the window and the latency. The most recent runs are the ones the
// user is actually talking about.
const sessionComfyReplayRuns = 3
const sessionComfyReplayImages = 4

// chatSessionEventStream marks a broadcast that carries a live stream event
// rather than a session state change.
const chatSessionEventStream = "stream"

// buildSessionMessages turns a stored transcript back into the message list the
// model expects. It mirrors the browser's buildOutboundMessages(): the in-flight
// assistant turn is skipped, and an assistant turn that only produced tools or
// an artifact becomes the same short placeholder the live chat uses.
//
// Attachment bytes are not kept on the stored messages, so the store hydrates
// them in first. It has to happen once per turn rather than per token: the model
// only ever sees the transcript at the moment the request is built.
func (st *chatSessionStore) buildSessionMessages(sess *ChatSession, caps sessionModelInfo) []ollama.ChatMessage {
	msgs := st.hydrateMessages(sess.Messages)
	out := make([]ollama.ChatMessage, 0, len(msgs)+1)
	if !caps.IsImage {
		if sys := strings.TrimSpace(sess.Settings.System); sys != "" {
			out = append(out, ollama.ChatMessage{Role: "system", Content: sys})
		}
	}
	// Generated images ride along with the assistant turn that announced them,
	// which is the only way a restored session keeps the ability to react to
	// them. Without this the model reads "here is a cat, make it blue" and has no
	// idea what the cat looked like.
	pendingComfy := sessionReplayComfyImages(msgs, caps.HasVision)
	comfyCursor := 0

	for _, m := range msgs {
		switch m.Role {
		case "assistant":
			if m.Pending {
				continue
			}
			text := strings.TrimSpace(m.Content)
			if text == "" {
				if m.ArtifactURL != "" || m.ArtifactNm != "" || len(m.ToolLog) > 0 {
					name := m.ArtifactNm
					if name == "" {
						name = "project"
					}
					out = append(out, ollama.ChatMessage{
						Role:    "assistant",
						Content: fmt.Sprintf("I have updated the artifact %s.", name),
					})
				}
				continue
			}
			msg := ollama.ChatMessage{Role: "assistant", Content: text}
			if comfyCursor < len(pendingComfy) {
				msg.Images = pendingComfy[comfyCursor]
				comfyCursor++
			}
			out = append(out, msg)
		case "user":
			msg := ollama.ChatMessage{Role: "user", Content: m.Content}
			if extra := sessionTextBlocks(m.Attach); extra != "" {
				if msg.Content != "" {
					msg.Content += "\n\n" + extra
				} else {
					msg.Content = extra
				}
			}
			for _, a := range m.Attach {
				if (a.Kind == "image" || a.Kind == "audio") && a.Data != "" {
					msg.Images = append(msg.Images, a.Data)
				}
			}
			// A new user turn resets the visual context: what the model is being
			// asked to correct is the thing just described, not the oldest one.
			if len(msg.Images) > 0 {
				comfyCursor = len(pendingComfy)
			}
			out = append(out, msg)
		}
	}
	return out
}

// comfyImageBatch is the set of images one ComfyUI run produced, ready to be
// re-encoded from disk when the transcript is replayed.
type comfyImageBatch []string

// sessionReplayComfyImages loads the base64 for the most recent ComfyUI runs in
// the transcript. Only still images qualify: video and audio were never shown to
// the model in the first place, so re-attaching them would be a lie.
func sessionReplayComfyImages(msgs []SessionMessage, hasVision bool) []comfyImageBatch {
	if !hasVision {
		return nil
	}
	var batches []comfyImageBatch
	// Walk backwards and stop as soon as the budget is spent, so an old session
	// does not read every file on disk.
	for i := len(msgs) - 1; i >= 0 && len(batches) < sessionComfyReplayRuns; i-- {
		for _, e := range msgs[i].ToolLog {
			if e.Name != comfyToolName {
				continue
			}
			var batch comfyImageBatch
			for _, md := range e.Media {
				if md.Kind != comfyMediaImage {
					continue
				}
				b64, _, _, ok := comfyModelImageFromMedia(md, comfyModelImageMaxDim)
				if !ok {
					continue
				}
				batch = append(batch, b64)
				if len(batch) >= sessionComfyReplayImages {
					break
				}
			}
			if len(batch) > 0 {
				batches = append(batches, batch)
				if len(batches) >= sessionComfyReplayRuns {
					break
				}
			}
		}
	}
	// Collected newest-first; the forward walk needs oldest-first.
	for i, j := 0, len(batches)-1; i < j; i, j = i+1, j-1 {
		batches[i], batches[j] = batches[j], batches[i]
	}
	return batches
}

func sessionTextBlocks(attach []ChatAttach) string {
	var blocks []string
	for _, a := range attach {
		if a.Kind != "text" || strings.TrimSpace(a.Text) == "" {
			continue
		}
		name := a.Name
		if name == "" {
			name = "text"
		}
		blocks = append(blocks, fmt.Sprintf("--- %s ---\n%s", name, strings.TrimSpace(a.Text)))
	}
	if len(blocks) == 0 {
		return ""
	}
	return sessionAttachedTextHeader + "\n\n" + strings.Join(blocks, "\n\n")
}

// sessionNumCtxTokens mirrors the browser's numCtxTokensForPct: 100% (or an
// unknown model) means "use the model default" and returns 0.
func sessionNumCtxTokens(pct int, modelMax int64) int {
	p := normalizeSessionNumCtxPct(pct)
	if p >= 100 {
		return 0
	}
	base := int64(32768)
	if modelMax > 0 {
		base = modelMax
	}
	n := int(math.Round(float64(base) * float64(p) / 100))
	if n < 256 {
		return 256
	}
	return n
}

func normalizeSessionNumCtxPct(p int) int {
	switch p {
	case 10, 25, 50, 75, 100:
		return p
	default:
		return 100
	}
}

// buildSessionBody assembles the chatRequestBody for a detached turn out of the
// session's stored options, so a session behaves exactly like the chat that
// created it.
func (s *Server) buildSessionBody(ctx context.Context, sess *ChatSession, caps sessionModelInfo, browserTools func() bool) chatRequestBody {
	body := chatRequestBody{
		Model:    sess.Model,
		Messages: s.chatSessions.buildSessionMessages(sess, caps),
		// The two browser-only tools rendezvous with the artifact preview panel, so
		// they are worth exposing only while a tab is actually watching. The loop
		// re-evaluates this on every round, which lets a session that was started
		// unattended pick them up as soon as the user opens it.
		BrowserToolsAvailable: browserTools,
		// Media produced during this turn is filed under the session, so deleting
		// the session takes its generated images with it.
		SessionID: sess.ID,
	}
	st := sess.Settings
	if caps.IsImage {
		body.Width = st.ImageWidth
		body.Height = st.ImageHeight
		body.Steps = st.ImageSteps
		body.Options = map[string]any{"seed": st.ImageSeed}
	} else {
		body.Options = map[string]any{
			"temperature": st.Temperature,
			"top_k":       st.TopK,
			"top_p":       st.TopP,
		}
		body.RAGEnabled = st.RAGEnabled
		body.RAGPaths = append([]string(nil), st.RAGPaths...)
		if show, err := s.ollama.Show(ctx, sess.Model); err == nil && show != nil {
			if toks := sessionNumCtxTokens(st.NumCtxPct, extractContextLength(show)); toks > 0 {
				body.Options["num_ctx"] = toks
			}
		}
		// "auto" means "let the model decide", but on the wire it is a hard
		// think:true, and Ollama rejects that outright for a model without the
		// capability. A quick chat never hits this because the browser hides the
		// thinking control for such a model; a detached turn has to do it here.
		if caps.CanThink {
			level := ollama.ThinkLevel(st.ThinkLevel)
			if level == "" {
				level = "auto"
			}
			body.Think = &level
		}
	}
	// Image generation models answer from the last prompt, not from a
	// conversation, and they never take tools.
	if st.WebTools && caps.CanTools && !caps.IsImage {
		yes := true
		body.WebTools = &yes
	}
	if st.Artifacts && caps.CanTools && !caps.IsImage {
		yes := true
		body.Artifacts = &yes
		body.ArtifactDir = latestSessionArtifactDir(sess)
	}
	// ComfyUI is independent of the other two: a plain vision chat with no
	// artifact workspace and no web search still generates images. It does need a
	// tool-capable model, and it is pointless on an image model, which takes
	// neither tools nor a conversation.
	if st.Comfy && caps.CanTools && !caps.IsImage {
		yes := true
		body.Comfy = &yes
		body.ComfyWorkflow = st.ComfyWorkflow
	}
	return body
}

// latestSessionArtifactDir is the artifact directory a continued turn should
// keep editing: the newest one referenced by the transcript.
func latestSessionArtifactDir(sess *ChatSession) string {
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		if ts := sess.Messages[i].ArtifactTS; ts != "" {
			return ts
		}
	}
	return ""
}

// sessionModelInfo is what a detached turn needs to know about the model it is
// about to run on.
type sessionModelInfo struct {
	// IsImage means the model generates images outright: it answers from the last
	// prompt and takes neither a conversation nor tools.
	IsImage bool
	// CanTools means the model supports tool calling, for web search and artifacts.
	CanTools bool
	// CanThink means the model supports thinking. Sending think to a model that
	// does not is a hard 400 from Ollama rather than a silent no-op, so it has to
	// be left out entirely.
	CanThink bool
	// HasVision means generated images can be replayed into the transcript, which
	// is what lets a restored session iterate on a picture it can actually see.
	HasVision bool
}

// sessionModelCaps reads the capabilities a session turn has to respect. A
// session keeps the settings it was created with even after the panel hides the
// controls the current model cannot honour, so these are re-checked on every
// turn instead of trusting the stored options.
func (s *Server) sessionModelCaps(ctx context.Context, model string) sessionModelInfo {
	if model == "" {
		return sessionModelInfo{}
	}
	show, err := s.ollama.Show(ctx, model)
	if err != nil || show == nil {
		return sessionModelInfo{}
	}
	var hasImage, hasVision, hasCompletion, hasThinking bool
	for _, c := range show.Capabilities {
		switch c {
		case "image":
			hasImage = true
		case "vision":
			hasVision = true
		case "completion":
			hasCompletion = true
		case "thinking":
			hasThinking = true
		}
	}
	return sessionModelInfo{
		IsImage:   hasImage && !hasVision && !hasCompletion,
		CanTools:  hasCompletion,
		CanThink:  hasThinking,
		HasVision: hasVision,
	}
}

// dispatchSessionTurn starts a detached turn for a session, parking it behind
// the running one when the session is already busy. The turn owns its own
// context so nothing stops when the browser that asked for it goes away.
func (s *Server) dispatchSessionTurn(id string) {
	s.chatSessions.Start(id, func() { s.sessionTurn(id) })
}

// sessionTurn is the body of one detached turn: it claims the cancel func, runs
// the model and settles the session. It is the default run the store uses when
// it needs to start a queued turn itself (after a paused queue resumes).
func (s *Server) sessionTurn(id string) {
	st := s.chatSessions
	ctx, cancel := context.WithCancel(context.Background())
	st.bindRunning(id, cancel)
	defer cancel()
	s.runSessionTurn(ctx, id)
}

// runSessionTurn executes one detached turn: it moves the next queued user turn
// into the transcript, runs the appropriate loop against a sessionSink, and
// settles the session status. It never touches an http.ResponseWriter.
func (s *Server) runSessionTurn(ctx context.Context, id string) {
	st := s.chatSessions
	// A queued user turn becomes the transcript only now, so the model never
	// sees a message the user sent while it was still answering the previous one.
	// A regenerate/edit already put its user turn in place, so there is nothing
	// to move.
	sess := st.Get(id)
	if sess == nil {
		st.releaseRunning(id)
		return
	}
	if !lastMessageIsUser(sess) {
		st.PopQueuedIntoMessages(id)
		sess = st.Get(id)
	}
	if sess == nil || !lastMessageIsUser(sess) {
		// Nothing to answer: a stale run (for example a queued run left over
		// from before a restart, or a removed message) must not append an
		// assistant reply to the turn that already finished.
		st.releaseRunning(id)
		return
	}
	model := sess.Model
	if model == "" {
		s.failSession(id, "session has no model")
		return
	}
	body := s.buildSessionBody(ctx, sess, s.sessionModelCaps(ctx, model), s.chatSessions.sessionWatcherCheck(sess.ID))
	startedAt := time.Now()
	sink := &sessionSink{srv: s, id: id, started: startedAt}

	if !s.beginTurn(id, startedAt) {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[chat-sessions] panic in session %s: %v", id, r)
			sink.Send("error", map[string]any{"error": fmt.Sprintf("internal error: %v", r)})
		}
	}()

	body = s.augmentChatWithRAG(ctx, sink, body)

	switch {
	case body.Artifacts != nil && *body.Artifacts:
		s.runArtifactAgentLoop(ctx, sink, body)
	case (body.WebTools != nil && *body.WebTools) || (body.Comfy != nil && *body.Comfy):
		// ComfyUI alone still needs the tool-call loop, and the web loop is the
		// one without the artifact filesystem surface.
		s.runWebToolAgentLoop(ctx, sink, body)
	default:
		s.runPlainChatLoop(ctx, sink, body)
	}
	s.finishSession(id, startedAt)
}

// beginTurn marks the session running and appends its pending assistant message.
func (s *Server) beginTurn(id string, startedAt time.Time) bool {
	st := s.chatSessions
	st.mu.Lock()
	sess := st.sessions[id]
	if sess == nil {
		st.mu.Unlock()
		return false
	}
	sess.Status = chatSessionRunning
	sess.Error = ""
	sess.Cancelled = false
	sess.LastActive = startedAt
	sess.Messages = append(sess.Messages, SessionMessage{
		Role:            "assistant",
		Model:           sess.Model,
		Pending:         true,
		CreatedAt:       startedAt,
		StreamStartedAt: startedAt.UnixMilli(),
		ToolLog:         []SessionToolEntry{},
	})
	st.touchLocked(sess)
	st.flushLocked(sess)
	sum := summaryOf(sess)
	st.mu.Unlock()
	st.broadcast(ChatSessionEvent{Kind: chatSessionUpdate, Session: &sum})
	return true
}

// finishSession settles a session after its turn ends: the pending message is
// closed, the status becomes idle/error/cancelled, and the reply is flagged
// unseen so its badge turns white.
func (s *Server) finishSession(id string, startedAt time.Time) {
	st := s.chatSessions
	st.mu.Lock()
	sess := st.sessions[id]
	if sess == nil {
		st.mu.Unlock()
		return
	}
	if sess.ResetPending {
		// The session was reset while this turn was in flight. Settle as a clean,
		// idle reset: no unread badge for a reply that was thrown away.
		clearSessionLocked(sess)
		sess.Status = chatSessionIdle
		st.touchLocked(sess)
		st.flushLocked(sess)
		sum := summaryOf(sess)
		st.mu.Unlock()
		st.dropSessionBlobs(id)
		st.broadcast(ChatSessionEvent{Kind: chatSessionUpdate, Session: &sum})
		st.broadcast(ChatSessionEvent{Kind: chatSessionEventStream, ID: id, Event: "reset"})
		st.releaseRunning(id)
		return
	}
	if msg := pendingMessageLocked(sess); msg != nil {
		msg.Pending = false
	}
	switch {
	case sess.Cancelled:
		sess.Status = chatSessionCancelled
	case sess.Error != "":
		sess.Status = chatSessionError
	default:
		sess.Status = chatSessionIdle
	}
	// Every finished reply counts as unread until a browser says otherwise. An
	// attached stream does not mean the user read it: with the chat view closed
	// the session keeps its event source open, so "someone is watching" stayed
	// true while the reply sat unread on the models page and the white badge
	// never appeared. A cancelled turn is the exception, because the user pressed
	// stop and therefore saw exactly what came of it.
	if sess.Status != chatSessionCancelled {
		sess.Unseen = true
	}
	// The replay log only exists to catch a browser up on a turn that is still
	// running. Once the turn settles, Messages holds the whole result: the text,
	// the tool log, the artifact fields and the counters. Keeping every chunk of
	// a finished turn would triple the size of the session file for nothing, and
	// a client that reconnects after this gets the transcript from the session
	// endpoint instead.
	sess.Events = nil
	st.touchLocked(sess)
	st.flushLocked(sess)
	sum := summaryOf(sess)
	st.mu.Unlock()
	log.Printf("[chat-sessions] %s finished after %s (status=%s, unseen=%v)", id,
		time.Since(startedAt).Round(time.Millisecond), sum.Status, sum.Unseen)
	st.broadcast(ChatSessionEvent{Kind: chatSessionUpdate, Session: &sum})
	st.releaseRunning(id)
}

func (s *Server) failSession(id, reason string) {
	st := s.chatSessions
	st.mu.Lock()
	sess := st.sessions[id]
	if sess == nil {
		st.mu.Unlock()
		return
	}
	if sess.ResetPending {
		// A reset cancelled this turn; its failure is not the user's error.
		clearSessionLocked(sess)
		sess.Status = chatSessionIdle
		st.touchLocked(sess)
		st.flushLocked(sess)
		sum := summaryOf(sess)
		st.mu.Unlock()
		st.dropSessionBlobs(id)
		st.broadcast(ChatSessionEvent{Kind: chatSessionUpdate, Session: &sum})
		st.broadcast(ChatSessionEvent{Kind: chatSessionEventStream, ID: id, Event: "reset"})
		st.releaseRunning(id)
		return
	}
	if msg := pendingMessageLocked(sess); msg != nil {
		msg.Pending = false
		msg.Error = reason
	}
	sess.Error = reason
	sess.Status = chatSessionError
	// A turn that never got off the ground is unread by definition: there is no
	// stream anyone could have been watching.
	sess.Unseen = true
	st.touchLocked(sess)
	st.flushLocked(sess)
	sum := summaryOf(sess)
	st.mu.Unlock()
	st.broadcast(ChatSessionEvent{Kind: chatSessionUpdate, Session: &sum})
	st.releaseRunning(id)
}

// --- store mutations used by the turn runner ------------------------------

// pendingMessageLocked returns the assistant message currently streaming.
func pendingMessageLocked(sess *ChatSession) *SessionMessage {
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		if sess.Messages[i].Pending {
			return &sess.Messages[i]
		}
	}
	return nil
}

// newSessionQueueID labels a queued turn so the browser can cancel or promote
// exactly that one without relying on a shifting list index.
func newSessionQueueID() string {
	return fmt.Sprintf("q-%d-%d", time.Now().UnixMilli(), chatSessionSeq.Add(1))
}

// enqueueUser parks a user turn in the session queue. deferred marks one sent
// while the session was already busy; only those announce themselves when they
// reach the transcript, because a direct message is already on screen.
func (st *chatSessionStore) enqueueUser(id, content string, attach []ChatAttach, deferred bool) (string, bool) {
	st.mu.Lock()
	sess := st.sessions[id]
	if sess == nil {
		st.mu.Unlock()
		return "", false
	}
	qid := newSessionQueueID()
	sess.Queue = append(sess.Queue, SessionMessage{
		Role:    "user",
		Content: content,
		// Park the bytes on disk for the same reason a normal turn does: the
		// queued message is persisted with the session.
		Attach:    st.storeAttachBlobs(sess.ID, attach),
		CreatedAt: time.Now(),
		QueueID:   qid,
		Deferred:  deferred,
	})
	st.touchLocked(sess)
	st.flushLocked(sess)
	sum := summaryOf(sess)
	st.mu.Unlock()
	st.broadcast(ChatSessionEvent{Kind: chatSessionUpdate, Session: &sum})
	return qid, true
}

// queueEchoForSend returns the queue the browser should draw right after a
// message is accepted, with attachments hydrated. It is what stops a direct
// message from showing up as a phantom queued row.
//
// A message that did not have to wait (wasBusy false) is moved into the
// transcript by the run goroutine, but for the instant before that happens it
// can still sit at the head of Queue. Echoing it there would draw a row the
// browser can never clear, because no queued_user event fires for a turn that
// was never deferred. Withhold exactly that entry, unless the turn is genuinely
// parked behind the global concurrency limit (status queued), where the message
// really is still waiting its slot.
func (st *chatSessionStore) queueEchoForSend(id, queueID string, wasBusy bool) []SessionMessage {
	st.mu.Lock()
	sess := st.sessions[id]
	if sess == nil {
		st.mu.Unlock()
		return nil
	}
	queue := make([]SessionMessage, 0, len(sess.Queue))
	for _, m := range sess.Queue {
		if !wasBusy && queueID != "" && sess.Status != chatSessionQueued && m.QueueID == queueID {
			continue
		}
		queue = append(queue, m)
	}
	st.mu.Unlock()
	return st.hydrateMessages(queue)
}

// PopQueuedIntoMessages moves the next queued user turn into the transcript. It
// runs at the start of a turn, so the model never sees a message that arrived
// while it was answering the previous one.
func (st *chatSessionStore) PopQueuedIntoMessages(id string) bool {
	st.mu.Lock()
	sess := st.sessions[id]
	if sess == nil || len(sess.Queue) == 0 {
		st.mu.Unlock()
		return false
	}
	msg := sess.Queue[0]
	sess.Queue = append([]SessionMessage(nil), sess.Queue[1:]...)
	sess.Messages = append(sess.Messages, msg)
	if st.trimMessagesLocked(sess) {
		st.dropOrphanBlobs(sess.ID, sess.Messages)
	}
	var seq int
	if msg.Deferred {
		sess.Seq++
		seq = sess.Seq
		st.appendReplayLocked(sess, seq, "queued_user", msg)
	}
	st.touchLocked(sess)
	st.flushLocked(sess)
	sum := summaryOf(sess)
	st.mu.Unlock()
	st.broadcast(ChatSessionEvent{Kind: chatSessionUpdate, Session: &sum})
	if msg.Deferred {
		// Tell whoever is watching to move this message from the queue panel
		// into the transcript. A reconnecting tab gets it from the transcript
		// instead; the event is only for a stream that is already open.
		hydrated := st.hydrateMessages([]SessionMessage{msg})[0]
		st.broadcast(ChatSessionEvent{
			Kind:  chatSessionEventStream,
			ID:    id,
			Seq:   seq,
			Event: "queued_user",
			Data:  hydrated,
		})
	}
	return true
}

// RemoveQueued takes one queued turn out and returns it, so the browser can put
// the text back in the composer.
func (st *chatSessionStore) RemoveQueued(id, queueID string) (SessionMessage, bool) {
	st.mu.Lock()
	sess := st.sessions[id]
	if sess == nil {
		st.mu.Unlock()
		return SessionMessage{}, false
	}
	idx := -1
	for i := range sess.Queue {
		if sess.Queue[i].QueueID == queueID {
			idx = i
			break
		}
	}
	if idx < 0 {
		st.mu.Unlock()
		return SessionMessage{}, false
	}
	msg := sess.Queue[idx]
	sess.Queue = append(sess.Queue[:idx], sess.Queue[idx+1:]...)
	st.dropAttachBlobs(msg.Attach)
	st.touchLocked(sess)
	st.flushLocked(sess)
	sum := summaryOf(sess)
	st.mu.Unlock()
	st.broadcast(ChatSessionEvent{Kind: chatSessionUpdate, Session: &sum})
	return msg, true
}

// PromoteQueued moves a queued turn to the front and, when a turn is running,
// interrupts it so the promoted message runs next.
func (st *chatSessionStore) PromoteQueued(id, queueID string) bool {
	st.mu.Lock()
	sess := st.sessions[id]
	if sess == nil {
		st.mu.Unlock()
		return false
	}
	idx := -1
	for i := range sess.Queue {
		if sess.Queue[i].QueueID == queueID {
			idx = i
			break
		}
	}
	if idx < 0 {
		st.mu.Unlock()
		return false
	}
	msg := sess.Queue[idx]
	rest := append(append([]SessionMessage{}, sess.Queue[:idx]...), sess.Queue[idx+1:]...)
	sess.Queue = append([]SessionMessage{msg}, rest...)
	st.paused[id] = false
	cancel, running := st.running[id]
	if running {
		sess.Cancelled = true
	}
	st.touchLocked(sess)
	st.flushLocked(sess)
	sum := summaryOf(sess)
	st.mu.Unlock()
	st.broadcast(ChatSessionEvent{Kind: chatSessionUpdate, Session: &sum})
	if running {
		if cancel != nil {
			cancel()
		}
		return true
	}
	// Nothing running: start the promoted turn now. Its run is parked in pending
	// or, after a restart, has to be rebuilt from the default runner.
	st.mu.Lock()
	if _, busy := st.running[id]; !busy {
		st.startNextLocked(id)
	}
	st.mu.Unlock()
	return true
}

// AppendUser adds a user turn straight to the transcript. The turn runner uses
// the queue instead; this stays for callers that build a transcript by hand.
func (st *chatSessionStore) AppendUser(id string, content string, attach []ChatAttach) bool {
	st.mu.Lock()
	sess := st.sessions[id]
	if sess == nil {
		st.mu.Unlock()
		return false
	}
	sess.Messages = append(sess.Messages, SessionMessage{
		Role:    "user",
		Content: content,
		// Park the bytes on disk before the message becomes part of the
		// transcript: from here on nothing may persist the base64 payload.
		Attach:    st.storeAttachBlobs(sess.ID, attach),
		CreatedAt: time.Now(),
	})
	trimmed := st.trimMessagesLocked(sess)
	if trimmed {
		// Trimming drops whole turns, so their attachments are now unreferenced.
		st.dropOrphanBlobs(sess.ID, sess.Messages)
	}
	st.touchLocked(sess)
	st.flushLocked(sess)
	// Deliberately no broadcast: the caller starts the turn right after, and
	// beginTurn's update already carries the new message count. Emitting an idle
	// update in between would briefly tell browsers that a turn had finished
	// when none had started yet.
	st.mu.Unlock()
	return true
}

// appendReplayLocked records one event in the per-session replay log. Callers
// must hold st.mu.
func (st *chatSessionStore) appendReplayLocked(sess *ChatSession, seq int, event string, payload any) {
	sess.Events = append(sess.Events, SessionEvent{Seq: seq, Event: event, Data: trimReplayPayload(event, payload)})
	if over := len(sess.Events) - maxSessionEvents; over > 0 {
		sess.Events = append([]SessionEvent(nil), sess.Events[over:]...)
	}
}

// trimMessagesLocked keeps a long-running session from growing without bound.
// The transcript is the render source of truth and it is rewritten on every
// token, so an unattended session that chats for hours would otherwise produce a
// file nothing can open. The first user message is kept as the conversation
// anchor, and the message being written right now is never dropped.
//
// It reports whether whole messages were dropped, which is the caller's cue to
// clean up the attachments that just became unreferenced.
func (st *chatSessionStore) trimMessagesLocked(sess *ChatSession) bool {
	if len(sess.Messages) <= maxSessionMessages {
		// A single runaway reply can still dominate the file on its own.
		for i := range sess.Messages {
			m := &sess.Messages[i]
			if m.Pending || len(m.Content)+len(m.Raw) <= maxSessionMessageRunes {
				continue
			}
			m.Content = truncateRunes(m.Content, maxSessionMessageRunes/2)
			m.Raw = ""
			m.ToolLog = nil
			m.Truncated = true
		}
		return false
	}
	// Drop from the front, but never the first user message: it is what the
	// title came from and the anchor for the rest of the transcript.
	drop := len(sess.Messages) - maxSessionMessages
	keepFrom := 0
	for i := 1; i < len(sess.Messages) && drop > 0; i++ {
		if sess.Messages[i].Role == "user" {
			drop--
			keepFrom = i + 1
		}
	}
	if keepFrom == 0 {
		return false
	}
	sess.Messages = append([]SessionMessage(nil), sess.Messages[keepFrom:]...)
	sess.DroppedMessages += keepFrom
	return true
}

// MergeSettings folds the current options panel into the session. It is called
// on every message, so a session always carries the settings that were in
// effect when its last message was sent. Only the fields the browser actually
// sent are touched, so a partial payload cannot wipe the rest.
func (st *chatSessionStore) MergeSettings(id string, in sessionSettingsInput) {
	st.mu.Lock()
	sess := st.sessions[id]
	if sess == nil {
		st.mu.Unlock()
		return
	}
	sess.Settings = in.mergeInto(sess.Settings)
	st.touchLocked(sess)
	st.saveLocked(sess)
	st.mu.Unlock()
}

// SetModel moves a session to a different model. The model is part of the
// configuration a message is sent with, so switching it inside an open session
// has to stick: the next turn runs on the new model and the model-list badge
// follows the model that is actually doing the work. It broadcasts so browsers
// see the move before the turn starts.
func (st *chatSessionStore) SetModel(id, model string) bool {
	st.mu.Lock()
	sess := st.sessions[id]
	if sess == nil {
		st.mu.Unlock()
		return false
	}
	if sess.Model == model {
		st.mu.Unlock()
		return true
	}
	sess.Model = model
	st.touchLocked(sess)
	st.saveLocked(sess)
	sum := summaryOf(sess)
	st.mu.Unlock()
	st.broadcast(ChatSessionEvent{Kind: chatSessionUpdate, Session: &sum})
	return true
}

// TrimAfterLastUser drops every message that follows the last user turn, which
// is the assistant reply and anything after it. A session's transcript belongs
// to the server, so "regenerate" has to trim it here rather than in the browser.
func (st *chatSessionStore) TrimAfterLastUser(id string) bool {
	st.mu.Lock()
	sess := st.sessions[id]
	if sess == nil {
		st.mu.Unlock()
		return false
	}
	last := -1
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		if sess.Messages[i].Role == "user" {
			last = i
			break
		}
	}
	if last < 0 || len(sess.Messages) == last+1 {
		st.mu.Unlock()
		return false
	}
	sess.Messages = append([]SessionMessage(nil), sess.Messages[:last+1]...)
	st.touchLocked(sess)
	st.flushLocked(sess)
	sum := summaryOf(sess)
	st.mu.Unlock()
	st.broadcast(ChatSessionEvent{Kind: chatSessionUpdate, Session: &sum})
	return true
}

// ReplaceLastUser rewrites the text and attachments of the last user turn in
// place. It backs "edit and resend" for sessions.
func (st *chatSessionStore) ReplaceLastUser(id, content string, attach []ChatAttach) bool {
	st.mu.Lock()
	sess := st.sessions[id]
	if sess == nil {
		st.mu.Unlock()
		return false
	}
	for i := len(sess.Messages) - 1; i >= 0; i-- {
		if sess.Messages[i].Role != "user" {
			continue
		}
		sess.Messages[i].Content = content
		// Park the new bytes first, then drop the old ones: the reverse order
		// would risk deleting a file the new attachment just claimed.
		st.dropAttachBlobs(sess.Messages[i].Attach)
		sess.Messages[i].Attach = st.storeAttachBlobs(sess.ID, attach)
		st.touchLocked(sess)
		st.flushLocked(sess)
		sum := summaryOf(sess)
		st.mu.Unlock()
		st.broadcast(ChatSessionEvent{Kind: chatSessionUpdate, Session: &sum})
		return true
	}
	st.mu.Unlock()
	return false
}

// Rename sets the user-visible title. Only a title set here is shown in the
// list; everything else stays anonymous and shows its relative time.
func (st *chatSessionStore) Rename(id, title string) bool {
	st.mu.Lock()
	sess := st.sessions[id]
	if sess == nil {
		st.mu.Unlock()
		return false
	}
	clean := strings.TrimSpace(title)
	sess.Title = chatSessionTitle(clean)
	sess.CustomTitle = clean != ""
	st.touchLocked(sess)
	st.saveLocked(sess)
	sum := summaryOf(sess)
	st.mu.Unlock()
	st.broadcast(ChatSessionEvent{Kind: chatSessionUpdate, Session: &sum})
	return true
}

// lastMessageIsUser reports whether the transcript ends on a user turn, i.e.
// whether there is anything for a new turn to answer.
func lastMessageIsUser(sess *ChatSession) bool {
	if sess == nil || len(sess.Messages) == 0 {
		return false
	}
	return sess.Messages[len(sess.Messages)-1].Role == "user"
}

// --- sessionSink ----------------------------------------------------------

// toolEvent is the JSON shape the agent loops emit for the "tool" event.
type toolEvent struct {
	Phase         string `json:"phase"`
	Name          string `json:"name"`
	Status        string `json:"status"`
	Query         string `json:"query,omitempty"`
	URL           string `json:"url,omitempty"`
	MaxResults    int    `json:"max_results,omitempty"`
	Path          string `json:"path,omitempty"`
	Command       string `json:"command,omitempty"`
	Code          string `json:"code,omitempty"`
	ArtifactName  string `json:"artifact_name,omitempty"`
	Description   string `json:"description,omitempty"`
	OK            *bool  `json:"ok,omitempty"`
	Error         string `json:"error,omitempty"`
	ResultPreview string `json:"result_preview,omitempty"`
	ResultRunes   int    `json:"result_runes,omitempty"`
	Image         string `json:"image,omitempty"`
	// Media carries the stored ComfyUI outputs. Unlike Image it is paths, not
	// bytes, so it is safe to persist in a file rewritten on every token.
	Media []ChatMedia `json:"media,omitempty"`
	// RunStatus is the human progress label ("queued as #3") reported while a
	// long render is in flight. It is separate from Status, which drives the
	// generating/running/ok/error state machine.
	RunStatus string `json:"run_status,omitempty"`
	Workflow  string `json:"workflow,omitempty"`
	Prompt    string `json:"prompt,omitempty"`
	Seed      int64  `json:"seed,omitempty"`
	PromptID  string `json:"prompt_id,omitempty"`
}

// firstComfyPromptID returns the ComfyUI prompt id behind a run's outputs, so the
// session file can tie a stored image back to the job that made it.
func firstComfyPromptID(media []ChatMedia) string {
	for _, m := range media {
		if m.PromptID != "" {
			return m.PromptID
		}
	}
	return ""
}

// sessionSink turns the chat stream events of a detached run into session state:
// it keeps the transcript current, appends to the replay log, and fans every
// event out to whatever browser is tailing this session. It is only ever called
// from the single goroutine running the turn.
type sessionSink struct {
	srv     *Server
	id      string
	started time.Time

	raw       string
	think     string
	answer    string
	tools     []SessionToolEntry
	thinkOpen bool
	thinkShut bool

	// thinkSpent accumulates the wall time spent inside think blocks. A model
	// can interleave thinking and prose, so each closed block adds up instead of
	// only the first one counting.
	thinkSpent time.Duration
	// thinkSince is when the currently open block started, if any. It is the
	// zero value while no block is open, which is also what makes a second
	// block start its own clock.
	thinkSince time.Time
}

func (k *sessionSink) Send(event string, payload any) {
	if event == "" {
		return
	}
	st := k.srv.chatSessions
	st.mu.Lock()
	sess := st.sessions[k.id]
	if sess == nil {
		st.mu.Unlock()
		return
	}
	msg := pendingMessageLocked(sess)
	if msg != nil {
		k.applyLocked(event, payload, msg)
	}
	sess.Seq++
	seq := sess.Seq
	k.appendEventLocked(sess, seq, event, payload)
	sess.LastActive = time.Now()
	st.saveLocked(sess)
	st.mu.Unlock()

	st.broadcast(ChatSessionEvent{
		Kind:  chatSessionEventStream,
		ID:    k.id,
		Seq:   seq,
		Event: event,
		Data:  payload,
	})
}

// appendEventLocked records an event in the replay log a reconnecting browser
// tails. Chunk payloads are trimmed first: a raw chunk repeats the model name and
// a timestamp that nothing in the replay needs, and a few hundred of them would
// otherwise dominate the session file.
func (k *sessionSink) appendEventLocked(sess *ChatSession, seq int, event string, payload any) {
	sess.Events = append(sess.Events, SessionEvent{Seq: seq, Event: event, Data: trimReplayPayload(event, payload)})
	if over := len(sess.Events) - maxSessionEvents; over > 0 {
		sess.Events = append([]SessionEvent(nil), sess.Events[over:]...)
	}
}

// trimReplayPayload drops the parts of a chunk event a reconnecting client never
// reads. applyChatStreamEvent only looks at message.content and message.thinking
// for a chunk, and the other events are small already, so they pass through
// untouched. The live broadcast still carries the full payload.
func trimReplayPayload(event string, payload any) any {
	if event != "chunk" {
		return payload
	}
	chunk, ok := payload.(ollama.ChatChunk)
	if !ok {
		raw, rok := payload.(json.RawMessage)
		if !rok {
			return payload
		}
		if err := json.Unmarshal(raw, &chunk); err != nil {
			return payload
		}
	}
	return map[string]any{
		"message": map[string]any{
			"role":     chunk.Message.Role,
			"content":  chunk.Message.Content,
			"thinking": chunk.Message.Thinking,
		},
		"done": chunk.Done,
	}
}

func (k *sessionSink) applyLocked(event string, payload any, msg *SessionMessage) {
	switch event {
	case "chunk":
		k.applyChunkLocked(payload, msg)
	case "tool":
		k.applyToolLocked(payload, msg)
	case "artifact":
		k.applyArtifactLocked(payload, msg)
	case "done":
		k.closeThinkLocked()
		k.applyDoneLocked(payload, msg)
		msg.Pending = false
	case "error":
		k.closeThinkLocked()
		msg.Pending = false
		msg.ThinkMs = k.thinkMillisLocked()
		msg.Error = sessionEventError(payload)
	}
}

// applyChunkLocked accumulates the raw stream text. Storing the raw text (with
// its think tags) rather than the parsed pieces means the browser can replay it
// through its own splitter and get byte-identical rendering.
func (k *sessionSink) applyChunkLocked(payload any, msg *SessionMessage) {
	chunk, ok := payload.(ollama.ChatChunk)
	if !ok {
		raw, rok := payload.(json.RawMessage)
		if !rok {
			return
		}
		if err := json.Unmarshal(raw, &chunk); err != nil {
			return
		}
	}
	think, content := chunk.Message.Thinking, chunk.Message.Content
	if think == "" && content == "" {
		return
	}
	if think != "" {
		// A model can answer a few words and then start thinking again. Every
		// block needs its own fence, otherwise the client splitter reads the
		// second one as part of the answer. The browser does this with its
		// thinkBlockClosed flag; the raw text has to match it byte for byte
		// because this is what gets persisted and replayed.
		started := false
		if !k.thinkOpen {
			k.raw += "<think>\n"
			k.thinkOpen = true
			started = true
		} else if k.thinkShut {
			k.raw += "\n<think>\n"
			started = true
		}
		k.thinkShut = false
		if started {
			k.thinkSince = time.Now()
		}
		k.raw += think
		k.think += think
	}
	if content != "" {
		k.closeThinkLocked()
		k.raw += content
		k.answer += content
	}
	msg.Raw = k.raw
	msg.Think = k.think
	msg.Content = k.answer
	msg.ThinkMs = k.thinkMillisLocked()
}

// closeThinkLocked closes the open think block, if any, and banks the time spent
// in it. It is called on every content chunk and on done/error, so the guard
// keeps it from firing twice for the same block. thinkOpen stays set: it means
// "this turn has emitted at least one block", and applyChunkLocked relies on it
// to decide whether a later think delta opens the first or a subsequent block.
func (k *sessionSink) closeThinkLocked() {
	if !k.thinkOpen || k.thinkShut {
		return
	}
	k.raw += "\n</think>\n"
	k.thinkShut = true
	if !k.thinkSince.IsZero() {
		k.thinkSpent += time.Since(k.thinkSince)
		k.thinkSince = time.Time{}
	}
}

// thinkMillisLocked reports the thinking time so far, including the block that
// is still open. The caller must hold the store lock.
func (k *sessionSink) thinkMillisLocked() int64 {
	total := k.thinkSpent
	if !k.thinkSince.IsZero() {
		total += time.Since(k.thinkSince)
	}
	return total.Milliseconds()
}

func (k *sessionSink) applyToolLocked(payload any, msg *SessionMessage) {
	ev := decodeToolEvent(payload)
	if ev.Name == "" {
		return
	}
	entry := SessionToolEntry{
		Name:          ev.Name,
		Status:        ev.Status,
		Phase:         ev.Phase,
		Query:         ev.Query,
		URL:           ev.URL,
		MaxResults:    ev.MaxResults,
		Path:          ev.Path,
		Command:       ev.Command,
		Code:          ev.Code,
		ArtifactName:  ev.ArtifactName,
		Description:   ev.Description,
		Error:         ev.Error,
		ResultPreview: ev.ResultPreview,
		ResultRunes:   ev.ResultRunes,
		Image:         ev.Image,
		Media:         ev.Media,
		Workflow:      ev.Workflow,
		Prompt:        ev.Prompt,
		Seed:          ev.Seed,
		RunStatus:     ev.RunStatus,
		PromptID:      ev.PromptID,
	}
	tools := msg.ToolLog
	switch ev.Phase {
	case "generating":
		for i := range tools {
			if tools[i].Name == ev.Name && tools[i].Status == "generating" {
				return
			}
		}
		tools = append(tools, entry)
	case "running":
		// Progress lines only update the label. Falling through to the default
		// branch would mark the tool finished before it has produced anything,
		// and the real "done" event would then be ignored.
		for i := len(tools) - 1; i >= 0; i-- {
			if tools[i].Name != ev.Name {
				continue
			}
			if ev.RunStatus != "" {
				tools[i].RunStatus = ev.RunStatus
			}
			if ev.Workflow != "" {
				tools[i].Workflow = ev.Workflow
			}
			if ev.Prompt != "" {
				tools[i].Prompt = ev.Prompt
			}
			if ev.Seed != 0 {
				tools[i].Seed = ev.Seed
			}
			msg.ToolLog = tools
			return
		}
		tools = append(tools, entry)
	case "start":
		for i := len(tools) - 1; i >= 0; i-- {
			if tools[i].Name == ev.Name && tools[i].Status == "generating" {
				tools[i].Status = "running"
				tools[i].Query = ev.Query
				tools[i].URL = ev.URL
				tools[i].MaxResults = ev.MaxResults
				tools[i].Path = ev.Path
				tools[i].Command = ev.Command
				tools[i].Code = ev.Code
				tools[i].ArtifactName = ev.ArtifactName
				tools[i].Description = ev.Description
				tools[i].Workflow = ev.Workflow
				tools[i].Prompt = ev.Prompt
				tools[i].Seed = ev.Seed
				msg.ToolLog = tools
				return
			}
		}
		tools = append(tools, entry)
	default:
		status := "ok"
		if ev.OK != nil && !*ev.OK {
			status = "error"
		}
		for i := len(tools) - 1; i >= 0; i-- {
			if tools[i].Name != ev.Name {
				continue
			}
			if tools[i].Status == "ok" || tools[i].Status == "error" {
				continue
			}
			tools[i].Status = status
			tools[i].Error = ev.Error
			tools[i].ResultPreview = ev.ResultPreview
			tools[i].ResultRunes = ev.ResultRunes
			tools[i].Image = ev.Image
			tools[i].RunStatus = ""
			if len(ev.Media) > 0 {
				tools[i].Media = ev.Media
				tools[i].PromptID = firstComfyPromptID(ev.Media)
			}
			if ev.ArtifactName != "" {
				tools[i].ArtifactName = ev.ArtifactName
			}
			msg.ToolLog = tools
			return
		}
		entry.Status = status
		tools = append(tools, entry)
	}
	msg.ToolLog = tools
	k.tools = tools
}

func decodeToolEvent(payload any) toolEvent {
	var ev toolEvent
	switch v := payload.(type) {
	case toolEvent:
		return v
	case json.RawMessage:
		_ = json.Unmarshal(v, &ev)
	case map[string]any:
		buf, err := json.Marshal(v)
		if err == nil {
			_ = json.Unmarshal(buf, &ev)
		}
	}
	return ev
}

func (k *sessionSink) applyArtifactLocked(payload any, msg *SessionMessage) {
	m, ok := payload.(map[string]any)
	if !ok {
		if raw, rok := payload.(json.RawMessage); rok {
			var decoded map[string]any
			if json.Unmarshal(raw, &decoded) == nil {
				m = decoded
				ok = true
			}
		}
		if !ok {
			return
		}
	}
	if v, _ := m["timestamp"].(string); v != "" {
		msg.ArtifactTS = v
	}
	if v, _ := m["name"].(string); v != "" {
		msg.ArtifactNm = v
	}
	if v, _ := m["url"].(string); v != "" {
		msg.ArtifactURL = v
	}
	if v, _ := m["description"].(string); v != "" {
		msg.ArtifactDesc = v
	}
	if v, _ := m["generating"].(bool); v {
		msg.ArtifactGenerating = true
	}
	if v, ok := m["generating"].(bool); !ok || !v {
		msg.ArtifactGenerating = false
	}
	if v, _ := m["loaded"].(bool); v {
		msg.ArtifactGenerating = false
	}
	if v, _ := m["reload"].(bool); v {
		msg.ArtifactGenerating = false
	}
}

func (k *sessionSink) applyDoneLocked(payload any, msg *SessionMessage) {
	m, ok := payload.(map[string]any)
	if !ok {
		if raw, rok := payload.(json.RawMessage); rok {
			var decoded map[string]any
			if json.Unmarshal(raw, &decoded) == nil {
				m = decoded
				ok = true
			}
		}
		if !ok {
			return
		}
	}
	msg.Raw = k.raw
	msg.Think = k.think
	msg.Content = k.answer
	msg.ElapsedMs = sessionEventInt(m["elapsed_ms"])
	msg.ThinkMs = k.thinkMillisLocked()
	msg.PromptTokens = int(sessionEventInt(m["prompt_tokens"]))
	msg.CompletionTokens = int(sessionEventInt(m["completion_tokens"]))
	msg.EvalNs = sessionEventInt(m["eval_duration_ns"])
	if v, _ := m["done_reason"].(string); v != "" {
		msg.DoneReason = v
	}
}

func sessionEventInt(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	default:
		return 0
	}
}

func sessionEventError(payload any) string {
	switch v := payload.(type) {
	case string:
		return v
	case map[string]any:
		s, _ := v["error"].(string)
		return s
	case json.RawMessage:
		var decoded map[string]any
		if json.Unmarshal(v, &decoded) == nil {
			s, _ := decoded["error"].(string)
			return s
		}
	}
	return ""
}
