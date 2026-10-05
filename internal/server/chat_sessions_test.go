package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gense/ollama-manager/internal/ollama"
)

func newTestSessionStore(t *testing.T) *chatSessionStore {
	t.Helper()
	st := newChatSessionStore(t.TempDir())
	st.Load()
	return st
}

func TestChatSessionStorePersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	st := newChatSessionStore(dir)
	st.Load()

	sess := st.Create("llama3", SessionSettings{Temperature: 0.2, TopK: 40, TopP: 0.9, NumCtxPct: 50, ThinkLevel: "low", Artifacts: true})
	if sess == nil || sess.ID == "" {
		t.Fatal("Create returned no session")
	}
	if sess.Status != chatSessionIdle {
		t.Fatalf("new session status = %q, want %q", sess.Status, chatSessionIdle)
	}
	if len(st.List()) != 1 {
		t.Fatalf("List() len = %d, want 1", len(st.List()))
	}

	if !st.AppendUser(sess.ID, "build me a todo app", []ChatAttach{{Kind: "text", Name: "spec.md", Text: "hello"}}) {
		t.Fatal("AppendUser returned false")
	}

	srv := &Server{chatSessions: st}
	if !srv.beginTurn(sess.ID, time.Now()) {
		t.Fatal("beginTurn returned false")
	}
	sink := &sessionSink{srv: srv, id: sess.ID, started: time.Now()}
	sink.Send("chunk", ollama.ChatChunk{Message: ollama.ChatMessage{Role: "assistant", Content: "Sure"}})
	sink.Send("chunk", ollama.ChatChunk{Message: ollama.ChatMessage{Role: "assistant", Thinking: "hmm"}})
	sink.Send("tool", map[string]any{"phase": "start", "name": "create_artifact", "description": "app"})
	sink.Send("tool", map[string]any{"phase": "done", "name": "create_artifact", "ok": true})
	sink.Send("artifact", map[string]any{"timestamp": "2026-10-04_10-00-00", "name": "app", "url": "/api/artifacts/x/index.html"})
	sink.Send("chunk", ollama.ChatChunk{Message: ollama.ChatMessage{Role: "assistant", Content: " done"}})
	sink.Send("done", map[string]any{"elapsed_ms": 1234, "total_tokens": 42, "done_reason": "stop"})
	srv.finishSession(sess.ID, time.Now())

	got := st.Get(sess.ID)
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Status != chatSessionIdle {
		t.Errorf("status = %q, want idle", got.Status)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("messages len = %d, want 2 (user + assistant)", len(got.Messages))
	}
	asst := got.Messages[1]
	if asst.Role != "assistant" || asst.Pending {
		t.Fatalf("assistant msg = %+v, want role=assistant pending=false", asst)
	}
	if asst.Content != "Sure done" {
		t.Errorf("content = %q, want %q", asst.Content, "Sure done")
	}
	if asst.Think != "hmm" {
		t.Errorf("think = %q, want %q", asst.Think, "hmm")
	}
	if !strings.Contains(asst.Raw, "<think>") {
		t.Errorf("raw should keep the think fences the browser replays, got %q", asst.Raw)
	}
	if len(asst.ToolLog) != 1 || asst.ToolLog[0].Name != "create_artifact" {
		t.Fatalf("tool_log = %+v, want one create_artifact entry", asst.ToolLog)
	}
	if asst.ArtifactNm != "app" || asst.ArtifactURL == "" || asst.ArtifactTS == "" {
		t.Errorf("artifact fields not stored: %+v", asst)
	}
	if asst.ElapsedMs != 1234 || asst.DoneReason != "stop" {
		t.Errorf("done stats not stored: elapsed=%d reason=%q", asst.ElapsedMs, asst.DoneReason)
	}
	// finishSession drops the replay log on purpose: Messages is the transcript
	// once a turn settles, so only Seq has to survive the reload.
	if got.Seq == 0 {
		t.Error("Seq = 0, want the event counter to keep counting")
	}
	if len(got.Events) != 0 {
		t.Errorf("Events = %d, want the replay log dropped once the turn settled", len(got.Events))
	}

	st.flush(sess.ID)

	// Reload from disk and make sure the transcript survived.
	st2 := newChatSessionStore(dir)
	st2.Load()
	reloaded := st2.Get(sess.ID)
	if reloaded == nil {
		t.Fatal("session missing after reload")
	}
	if len(reloaded.Messages) != 2 {
		t.Fatalf("reloaded messages len = %d, want 2", len(reloaded.Messages))
	}
	if reloaded.Messages[1].Content != "Sure done" {
		t.Errorf("reloaded content = %q", reloaded.Messages[1].Content)
	}
	if reloaded.Settings.NumCtxPct != 50 || !reloaded.Settings.Artifacts {
		t.Errorf("settings not persisted: %+v", reloaded.Settings)
	}
	if _, err := os.Stat(filepath.Join(dir, sess.ID+".json")); err != nil {
		t.Errorf("session file missing: %v", err)
	}
}

func TestChatSessionLoadRewritesRunningOrphans(t *testing.T) {
	dir := t.TempDir()
	raw := ChatSession{
		ID:     "cs-orphan",
		Model:  "llama3",
		Status: chatSessionRunning,
		Messages: []SessionMessage{
			{Role: "user", Content: "hi", CreatedAt: time.Now()},
			{Role: "assistant", Pending: true, CreatedAt: time.Now()},
		},
	}
	if err := writeJSONFileAtomic(filepath.Join(dir, raw.ID+".json"), &raw, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	st := newChatSessionStore(dir)
	st.Load()
	got := st.Get(raw.ID)
	if got == nil {
		t.Fatal("orphan session not loaded")
	}
	if got.Status != chatSessionError {
		t.Errorf("status = %q, want %q", got.Status, chatSessionError)
	}
	if got.Messages[1].Pending {
		t.Error("pending flag should be cleared for an interrupted turn")
	}
	if got.Error == "" {
		t.Error("expected an interruption error message")
	}
}

func TestChatSessionSeenAndDelete(t *testing.T) {
	st := newTestSessionStore(t)
	sess := st.Create("m", SessionSettings{})
	st.AppendUser(sess.ID, "hi", nil)

	ch, cancel := st.Subscribe()
	defer cancel()

	// MarkSeen is a no-op (and reports false) when nothing is pending, then
	// flips Unseen off for real.
	if st.MarkSeen(sess.ID) {
		t.Error("MarkSeen should report false when the session is already seen")
	}
	st.mu.Lock()
	st.sessions[sess.ID].Unseen = true
	st.mu.Unlock()
	if !st.MarkSeen(sess.ID) {
		t.Fatal("MarkSeen returned false on an unseen session")
	}
	if st.Get(sess.ID).Unseen {
		t.Error("MarkSeen should clear Unseen")
	}

	if !st.Delete(sess.ID) {
		t.Fatal("Delete returned false")
	}
	if st.Get(sess.ID) != nil {
		t.Error("session still present after Delete")
	}
	if len(st.List()) != 0 {
		t.Errorf("List() len = %d, want 0", len(st.List()))
	}
	if _, err := os.Stat(st.pathFor(sess.ID)); !os.IsNotExist(err) {
		t.Errorf("session file still on disk: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Kind != chatSessionRemove {
				continue // an update from MarkSeen; keep waiting for the removal
			}
			if ev.ID != sess.ID {
				t.Errorf("remove event for %q, want %s", ev.ID, sess.ID)
			}
			return
		case <-deadline:
			t.Fatal("no remove event broadcast")
		}
	}
}

func TestChatSessionQueuesBeyondConcurrencyLimit(t *testing.T) {
	st := newTestSessionStore(t)
	release := make(chan struct{})
	started := make(chan string, 3)
	var ids []string
	for i := 0; i < 3; i++ {
		id := st.Create("m", SessionSettings{}).ID
		ids = append(ids, id)
	}
	for _, id := range ids {
		id := id
		st.Start(id, func() {
			started <- id
			<-release
			st.releaseRunning(id)
		})
	}

	for i := 0; i < maxConcurrentChatSessions; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d of %d runs started", i, maxConcurrentChatSessions)
		}
	}
	select {
	case id := <-started:
		t.Fatalf("more than %d runs started concurrently (got %s)", maxConcurrentChatSessions, id)
	case <-time.After(200 * time.Millisecond):
	}

	queued := 0
	for _, s := range st.List() {
		if s.Status == chatSessionQueued {
			queued++
		}
	}
	if queued != 1 {
		t.Fatalf("queued sessions = %d, want 1", queued)
	}

	// Releasing a slot must pump the queued turn.
	close(release)
	select {
	case id := <-started:
		if id == ids[0] || id == ids[1] {
			t.Fatalf("a finished slot was started twice (%s)", id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the queued turn never started after a slot freed up")
	}
}

func TestSessionSinkBumpsUnseenWhenNotWatched(t *testing.T) {
	st := newTestSessionStore(t)
	srv := &Server{chatSessions: st}

	sess := st.Create("m", SessionSettings{})
	st.AppendUser(sess.ID, "hi", nil)
	started := time.Now()
	srv.beginTurn(sess.ID, started)
	sink := &sessionSink{srv: srv, id: sess.ID, started: started}
	sink.Send("done", map[string]any{"elapsed_ms": 5, "total_tokens": 1})
	srv.finishSession(sess.ID, started)

	got := st.Get(sess.ID)
	if got.Status != chatSessionIdle {
		t.Errorf("status = %q, want idle", got.Status)
	}
	if !got.Unseen {
		t.Error("expected Unseen = true when nobody was watching")
	}

	// With a watcher attached the same run must stay seen.
	sess2 := st.Create("m", SessionSettings{})
	st.AppendUser(sess2.ID, "hi", nil)
	st.AddWatcher(sess2.ID)
	started2 := time.Now()
	srv.beginTurn(sess2.ID, started2)
	sink2 := &sessionSink{srv: srv, id: sess2.ID, started: started2}
	sink2.Send("done", map[string]any{"elapsed_ms": 5})
	srv.finishSession(sess2.ID, started2)
	st.RemoveWatcher(sess2.ID)
	if st.Get(sess2.ID).Unseen {
		t.Error("expected Unseen = false while a watcher was attached")
	}
}

func TestBuildSessionMessages(t *testing.T) {
	st := newTestSessionStore(t)
	srv := &Server{chatSessions: st}
	sess := st.Create("m", SessionSettings{})
	st.AppendUser(sess.ID, "first", nil)
	srv.beginTurn(sess.ID, time.Now())
	// A still-pending assistant turn must never reach the model.
	st.AppendUser(sess.ID, "second", []ChatAttach{
		{Kind: "text", Name: "a.txt", Text: "body"},
		{Kind: "image", Data: "data:image/png;base64,AAA"},
	})

	msgs := st.buildSessionMessages(st.Get(sess.ID), sessionModelInfo{})
	if len(msgs) != 2 {
		t.Fatalf("messages len = %d, want 2 (pending turn must be skipped)", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "first" {
		t.Errorf("msgs[0] = %+v", msgs[0])
	}
	if len(msgs[1].Images) != 1 {
		t.Errorf("expected the image attachment to be forwarded, got %+v", msgs[1].Images)
	}
	if !strings.Contains(msgs[1].Content, "--- a.txt ---") {
		t.Errorf("text attachment not appended: %q", msgs[1].Content)
	}
}

func TestBuildSessionMessagesPlaceholderForEmptyArtifactAnswer(t *testing.T) {
	st := newTestSessionStore(t)
	srv := &Server{chatSessions: st}
	sess := st.Create("m", SessionSettings{})
	srv.beginTurn(sess.ID, time.Now())
	srv.finishSession(sess.ID, time.Now())
	// Mutate in place: Messages is a slice of values, so a copy would not stick.
	st.Get(sess.ID).Messages[0].Content = ""
	st.Get(sess.ID).Messages[0].ArtifactNm = "todo-app"
	st.Get(sess.ID).Messages[0].ArtifactURL = "/api/artifacts/x/index.html"

	msgs := st.buildSessionMessages(st.Get(sess.ID), sessionModelInfo{})
	if len(msgs) != 1 {
		t.Fatalf("messages len = %d, want 1", len(msgs))
	}
	if !strings.Contains(msgs[0].Content, "todo-app") {
		t.Errorf("placeholder = %q, want it to mention the artifact", msgs[0].Content)
	}
}

func TestSessionEventReplayLogIsCapped(t *testing.T) {
	st := newTestSessionStore(t)
	srv := &Server{chatSessions: st}
	sess := st.Create("m", SessionSettings{})
	st.AppendUser(sess.ID, "hi", nil)
	srv.beginTurn(sess.ID, time.Now())
	sink := &sessionSink{srv: srv, id: sess.ID, started: time.Now()}
	for i := 0; i < maxSessionEvents+50; i++ {
		sink.Send("chunk", ollama.ChatChunk{Message: ollama.ChatMessage{Role: "assistant", Content: "x"}})
	}
	got := st.Get(sess.ID)
	if len(got.Events) > maxSessionEvents {
		t.Fatalf("events len = %d, want <= %d", len(got.Events), maxSessionEvents)
	}
	if got.Seq < maxSessionEvents {
		t.Errorf("seq = %d, want >= %d", got.Seq, maxSessionEvents)
	}
}

func TestSessionSummaryOmitsTranscript(t *testing.T) {
	st := newTestSessionStore(t)
	sess := st.Create("m", SessionSettings{})
	st.AppendUser(sess.ID, "hi", nil)
	sum := st.Summary(sess.ID)
	if sum == nil {
		t.Fatal("Summary returned nil")
	}
	buf, _ := json.Marshal(sum)
	if strings.Contains(string(buf), "\"messages\":[") {
		t.Errorf("summary should not carry the transcript: %s", buf)
	}
	if sum.Messages != 1 {
		t.Errorf("summary message count = %d, want 1", sum.Messages)
	}
}

func TestCancelMarksSessionCancelled(t *testing.T) {
	st := newTestSessionStore(t)
	sess := st.Create("m", SessionSettings{})
	release := make(chan struct{})
	started := make(chan struct{})
	st.Start(sess.ID, func() {
		close(started)
		<-release
		st.releaseRunning(sess.ID)
	})
	<-started

	if !st.Cancel(sess.ID) {
		t.Fatal("Cancel returned false")
	}
	if !st.Get(sess.ID).Cancelled {
		t.Error("expected Cancelled = true")
	}
	close(release)
}

func TestBuildSessionBodyDropsToolsWhenModelHasNone(t *testing.T) {
	st := newTestSessionStore(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"model not found"}`, http.StatusNotFound)
	}))
	defer ts.Close()
	srv := &Server{chatSessions: st, ollama: ollama.New(ts.URL)}

	sess := st.Create("some-model", SessionSettings{WebTools: true, Artifacts: true})
	st.AppendUser(sess.ID, "hi", nil)

	// A session keeps the options it was created with, so a model that cannot
	// call tools has to drop them rather than fail every round.
	body := srv.buildSessionBody(context.Background(), st.Get(sess.ID), sessionModelInfo{}, nil)
	if body.WebTools != nil || body.Artifacts != nil {
		t.Errorf("tools should be dropped: web=%v artifacts=%v", body.WebTools, body.Artifacts)
	}
}

func TestBuildSessionBodyDropsThinkWhenModelCannotThink(t *testing.T) {
	st := newTestSessionStore(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"model not found"}`, http.StatusNotFound)
	}))
	defer ts.Close()
	srv := &Server{chatSessions: st, ollama: ollama.New(ts.URL)}

	// think_level defaults to "auto", which travels as a hard think:true and
	// makes Ollama answer 400 for a model without the capability. A detached turn
	// has no browser around to have hidden the control, so the guard lives here.
	sess := st.Create("no-thinker", SessionSettings{ThinkLevel: "auto"})
	st.AppendUser(sess.ID, "hi", nil)

	body := srv.buildSessionBody(context.Background(), st.Get(sess.ID), sessionModelInfo{CanTools: true}, nil)
	if body.Think != nil {
		t.Errorf("think should be omitted for a model that cannot think, got %q", *body.Think)
	}

	// The same settings on a model that does think must keep the level.
	body = srv.buildSessionBody(context.Background(), st.Get(sess.ID), sessionModelInfo{CanTools: true, CanThink: true}, nil)
	if body.Think == nil {
		t.Fatal("think should be sent when the model supports it")
	}
	if got := string(*body.Think); got != "auto" {
		t.Errorf("think level = %q, want %q", got, "auto")
	}
}

func TestSessionSettingsMergeKeepsUnsentOptions(t *testing.T) {
	st := newTestSessionStore(t)
	sess := st.Create("m", SessionSettings{
		System:      "keep me",
		Temperature: 0.7,
		TopK:        40,
		TopP:        0.9,
		NumCtxPct:   100,
		ThinkLevel:  "auto",
		WebTools:    true,
		Artifacts:   true,
		ImageWidth:  512,
		ImageHeight: 512,
		ImageSteps:  4,
		ImageSeed:   0,
	})

	// The browser sends every option it knows about, so a field it omits must
	// keep the value the session already had.
	st.MergeSettings(sess.ID, sessionSettingsInput{Temperature: 0.2})

	got := st.Get(sess.ID).Settings
	if got.Temperature != 0.2 {
		t.Errorf("temperature = %v, want 0.2", got.Temperature)
	}
	if got.System != "keep me" {
		t.Errorf("system = %q, want it preserved", got.System)
	}
	if !got.WebTools || !got.Artifacts {
		t.Errorf("toggles should survive a partial payload: %+v", got)
	}
	if got.TopK != 40 || got.TopP != 0.9 || got.NumCtxPct != 100 {
		t.Errorf("numeric options lost: %+v", got)
	}

	// An explicit false must still switch a toggle off.
	st.MergeSettings(sess.ID, sessionSettingsInput{WebTools: false})
	if st.Get(sess.ID).Settings.WebTools {
		t.Error("explicit web_tools=false should turn the toggle off")
	}
}

// A session carries the configuration of its last input, so a message sent
// after the user edits the system prompt or moves a slider must overwrite what
// came before, and both must survive a reload from disk.
func TestSessionAdoptsLastInputConfig(t *testing.T) {
	st := newTestSessionStore(t)
	sess := st.Create("model-a", SessionSettings{Temperature: 0.7, TopK: 40, NumCtxPct: 100})

	st.MergeSettings(sess.ID, sessionSettingsInput{
		System:      "be terse",
		Temperature: 0.2,
		TopK:        10,
		TopP:        0.5,
		NumCtxPct:   25,
		ThinkLevel:  "low",
		Artifacts:   true,
	})
	if !st.SetModel(sess.ID, "model-b") {
		t.Fatal("SetModel on an existing session should succeed")
	}
	st.flush(sess.ID)

	reloaded := newChatSessionStore(st.dir)
	reloaded.Load()
	got := reloaded.Get(sess.ID)
	if got == nil {
		t.Fatal("session did not survive a reload")
	}
	if got.Model != "model-b" {
		t.Errorf("model = %q, want model-b", got.Model)
	}
	want := SessionSettings{
		System:      "be terse",
		Temperature: 0.2,
		TopK:        10,
		TopP:        0.5,
		NumCtxPct:   25,
		ThinkLevel:  "low",
		Artifacts:   true,
	}
	if got.Settings != want {
		t.Errorf("settings = %+v, want %+v", got.Settings, want)
	}

	// Setting the same model again is a no-op rather than a broadcast storm.
	if !st.SetModel(sess.ID, "model-b") {
		t.Error("SetModel with an unchanged model should still report success")
	}
	if st.SetModel("missing", "model-c") {
		t.Error("SetModel on an unknown session should fail")
	}
}

func TestBuildSessionBodyUsesSavedSettings(t *testing.T) {
	st := newTestSessionStore(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"model not found"}`, http.StatusNotFound)
	}))
	defer ts.Close()
	srv := &Server{chatSessions: st, ollama: ollama.New(ts.URL)}

	sess := st.Create("some-model", SessionSettings{
		Temperature: 0.11,
		TopK:        17,
		TopP:        0.55,
		NumCtxPct:   25,
		ThinkLevel:  "high",
		WebTools:    true,
		Artifacts:   true,
	})
	st.AppendUser(sess.ID, "hi", nil)

	body := srv.buildSessionBody(context.Background(), st.Get(sess.ID),
		sessionModelInfo{CanTools: true, CanThink: true}, st.sessionWatcherCheck(sess.ID))

	if body.Model != "some-model" {
		t.Errorf("model = %q", body.Model)
	}
	if body.browserToolsAllowed() {
		t.Error("an unwatched session must not offer the browser-only artifact tools")
	}
	if body.WebTools == nil || !*body.WebTools {
		t.Error("web tools should be enabled from the saved settings")
	}
	if body.Artifacts == nil || !*body.Artifacts {
		t.Error("artifacts should be enabled from the saved settings")
	}
	if body.Options["temperature"] != 0.11 || body.Options["top_k"] != 17 || body.Options["top_p"] != 0.55 {
		t.Errorf("options = %+v", body.Options)
	}
	if body.Think == nil || *body.Think != ollama.ThinkLevel("high") {
		t.Errorf("think = %v, want high", body.Think)
	}
	if len(body.Messages) != 1 {
		t.Errorf("messages = %d, want 1", len(body.Messages))
	}
}

func TestNormalizeSessionNumCtxPct(t *testing.T) {
	for _, in := range []int{0, 10, 25, 33, 50, 75, 100, 150} {
		got := normalizeSessionNumCtxPct(in)
		switch got {
		case 10, 25, 50, 75, 100:
		default:
			t.Errorf("normalizeSessionNumCtxPct(%d) = %d, not a preset", in, got)
		}
	}
	if normalizeSessionNumCtxPct(0) != 100 {
		t.Error("an unknown percentage should fall back to the model default")
	}
	if sessionNumCtxTokens(100, 8192) != 0 {
		t.Error("100% should mean \"let the model decide\" (0 tokens)")
	}
	if got := sessionNumCtxTokens(50, 8192); got != 4096 {
		t.Errorf("50%% of 8192 = %d, want 4096", got)
	}
	if got := sessionNumCtxTokens(50, 0); got != 16384 {
		t.Errorf("50%% of the 32768 fallback = %d, want 16384", got)
	}
}

func TestDeleteByModelOnlyRemovesThatModelsSessions(t *testing.T) {
	st := newTestSessionStore(t)
	a := st.Create("model-a", defaultSessionSettings())
	b := st.Create("model-b", defaultSessionSettings())
	c := st.Create("model-a", defaultSessionSettings())
	st.flush(a.ID)
	st.flush(b.ID)
	st.flush(c.ID)

	if n := st.DeleteByModel("model-a"); n != 2 {
		t.Fatalf("DeleteByModel(model-a) = %d, want 2", n)
	}
	if st.Get(a.ID) != nil || st.Get(c.ID) != nil {
		t.Error("the model-a sessions should be gone")
	}
	if st.Get(b.ID) == nil {
		t.Error("the model-b session should have been left alone")
	}
	if n := st.DeleteByModel("nobody"); n != 0 {
		t.Errorf("DeleteByModel(nobody) = %d, want 0", n)
	}

	// The files have to go too, otherwise a restart would resurrect them.
	st2 := newChatSessionStore(st.dir)
	st2.Load()
	if got := len(st2.List()); got != 1 {
		t.Errorf("after reload there are %d sessions, want 1", got)
	}
}

func TestDeleteAllClearsEverySessionAndItsFiles(t *testing.T) {
	st := newTestSessionStore(t)
	for i := 0; i < 3; i++ {
		sess := st.Create("model-a", defaultSessionSettings())
		st.flush(sess.ID)
	}
	// A stray file that no longer has a session: DeleteAll sweeps those too,
	// otherwise it would come back on the next restart.
	stray := filepath.Join(st.dir, "cs-9999999999999-9.json")
	if err := os.WriteFile(stray, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write stray file: %v", err)
	}

	if n := st.DeleteAll(); n != 3 {
		t.Fatalf("DeleteAll = %d, want 3", n)
	}
	if len(st.List()) != 0 {
		t.Error("no session should be left in memory")
	}
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Error("the stray session file should have been swept")
	}

	st2 := newChatSessionStore(st.dir)
	st2.Load()
	if got := len(st2.List()); got != 0 {
		t.Errorf("after reload there are %d sessions, want 0", got)
	}
}

func TestCancelAllStopsRunningAndQueuedTurns(t *testing.T) {
	st := newTestSessionStore(t)
	started := make(chan string, 3)
	stop := make(chan struct{})
	sess := st.Create("model-a", defaultSessionSettings())

	// run stands in for dispatchSessionTurn: it binds a cancel func so Cancel has
	// something to call, and releases the slot on the way out the way
	// runSessionTurn does through finishSession.
	run := func(id string) func() {
		return func() {
			_, release := context.WithCancel(context.Background())
			st.bindRunning(id, release)
			started <- id
			<-stop
			st.releaseRunning(id)
		}
	}
	// Three separate sessions: the first two fill the concurrency slots and the
	// third has to wait in the queue. Reusing one id would just be rejected by
	// the one-turn-per-session guard.
	ids := []string{sess.ID}
	for i := 0; i < 2; i++ {
		ids = append(ids, st.Create("model-a", defaultSessionSettings()).ID)
	}
	for _, id := range ids {
		st.Start(id, run(id))
	}

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("the first turn never started")
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("the second turn never started")
	}
	if !st.HasBusy() {
		t.Fatal("two turns plus one queued means the store is busy")
	}

	if n := st.CancelAll(); n != 3 {
		t.Fatalf("CancelAll = %d, want 3", n)
	}
	close(stop)
	time.Sleep(200 * time.Millisecond)

	if st.HasBusy() {
		t.Error("nothing should be busy after CancelAll")
	}
	if got := st.Summary(sess.ID); got == nil || got.Status == chatSessionRunning {
		t.Errorf("the first session should have settled, got %+v", got)
	}
}

func TestTrimMessagesKeepsAnchorAndCountsDropped(t *testing.T) {
	st := newTestSessionStore(t)
	sess := st.Create("model-a", defaultSessionSettings())
	for i := 0; i < maxSessionMessages+10; i++ {
		if !st.AppendUser(sess.ID, "turn", nil) {
			t.Fatalf("AppendUser failed on turn %d", i)
		}
	}

	got := st.Get(sess.ID)
	if len(got.Messages) > maxSessionMessages {
		t.Fatalf("transcript kept %d messages, want at most %d", len(got.Messages), maxSessionMessages)
	}
	if len(got.Messages) == 0 {
		t.Fatal("trimming everything away would leave an unusable session")
	}
	if got.Messages[0].Content != "turn" {
		t.Errorf("the first message is %q, want the anchor to survive", got.Messages[0].Content)
	}
	if got.DroppedMessages == 0 {
		t.Error("dropped messages should be reported so the UI can say so")
	}
	if sum := st.Summary(sess.ID); sum == nil || sum.DroppedMessages != got.DroppedMessages {
		t.Errorf("summary.DroppedMessages = %+v, want %d", sum, got.DroppedMessages)
	}
}

func TestTrimMessagesTruncatesRunawayMessage(t *testing.T) {
	st := newTestSessionStore(t)
	sess := st.Create("model-a", defaultSessionSettings())
	st.AppendUser(sess.ID, "go", nil)

	huge := strings.Repeat("x", maxSessionMessageRunes+5000)
	st.Get(sess.ID).Messages = append(st.Get(sess.ID).Messages, SessionMessage{
		Role:    "assistant",
		Content: huge,
		Raw:     huge,
	})
	st.trimMessagesLocked(st.Get(sess.ID))

	got := st.Get(sess.ID)
	last := got.Messages[len(got.Messages)-1]
	if !last.Truncated {
		t.Error("an oversized message should be flagged as truncated")
	}
	if len(last.Content) >= len(huge) {
		t.Errorf("content is still %d runes, want it cut", len(last.Content))
	}
	if last.Raw != "" {
		t.Error("the raw stream should be dropped, it is what makes these files huge")
	}
}

// attachmentBase64 builds a bare base64 payload of the given size, which is what
// the browser actually puts in ChatAttach.Data: it strips the data URL prefix
// when it reads a file (web/app-svg.js splits on ",").
func attachmentBase64(size int) string {
	raw := make([]byte, size)
	for i := range raw {
		raw[i] = byte(i % 251)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// attachmentDataURL is the same payload wrapped in a data URL, for the clients
// that send one.
func attachmentDataURL(mime string, size int) string {
	return "data:" + mime + ";base64," + attachmentBase64(size)
}

func TestSessionAttachmentsLiveOnDiskNotInTheJSON(t *testing.T) {
	st := newTestSessionStore(t)
	sess := st.Create("m", defaultSessionSettings())

	payload := attachmentBase64(4096)
	if !st.AppendUser(sess.ID, "look", []ChatAttach{{Kind: "image", Name: "a.png", MimeType: "image/png", Data: payload}}) {
		t.Fatal("AppendUser should have accepted the message")
	}
	st.flush(sess.ID)

	// The stored transcript must not carry the base64 payload any more.
	stored := st.Get(sess.ID)
	if len(stored.Messages) != 1 || len(stored.Messages[0].Attach) != 1 {
		t.Fatalf("expected one message with one attachment, got %#v", stored.Messages)
	}
	got := stored.Messages[0].Attach[0]
	if got.Data != "" {
		t.Error("the attachment bytes should have been moved out of the stored message")
	}
	if got.Blob == "" {
		t.Fatal("the attachment should reference a stored blob")
	}
	if got.Size != 4096 {
		t.Errorf("size = %d, want 4096", got.Size)
	}
	if _, err := os.Stat(filepath.Join(st.chatSessionBlobDir(), got.Blob)); err != nil {
		t.Errorf("blob file missing: %v", err)
	}

	// The file on disk must be small: that was the whole point.
	info, err := os.Stat(st.pathFor(sess.ID))
	if err != nil {
		t.Fatalf("stat session file: %v", err)
	}
	if info.Size() > 4096 {
		t.Errorf("session file is %d bytes; the attachment should not be inline any more", info.Size())
	}

	// And the bytes have to come back byte for byte.
	reloaded := newChatSessionStore(st.dir)
	reloaded.Load()
	back := reloaded.Get(sess.ID)
	if back == nil {
		t.Fatal("the session should survive a reload")
	}
	// A reloaded store holds the reference only, so it is hydration that has to
	// bring the bytes back, byte for byte.
	hydrated := reloaded.hydrateAttach(back.Messages[0].Attach)[0]
	if hydrated.Data != payload {
		t.Error("hydrating the attachment did not restore the original base64 payload")
	}
	// Hydration must not write the payload back into the stored message.
	if reloaded.Get(sess.ID).Messages[0].Attach[0].Data != "" {
		t.Error("hydration leaked the bytes back into the stored transcript")
	}
}

func TestBuildSessionMessagesRehydratesImages(t *testing.T) {
	st := newTestSessionStore(t)
	sess := st.Create("m", defaultSessionSettings())
	payload := attachmentBase64(512)
	st.AppendUser(sess.ID, "what is this", []ChatAttach{{Kind: "image", MimeType: "image/png", Data: payload}})

	msgs := st.buildSessionMessages(st.Get(sess.ID), sessionModelInfo{})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if len(msgs[0].Images) != 1 || msgs[0].Images[0] != payload {
		t.Errorf("the image never made it into the prompt: %#v", msgs[0].Images)
	}
}

func TestDeletingASessionRemovesItsBlobs(t *testing.T) {
	st := newTestSessionStore(t)
	sess := st.Create("m", defaultSessionSettings())
	st.AppendUser(sess.ID, "keep", []ChatAttach{{Kind: "image", MimeType: "image/png", Data: attachmentBase64(256)}})
	other := st.Create("m", defaultSessionSettings())
	st.AppendUser(other.ID, "stays", []ChatAttach{{Kind: "image", MimeType: "image/png", Data: attachmentBase64(256)}})

	blobDir := st.chatSessionBlobDir()
	before, _ := os.ReadDir(blobDir)
	if len(before) != 2 {
		t.Fatalf("expected 2 blob files, got %d", len(before))
	}

	st.Delete(sess.ID)

	after, _ := os.ReadDir(blobDir)
	if len(after) != 1 {
		t.Fatalf("expected 1 blob file left for the surviving session, got %d", len(after))
	}
	if !strings.HasPrefix(after[0].Name(), other.ID+"-") {
		t.Errorf("the wrong file survived: %q", after[0].Name())
	}
}

func TestDeleteAllRemovesEveryBlob(t *testing.T) {
	st := newTestSessionStore(t)
	for i := 0; i < 3; i++ {
		s := st.Create("m", defaultSessionSettings())
		st.AppendUser(s.ID, "x", []ChatAttach{{Kind: "image", MimeType: "image/png", Data: attachmentBase64(128)}})
	}
	blobDir := st.chatSessionBlobDir()
	if before, _ := os.ReadDir(blobDir); len(before) != 3 {
		t.Fatalf("expected 3 blob files, got %d", len(before))
	}

	if n := st.DeleteAll(); n != 3 {
		t.Errorf("DeleteAll reported %d sessions, want 3", n)
	}
	if after, _ := os.ReadDir(blobDir); len(after) != 0 {
		t.Errorf("clear all left %d attachment file(s) behind", len(after))
	}
}

func TestTrimReclaimsBlobsOfDroppedMessages(t *testing.T) {
	st := newTestSessionStore(t)
	sess := st.Create("m", defaultSessionSettings())
	// One turn past the cap so the oldest gets dropped.
	for i := 0; i < maxSessionMessages+2; i++ {
		if !st.AppendUser(sess.ID, "turn", []ChatAttach{{Kind: "image", MimeType: "image/png", Data: attachmentBase64(64)}}) {
			t.Fatalf("AppendUser failed at %d", i)
		}
	}
	if got := st.Get(sess.ID).DroppedMessages; got == 0 {
		t.Fatal("expected messages to have been trimmed")
	}

	// Every surviving turn still has its image, and no unreferenced file is left.
	msgs := st.Get(sess.ID).Messages
	live := map[string]bool{}
	for i := range msgs {
		if len(msgs[i].Attach) != 1 {
			t.Fatalf("message %d lost its attachment reference", i)
		}
		live[msgs[i].Attach[0].Blob] = true
	}
	entries, _ := os.ReadDir(st.chatSessionBlobDir())
	if len(entries) != len(live) {
		t.Errorf("%d blob file(s) on disk but %d live reference(s)", len(entries), len(live))
	}
	for _, e := range entries {
		if !live[e.Name()] {
			t.Errorf("orphaned attachment file %q was not reclaimed", e.Name())
		}
	}
}

func TestDecodeAttachData(t *testing.T) {
	// Bare base64 is the normal case and carries no mime of its own.
	mime, body, ok := decodeAttachData(attachmentBase64(32))
	if !ok {
		t.Fatal("bare base64 should decode")
	}
	if mime != "" {
		t.Errorf("mime = %q, want empty: bare base64 has no media type", mime)
	}
	if len(body) != 32 {
		t.Errorf("decoded %d bytes, want 32", len(body))
	}

	// A data URL also decodes, and does hand back its media type.
	mime, body, ok = decodeAttachData(attachmentDataURL("image/png", 32))
	if !ok {
		t.Fatal("a base64 data URL should decode")
	}
	if mime != "image/png" {
		t.Errorf("mime = %q, want image/png", mime)
	}
	if len(body) != 32 {
		t.Errorf("decoded %d bytes, want 32", len(body))
	}

	// Percent-encoded payloads are legal data URLs too.
	if _, body, ok := decodeAttachData("data:text/plain,a%20b"); !ok || string(body) != "a b" {
		t.Errorf("percent-encoded payload: ok=%v body=%q", ok, body)
	}

	// Undecodable input must be reported as such rather than stored as garbage.
	if _, _, ok := decodeAttachData("/not/base64/at/all!"); ok {
		t.Error("a plain path should not decode")
	}
	if _, _, ok := decodeAttachData(""); ok {
		t.Error("an empty attachment should not decode")
	}
}

// TestSessionHydrateReturnsBareBase64 pins the format the browser gets back. It
// rebuilds the data URL itself from the mime type, so a rehydrated attachment
// that carries its own "data:" prefix would render as a double prefix.
func TestSessionHydrateReturnsBareBase64(t *testing.T) {
	st := newTestSessionStore(t)
	sess := st.Create("m", defaultSessionSettings())
	payload := attachmentBase64(2048)
	st.AppendUser(sess.ID, "look", []ChatAttach{{Kind: "image", Name: "a.png", MimeType: "image/png", Data: payload}})

	got := st.buildSessionMessages(st.Get(sess.ID), sessionModelInfo{})
	var images []string
	for _, m := range got {
		images = append(images, m.Images...)
	}
	if len(images) != 1 {
		t.Fatalf("built %d images, want 1", len(images))
	}
	if images[0] != payload {
		t.Errorf("image payload came back as %.40q..., want the bare base64 the browser sent", images[0])
	}
	if strings.Contains(images[0], "data:") {
		t.Error("rehydrated image must be bare base64, not a data URL")
	}
}

func TestBrowserToolsFollowTheWatcherLive(t *testing.T) {
	st := newTestSessionStore(t)
	sess := st.Create("m", defaultSessionSettings())
	check := st.sessionWatcherCheck(sess.ID)

	if check() {
		t.Fatal("a session nobody opened must not report a watcher")
	}
	st.AddWatcher(sess.ID)
	if !check() {
		t.Error("opening the session should make the browser-only tools available")
	}
	// The check is a function on purpose: a turn started unattended has to pick
	// the tools up as soon as a tab appears, without rebuilding the body.
	st.RemoveWatcher(sess.ID)
	if check() {
		t.Error("closing the session should take the browser-only tools away again")
	}

	// A session that no longer exists must not report a watcher either, or a
	// deleted session that gets its id reused could claim a stale panel.
	st.Delete(sess.ID)
	if check() {
		t.Error("a deleted session must not report a watcher")
	}
}

func TestBuildSessionBodyOffersBrowserToolsWhenWatched(t *testing.T) {
	st := newTestSessionStore(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"model not found"}`, http.StatusNotFound)
	}))
	defer ts.Close()
	srv := &Server{chatSessions: st, ollama: ollama.New(ts.URL)}

	sess := st.Create("m", SessionSettings{Artifacts: true})
	st.AddWatcher(sess.ID)
	body := srv.buildSessionBody(context.Background(), st.Get(sess.ID),
		sessionModelInfo{CanTools: true}, st.sessionWatcherCheck(sess.ID))
	if !body.browserToolsAllowed() {
		t.Error("a watched session should offer the browser-only artifact tools")
	}
}

// TestSessionSinkRecordsThinkDuration pins the thinking time so a restored
// transcript shows the same "thinking 2.1s" the live turn showed, instead of an
// empty "(0ms)" next to a full block of reasoning.
func TestSessionSinkRecordsThinkDuration(t *testing.T) {
	st := newTestSessionStore(t)
	srv := &Server{chatSessions: st}
	sess := st.Create("model-a", defaultSessionSettings())
	srv.beginTurn(sess.ID, time.Now())
	sink := &sessionSink{srv: srv, id: sess.ID, started: time.Now()}

	chunk := func(think, content string) ollama.ChatChunk {
		return ollama.ChatChunk{Message: ollama.ChatMessage{Role: "assistant", Thinking: think, Content: content}}
	}
	sink.Send("chunk", chunk("let me think", ""))
	// A real turn spends measurable time here; the test only needs a clock that
	// is not zero, so the wait is short but not instantaneous.
	time.Sleep(12 * time.Millisecond)
	sink.Send("chunk", chunk(" harder", ""))
	sink.Send("chunk", chunk("", "the answer"))
	sink.Send("done", map[string]any{"elapsed_ms": 40})

	got := st.Get(sess.ID).Messages
	last := got[len(got)-1]
	if last.ThinkMs < 10 {
		t.Errorf("ThinkMs = %d, want at least the ~12ms spent inside the think block", last.ThinkMs)
	}
	if last.Content != "the answer" {
		t.Errorf("Content = %q, want %q", last.Content, "the answer")
	}
	if !strings.Contains(last.Raw, "<think>") || !strings.Contains(last.Raw, "</think>") {
		t.Errorf("Raw = %q, want the think block still fenced for the client splitter", last.Raw)
	}
}

// TestSessionSinkRecordsThinkDurationAfterError makes sure an interrupted or
// failed turn still keeps the thinking time it already spent.
func TestSessionSinkRecordsThinkDurationAfterError(t *testing.T) {
	st := newTestSessionStore(t)
	srv := &Server{chatSessions: st}
	sess := st.Create("model-a", defaultSessionSettings())
	srv.beginTurn(sess.ID, time.Now())
	sink := &sessionSink{srv: srv, id: sess.ID, started: time.Now()}

	sink.Send("chunk", ollama.ChatChunk{Message: ollama.ChatMessage{
		Role: "assistant", Thinking: "half a thought", Content: "",
	}})
	time.Sleep(12 * time.Millisecond)
	sink.Send("error", map[string]any{"error": "ollama exploded"})

	got := st.Get(sess.ID).Messages
	last := got[len(got)-1]
	if last.ThinkMs < 10 {
		t.Errorf("ThinkMs = %d after an error, want the time already spent thinking", last.ThinkMs)
	}
	if last.Error != "ollama exploded" {
		t.Errorf("Error = %q, want the model failure to be kept", last.Error)
	}
}

// TestSessionSinkSumsInterleavedThinkBlocks covers a model that thinks, answers a
// few words, then thinks again. Each closed block has to add up instead of only
// the first one counting.
func TestSessionSinkSumsInterleavedThinkBlocks(t *testing.T) {
	st := newTestSessionStore(t)
	srv := &Server{chatSessions: st}
	sess := st.Create("model-a", defaultSessionSettings())
	srv.beginTurn(sess.ID, time.Now())
	sink := &sessionSink{srv: srv, id: sess.ID, started: time.Now()}

	send := func(think, content string) {
		sink.Send("chunk", ollama.ChatChunk{Message: ollama.ChatMessage{
			Role: "assistant", Thinking: think, Content: content,
		}})
	}
	send("first", "")
	time.Sleep(10 * time.Millisecond)
	send("", "answer ")
	send("second", "")
	time.Sleep(10 * time.Millisecond)
	sink.Send("done", map[string]any{})

	got := st.Get(sess.ID).Messages
	last := got[len(got)-1]
	// Two separate 10ms blocks, so the total must clear 20ms. Without summing,
	// only the first block would be counted and this would land under 20.
	if last.ThinkMs < 20 {
		t.Errorf("ThinkMs = %d, want at least 20ms from two separate think blocks", last.ThinkMs)
	}
	if last.Content != "answer " {
		t.Errorf("Content = %q, want the prose between the think blocks", last.Content)
	}
}

// TestSessionSinkRefencesInterleavedThinkBlocks guards the raw text the client
// replays through splitThink. A model that answers and then keeps thinking has to
// get a fresh <think> fence, or the second block is read back as part of the
// answer. This mirrors what the browser does live with its thinkBlockClosed flag,
// and it is why the server-side raw has to stay byte-identical to the browser's.
func TestSessionSinkRefencesInterleavedThinkBlocks(t *testing.T) {
	st := newTestSessionStore(t)
	srv := &Server{chatSessions: st}
	sess := st.Create("model-a", defaultSessionSettings())
	srv.beginTurn(sess.ID, time.Now())
	sink := &sessionSink{srv: srv, id: sess.ID, started: time.Now()}

	send := func(think, content string) {
		sink.Send("chunk", ollama.ChatChunk{Message: ollama.ChatMessage{
			Role: "assistant", Thinking: think, Content: content,
		}})
	}
	send("first thoughts", "")
	send("", "partial answer")
	send("second thoughts", "")
	send("", " final")
	sink.Send("done", map[string]any{})

	last := st.Get(sess.ID).Messages[len(st.Get(sess.ID).Messages)-1]
	if got := strings.Count(last.Raw, "<think>"); got != 2 {
		t.Errorf("Raw has %d <think> fences, want 2 so the replayed splitter sees two blocks:\n%q", got, last.Raw)
	}
	if got := strings.Count(last.Raw, "</think>"); got != 2 {
		t.Errorf("Raw has %d </think> fences, want 2:\n%q", got, last.Raw)
	}
	// The reconstructed answer must only hold prose, never the thinking text.
	if strings.Contains(last.Content, "thoughts") {
		t.Errorf("Content = %q, want the thinking kept out of the answer", last.Content)
	}
	if last.Content != "partial answer final" {
		t.Errorf("Content = %q, want the prose from both stretches", last.Content)
	}
	if last.Think != "first thoughtssecond thoughts" {
		t.Errorf("Think = %q, want both blocks concatenated", last.Think)
	}
}

// TestSessionSinkTrimsReplayChunkPayload keeps the persisted replay log small.
// A raw chunk repeats the model name and a timestamp per token, and nothing in a
// replay reads them.
func TestSessionSinkTrimsReplayChunkPayload(t *testing.T) {
	st := newTestSessionStore(t)
	srv := &Server{chatSessions: st}
	sess := st.Create("a-very-long-model-name:tag", defaultSessionSettings())
	srv.beginTurn(sess.ID, time.Now())
	sink := &sessionSink{srv: srv, id: sess.ID, started: time.Now()}

	sink.Send("chunk", ollama.ChatChunk{
		Model:     "a-very-long-model-name:tag",
		CreatedAt: time.Now(),
		Message:   ollama.ChatMessage{Role: "assistant", Content: "hi"},
		Done:      false,
	})

	ev := st.Get(sess.ID).Events
	if len(ev) != 1 {
		t.Fatalf("got %d events, want 1", len(ev))
	}
	raw, err := json.Marshal(ev[0].Data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "a-very-long-model-name") {
		t.Errorf("replayed chunk still carries the model name: %s", raw)
	}
	if strings.Contains(string(raw), "created_at") {
		t.Errorf("replayed chunk still carries the timestamp: %s", raw)
	}
	// What the replay actually needs has to survive.
	if !strings.Contains(string(raw), "hi") {
		t.Errorf("replayed chunk lost its content: %s", raw)
	}
}

// TestSessionSinkTrimsThinkingInReplayChunk keeps the reasoning text a reconnect
// needs while still dropping the noise.
func TestSessionSinkTrimsThinkingInReplayChunk(t *testing.T) {
	st := newTestSessionStore(t)
	srv := &Server{chatSessions: st}
	sess := st.Create("m", defaultSessionSettings())
	srv.beginTurn(sess.ID, time.Now())
	sink := &sessionSink{srv: srv, id: sess.ID, started: time.Now()}

	sink.Send("chunk", ollama.ChatChunk{
		Model:   "m",
		Message: ollama.ChatMessage{Role: "assistant", Thinking: "pondering"},
	})
	raw, _ := json.Marshal(st.Get(sess.ID).Events[0].Data)
	if !strings.Contains(string(raw), "pondering") {
		t.Errorf("replayed chunk lost its thinking: %s", raw)
	}
}

// TestFinishSessionDropsTheReplayLog is the size fix that matters: a finished
// turn's chunks are dead weight, because Messages already holds the whole
// result and a client that reconnects reads the session endpoint instead.
func TestFinishSessionDropsTheReplayLog(t *testing.T) {
	st := newTestSessionStore(t)
	srv := &Server{chatSessions: st}
	sess := st.Create("m", defaultSessionSettings())
	started := time.Now()
	srv.beginTurn(sess.ID, started)
	sink := &sessionSink{srv: srv, id: sess.ID, started: started}

	for i := 0; i < 50; i++ {
		sink.Send("chunk", ollama.ChatChunk{
			Model:   "m",
			Message: ollama.ChatMessage{Role: "assistant", Content: "token "},
		})
	}
	if got := len(st.Get(sess.ID).Events); got != 50 {
		t.Fatalf("mid-turn the log should hold the 50 chunks, got %d", got)
	}
	if st.Get(sess.ID).Status != chatSessionRunning {
		t.Fatalf("status = %q, want running mid-turn", st.Get(sess.ID).Status)
	}

	srv.finishSession(sess.ID, started)

	got := st.Get(sess.ID)
	if len(got.Events) != 0 {
		t.Errorf("settled session kept %d replay events, want 0", len(got.Events))
	}
	// The transcript has to be intact: that is what the dropped events replaced.
	last := got.Messages[len(got.Messages)-1]
	if !strings.Contains(last.Content, "token") {
		t.Errorf("dropping the log also lost the text: %q", last.Content)
	}
	if last.Pending {
		t.Error("the pending flag should be cleared when the turn settles")
	}
	// Seq keeps climbing so a client that reconnects with from=N is not confused
	// by events that no longer exist.
	if got.Seq != 50 {
		t.Errorf("Seq = %d, want it to keep counting past the dropped events", got.Seq)
	}
	st.flush(sess.ID)
	reloaded := newChatSessionStore(st.dir)
	reloaded.Load()
	if len(reloaded.Get(sess.ID).Events) != 0 {
		t.Error("the replay log came back from disk, so the file is still fat")
	}
}

// TestFinishSessionKeepsTheReplayLogOfAFailedTurn guards the error path: a turn
// that blows up mid-stream is the one case where the chunk history is the only
// record of what the model managed to say, so the message text must survive.
func TestFinishSessionKeepsTheTranscriptOfAFailedTurn(t *testing.T) {
	st := newTestSessionStore(t)
	srv := &Server{chatSessions: st}
	sess := st.Create("m", defaultSessionSettings())
	started := time.Now()
	srv.beginTurn(sess.ID, started)
	sink := &sessionSink{srv: srv, id: sess.ID, started: started}

	sink.Send("chunk", ollama.ChatChunk{
		Message: ollama.ChatMessage{Role: "assistant", Content: "partial answer"},
	})
	sink.Send("error", map[string]any{"error": "connection reset"})
	srv.finishSession(sess.ID, started)

	// A turn that failed mid-stream does not make the session unusable, so it
	// settles as idle with the error on the message. Only failSession, which
	// handles setup problems like a missing model, marks the session error.
	got := st.Get(sess.ID)
	if got.Status != chatSessionIdle {
		t.Errorf("status = %q, want idle after a failed turn", got.Status)
	}
	if got.Error != "" {
		t.Errorf("session error = %q, want it on the message instead", got.Error)
	}
	last := got.Messages[len(got.Messages)-1]
	if last.Content != "partial answer" {
		t.Errorf("Content = %q, want the partial answer kept", last.Content)
	}
	if last.Error == "" {
		t.Error("the error should be recorded on the message")
	}
}

// TestSessionTitleWaitsForASecondTurn pins when a session gets its name. A session
// holding a single prompt is still "the one I just started", and in the list
// every such row would carry the same kind of label where the relative time is
// the only thing telling them apart, so the title waits for the second turn.
func TestSessionTitleWaitsForASecondTurn(t *testing.T) {
	st := newTestSessionStore(t)
	sess := st.Create("model-a", defaultSessionSettings())
	if sess.Title != "" {
		t.Fatalf("a brand-new session should have no title, got %q", sess.Title)
	}

	st.AppendUser(sess.ID, "first question, please answer at length", nil)
	if got := st.Get(sess.ID).Title; got != "" {
		t.Errorf("Title = %q after one user turn, want it to stay empty", got)
	}

	st.AppendUser(sess.ID, "second question", nil)
	// The name comes from the FIRST prompt: that is what the user asked for, and
	// the second turn is only what made the session worth naming.
	if got := st.Get(sess.ID).Title; got != "first question, please answer at length" {
		t.Errorf("Title = %q after two user turns, want the first prompt", got)
	}
}

// TestSessionTitleIgnoresEmptyPrompts makes sure a turn carrying only an
// attachment does not count towards the second turn, otherwise a user who sends
// an image and then a question would get a name made of nothing.
func TestSessionTitleIgnoresEmptyPrompts(t *testing.T) {
	st := newTestSessionStore(t)
	sess := st.Create("model-a", defaultSessionSettings())
	st.AppendUser(sess.ID, "look at this", []ChatAttach{{Kind: "image", MimeType: "image/png", Data: attachmentBase64(64)}})
	st.AppendUser(sess.ID, "   ", nil)
	if got := st.Get(sess.ID).Title; got != "" {
		t.Errorf("Title = %q, want an attachment-only plus blank turn to leave it unnamed", got)
	}
	st.AppendUser(sess.ID, "now explain it", nil)
	if got := st.Get(sess.ID).Title; got != "look at this" {
		t.Errorf("Title = %q, want the first prompt once a second real turn exists", got)
	}
}
