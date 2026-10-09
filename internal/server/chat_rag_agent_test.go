package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gense/ollama-manager/internal/ollama"
	ragpkg "github.com/gense/ollama-manager/internal/rag"
)

type ragAgentChatStep struct {
	content  string
	toolName string
	toolArgs map[string]any
}

type fakeOllamaRAGAgent struct {
	srv *httptest.Server

	mu           sync.Mutex
	embedVecs    map[string][]float64
	embedErrs    map[string]error
	embedRaw     map[string]string
	embedReqs    []map[string]any
	embedWait    chan struct{}
	embedStarted chan struct{}
	tags         []map[string]any
	tagsCalls    int
	tagsBlocked  chan struct{}
	tagsRelease  chan struct{}
	caps         map[string][]string
	chatSteps    []ragAgentChatStep
	stepFn       func(ollama.ChatRequest) ragAgentChatStep
	chatReqs     []ollama.ChatRequest
}

func newFakeOllamaRAGAgent() *fakeOllamaRAGAgent {
	f := &fakeOllamaRAGAgent{
		embedVecs: map[string][]float64{"embed-model:latest": {1, 0}},
		embedErrs: map[string]error{},
		embedRaw:  map[string]string{},
		tags: []map[string]any{
			{"name": "embed-model:latest", "digest": "sha256:embed1"},
			{"name": "chat-model:latest", "digest": "sha256:chat1"},
			{"name": "plain-model:latest", "digest": "sha256:plain1"},
		},
		caps: map[string][]string{
			"embed-model:latest": {"embedding"},
			"chat-model:latest":  {"completion", "tools"},
			"plain-model:latest": {"completion"},
		},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			f.mu.Lock()
			f.tagsCalls++
			n := f.tagsCalls
			tags := f.tags
			blocked := f.tagsBlocked
			release := f.tagsRelease
			f.mu.Unlock()
			if blocked != nil && n == 2 {
				close(blocked)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
			writeJSON(w, http.StatusOK, map[string]any{"models": tags})
		case "/api/ps":
			writeJSON(w, http.StatusOK, map[string]any{"models": []any{}})
		case "/api/show":
			var req struct {
				Name string `json:"name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			f.mu.Lock()
			c, ok := f.caps[req.Name]
			f.mu.Unlock()
			if !ok {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"capabilities": c,
				"details":      map[string]any{"format": "safetensors"},
				"model_info":   map[string]any{},
			})
		case "/api/embed":
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			f.mu.Lock()
			wait := f.embedWait
			started := f.embedStarted
			f.mu.Unlock()
			if started != nil {
				select {
				case started <- struct{}{}:
				default:
				}
			}
			if wait != nil {
				select {
				case <-wait:
				case <-r.Context().Done():
					return
				}
			}
			model, _ := req["model"].(string)
			f.mu.Lock()
			f.embedReqs = append(f.embedReqs, req)
			vec := f.embedVecs[model]
			eErr := f.embedErrs[model]
			rawBody := f.embedRaw[model]
			f.mu.Unlock()
			if eErr != nil {
				http.Error(w, eErr.Error(), http.StatusInternalServerError)
				return
			}
			if rawBody != "" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(rawBody))
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"embedding": vec})
		case "/api/chat":
			var req ollama.ChatRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			f.mu.Lock()
			f.chatReqs = append(f.chatReqs, req)
			var step ragAgentChatStep
			if f.stepFn != nil {
				step = f.stepFn(req)
			} else if len(f.chatSteps) > 0 {
				step = f.chatSteps[0]
				f.chatSteps = f.chatSteps[1:]
			} else {
				step = ragAgentChatStep{content: "ok"}
			}
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/x-ndjson")
			var calls string
			if step.toolName != "" {
				args, _ := json.Marshal(step.toolArgs)
				calls = `,"tool_calls":[{"function":{"name":` + mustJSON(step.toolName) + `,"arguments":` + string(args) + `}}]`
			}
			_, _ = w.Write([]byte(`{"model":"` + req.Model + `","message":{"role":"assistant","content":` +
				mustJSON(step.content) + calls + `},"done":true,"done_reason":"stop","eval_count":1,"prompt_eval_count":1}` + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	return f
}

func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func (f *fakeOllamaRAGAgent) Close() { f.srv.Close() }

func (f *fakeOllamaRAGAgent) embedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.embedReqs)
}

func (f *fakeOllamaRAGAgent) chatRequests() []ollama.ChatRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ollama.ChatRequest(nil), f.chatReqs...)
}

func ragAgentToolNames(req ollama.ChatRequest) map[string]bool {
	names := map[string]bool{}
	tools, _ := req.Tools.([]any)
	for _, tl := range tools {
		m, _ := tl.(map[string]any)
		fn, _ := m["function"].(map[string]any)
		if fn == nil {
			continue
		}
		if n, _ := fn["name"].(string); n != "" {
			names[n] = true
		}
	}
	return names
}

func (f *fakeOllamaRAGAgent) lastEmbed() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.embedReqs) == 0 {
		return nil
	}
	return f.embedReqs[len(f.embedReqs)-1]
}

func ragAgentBody(filename string, editable bool) chatRequestBody {
	body := chatRequestBody{
		Model:      "chat-model:latest",
		Messages:   []ollama.ChatMessage{{Role: "user", Content: "hi"}},
		RAGEnabled: true,
		RAGPaths:   []string{filename},
	}
	if editable {
		body.RAGEditable = []string{filename}
	}
	return body
}

func ragToolCall(t *testing.T, srv *Server, sink *recordSink, body chatRequestBody, name string, args map[string]any) (string, string, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	return srv.runRAGTool(context.Background(), sink, body, name, raw)
}

func TestRAGAgentScopeAndDefinitions(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)
	_, f1 := createChatRAGBase(t, dir, "One", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "a", Content: "c", Embedding: []float64{1, 0}},
	})
	_, f2 := createChatRAGBase(t, dir, "Two", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "b", Content: "c", Embedding: []float64{1, 0}},
	})

	ro := ragAgentBody(f1, false)
	defs := srv.ragToolDefinitions(ro)
	names := map[string]bool{}
	for _, d := range defs {
		m, _ := d.(map[string]any)
		fn, _ := m["function"].(map[string]any)
		names[fn["name"].(string)] = true
	}
	if !names["rag_list_bases"] || !names["rag_search"] || names["rag_create_entry"] || names["rag_update_base"] {
		t.Fatalf("readonly defs = %v", names)
	}

	body := ragAgentBody(f1, true)
	body.RAGPaths = append(body.RAGPaths, f2)
	defs = srv.ragToolDefinitions(body)
	var create map[string]any
	for _, d := range defs {
		m, _ := d.(map[string]any)
		fn, _ := m["function"].(map[string]any)
		if fn["name"] == "rag_create_entry" {
			create = m
		}
	}
	if create == nil {
		t.Fatal("editable base must expose rag_create_entry")
	}
	fn, _ := create["function"].(map[string]any)
	params, _ := fn["parameters"].(map[string]any)
	props, _ := params["properties"].(map[string]any)
	fprop, _ := props["filename"].(map[string]any)
	enum, _ := fprop["enum"].([]string)
	if len(enum) != 1 || enum[0] != f1 {
		t.Fatalf("create enum must list only writable bases: %v", enum)
	}

	var listDef map[string]any
	for _, d := range defs {
		m, _ := d.(map[string]any)
		fn, _ := m["function"].(map[string]any)
		if fn["name"] == "rag_list_bases" {
			listDef = m
		}
	}
	lfn, _ := listDef["function"].(map[string]any)
	lparams, _ := lfn["parameters"].(map[string]any)
	if req, ok := lparams["required"].([]string); !ok || req == nil {
		t.Fatalf("rag_list_bases required must be a JSON array, got %#v", lparams["required"])
	}

	if !srv.ragAgentEnabled(context.Background(), body) {
		t.Fatal("agent should be enabled for a tools-capable model")
	}
	noTools := ragAgentBody(f1, true)
	noTools.Model = "plain-model:latest"
	if srv.ragAgentEnabled(context.Background(), noTools) {
		t.Fatal("agent must stay off for a model without the tools capability")
	}
}

func TestRAGAgentReadToolsAndForgedMutationDenied(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)
	_, filename := createChatRAGBase(t, dir, "Fruits", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "apple", Content: "an apple is a fruit", Embedding: []float64{1, 0}},
	})
	detail, err := ragpkg.Get(dir, filename)
	if err != nil {
		t.Fatal(err)
	}
	entryID := detail.Entries[0].ID
	sink := &recordSink{}
	body := ragAgentBody(filename, false)

	out, _, err := ragToolCall(t, srv, sink, body, "rag_list_bases", map[string]any{})
	if err != nil || !strings.Contains(out, filename) || !strings.Contains(out, `"editable":false`) {
		t.Fatalf("list_bases = %q, %v", out, err)
	}
	out, _, err = ragToolCall(t, srv, sink, body, "rag_list_entries", map[string]any{"filename": filename})
	if err != nil || !strings.Contains(out, `"total":1`) || !strings.Contains(out, "apple") {
		t.Fatalf("list_entries = %q, %v", out, err)
	}
	out, _, err = ragToolCall(t, srv, sink, body, "rag_get_entry", map[string]any{"filename": filename, "entry_id": entryID})
	if err != nil || !strings.Contains(out, "an apple is a fruit") || !strings.Contains(out, `"revision"`) {
		t.Fatalf("get_entry = %q, %v", out, err)
	}
	out, _, err = ragToolCall(t, srv, sink, body, "rag_search", map[string]any{"filename": filename, "query": "fruit"})
	if err != nil || !strings.Contains(out, "apple") {
		t.Fatalf("search = %q, %v", out, err)
	}

	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"rag_create_entry", map[string]any{"filename": filename, "term": "x", "content": "y"}},
		{"rag_update_entry", map[string]any{"filename": filename, "entry_id": entryID, "expected_revision": "r", "term": "x"}},
		{"rag_delete_entry", map[string]any{"filename": filename, "entry_id": entryID, "expected_revision": "r"}},
		{"rag_update_base", map[string]any{"filename": filename, "expected_revision": "r", "name": "x"}},
	} {
		if _, mutated, err := ragToolCall(t, srv, sink, body, tc.name, tc.args); err == nil || mutated != "" {
			t.Fatalf("%s on readonly base = %v", tc.name, err)
		}
	}
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"rag_list_entries", map[string]any{"filename": "../evil.db"}},
		{"rag_get_entry", map[string]any{"filename": "not-selected.db", "entry_id": 1}},
	} {
		if _, _, err := ragToolCall(t, srv, sink, body, tc.name, tc.args); err == nil {
			t.Fatalf("%s with %v should be denied", tc.name, tc.args)
		}
	}
	if fake.embedCount() != 1 {
		t.Fatalf("embeds = %d, want 1 (search only)", fake.embedCount())
	}
	after, err := ragpkg.Get(dir, filename)
	if err != nil || len(after.Entries) != 1 {
		t.Fatalf("forged mutation changed the base: %+v %v", after.Entries, err)
	}
}

func TestRAGAgentMutationFlow(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)
	_, filename := createChatRAGBase(t, dir, "Fruits", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "apple", Content: "an apple is a fruit", Embedding: []float64{1, 0}},
	})
	sink := &recordSink{}
	body := ragAgentBody(filename, true)

	out, mutated, err := ragToolCall(t, srv, sink, body, "rag_create_entry",
		map[string]any{"filename": filename, "term": "pear", "content": "a pear is a fruit"})
	if err != nil || mutated != filename {
		t.Fatalf("create = %q %q %v", out, mutated, err)
	}
	if !strings.Contains(out, `"status":"created"`) || !strings.Contains(out, `"revision"`) {
		t.Fatalf("create result = %q", out)
	}
	var created map[string]any
	if err := json.Unmarshal([]byte(out), &created); err != nil {
		t.Fatal(err)
	}
	newID := int64(created["id"].(float64))

	last := fake.lastEmbed()
	if last == nil || last["input"] != "pear\n\na pear is a fruit" {
		t.Fatalf("embed input = %v", last)
	}

	out, _, err = ragToolCall(t, srv, sink, body, "rag_get_entry",
		map[string]any{"filename": filename, "entry_id": newID})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	rev, _ := got["revision"].(string)
	if rev == "" {
		t.Fatalf("get_entry lacks revision: %q", out)
	}

	if _, _, err := ragToolCall(t, srv, sink, body, "rag_update_entry",
		map[string]any{"filename": filename, "entry_id": newID, "expected_revision": "stale", "term": "x"}); err == nil {
		t.Fatal("stale revision must be rejected")
	}
	out, mutated, err = ragToolCall(t, srv, sink, body, "rag_update_entry",
		map[string]any{"filename": filename, "entry_id": newID, "expected_revision": rev, "term": "green pear"})
	if err != nil || mutated != filename {
		t.Fatalf("update = %q %q %v", out, mutated, err)
	}
	if !strings.Contains(out, `"changed":true`) {
		t.Fatalf("update result = %q", out)
	}
	_, snap, err := ragpkg.ReadEntry(context.Background(), dir, filename, newID)
	if err != nil || snap.Entry.Term != "green pear" {
		t.Fatalf("updated entry = %+v %v", snap.Entry, err)
	}

	out, _, err = ragToolCall(t, srv, sink, body, "rag_update_entry",
		map[string]any{"filename": filename, "entry_id": newID, "expected_revision": snap.Revision, "term": "green pear"})
	if err != nil || !strings.Contains(out, `"changed":false`) {
		t.Fatalf("no-op update = %q %v", out, err)
	}

	meta, _, err := ragpkg.ReadEntry(context.Background(), dir, filename, newID)
	if err != nil {
		t.Fatal(err)
	}
	out, mutated, err = ragToolCall(t, srv, sink, body, "rag_update_base",
		map[string]any{"filename": filename, "expected_revision": ragpkg.MetaRevision(meta), "name": "Fruit Bowl"})
	if err != nil || mutated != filename || !strings.Contains(out, "Fruit Bowl") {
		t.Fatalf("update_base = %q %q %v", out, mutated, err)
	}
	if _, _, err := ragToolCall(t, srv, sink, body, "rag_update_base",
		map[string]any{"filename": filename, "expected_revision": ragpkg.MetaRevision(meta), "name": "Other"}); err == nil {
		t.Fatal("stale base revision must be rejected")
	}

	out, mutated, err = ragToolCall(t, srv, sink, body, "rag_delete_entry",
		map[string]any{"filename": filename, "entry_id": newID, "expected_revision": snap.Revision})
	if err != nil || mutated != filename || !strings.Contains(out, `"entries":1`) {
		t.Fatalf("delete = %q %q %v", out, mutated, err)
	}
	after, err := ragpkg.Get(dir, filename)
	if err != nil || len(after.Entries) != 1 || after.Meta.Name != "Fruit Bowl" {
		t.Fatalf("final base = %+v %v", after.Meta, err)
	}
}

func TestRAGAgentStrictArgs(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)
	_, filename := createChatRAGBase(t, dir, "Fruits", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "a", Content: "c", Embedding: []float64{1, 0}},
	})
	sink := &recordSink{}
	body := ragAgentBody(filename, true)

	cases := []struct {
		name string
		tool string
		raw  string
	}{
		{"fraction", "rag_list_entries", `{"filename":"` + filename + `","limit":1.5}`},
		{"negative", "rag_list_entries", `{"filename":"` + filename + `","offset":-1}`},
		{"null field", "rag_get_entry", `{"filename":"` + filename + `","entry_id":1,"content_offset":null}`},
		{"unknown field", "rag_get_entry", `{"filename":"` + filename + `","entry_id":1,"hack":true}`},
		{"wrong type", "rag_get_entry", `{"filename":"` + filename + `","entry_id":"one"}`},
		{"trailing", "rag_list_bases", `{} {}`},
		{"string body", "rag_list_bases", `"hello"`},
		{"root null", "rag_list_bases", `null`},
		{"string object", "rag_list_bases", `"{}"`},
		{"root array", "rag_list_bases", `[]`},
		{"vector field", "rag_create_entry", `{"filename":"` + filename + `","term":"x","content":"y","embedding":[1,0]}`},
		{"model field", "rag_update_base", `{"filename":"` + filename + `","expected_revision":"r","embedding_model":"evil"}`},
		{"schema field", "rag_update_base", `{"filename":"` + filename + `","expected_revision":"r","schema_version":1}`},
		{"id field", "rag_update_entry", `{"filename":"` + filename + `","entry_id":1,"expected_revision":"r","id":99,"term":"x"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := srv.runRAGTool(context.Background(), sink, body, tc.tool, json.RawMessage(tc.raw)); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestRAGAgentEntryPagination(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)
	long := strings.Repeat("x", 9000) + "END"
	_, filename := createChatRAGBase(t, dir, "Big", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "long", Content: long, Embedding: []float64{1, 0}},
	})
	sink := &recordSink{}
	body := ragAgentBody(filename, false)
	var entryID int64
	if d, err := ragpkg.Get(dir, filename); err == nil {
		entryID = d.Entries[0].ID
	}

	var full strings.Builder
	offset := int64(0)
	for {
		out, _, err := ragToolCall(t, srv, sink, body, "rag_get_entry",
			map[string]any{"filename": filename, "entry_id": entryID, "content_offset": offset, "content_limit": 4000})
		if err != nil {
			t.Fatal(err)
		}
		var res map[string]any
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatal(err)
		}
		full.WriteString(res["content"].(string))
		next := res["next_content_offset"]
		if next == nil {
			break
		}
		offset = int64(next.(float64))
	}
	if full.String() != long {
		t.Fatalf("paginated reconstruction = %d chars", full.Len())
	}
	if n := len(full.String()); n != 9003 {
		t.Fatalf("rebuilt %d chars", n)
	}
}

func TestRAGAgentLoopEndToEnd(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)
	_, filename := createChatRAGBase(t, dir, "Fruits", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "apple", Content: "an apple is a fruit", Embedding: []float64{1, 0}},
	})
	fake.chatSteps = []ragAgentChatStep{
		{toolName: "rag_create_entry", toolArgs: map[string]any{
			"filename": filename, "term": "pear", "content": "a pear is a fruit"}},
		{content: "Added the pear entry."},
	}

	code, sse := postChat(t, srv, map[string]any{
		"model":        "chat-model:latest",
		"messages":     []map[string]any{{"role": "user", "content": "add a pear to my base"}},
		"rag_enabled":  true,
		"rag_paths":    []string{filename},
		"rag_editable": []string{filename},
	})
	if code != http.StatusOK {
		t.Fatalf("chat status = %d, body = %s", code, sse)
	}
	if !strings.Contains(sse, "event: rag_updated") || !strings.Contains(sse, filename) {
		t.Fatalf("stream lacks rag_updated: %s", sse)
	}
	if !strings.Contains(sse, `"name":"rag_create_entry"`) {
		t.Fatalf("stream lacks tool events: %s", sse)
	}
	if !strings.Contains(sse, "Added the pear entry.") {
		t.Fatalf("final answer missing: %s", sse)
	}
	after, err := ragpkg.Get(dir, filename)
	if err != nil || len(after.Entries) != 2 {
		t.Fatalf("base after agent create = %+v %v", after.Entries, err)
	}
	var added *ragpkg.EntryView
	for i := range after.Entries {
		if after.Entries[i].Term == "pear" {
			added = &after.Entries[i]
		}
	}
	if added == nil || added.Content != "a pear is a fruit" {
		t.Fatalf("created entry missing: %+v", after.Entries)
	}

	reqs := fake.chatRequests()
	if len(reqs) != 2 {
		t.Fatalf("chat rounds = %d", len(reqs))
	}
	toolMsg := reqs[1].Messages[len(reqs[1].Messages)-1]
	if toolMsg.Role != "tool" || toolMsg.ToolName != "rag_create_entry" || !strings.Contains(toolMsg.Content, `"status":"created"`) {
		t.Fatalf("tool result message = %+v", toolMsg)
	}
	toolNames := ragAgentToolNames(reqs[0])
	if !toolNames["rag_create_entry"] || toolNames["web_search"] || toolNames["web_fetch"] {
		t.Fatalf("RAG-only tools: %v", toolNames)
	}
	var sys string
	for _, m := range reqs[0].Messages {
		if m.Role == "system" {
			sys = m.Content
		}
	}
	if !strings.Contains(sys, "rag_list_bases") || !strings.Contains(sys, "untrusted reference data") {
		t.Fatalf("agent instruction missing: %q", sys)
	}
}

func TestRAGAgentReadOnlyForgeryKeepsBase(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)
	_, filename := createChatRAGBase(t, dir, "Fruits", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "apple", Content: "c", Embedding: []float64{1, 0}},
	})
	fake.chatSteps = []ragAgentChatStep{
		{toolName: "rag_create_entry", toolArgs: map[string]any{
			"filename": filename, "term": "evil", "content": "forge"}},
		{content: "sorry"},
	}

	code, sse := postChat(t, srv, map[string]any{
		"model":       "chat-model:latest",
		"messages":    []map[string]any{{"role": "user", "content": "add something"}},
		"rag_enabled": true,
		"rag_paths":   []string{filename},
	})
	if code != http.StatusOK {
		t.Fatalf("chat status = %d", code)
	}
	if strings.Contains(sse, "rag_updated") {
		t.Fatalf("forged mutation fired rag_updated: %s", sse)
	}
	if !strings.Contains(sse, `"ok":false`) {
		t.Fatalf("forged call should end ok=false: %s", sse)
	}
	after, err := ragpkg.Get(dir, filename)
	if err != nil || len(after.Entries) != 1 {
		t.Fatalf("forged call mutated the base: %+v %v", after.Entries, err)
	}
	if fake.embedCount() != 1 {
		t.Fatalf("embeds = %d, want 1 (automatic retrieval only, none from the forged call)", fake.embedCount())
	}
}

func TestRAGAgentNonToolModelStaysPlain(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)
	_, filename := createChatRAGBase(t, dir, "Fruits", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "apple", Content: "c", Embedding: []float64{1, 0}},
	})
	code, _ := postChat(t, srv, map[string]any{
		"model":        "plain-model:latest",
		"messages":     []map[string]any{{"role": "user", "content": "hi"}},
		"rag_enabled":  true,
		"rag_paths":    []string{filename},
		"rag_editable": []string{filename},
	})
	if code != http.StatusOK {
		t.Fatalf("chat status = %d", code)
	}
	reqs := fake.chatRequests()
	if len(reqs) != 1 || len(ragAgentToolNames(reqs[0])) != 0 {
		t.Fatalf("non-tool model must not receive tool schemas: %+v", reqs)
	}
}

func TestRAGAgentSessionRevokeDeniesMutation(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)
	_, filename := createChatRAGBase(t, dir, "Fruits", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "apple", Content: "c", Embedding: []float64{1, 0}},
	})

	st := srv.chatSessions
	sess := st.Create("chat-model:latest", SessionSettings{
		RAGEnabled: true, RAGPaths: []string{filename}, RAGEditable: []string{filename}, NumCtxPct: 100,
	})
	sink := &recordSink{}
	body := ragAgentBody(filename, true)
	body.SessionID = sess.ID

	if _, _, err := ragToolCall(t, srv, sink, body, "rag_list_bases", map[string]any{}); err != nil {
		t.Fatalf("read before revoke: %v", err)
	}
	st.MergeSettings(sess.ID, sessionSettingsInput{RAGEditable: []string{}})
	if _, _, err := ragToolCall(t, srv, sink, body, "rag_create_entry",
		map[string]any{"filename": filename, "term": "x", "content": "y"}); err == nil {
		t.Fatal("revoked edit must be denied")
	}
	body.RAGPaths = append(body.RAGPaths, "ghost.db")
	sel, _ := srv.ragToolScope(body)
	for _, f := range sel {
		if f == "ghost.db" {
			t.Fatal("session revoke must not widen the original turn scope")
		}
	}
	after, err := ragpkg.Get(dir, filename)
	if err != nil || len(after.Entries) != 1 {
		t.Fatalf("revoked mutation changed the base: %+v %v", after.Entries, err)
	}
}

func TestRAGAgentSessionRevokeDuringEmbed(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)
	_, filename := createChatRAGBase(t, dir, "Fruits", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "apple", Content: "c", Embedding: []float64{1, 0}},
	})
	st := srv.chatSessions
	sess := st.Create("chat-model:latest", SessionSettings{
		RAGEnabled: true, RAGPaths: []string{filename}, RAGEditable: []string{filename}, NumCtxPct: 100,
	})
	fake.embedWait = make(chan struct{})
	fake.embedStarted = make(chan struct{}, 1)
	sink := &recordSink{}
	body := ragAgentBody(filename, true)
	body.SessionID = sess.ID

	done := make(chan error, 1)
	go func() {
		_, _, err := srv.runRAGTool(context.Background(), sink, body, "rag_create_entry",
			json.RawMessage(`{"filename":"`+filename+`","term":"x","content":"y"}`))
		done <- err
	}()
	select {
	case <-fake.embedStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("embed never started")
	}
	st.MergeSettings(sess.ID, sessionSettingsInput{RAGEditable: []string{}})
	close(fake.embedWait)
	if err := <-done; err == nil {
		t.Fatal("revocation during embed must block the commit")
	}
	after, err := ragpkg.Get(dir, filename)
	if err != nil || len(after.Entries) != 1 {
		t.Fatalf("commit after revoke changed the base: %+v %v", after.Entries, err)
	}
}

func TestRAGAgentMutationMissingModelIsNonfatal(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)
	_, filename := createChatRAGBase(t, dir, "Ghost", "ghost-model:latest", "sha256:ghost", 2, []ragpkg.Entry{
		{Term: "apple", Content: "c", Embedding: []float64{1, 0}},
	})
	sink := &recordSink{}
	body := ragAgentBody(filename, true)
	if _, mutated, err := ragToolCall(t, srv, sink, body, "rag_create_entry",
		map[string]any{"filename": filename, "term": "x", "content": "y"}); err == nil || mutated != "" {
		t.Fatalf("missing model create = %q %v", mutated, err)
	}
	all := sink.all()
	if !strings.Contains(all, "rag_unavailable") {
		t.Fatalf("missing warning: %s", all)
	}
	after, err := ragpkg.Get(dir, filename)
	if err != nil || len(after.Entries) != 1 {
		t.Fatalf("failed mutation changed the base: %+v %v", after.Entries, err)
	}
}

func TestRAGAgentSessionTurnRoutesAndStores(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)
	_, filename := createChatRAGBase(t, dir, "Fruits", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "apple", Content: "c", Embedding: []float64{1, 0}},
	})
	fake.chatSteps = []ragAgentChatStep{
		{toolName: "rag_update_base", toolArgs: nil},
		{content: "renamed"},
	}
	st := srv.chatSessions
	sess := st.Create("chat-model:latest", SessionSettings{
		RAGEnabled: true, RAGPaths: []string{filename}, RAGEditable: []string{filename}, NumCtxPct: 100,
	})
	meta, _, err := ragpkg.ReadEntry(context.Background(), dir, filename, 1)
	if err != nil {
		d, gerr := ragpkg.Get(dir, filename)
		if gerr != nil {
			t.Fatal(gerr)
		}
		meta = d.Meta
	}
	fake.mu.Lock()
	fake.chatSteps[0].toolArgs = map[string]any{
		"filename": filename, "expected_revision": ragpkg.MetaRevision(meta), "name": "Renamed Base"}
	fake.mu.Unlock()

	stream, unsub := st.Subscribe()
	defer unsub()
	st.AppendUser(sess.ID, "rename the base", nil)
	srv.runSessionTurn(context.Background(), sess.ID)

	got := st.Get(sess.ID)
	if got.Status != chatSessionIdle {
		t.Fatalf("session status = %q (err=%q)", got.Status, got.Error)
	}
	after, err := ragpkg.Get(dir, filename)
	if err != nil || after.Meta.Name != "Renamed Base" {
		t.Fatalf("session agent mutation = %+v %v", after.Meta, err)
	}
	var assistant *SessionMessage
	for i := range got.Messages {
		if got.Messages[i].Role == "assistant" {
			assistant = &got.Messages[i]
		}
	}
	foundRAG, foundUpd := false, false
	for _, e := range assistant.ToolLog {
		if e.Name == "rag_update_base" {
			foundRAG = true
		}
	}
	for {
		select {
		case ev := <-stream:
			if ev.Event == "rag_updated" && ev.ID == sess.ID {
				foundUpd = true
			}
		default:
			if !foundRAG || !foundUpd {
				t.Fatalf("tool log / stream lack rag agent entries: %+v", assistant.ToolLog)
			}
			return
		}
	}
}

func TestRAGAgentArtifactLoopExposesRAGWithoutDir(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)
	_, filename := createChatRAGBase(t, dir, "Fruits", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "apple", Content: "c", Embedding: []float64{1, 0}},
	})
	d, err := ragpkg.Get(dir, filename)
	if err != nil {
		t.Fatal(err)
	}
	fake.chatSteps = []ragAgentChatStep{
		{toolName: "rag_update_base", toolArgs: map[string]any{
			"filename": filename, "expected_revision": ragpkg.MetaRevision(d.Meta), "name": "ArtRAG"}},
		{content: "done"},
	}
	t.Chdir(t.TempDir())
	yes := true
	code, sse := postChat(t, srv, map[string]any{
		"model":        "chat-model:latest",
		"messages":     []map[string]any{{"role": "user", "content": "rename my base"}},
		"artifacts":    yes,
		"rag_enabled":  true,
		"rag_paths":    []string{filename},
		"rag_editable": []string{filename},
	})
	if code != http.StatusOK {
		t.Fatalf("chat status = %d, body = %s", code, sse)
	}
	if !strings.Contains(sse, "rag_updated") {
		t.Fatalf("artifact loop lacks rag_updated: %s", sse)
	}
	if _, err := os.Stat("artifacts"); !os.IsNotExist(err) {
		t.Fatalf("RAG mutation must not create an artifacts dir: %v", err)
	}
	reqs := fake.chatRequests()
	if !ragAgentToolNames(reqs[0])["rag_update_base"] {
		t.Fatal("artifact loop must expose RAG tools from round one")
	}
	after, err := ragpkg.Get(dir, filename)
	if err != nil || after.Meta.Name != "ArtRAG" {
		t.Fatalf("artifact-loop mutation = %+v %v", after.Meta, err)
	}
}

func TestRAGAgentEmbedFailureLeavesBase(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	fake.embedErrs["embed-model:latest"] = errors.New("embed boom")
	dir := ragDirOf(t, srv)
	_, filename := createChatRAGBase(t, dir, "Fruits", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "apple", Content: "c", Embedding: []float64{1, 0}},
	})
	sink := &recordSink{}
	body := ragAgentBody(filename, true)
	if _, mutated, err := ragToolCall(t, srv, sink, body, "rag_create_entry",
		map[string]any{"filename": filename, "term": "x", "content": "y"}); err == nil || mutated != "" {
		t.Fatalf("embed failure create = %q %v", mutated, err)
	}
	after, err := ragpkg.Get(dir, filename)
	if err != nil || len(after.Entries) != 1 {
		t.Fatalf("embed failure changed the base: %+v %v", after.Entries, err)
	}
	if !strings.Contains(sink.all(), "rag_unavailable") {
		t.Fatal("embed failure should warn")
	}
}

func TestRAGAgentSessionRevokeDuringLockWait(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)
	_, filename := createChatRAGBase(t, dir, "Fruits", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "apple", Content: "c", Embedding: []float64{1, 0}},
	})
	d, err := ragpkg.Get(dir, filename)
	if err != nil {
		t.Fatal(err)
	}
	_, snap, err := ragpkg.ReadEntry(context.Background(), dir, filename, d.Entries[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	st := srv.chatSessions
	sess := st.Create("chat-model:latest", SessionSettings{
		RAGEnabled: true, RAGPaths: []string{filename}, RAGEditable: []string{filename}, NumCtxPct: 100,
	})
	sink := &recordSink{}
	body := ragAgentBody(filename, true)
	body.SessionID = sess.ID

	for _, tc := range []struct {
		name string
		tool string
		args map[string]any
	}{
		{"delete", "rag_delete_entry", map[string]any{
			"filename": filename, "entry_id": snap.ID, "expected_revision": snap.Revision}},
		{"update base", "rag_update_base", map[string]any{
			"filename": filename, "expected_revision": ragpkg.MetaRevision(d.Meta), "name": "Nope"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv.ragWriteMu.Lock()
			done := make(chan error, 1)
			go func() {
				_, _, err := srv.runRAGTool(context.Background(), sink, body, tc.tool, mustRaw(tc.args))
				done <- err
			}()
			select {
			case err := <-done:
				srv.ragWriteMu.Unlock()
				t.Fatalf("tool returned while the write lock was held: %v", err)
			case <-time.After(200 * time.Millisecond):
			}
			st.MergeSettings(sess.ID, sessionSettingsInput{RAGEditable: []string{}})
			srv.ragWriteMu.Unlock()
			if err := <-done; err == nil {
				t.Fatal("revocation while waiting for the write lock must deny the mutation")
			}
			st.MergeSettings(sess.ID, sessionSettingsInput{RAGEditable: []string{filename}})
		})
	}
	after, err := ragpkg.Get(dir, filename)
	if err != nil || len(after.Entries) != 1 || after.Meta.Name != "Fruits" {
		t.Fatalf("denied mutations changed the base: %+v %v", after.Meta, err)
	}
	if fake.embedCount() != 0 {
		t.Fatalf("denied mutations must not embed: %d", fake.embedCount())
	}
}

func mustRaw(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}

func TestRAGAgentSessionRevokeDuringDigestCheck(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)
	_, filename := createChatRAGBase(t, dir, "Fruits", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "apple", Content: "c", Embedding: []float64{1, 0}},
	})
	st := srv.chatSessions
	sess := st.Create("chat-model:latest", SessionSettings{
		RAGEnabled: true, RAGPaths: []string{filename}, RAGEditable: []string{filename}, NumCtxPct: 100,
	})
	fake.tagsBlocked = make(chan struct{})
	fake.tagsRelease = make(chan struct{})
	sink := &recordSink{}
	body := ragAgentBody(filename, true)
	body.SessionID = sess.ID

	done := make(chan error, 1)
	go func() {
		_, _, err := srv.runRAGTool(context.Background(), sink, body, "rag_create_entry",
			json.RawMessage(`{"filename":"`+filename+`","term":"x","content":"y"}`))
		done <- err
	}()
	select {
	case <-fake.tagsBlocked:
	case <-time.After(5 * time.Second):
		t.Fatal("digest re-check List never started")
	}
	st.MergeSettings(sess.ID, sessionSettingsInput{RAGEditable: []string{}})
	close(fake.tagsRelease)
	if err := <-done; err == nil {
		t.Fatal("revocation during the digest re-check must deny the commit")
	}
	after, err := ragpkg.Get(dir, filename)
	if err != nil || len(after.Entries) != 1 {
		t.Fatalf("commit after revoke changed the base: %+v %v", after.Entries, err)
	}
}

func TestRAGAgentResultBounds(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)
	tricky := strings.Repeat(`"\\\n`, 3000) + "TAIL-MARKER"
	_, filename := createChatRAGBase(t, dir, "Escape", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: strings.Repeat("t", 300), Content: tricky, Embedding: []float64{1, 0}},
	})
	d, err := ragpkg.Get(dir, filename)
	if err != nil {
		t.Fatal(err)
	}
	entryID := d.Entries[0].ID
	sink := &recordSink{}
	body := ragAgentBody(filename, false)

	var full strings.Builder
	offset := int64(0)
	pages := 0
	for {
		pages++
		out, _, err := ragToolCall(t, srv, sink, body, "rag_get_entry",
			map[string]any{"filename": filename, "entry_id": entryID, "content_offset": offset, "content_limit": 8000})
		if err != nil {
			t.Fatal(err)
		}
		if utf8.RuneCountInString(out) > ragContextMaxRunes {
			t.Fatalf("page %d exceeds %d runes: %d", pages, ragContextMaxRunes, utf8.RuneCountInString(out))
		}
		var res map[string]any
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatalf("page %d invalid JSON: %v", pages, err)
		}
		full.WriteString(res["content"].(string))
		next := res["next_content_offset"]
		if next == nil {
			break
		}
		offset = int64(next.(float64))
		if pages > 20 {
			t.Fatal("pagination never terminated")
		}
	}
	if full.String() != tricky {
		t.Fatalf("reconstructed content differs: %d vs %d runes", utf8.RuneCountInString(full.String()), utf8.RuneCountInString(tricky))
	}
	if pages < 2 {
		t.Fatalf("escaped content should span multiple pages, got %d", pages)
	}
}

func TestRAGAgentListBasesPaginationBounds(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)
	var paths []string
	for i := 0; i < 32; i++ {
		_, fn := createChatRAGBase(t, dir, fmt.Sprintf("Base %02d", i), "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
			{Term: "a", Content: "c", Embedding: []float64{1, 0}},
		})
		paths = append(paths, fn)
	}
	sink := &recordSink{}
	body := ragAgentBody(paths[0], false)
	body.RAGPaths = paths

	seen := map[string]bool{}
	offset := int64(0)
	for {
		out, _, err := ragToolCall(t, srv, sink, body, "rag_list_bases",
			map[string]any{"offset": offset, "limit": 20})
		if err != nil {
			t.Fatal(err)
		}
		if utf8.RuneCountInString(out) > ragContextMaxRunes {
			t.Fatalf("page exceeds %d runes: %d", ragContextMaxRunes, utf8.RuneCountInString(out))
		}
		var res struct {
			Bases []struct {
				Filename string `json:"filename"`
			} `json:"bases"`
			Total int64 `json:"total"`
			Next  any   `json:"next_offset"`
		}
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if res.Total != 32 {
			t.Fatalf("total = %d", res.Total)
		}
		for _, b := range res.Bases {
			if seen[b.Filename] {
				t.Fatalf("duplicate filename %q", b.Filename)
			}
			seen[b.Filename] = true
		}
		if res.Next == nil {
			break
		}
		offset = int64(res.Next.(float64))
	}
	if len(seen) != 32 {
		t.Fatalf("saw %d of 32 bases", len(seen))
	}
}

func TestRAGAgentSearchResultBounds(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)
	var entries []ragpkg.Entry
	for i := 0; i < 10; i++ {
		entries = append(entries, ragpkg.Entry{
			Term:      strings.Repeat(fmt.Sprintf("term%d", i), 50),
			Content:   strings.Repeat(`"\\\nbody`, 300),
			Embedding: []float64{1, 0},
		})
	}
	_, filename := createChatRAGBase(t, dir, "Big", "embed-model:latest", "sha256:embed1", 2, entries)
	sink := &recordSink{}
	body := ragAgentBody(filename, false)
	out, _, err := ragToolCall(t, srv, sink, body, "rag_search",
		map[string]any{"filename": filename, "query": "anything", "limit": 10})
	if err != nil {
		t.Fatal(err)
	}
	if utf8.RuneCountInString(out) > ragContextMaxRunes {
		t.Fatalf("search result exceeds %d runes: %d", ragContextMaxRunes, utf8.RuneCountInString(out))
	}
	var res struct {
		Returned int `json:"returned_matches"`
		Total    int `json:"total_matches"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if res.Total != 10 || res.Returned >= res.Total {
		t.Fatalf("expected dropped matches under the budget: %+v", res)
	}
}

func TestRAGAgentUpdateBaseResultBounds(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)
	_, filename := createChatRAGBase(t, dir, "Desc", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "a", Content: "c", Embedding: []float64{1, 0}},
	})
	d, err := ragpkg.Get(dir, filename)
	if err != nil {
		t.Fatal(err)
	}
	sink := &recordSink{}
	body := ragAgentBody(filename, true)
	bigDesc := strings.Repeat(`"\\\n`, 1300)
	out, _, err := ragToolCall(t, srv, sink, body, "rag_update_base",
		map[string]any{"filename": filename, "expected_revision": ragpkg.MetaRevision(d.Meta), "description": bigDesc})
	if err != nil {
		t.Fatal(err)
	}
	if utf8.RuneCountInString(out) > ragContextMaxRunes {
		t.Fatalf("update_base result exceeds %d runes: %d", ragContextMaxRunes, utf8.RuneCountInString(out))
	}
	var res map[string]any
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if res["description_truncated"] != true {
		t.Fatalf("expected description_truncated flag: %q", out)
	}
	if utf8.RuneCountInString(res["description"].(string)) != 512 {
		t.Fatalf("description bound = %d", utf8.RuneCountInString(res["description"].(string)))
	}
	stored, err := ragpkg.Get(dir, filename)
	if err != nil || stored.Meta.Description != strings.TrimSpace(bigDesc) {
		t.Fatalf("stored description must be full, got %d runes", utf8.RuneCountInString(stored.Meta.Description))
	}
}

func TestRAGAgentUpdateWithMediaEmbedInput(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)
	img := []byte{9, 8, 7}
	aud := []byte{4, 5}
	_, filename := createChatRAGBase(t, dir, "Media", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "pic", Content: "caption", InputMode: "combined",
			Media: []ragpkg.Media{
				{Type: "image", Name: "a.png", MIME: "image/png", Data: img},
				{Type: "audio", Name: "a.wav", MIME: "audio/wav", Data: aud},
			},
			Embedding: []float64{1, 0}},
	})
	d, err := ragpkg.Get(dir, filename)
	if err != nil {
		t.Fatal(err)
	}
	_, snap, err := ragpkg.ReadEntry(context.Background(), dir, filename, d.Entries[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	sink := &recordSink{}
	body := ragAgentBody(filename, true)

	out, _, err := ragToolCall(t, srv, sink, body, "rag_update_entry",
		map[string]any{"filename": filename, "entry_id": snap.ID, "expected_revision": snap.Revision,
			"term": "newTerm", "content": "newContent"})
	if err != nil || !strings.Contains(out, `"changed":true`) {
		t.Fatalf("update = %q %v", out, err)
	}
	last := fake.lastEmbed()
	input, ok := last["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("embed input = %v", last["input"])
	}
	item, _ := input[0].(map[string]any)
	if item["text"] != "newTerm\n\nnewContent" {
		t.Fatalf("embed text = %v", item["text"])
	}
	if item["image"] != "CQgH" || item["audio"] != "BAU=" {
		t.Fatalf("embed media = %v", item)
	}
	_, after, err := ragpkg.ReadEntry(context.Background(), dir, filename, snap.ID)
	if err != nil || len(after.Entry.Media) != 2 ||
		string(after.Entry.Media[0].Data) != string(img) || string(after.Entry.Media[1].Data) != string(aud) {
		t.Fatalf("media changed: %+v %v", after.Entry.Media, err)
	}

	embedsBefore := fake.embedCount()
	out, _, err = ragToolCall(t, srv, sink, body, "rag_update_entry",
		map[string]any{"filename": filename, "entry_id": snap.ID, "expected_revision": after.Revision,
			"input_mode": "media"})
	if err != nil || !strings.Contains(out, `"changed":true`) {
		t.Fatalf("media-mode update = %q %v", out, err)
	}
	last = fake.lastEmbed()
	input, _ = last["input"].([]any)
	item, _ = input[0].(map[string]any)
	if _, hasText := item["text"]; hasText {
		t.Fatalf("media mode must omit text: %v", item)
	}
	if item["image"] != "CQgH" || item["audio"] != "BAU=" {
		t.Fatalf("media-mode embed media = %v", item)
	}

	_, snap2, err := ragpkg.ReadEntry(context.Background(), dir, filename, snap.ID)
	if err != nil {
		t.Fatal(err)
	}
	out, _, err = ragToolCall(t, srv, sink, body, "rag_update_entry",
		map[string]any{"filename": filename, "entry_id": snap.ID, "expected_revision": snap2.Revision,
			"input_mode": "media"})
	if err != nil || !strings.Contains(out, `"changed":false`) {
		t.Fatalf("no-op update = %q %v", out, err)
	}
	if fake.embedCount() != embedsBefore+1 {
		t.Fatalf("no-op update re-embedded: %d vs %d", fake.embedCount(), embedsBefore+1)
	}
}

func TestRAGAgentMutationFailureTable(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)
	_, filename := createChatRAGBase(t, dir, "Fruits", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "apple", Content: "c", Embedding: []float64{1, 0}},
	})
	d, err := ragpkg.Get(dir, filename)
	if err != nil {
		t.Fatal(err)
	}
	_, snap, err := ragpkg.ReadEntry(context.Background(), dir, filename, d.Entries[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	sink := &recordSink{}
	body := ragAgentBody(filename, true)

	setVec := func(v []float64) {
		fake.mu.Lock()
		fake.embedVecs["embed-model:latest"] = v
		fake.mu.Unlock()
	}
	setRaw := func(raw string) {
		fake.mu.Lock()
		fake.embedRaw["embed-model:latest"] = raw
		fake.mu.Unlock()
	}
	setTags := func(tags []map[string]any) {
		fake.mu.Lock()
		fake.tags = tags
		fake.mu.Unlock()
	}
	reset := func() {
		fake.mu.Lock()
		fake.embedVecs["embed-model:latest"] = []float64{1, 0}
		delete(fake.embedRaw, "embed-model:latest")
		fake.tags = []map[string]any{
			{"name": "embed-model:latest", "digest": "sha256:embed1"},
			{"name": "chat-model:latest", "digest": "sha256:chat1"},
			{"name": "plain-model:latest", "digest": "sha256:plain1"},
		}
		fake.mu.Unlock()
	}

	for _, tc := range []struct {
		name  string
		setup func()
	}{
		{"missing digest", func() {
			setTags([]map[string]any{
				{"name": "embed-model:latest", "digest": "sha256:changed"},
				{"name": "chat-model:latest", "digest": "sha256:chat1"},
			})
		}},
		{"model gone", func() {
			setTags([]map[string]any{
				{"name": "chat-model:latest", "digest": "sha256:chat1"},
			})
		}},
		{"wrong dims", func() { setVec([]float64{1, 0, 0}) }},
		{"zero vector", func() { setVec([]float64{0, 0}) }},
		{"empty vector", func() { setVec(nil) }},
		{"nonfinite vector", func() { setRaw(`{"embedding":[NaN,0]}`) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup()
			defer reset()
			if _, _, err := ragToolCall(t, srv, sink, body, "rag_create_entry",
				map[string]any{"filename": filename, "term": "x", "content": "y"}); err == nil {
				t.Fatal("create should fail")
			}
			if _, _, err := ragToolCall(t, srv, sink, body, "rag_update_entry",
				map[string]any{"filename": filename, "entry_id": snap.ID, "expected_revision": snap.Revision, "term": "x"}); err == nil {
				t.Fatal("update should fail")
			}
			after, err := ragpkg.Get(dir, filename)
			if err != nil || len(after.Entries) != 1 || after.Entries[0].Term != "apple" {
				t.Fatalf("failed mutation changed the base: %+v %v", after.Entries, err)
			}
		})
	}
}

func TestRAGAgentMetadataOpsWithoutEmbeddingModel(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)
	_, filename := createChatRAGBase(t, dir, "Ghost", "ghost-model:latest", "sha256:ghost", 2, []ragpkg.Entry{
		{Term: "a", Content: "c", Embedding: []float64{1, 0}},
	})
	d, err := ragpkg.Get(dir, filename)
	if err != nil {
		t.Fatal(err)
	}
	_, snap, err := ragpkg.ReadEntry(context.Background(), dir, filename, d.Entries[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	sink := &recordSink{}
	body := ragAgentBody(filename, true)

	out, _, err := ragToolCall(t, srv, sink, body, "rag_update_base",
		map[string]any{"filename": filename, "expected_revision": ragpkg.MetaRevision(d.Meta), "name": "Renamed"})
	if err != nil || !strings.Contains(out, "Renamed") {
		t.Fatalf("rename without model = %q %v", out, err)
	}
	out, _, err = ragToolCall(t, srv, sink, body, "rag_delete_entry",
		map[string]any{"filename": filename, "entry_id": snap.ID, "expected_revision": snap.Revision})
	if err != nil || !strings.Contains(out, `"entries":0`) {
		t.Fatalf("delete without model = %q %v", out, err)
	}
	if fake.embedCount() != 0 {
		t.Fatalf("metadata ops must not embed: %d", fake.embedCount())
	}
	after, err := ragpkg.Get(dir, filename)
	if err != nil || after.Meta.Name != "Renamed" || len(after.Entries) != 0 {
		t.Fatalf("final base = %+v %v", after.Meta, err)
	}
}

func TestRAGAgentChainedReadThenUpdate(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)
	_, filename := createChatRAGBase(t, dir, "Fruits", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "apple", Content: "an apple is a fruit", Embedding: []float64{1, 0}},
	})
	d, err := ragpkg.Get(dir, filename)
	if err != nil {
		t.Fatal(err)
	}
	entryID := d.Entries[0].ID

	fake.stepFn = func(req ollama.ChatRequest) ragAgentChatStep {
		var rev string
		for _, m := range req.Messages {
			if m.Role != "tool" || m.ToolName != "rag_get_entry" {
				continue
			}
			var res struct {
				Revision string `json:"revision"`
			}
			if json.Unmarshal([]byte(m.Content), &res) == nil {
				rev = res.Revision
			}
		}
		for _, m := range req.Messages {
			if m.Role == "tool" && m.ToolName == "rag_update_entry" {
				return ragAgentChatStep{content: "entry updated"}
			}
		}
		if rev == "" {
			return ragAgentChatStep{toolName: "rag_get_entry", toolArgs: map[string]any{
				"filename": filename, "entry_id": entryID}}
		}
		return ragAgentChatStep{toolName: "rag_update_entry", toolArgs: map[string]any{
			"filename": filename, "entry_id": entryID, "expected_revision": rev,
			"term": "golden apple", "content": "a golden apple"}}
	}

	code, sse := postChat(t, srv, map[string]any{
		"model":        "chat-model:latest",
		"messages":     []map[string]any{{"role": "user", "content": "improve the apple entry"}},
		"rag_enabled":  true,
		"rag_paths":    []string{filename},
		"rag_editable": []string{filename},
	})
	if code != http.StatusOK {
		t.Fatalf("chat status = %d, body = %s", code, sse)
	}
	if !strings.Contains(sse, "entry updated") || !strings.Contains(sse, "rag_updated") {
		t.Fatalf("stream = %s", sse)
	}
	_, snap, err := ragpkg.ReadEntry(context.Background(), dir, filename, entryID)
	if err != nil || snap.Entry.Term != "golden apple" || snap.Entry.Content != "a golden apple" {
		t.Fatalf("updated entry = %+v %v", snap.Entry, err)
	}
	last := fake.lastEmbed()
	if last == nil || last["input"] != "golden apple\n\na golden apple" {
		t.Fatalf("update embed input = %v", last)
	}
}

func TestRAGAgentForgedWebCallDenied(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	dir := ragDirOf(t, srv)
	_, filename := createChatRAGBase(t, dir, "Fruits", "embed-model:latest", "sha256:embed1", 2, []ragpkg.Entry{
		{Term: "apple", Content: "c", Embedding: []float64{1, 0}},
	})
	fake.chatSteps = []ragAgentChatStep{
		{toolName: "web_search", toolArgs: map[string]any{"query": "x"}},
		{content: "denied"},
	}
	code, sse := postChat(t, srv, map[string]any{
		"model":       "chat-model:latest",
		"messages":    []map[string]any{{"role": "user", "content": "hi"}},
		"rag_enabled": true,
		"rag_paths":   []string{filename},
	})
	if code != http.StatusOK {
		t.Fatalf("chat status = %d", code)
	}
	if !strings.Contains(sse, `"name":"web_search"`) || !strings.Contains(sse, `"ok":false`) {
		t.Fatalf("forged web call must fail: %s", sse)
	}
	if !strings.Contains(sse, "web tools are not enabled for this chat") {
		t.Fatalf("denied reason missing: %s", sse)
	}
}
