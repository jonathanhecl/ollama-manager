package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gense/ollama-manager/internal/ollama"
)

func writeTestSkillFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverSkillsAndFrontmatter(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)

	tmp := t.TempDir()
	dir1 := filepath.Join(tmp, "skills-primary")
	dir2 := filepath.Join(tmp, "skills-secondary")
	dirDirect := filepath.Join(tmp, "direct-skill")
	missingDir := filepath.Join(tmp, "does-not-exist")

	// Depth 1 skill with rich YAML frontmatter and extra files.
	writeTestSkillFile(t, filepath.Join(dir1, "pdf-reader", "SKILL.md"), `---
name: pdf-reader
description: >
  Extract text and tables from PDF documents.
  Supports multi-page layouts.
license: MIT
compatibility: Requires python3
allowed-tools: Bash(python3:*) Read
---

# PDF Reader
Use scripts/extract.py to inspect documents.
`)
	writeTestSkillFile(t, filepath.Join(dir1, "pdf-reader", "references", "guide.md"), "# Guide\nRead carefully.\n")
	writeTestSkillFile(t, filepath.Join(dir1, "pdf-reader", "scripts", "extract.py"), "print('ok')\n")

	// Depth 2 grouped skill with lowercase skill.md and no frontmatter (falls back to dir name & paragraph).
	writeTestSkillFile(t, filepath.Join(dir1, "web-pack", "api-helper", "skill.md"), "# Heading\n\nHelpful REST endpoint checklist for API design.\n")

	// Hidden and node_modules directories must be ignored.
	writeTestSkillFile(t, filepath.Join(dir1, ".hidden-skill", "SKILL.md"), "---\nname: hidden\ndescription: ignore\n---\n")
	writeTestSkillFile(t, filepath.Join(dir1, "node_modules", "ignored-skill", "SKILL.md"), "---\nname: ignored\ndescription: ignore\n---\n")

	// Secondary directory: duplicate name must be skipped (first directory wins), unique skill included.
	writeTestSkillFile(t, filepath.Join(dir2, "pdf-reader", "SKILL.md"), "---\nname: pdf-reader\ndescription: Duplicate override should be ignored\n---\n")
	writeTestSkillFile(t, filepath.Join(dir2, "sql-tuning", "SKILL.md"), "---\nname: sql-tuning\ndescription: |-\n  Analyze slow PostgreSQL queries.\n---\nBody.\n")

	// Depth 0 skill pointing straight to one skill directory.
	writeTestSkillFile(t, filepath.Join(dirDirect, "SKILL.md"), "---\nname: direct-ops\ndescription: Direct folder skill.\n---\n")

	srv.cfgMu.Lock()
	srv.cfg.Skills.Directories = []string{dir1, dir2, dirDirect, missingDir}
	srv.cfgMu.Unlock()

	skills, statuses := srv.discoverSkills()
	if len(statuses) != 4 {
		t.Fatalf("statuses len = %d, want 4", len(statuses))
	}
	if !statuses[0].Exists || statuses[0].SkillCount != 2 {
		t.Fatalf("dir1 status = %+v, want exists=true skill_count=2", statuses[0])
	}
	if !statuses[1].Exists || statuses[1].SkillCount != 1 {
		t.Fatalf("dir2 status = %+v, want exists=true skill_count=1 (duplicate skipped)", statuses[1])
	}
	if !statuses[2].Exists || statuses[2].SkillCount != 1 {
		t.Fatalf("dirDirect status = %+v, want exists=true skill_count=1", statuses[2])
	}
	if statuses[3].Exists || statuses[3].SkillCount != 0 {
		t.Fatalf("missingDir status = %+v, want exists=false skill_count=0", statuses[3])
	}

	if len(skills) != 4 {
		t.Fatalf("discovered skills len = %d, want 4: %+v", len(skills), skills)
	}
	byName := map[string]DiscoveredSkill{}
	for _, sk := range skills {
		byName[sk.Name] = sk
	}
	pdf, ok := byName["pdf-reader"]
	if !ok {
		t.Fatalf("missing pdf-reader in %+v", byName)
	}
	if pdf.Description != "Extract text and tables from PDF documents. Supports multi-page layouts." {
		t.Fatalf("pdf description = %q", pdf.Description)
	}
	if pdf.License != "MIT" || pdf.Compatibility != "Requires python3" || pdf.AllowedTools != "Bash(python3:*) Read" {
		t.Fatalf("pdf metadata unexpected: %+v", pdf)
	}
	apiHelper, ok := byName["api-helper"]
	if !ok || apiHelper.Description != "Helpful REST endpoint checklist for API design." {
		t.Fatalf("api-helper fallback unexpected: %+v", apiHelper)
	}
	if sql, ok := byName["sql-tuning"]; !ok || sql.Description != "Analyze slow PostgreSQL queries." {
		t.Fatalf("sql-tuning unexpected: %+v", sql)
	}
	if _, ok := byName["direct-ops"]; !ok {
		t.Fatalf("missing direct-ops")
	}

	// Verify GET /api/skills endpoint returns matching catalog.
	req := httptest.NewRequest(http.MethodGet, "/api/skills", nil)
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/skills status = %d: %s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Count       int                    `json:"count"`
		Directories []SkillDirectoryStatus `json:"directories"`
		Skills      []DiscoveredSkill      `json:"skills"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON from /api/skills: %v", err)
	}
	if payload.Count != 4 || len(payload.Skills) != 4 || len(payload.Directories) != 4 {
		t.Fatalf("/api/skills payload unexpected: %+v", payload)
	}

	// Verify PATCH /api/config + GET /api/config round-trips skills settings.
	patchBody := map[string]any{
		"skills":        map[string]any{"directories": []string{dir1, dir2}},
		"chat_defaults": map[string]any{"think_level": "auto", "skills": true},
	}
	rawPatch, _ := json.Marshal(patchBody)
	pReq := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewReader(rawPatch))
	pReq.Header.Set("Content-Type", "application/json")
	pRR := httptest.NewRecorder()
	srv.Routes().ServeHTTP(pRR, pReq)
	if pRR.Code != http.StatusOK {
		t.Fatalf("PATCH /api/config status = %d: %s", pRR.Code, pRR.Body.String())
	}
	gReq := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	gRR := httptest.NewRecorder()
	srv.Routes().ServeHTTP(gRR, gReq)
	if gRR.Code != http.StatusOK {
		t.Fatalf("GET /api/config status = %d", gRR.Code)
	}
	var cfgResp struct {
		Skills struct {
			Directories []string `json:"directories"`
		} `json:"skills"`
		SkillsDirectories []string `json:"skills_directories"`
		ChatDefaults      struct {
			Skills bool `json:"skills"`
		} `json:"chat_defaults"`
	}
	if err := json.Unmarshal(gRR.Body.Bytes(), &cfgResp); err != nil {
		t.Fatal(err)
	}
	if !cfgResp.ChatDefaults.Skills || len(cfgResp.Skills.Directories) != 2 || len(cfgResp.SkillsDirectories) != 2 {
		t.Fatalf("GET /api/config skills fields unexpected: %+v", cfgResp)
	}
}

func TestSkillToolsSandboxing(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)

	tmp := t.TempDir()
	skillsRoot := filepath.Join(tmp, "skills")
	secretFile := filepath.Join(tmp, "secret.txt")
	if err := os.WriteFile(secretFile, []byte("TOP_SECRET_OUTSIDE"), 0644); err != nil {
		t.Fatal(err)
	}

	writeTestSkillFile(t, filepath.Join(skillsRoot, "demo-skill", "SKILL.md"), "---\nname: demo-skill\ndescription: Demo skill\n---\nFollow guide.\n")
	writeTestSkillFile(t, filepath.Join(skillsRoot, "demo-skill", "references", "guide.md"), "Reference details\n")
	writeTestSkillFile(t, filepath.Join(skillsRoot, "demo-skill", ".git", "config"), "ignored\n")
	if err := os.WriteFile(filepath.Join(skillsRoot, "demo-skill", "assets", "blob.bin"), func() []byte {
		_ = os.MkdirAll(filepath.Join(skillsRoot, "demo-skill", "assets"), 0755)
		return []byte{0x00, 0x01, 0xff, 0xfe}
	}(), 0644); err != nil {
		_ = os.MkdirAll(filepath.Join(skillsRoot, "demo-skill", "assets"), 0755)
		if err2 := os.WriteFile(filepath.Join(skillsRoot, "demo-skill", "assets", "blob.bin"), []byte{0x00, 0x01, 0xff, 0xfe}, 0644); err2 != nil {
			t.Fatal(err2)
		}
	}
	largeContent := strings.Repeat("A", maxSkillFileReadBytes+1024)
	writeTestSkillFile(t, filepath.Join(skillsRoot, "demo-skill", "references", "large.txt"), largeContent)

	symlinkSupported := true
	linkPath := filepath.Join(skillsRoot, "demo-skill", "references", "escape-link.txt")
	if err := os.Symlink(secretFile, linkPath); err != nil {
		symlinkSupported = false
	}

	srv.cfgMu.Lock()
	srv.cfg.Skills.Directories = []string{skillsRoot}
	srv.cfgMu.Unlock()

	call := func(name string, args any) string {
		raw, _ := json.Marshal(args)
		out, err := srv.runSkillTool(context.Background(), name, raw)
		if err != nil {
			t.Fatalf("runSkillTool(%s) unexpected error: %v", name, err)
		}
		return out
	}

	loaded := call("load_skill", map[string]any{"name": "demo-skill"})
	if !strings.Contains(loaded, "# Skill: demo-skill") || !strings.Contains(loaded, "references/guide.md") || !strings.Contains(loaded, "Follow guide.") {
		t.Fatalf("load_skill output unexpected: %q", loaded)
	}
	if strings.Contains(loaded, ".git") {
		t.Fatalf("load_skill leaked hidden dir: %q", loaded)
	}

	listed := call("list_skill_files", map[string]any{"skill": "demo-skill", "path": "references"})
	if !strings.Contains(listed, "guide.md") {
		t.Fatalf("list_skill_files(references) = %q", listed)
	}
	if strings.Contains(listed, "escape-link.txt") {
		t.Fatalf("list_skill_files leaked symlink escaping skill dir: %q", listed)
	}

	readOK := call("read_skill_file", map[string]any{"skill": "demo-skill", "path": "references/guide.md"})
	if readOK != "Reference details\n" {
		t.Fatalf("read_skill_file(references/guide.md) = %q", readOK)
	}

	readLarge := call("read_skill_file", map[string]any{"skill": "demo-skill", "path": "references/large.txt"})
	if !strings.Contains(readLarge, "truncated at") || strings.Contains(readLarge, largeContent) {
		t.Fatalf("large file was not truncated safely: len=%d", len(readLarge))
	}

	readBin := call("read_skill_file", map[string]any{"skill": "demo-skill", "path": "assets/blob.bin"})
	if !strings.Contains(readBin, "Error:") || !strings.Contains(readBin, "binary") {
		t.Fatalf("binary file should be rejected: %q", readBin)
	}

	// Traversal & absolute paths must be denied without revealing external content.
	for _, badPath := range []string{"../secret.txt", "../../secret.txt", secretFile, "/etc/passwd"} {
		out := call("read_skill_file", map[string]any{"skill": "demo-skill", "path": badPath})
		if !strings.HasPrefix(out, "Error:") || strings.Contains(out, "TOP_SECRET_OUTSIDE") {
			t.Fatalf("path %q was not blocked cleanly: %q", badPath, out)
		}
		listOut := call("list_skill_files", map[string]any{"skill": "demo-skill", "path": badPath})
		if !strings.HasPrefix(listOut, "Error:") {
			t.Fatalf("list_skill_files path %q was not blocked: %q", badPath, listOut)
		}
	}

	if symlinkSupported {
		out := call("read_skill_file", map[string]any{"skill": "demo-skill", "path": "references/escape-link.txt"})
		if !strings.HasPrefix(out, "Error:") || strings.Contains(out, "TOP_SECRET_OUTSIDE") {
			t.Fatalf("symlink escape was not blocked: %q", out)
		}
	}
}

func TestSkillsInWebToolAgentLoopReadOnly(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	fake2 := newFakeOllamaRAGAgent()
	defer fake2.Close()
	srv2 := newTestServer(t, fake2.srv.URL)
	t.Chdir(t.TempDir())

	skillsRoot := filepath.Join(t.TempDir(), "skills")
	writeTestSkillFile(t, filepath.Join(skillsRoot, "brand-voice", "SKILL.md"), "---\nname: brand-voice\ndescription: Company tone and writing rules.\n---\nAlways check references/tone.md.\n")
	writeTestSkillFile(t, filepath.Join(skillsRoot, "brand-voice", "references", "tone.md"), "Keep answers concise and friendly.\n")
	writeTestSkillFile(t, filepath.Join(skillsRoot, "code-review", "SKILL.md"), "---\nname: code-review\ndescription: Systematic PR checklist.\n---\nCheck tests.\n")

	srv.cfgMu.Lock()
	srv.cfg.Skills.Directories = []string{skillsRoot}
	srv.cfgMu.Unlock()

	// Part 1: Skills ON, Artifacts OFF, Internet OFF -> only skill tools, full catalog injected, read-only.
	fake.chatSteps = []ragAgentChatStep{
		{toolName: "load_skill", toolArgs: map[string]any{"name": "brand-voice"}},
		{toolName: "read_skill_file", toolArgs: map[string]any{"skill": "brand-voice", "path": "references/tone.md"}},
		{content: "Following brand-voice: kept it concise and friendly."},
	}

	code, sse := postChat(t, srv, map[string]any{
		"model":     "chat-model:latest",
		"messages":  []map[string]any{{"role": "user", "content": "Help me write using brand-voice"}},
		"skills":    true,
		"artifacts": false,
		"web_tools": false,
	})
	if code != http.StatusOK {
		t.Fatalf("POST /api/chat status = %d: %s", code, sse)
	}
	if !strings.Contains(sse, "kept it concise and friendly") {
		t.Fatalf("expected final response in SSE, got %q", sse)
	}

	reqs := fake.chatRequests()
	if len(reqs) != 3 {
		t.Fatalf("chat requests = %d, want 3", len(reqs))
	}
	sysMsg := reqs[0].Messages[0]
	if sysMsg.Role != "system" || !strings.Contains(sysMsg.Content, "<available_skills>") ||
		!strings.Contains(sysMsg.Content, "<name>brand-voice</name>") ||
		!strings.Contains(sysMsg.Content, "<name>code-review</name>") {
		t.Fatalf("system prompt missing full skills catalog: %q", sysMsg.Content)
	}
	for i, req := range reqs {
		names := ragAgentToolNames(req)
		if !names["load_skill"] || !names["list_skill_files"] || !names["read_skill_file"] {
			t.Errorf("req %d missing skill tools: %v", i, names)
		}
		for _, forbidden := range []string{"write_file", "replace_in_file", "exec", "create_artifact", "web_search", "web_fetch"} {
			if names[forbidden] {
				t.Errorf("req %d exposed forbidden tool %s without Artifacts/Internet: %v", i, forbidden, names)
			}
		}
	}
	if toolOut := lastToolContent(reqs[2], "read_skill_file"); !strings.Contains(toolOut, "Keep answers concise and friendly.") {
		t.Fatalf("read_skill_file tool response unexpected: %q", toolOut)
	}
	if _, err := os.Stat("artifacts"); !os.IsNotExist(err) {
		t.Fatalf("read-only skills mode must not create artifacts dir on disk")
	}

	// Part 2: Skills ON + Internet ON + Artifacts OFF -> both skill tools AND web tools, still no disk write tools.
	srv2.cfgMu.Lock()
	srv2.cfg.Skills.Directories = []string{skillsRoot}
	srv2.cfgMu.Unlock()
	fake2.chatSteps = []ragAgentChatStep{
		{content: "Combined skills + web read-only ready."},
	}

	code, sse = postChat(t, srv2, map[string]any{
		"model":     "chat-model:latest",
		"messages":  []map[string]any{{"role": "user", "content": "Check guidelines then search online"}},
		"skills":    true,
		"web_tools": true,
		"artifacts": false,
	})
	if code != http.StatusOK {
		t.Fatalf("POST /api/chat (skills+web) status = %d: %s", code, sse)
	}
	reqs = fake2.chatRequests()
	if len(reqs) != 1 {
		t.Fatalf("chat requests (skills+web) = %d, want 1", len(reqs))
	}
	names := ragAgentToolNames(reqs[0])
	for _, want := range []string{"load_skill", "list_skill_files", "read_skill_file", "web_search", "web_fetch"} {
		if !names[want] {
			t.Errorf("skills+web missing tool %s: %v", want, names)
		}
	}
	for _, forbidden := range []string{"write_file", "replace_in_file", "exec", "create_artifact"} {
		if names[forbidden] {
			t.Errorf("skills+web exposed disk tool %s without Artifacts: %v", forbidden, names)
		}
	}
}

func TestSkillsInArtifactAgentLoop(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	t.Chdir(t.TempDir())

	skillsRoot := filepath.Join(t.TempDir(), "skills")
	writeTestSkillFile(t, filepath.Join(skillsRoot, "html-card", "SKILL.md"), "---\nname: html-card\ndescription: Build semantic card components.\n---\nAlways use references/template.txt marker.\n")
	writeTestSkillFile(t, filepath.Join(skillsRoot, "html-card", "references", "template.txt"), "SKILL_MARKER_42")

	srv.cfgMu.Lock()
	srv.cfg.Skills.Directories = []string{skillsRoot}
	srv.cfgMu.Unlock()

	idx := 0
	var artifactsDirExistedBeforeCreate bool
	fake.stepFn = func(req ollama.ChatRequest) ragAgentChatStep {
		step := idx
		idx++
		switch step {
		case 0:
			return ragAgentChatStep{toolName: "load_skill", toolArgs: map[string]any{"name": "html-card"}}
		case 1:
			return ragAgentChatStep{toolName: "read_skill_file", toolArgs: map[string]any{"skill": "html-card", "path": "references/template.txt"}}
		case 2:
			_, err := os.Stat("artifacts")
			artifactsDirExistedBeforeCreate = !os.IsNotExist(err)
			return ragAgentChatStep{toolName: "create_artifact", toolArgs: map[string]any{"name": "card-demo", "description": "Demo card"}}
		case 3:
			return ragAgentChatStep{toolName: "write_file", toolArgs: map[string]any{
				"path":    "index.html",
				"content": "<html><body>SKILL_MARKER_42</body></html>",
			}}
		default:
			return ragAgentChatStep{content: "Created card-demo using html-card skill."}
		}
	}

	st := srv.chatSessions
	sess := st.Create("chat-model:latest", SessionSettings{Skills: true, Artifacts: true})
	if !st.AppendUser(sess.ID, "build a card using html-card", nil) {
		t.Fatal("AppendUser failed")
	}

	srv.runSessionTurn(context.Background(), sess.ID)

	got := st.Get(sess.ID)
	if got.Status != chatSessionIdle {
		t.Fatalf("session status = %q (err=%q), want idle", got.Status, got.Error)
	}
	if artifactsDirExistedBeforeCreate {
		t.Fatal("reading skills before create_artifact must not create artifacts/ folder prematurely")
	}
	if len(got.Messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(got.Messages))
	}
	asst := got.Messages[1]
	wantOrder := []string{"load_skill", "read_skill_file", "create_artifact", "write_file"}
	gotOrder := toolLogNames(asst.ToolLog)
	if strings.Join(gotOrder, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("tool order = %v, want %v", gotOrder, wantOrder)
	}
	if asst.ToolLog[0].Skill != "html-card" || asst.ToolLog[1].Skill != "html-card" {
		t.Fatalf("skill tool log metadata not persisted: %+v", asst.ToolLog[:2])
	}

	reqs := fake.chatRequests()
	if len(reqs) != 5 {
		t.Fatalf("chat requests = %d, want 5", len(reqs))
	}
	if !strings.Contains(reqs[0].Messages[0].Content, "<available_skills>") {
		t.Fatalf("artifact loop system prompt missing <available_skills>: %q", reqs[0].Messages[0].Content)
	}
	for i, req := range reqs {
		names := ragAgentToolNames(req)
		if !names["load_skill"] || !names["list_skill_files"] || !names["read_skill_file"] {
			t.Errorf("req %d: skill tools must stay available across all agent rounds: %v", i, names)
		}
		for _, ws := range []string{"write_file", "replace_in_file", "read_file", "list_dir", "exec"} {
			if !names[ws] {
				t.Errorf("req %d: workspace tool %s must be exposed in Agent Mode: %v", i, ws, names)
			}
		}
		if i < 3 && names["get_artifact_console"] {
			t.Errorf("req %d: get_artifact_console must remain hidden before preview activation: %v", i, names)
		}
		if i >= 3 && !names["get_artifact_console"] {
			t.Errorf("req %d: get_artifact_console must be exposed after create_artifact: %v", i, names)
		}
	}

	indexPath := filepath.Join("artifacts", asst.ArtifactTS, "index.html")
	written, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("artifact index.html missing: %v", err)
	}
	if !strings.Contains(string(written), "SKILL_MARKER_42") {
		t.Fatalf("artifact content = %q", string(written))
	}
}

func TestSelectiveSkillsPerChatAndSession(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	fake2 := newFakeOllamaRAGAgent()
	defer fake2.Close()
	srv2 := newTestServer(t, fake2.srv.URL)
	t.Chdir(t.TempDir())

	skillsRoot := filepath.Join(t.TempDir(), "skills")
	writeTestSkillFile(t, filepath.Join(skillsRoot, "brand-voice", "SKILL.md"), "---\nname: brand-voice\ndescription: Company tone rules.\n---\nUse friendly tone.\n")
	writeTestSkillFile(t, filepath.Join(skillsRoot, "code-review", "SKILL.md"), "---\nname: code-review\ndescription: PR checklist.\n---\nCheck tests.\n")

	srv.cfgMu.Lock()
	srv.cfg.Skills.Directories = []string{skillsRoot}
	srv.cfgMu.Unlock()
	srv2.cfgMu.Lock()
	srv2.cfg.Skills.Directories = []string{skillsRoot}
	srv2.cfgMu.Unlock()

	// 1. Explicit empty selected_skills disables skills in /api/chat.
	fake.chatSteps = []ragAgentChatStep{{content: "plain reply"}}
	code, sse := postChat(t, srv, map[string]any{
		"model":           "chat-model:latest",
		"messages":        []map[string]any{{"role": "user", "content": "hi"}},
		"skills":          true,
		"selected_skills": []string{},
	})
	if code != http.StatusOK || !strings.Contains(sse, "plain reply") {
		t.Fatalf("empty selected_skills chat failed: %d %q", code, sse)
	}
	reqs := fake.chatRequests()
	if len(reqs) != 1 || strings.Contains(reqs[0].Messages[0].Content, "<available_skills>") || len(ragAgentToolNames(reqs[0])) != 0 {
		t.Fatalf("expected plain chat without skills when selected_skills=[], got %+v", reqs)
	}

	// 2. Selecting only brand-voice hides code-review from catalog AND blocks load_skill("code-review").
	fake2.chatSteps = []ragAgentChatStep{
		{toolName: "load_skill", toolArgs: map[string]any{"name": "code-review"}},
		{toolName: "load_skill", toolArgs: map[string]any{"name": "brand-voice"}},
		{content: "done"},
	}
	code, _ = postChat(t, srv2, map[string]any{
		"model":           "chat-model:latest",
		"messages":        []map[string]any{{"role": "user", "content": "write copy"}},
		"skills":          true,
		"selected_skills": []string{"brand-voice"},
	})
	if code != http.StatusOK {
		t.Fatalf("selective skills chat status = %d", code)
	}
	reqs = fake2.chatRequests()
	if len(reqs) != 3 {
		t.Fatalf("chat requests = %d, want 3", len(reqs))
	}
	sysContent := reqs[0].Messages[0].Content
	if !strings.Contains(sysContent, "<name>brand-voice</name>") || strings.Contains(sysContent, "<name>code-review</name>") {
		t.Fatalf("catalog should contain only brand-voice, got: %q", sysContent)
	}
	if blocked := lastToolContent(reqs[1], "load_skill"); !strings.Contains(blocked, "not found") {
		t.Fatalf("expected unselected skill to be blocked, got %q", blocked)
	}
	if allowed := lastToolContent(reqs[2], "load_skill"); !strings.Contains(allowed, "Use friendly tone.") {
		t.Fatalf("expected selected skill to load, got %q", allowed)
	}

	// 3. Session settings PATCH + JSON round-trip preserves empty vs selected skills.
	sess := srv.chatSessions.Create("chat-model:latest", defaultSessionSettings())

	patchBody := `{"skills":true,"selected_skills":[]}`
	req := httptest.NewRequest(http.MethodPatch, "/api/chat/sessions/"+sess.ID+"/settings", strings.NewReader(patchBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("PATCH settings status = %d: %s", rr.Code, rr.Body.String())
	}

	got := srv.chatSessions.Get(sess.ID)
	if got == nil || !got.Settings.Skills || got.Settings.SelectedSkills == nil || len(*got.Settings.SelectedSkills) != 0 {
		t.Fatalf("expected Skills=true and non-nil empty SelectedSkills, got %+v", got)
	}
	raw, err := json.Marshal(got.Settings)
	if err != nil {
		t.Fatalf("marshal SessionSettings: %v", err)
	}
	var roundTrip SessionSettings
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatalf("unmarshal SessionSettings: %v", err)
	}
	if !roundTrip.Skills || roundTrip.SelectedSkills == nil || len(*roundTrip.SelectedSkills) != 0 {
		t.Fatalf("expected Skills=true and non-nil empty SelectedSkills after JSON round-trip, got %+v (json=%s)", roundTrip, string(raw))
	}
}

