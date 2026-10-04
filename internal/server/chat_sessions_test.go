package server

import (
	"context"
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
	if got.Seq == 0 || len(got.Events) == 0 {
		t.Errorf("replay log not filled: seq=%d events=%d", got.Seq, len(got.Events))
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

	msgs := buildSessionMessages(st.Get(sess.ID), false)
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

	msgs := buildSessionMessages(st.Get(sess.ID), false)
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
	body := srv.buildSessionBody(context.Background(), st.Get(sess.ID), sessionModelInfo{})
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

	body := srv.buildSessionBody(context.Background(), st.Get(sess.ID), sessionModelInfo{CanTools: true})
	if body.Think != nil {
		t.Errorf("think should be omitted for a model that cannot think, got %q", *body.Think)
	}

	// The same settings on a model that does think must keep the level.
	body = srv.buildSessionBody(context.Background(), st.Get(sess.ID), sessionModelInfo{CanTools: true, CanThink: true})
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

	body := srv.buildSessionBody(context.Background(), st.Get(sess.ID), sessionModelInfo{CanTools: true, CanThink: true})

	if body.Model != "some-model" {
		t.Errorf("model = %q", body.Model)
	}
	if !body.NoBrowserTools {
		t.Error("detached runs must set NoBrowserTools")
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
