package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// sessionSettingsInput accepts the chat options panel exactly as the browser
// reads it from the DOM, where numbers arrive as strings.
type sessionSettingsInput struct {
	System      any `json:"system"`
	Temperature any `json:"temperature"`
	TopK        any `json:"top_k"`
	TopP        any `json:"top_p"`
	NumCtxPct   any `json:"num_ctx"`
	ThinkLevel  any `json:"think_level"`
	WebTools    any `json:"web_tools"`
	Artifacts   any `json:"artifacts"`
	ImageWidth  any `json:"image_width"`
	ImageHeight any `json:"image_height"`
	ImageSteps  any `json:"image_steps"`
	ImageSeed   any `json:"image_seed"`
	Comfy       any `json:"comfy"`
	// ComfyWorkflow is the id (or empty for "let the model choose") of the workflow
	// this session should use by default.
	ComfyWorkflow any `json:"comfy_workflow"`
}

// defaultSessionSettings mirrors the values the options panel starts with, so a
// session created without a settings payload still looks like a fresh chat.
func defaultSessionSettings() SessionSettings {
	return SessionSettings{
		Temperature: 0.7,
		TopK:        40,
		TopP:        0.9,
		NumCtxPct:   100,
		ThinkLevel:  "auto",
		ImageWidth:  512,
		ImageHeight: 512,
		ImageSteps:  4,
		ImageSeed:   0,
	}
}

// mergeInto folds the fields the caller actually sent into an existing set of
// settings. Absent fields keep their previous value, so a partial payload can
// never silently drop options the user configured earlier in the session.
func (in sessionSettingsInput) mergeInto(dst SessionSettings) SessionSettings {
	if in.System != nil {
		dst.System = sessionOptString(in.System)
	}
	if in.Temperature != nil {
		dst.Temperature = sessionOptFloat(in.Temperature, dst.Temperature)
	}
	if in.TopK != nil {
		dst.TopK = sessionOptInt(in.TopK, dst.TopK)
	}
	if in.TopP != nil {
		dst.TopP = sessionOptFloat(in.TopP, dst.TopP)
	}
	if in.NumCtxPct != nil {
		dst.NumCtxPct = normalizeSessionNumCtxPct(sessionOptInt(in.NumCtxPct, dst.NumCtxPct))
	}
	if in.ThinkLevel != nil {
		if lvl := sessionOptString(in.ThinkLevel); lvl != "" {
			dst.ThinkLevel = lvl
		} else {
			dst.ThinkLevel = "auto"
		}
	}
	if in.WebTools != nil {
		dst.WebTools = sessionOptBool(in.WebTools)
	}
	if in.Artifacts != nil {
		dst.Artifacts = sessionOptBool(in.Artifacts)
	}
	if in.ImageWidth != nil {
		dst.ImageWidth = sessionOptInt(in.ImageWidth, dst.ImageWidth)
	}
	if in.ImageHeight != nil {
		dst.ImageHeight = sessionOptInt(in.ImageHeight, dst.ImageHeight)
	}
	if in.ImageSteps != nil {
		dst.ImageSteps = sessionOptInt(in.ImageSteps, dst.ImageSteps)
	}
	if in.ImageSeed != nil {
		dst.ImageSeed = sessionOptInt(in.ImageSeed, dst.ImageSeed)
	}
	if in.Comfy != nil {
		dst.Comfy = sessionOptBool(in.Comfy)
	}
	if in.ComfyWorkflow != nil {
		dst.ComfyWorkflow = sessionOptString(in.ComfyWorkflow)
	}
	return dst
}

// toSessionSettings coerces a full loose input into the persisted shape,
// filling in the same defaults the options panel starts with.
func (in sessionSettingsInput) toSessionSettings() SessionSettings {
	return in.mergeInto(defaultSessionSettings())
}

func sessionOptString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case nil:
		return ""
	default:
		return ""
	}
}

func sessionOptFloat(v any, def float64) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(t), 64); err == nil {
			return f
		}
	case json.Number:
		if f, err := t.Float64(); err == nil {
			return f
		}
	}
	return def
}

func sessionOptInt(v any, def int) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case float32:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
			return n
		}
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return int(n)
		}
	case bool:
		if t {
			return 1
		}
		return 0
	}
	return def
}

func sessionOptBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(t))
		return err == nil && b
	case float64:
		return t != 0
	case float32:
		return t != 0
	case int:
		return t != 0
	case int64:
		return t != 0
	}
	return false
}

type createSessionRequest struct {
	Model    string               `json:"model"`
	Settings sessionSettingsInput `json:"settings"`
}

type sessionMessageRequest struct {
	Content     string                `json:"content"`
	Attachments []ChatAttach          `json:"attachments"`
	Settings    *sessionSettingsInput `json:"settings"`
	// Model is the model the browser currently has selected. The session adopts
	// it, the same way it adopts the options panel, so a session always keeps
	// generating with the configuration of its last input.
	Model string `json:"model,omitempty"`
	// ReplaceLast drops the assistant reply that follows the last user turn
	// before the new turn is appended. The browser used to do this locally;
	// on a session the server owns the transcript, so it has to.
	ReplaceLast bool `json:"replace_last,omitempty"`
	// EditLast rewrites the last user turn instead of appending a new one,
	// which is what "edit and resend" means once the transcript is persisted.
	EditLast bool `json:"edit_last,omitempty"`
}

type renameSessionRequest struct {
	Title string `json:"title"`
}

// handleChatSessionsList serves the session rows used by the side panel and by
// the badges next to each model.
func (s *Server) handleChatSessionsList(w http.ResponseWriter, r *http.Request) {
	if !s.chatSessionsEnabled() {
		// Feature switched off in settings. Report it instead of an empty list so
		// the UI can hide the panel rather than look like a bug.
		writeJSON(w, http.StatusOK, map[string]any{
			"sessions":     []SessionSummary{},
			"max_parallel": maxConcurrentChatSessions,
			"enabled":      false,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sessions":     s.chatSessions.List(),
		"max_parallel": maxConcurrentChatSessions,
		"enabled":      true,
	})
}

// handleChatSessionsDeleteAll wipes every session of every model ("clear all").
// It never 404s: clearing an already empty list is a no-op the UI can treat as
// success.
func (s *Server) handleChatSessionsDeleteAll(w http.ResponseWriter, r *http.Request) {
	n := s.chatSessions.DeleteAll()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": n})
}

// chatSessionsEnabled reports the settings switch. Reading it takes the config
// lock, so callers must not already hold it.
func (s *Server) chatSessionsEnabled() bool {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg.ChatSessions.IsEnabled()
}

// requireChatSessions writes a 403 and reports false when the feature is off.
func (s *Server) requireChatSessions(w http.ResponseWriter) bool {
	if s.chatSessionsEnabled() {
		return true
	}
	writeError(w, http.StatusForbidden, errors.New("persistent chat sessions are disabled in settings"))
	return false
}

// handleChatSessionCreate registers a new persistent session.
func (s *Server) handleChatSessionCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireChatSessions(w) {
		return
	}
	var req createSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid body"))
		return
	}
	req.Model = strings.TrimSpace(req.Model)
	if req.Model == "" {
		writeError(w, http.StatusBadRequest, errors.New("model is required"))
		return
	}
	sess := s.chatSessions.Create(req.Model, req.Settings.toSessionSettings())
	writeJSON(w, http.StatusOK, s.sessionDetail(sess))
}

// handleChatSessionGet returns the full transcript plus the saved options.
func (s *Server) handleChatSessionGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess := s.chatSessions.Get(id)
	if sess == nil {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	writeJSON(w, http.StatusOK, s.sessionDetail(sess))
}

func (s *Server) sessionDetail(sess *ChatSession) map[string]any {
	s.chatSessions.mu.Lock()
	defer s.chatSessions.mu.Unlock()
	detail := map[string]any{
		"id":       sess.ID,
		"title":    sess.Title,
		"model":    sess.Model,
		"status":   sess.Status,
		"unseen":   sess.Unseen,
		"error":    sess.Error,
		"settings": sess.Settings,
		// Hydrated on the way out: the stored transcript keeps attachment bytes on
		// disk, but the browser has to be able to render them again.
		"messages":       s.chatSessions.hydrateMessages(sess.Messages),
		"seq":            sess.Seq,
		"created_at":     sess.CreatedAt,
		"updated_at":     sess.UpdatedAt,
		"last_active_at": sess.LastActive,
		"watching":       s.chatSessions.watchers[sess.ID] > 0,
	}
	return detail
}

// handleChatSessionPatch renames a session.
func (s *Server) handleChatSessionPatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req renameSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid body"))
		return
	}
	if !s.chatSessions.Rename(id, req.Title) {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleChatSessionDelete cancels any run and removes the session for good.
func (s *Server) handleChatSessionDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireChatSessions(w) {
		return
	}
	id := r.PathValue("id")
	if !s.chatSessions.Delete(id) {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleChatSessionSend appends a user turn and starts a detached run. It
// answers 202 right away: the browser never waits for the model.
func (s *Server) handleChatSessionSend(w http.ResponseWriter, r *http.Request) {
	if !s.requireChatSessions(w) {
		return
	}
	id := r.PathValue("id")
	if s.chatSessions.Get(id) == nil {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	var req sessionMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid body"))
		return
	}
	if strings.TrimSpace(req.Content) == "" && len(req.Attachments) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("content is required"))
		return
	}
	// Refuse early, before the message is appended. A session runs one turn at a
	// time, and Start quietly ignores a second one, so accepting the message here
	// would leave it sitting in the transcript with no reply coming and no way for
	// the browser to tell. Saying "conflict" instead lets the client keep the text
	// in the composer for a retry once the reply lands.
	if s.chatSessions.IsBusy(id) {
		writeError(w, http.StatusConflict, errors.New("this session is still working on its previous message"))
		return
	}
	if req.Settings != nil {
		s.chatSessions.MergeSettings(id, *req.Settings)
	}
	if model := strings.TrimSpace(req.Model); model != "" {
		s.chatSessions.SetModel(id, model)
	}
	if req.ReplaceLast {
		s.chatSessions.TrimAfterLastUser(id)
	}
	if req.EditLast {
		s.chatSessions.ReplaceLastUser(id, req.Content, req.Attachments)
	} else {
		s.chatSessions.AppendUser(id, req.Content, req.Attachments)
	}
	sum := s.chatSessions.Summary(id)
	if sum == nil {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	s.dispatchSessionTurn(id)
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "status": sum.Status})
}

// handleChatSessionCancel stops the turn in flight, leaving the session intact.
func (s *Server) handleChatSessionCancel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.chatSessions.Get(id) == nil {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	s.chatSessions.Cancel(id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleChatSessionSeen clears the white badge once the user has read the reply.
func (s *Server) handleChatSessionSeen(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.chatSessions.Get(id) == nil {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	s.chatSessions.MarkSeen(id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleChatEvents is the global badge feed: every session state change and
// every live stream event, so a closed tab still learns when work finishes.
func (s *Server) handleChatEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("streaming not supported"))
		return
	}
	writeSSEHeaders(w)
	send := func(event string, payload any) {
		buf, err := json.Marshal(payload)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "event: %s\n", event)
		fmt.Fprintf(w, "data: %s\n\n", buf)
		flusher.Flush()
	}
	send("snapshot", map[string]any{
		"sessions":     s.chatSessions.List(),
		"max_parallel": maxConcurrentChatSessions,
	})
	ch, cancel := s.chatSessions.Subscribe()
	defer cancel()
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			send("session", ev)
		case <-ticker.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// handleChatSessionEvents tails one session: it replays whatever was buffered
// after ?from= and then follows the live stream, so a tab opened mid-run catches
// up instead of starting from nothing.
func (s *Server) handleChatSessionEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess := s.chatSessions.Get(id)
	if sess == nil {
		writeError(w, http.StatusNotFound, errors.New("session not found"))
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("streaming not supported"))
		return
	}
	from := 0
	if v := r.URL.Query().Get("from"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			from = n
		}
	}
	writeSSEHeaders(w)
	send := func(event string, payload any) {
		buf, err := json.Marshal(payload)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "event: %s\n", event)
		fmt.Fprintf(w, "data: %s\n\n", buf)
		flusher.Flush()
	}

	// Subscribe before replaying so nothing produced in between is lost.
	ch, cancel := s.chatSessions.Subscribe()
	defer cancel()
	s.chatSessions.AddWatcher(id)
	defer s.chatSessions.RemoveWatcher(id)

	s.chatSessions.mu.Lock()
	replay := append([]SessionEvent(nil), sess.Events...)
	sum := summaryOf(sess)
	s.chatSessions.mu.Unlock()

	send("snapshot", map[string]any{
		"session": sum,
		"seq":     sess.Seq,
	})
	for _, ev := range replay {
		if ev.Seq <= from {
			continue
		}
		send(ev.Event, map[string]any{"seq": ev.Seq, "data": ev.Data})
	}

	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if ev.ID != id || ev.Kind != chatSessionEventStream {
				continue
			}
			send(ev.Event, map[string]any{"seq": ev.Seq, "data": ev.Data})
		case <-ticker.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}
