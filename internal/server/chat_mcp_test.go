package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gense/ollama-manager/internal/config"
	"github.com/gense/ollama-manager/internal/ollama"
)

// TestMCPHelperProcess acts as a fake stdio MCP server when invoked with
// GO_WANT_MCP_HELPER=1.
func TestMCPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_HELPER") != "1" {
		return
	}
	prefix := os.Getenv("MCP_HELPER_PREFIX")
	if prefix == "" {
		prefix = "stdio"
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		if len(bytes.TrimSpace(req.ID)) == 0 {
			// Notification: do not respond.
			continue
		}
		reply := map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(req.ID),
		}
		switch req.Method {
		case "initialize":
			reply["result"] = map[string]any{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo": map[string]any{
					"name":    "helper-stdio-mcp",
					"version": "1.2.3",
				},
			}
		case "tools/list":
			var p struct {
				Cursor string `json:"cursor"`
			}
			_ = json.Unmarshal(req.Params, &p)
			if p.Cursor == "" {
				reply["result"] = map[string]any{
					"tools": []map[string]any{
						{
							"name":        "echo_note",
							"description": "Echo text with prefix",
							"inputSchema": map[string]any{
								"type":     "object",
								"required": []string{"text"},
								"properties": map[string]any{
									"text": map[string]any{"type": "string"},
								},
							},
						},
					},
					"nextCursor": "page2",
				}
			} else {
				reply["result"] = map[string]any{
					"tools": []map[string]any{
						{
							"name":        "fail_always",
							"description": "Always returns an MCP tool error",
							"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
						},
					},
				}
			}
		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			switch p.Name {
			case "echo_note":
				txt, _ := p.Arguments["text"].(string)
				reply["result"] = map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": prefix + ":" + txt},
					},
				}
			case "fail_always":
				reply["result"] = map[string]any{
					"isError": true,
					"content": []map[string]any{
						{"type": "text", "text": "boom from helper"},
					},
				}
			default:
				reply["error"] = map[string]any{"code": -32601, "message": "unknown tool"}
			}
		default:
			reply["error"] = map[string]any{"code": -32601, "message": "method not found"}
		}
		out, _ := json.Marshal(reply)
		_, _ = fmt.Fprintln(os.Stdout, string(out))
	}
	os.Exit(0)
}

func helperStdioServerConfig(prefix string) config.MCPServerConfig {
	return config.MCPServerConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=^TestMCPHelperProcess$"},
		Env: map[string]string{
			"GO_WANT_MCP_HELPER": "1",
			"MCP_HELPER_PREFIX":  prefix,
		},
	}
}

func newFakeStreamableHTTPMCPServer(t *testing.T, useSSE bool) (*httptest.Server, *int) {
	t.Helper()
	var calls int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(bytes.TrimSpace(req.ID)) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		mu.Lock()
		calls++
		mu.Unlock()
		w.Header().Set("Mcp-Session-Id", "sess-http-42")
		reply := map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(req.ID),
		}
		switch req.Method {
		case "initialize":
			reply["result"] = map[string]any{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fake-http-mcp", "version": "0.9.0"},
			}
		case "tools/list":
			reply["result"] = map[string]any{
				"tools": []map[string]any{
					{
						"name":        "lookup_ticket",
						"description": "Look up support ticket status",
						"inputSchema": map[string]any{
							"type":     "object",
							"required": []string{"id"},
							"properties": map[string]any{
								"id": map[string]any{"type": "string"},
							},
						},
					},
				},
			}
		case "tools/call":
			if gotSess := r.Header.Get("Mcp-Session-Id"); gotSess != "sess-http-42" {
				http.Error(w, "missing Mcp-Session-Id", http.StatusBadRequest)
				return
			}
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			idVal, _ := p.Arguments["id"].(string)
			reply["result"] = map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": "ticket:" + idVal + ":OPEN"},
				},
			}
		}
		if useSSE {
			w.Header().Set("Content-Type", "text/event-stream")
			data, _ := json.Marshal(reply)
			_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", string(data))
			return
		}
		writeJSON(w, http.StatusOK, reply)
	}))
	return srv, &calls
}

func TestMCPStdioAndHTTPClientsAndEndpoints(t *testing.T) {
	fakeOllama := newFakeOllamaRAGAgent()
	defer fakeOllama.Close()
	srv := newTestServer(t, fakeOllama.srv.URL)

	httpMCP, _ := newFakeStreamableHTTPMCPServer(t, false)
	defer httpMCP.Close()
	sseMCP, _ := newFakeStreamableHTTPMCPServer(t, true)
	defer sseMCP.Close()

	// Configure servers via PATCH /api/config
	patchPayload := map[string]any{
		"mcp": map[string]any{
			"mcpServers": map[string]any{
				"local-helper": helperStdioServerConfig("ok"),
				"http-tickets": map[string]any{
					"url":     httpMCP.URL,
					"headers": map[string]string{"X-Test": "1"},
				},
				"sse-tickets": map[string]any{
					"url": sseMCP.URL,
				},
			},
		},
	}
	rawPatch, _ := json.Marshal(patchPayload)
	req := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewReader(rawPatch))
	rr := httptest.NewRecorder()
	srv.handlePatchConfig(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("PATCH /api/config status = %d: %s", rr.Code, rr.Body.String())
	}

	// Probe all configured servers via POST /api/mcp/test
	testReq := httptest.NewRequest(http.MethodPost, "/api/mcp/test", bytes.NewReader([]byte(`{}`)))
	testRR := httptest.NewRecorder()
	srv.handleMCPTest(testRR, testReq)
	if testRR.Code != http.StatusOK {
		t.Fatalf("POST /api/mcp/test status = %d: %s", testRR.Code, testRR.Body.String())
	}
	var testResp struct {
		Servers []MCPServerStatus `json:"servers"`
	}
	if err := json.Unmarshal(testRR.Body.Bytes(), &testResp); err != nil {
		t.Fatal(err)
	}
	if len(testResp.Servers) != 3 {
		t.Fatalf("servers len = %d, want 3", len(testResp.Servers))
	}
	byName := map[string]MCPServerStatus{}
	for _, st := range testResp.Servers {
		byName[st.Name] = st
	}
	if st := byName["local-helper"]; !st.Connected || st.ToolCount != 2 || st.ServerName != "helper-stdio-mcp" {
		t.Fatalf("local-helper status unexpected: %+v", st)
	}
	if st := byName["http-tickets"]; !st.Connected || st.ToolCount != 1 || st.ServerName != "fake-http-mcp" {
		t.Fatalf("http-tickets status unexpected: %+v", st)
	}
	if st := byName["sse-tickets"]; !st.Connected || st.ToolCount != 1 {
		t.Fatalf("sse-tickets status unexpected: %+v", st)
	}

	// GET /api/mcp/servers should reflect cached status
	getReq := httptest.NewRequest(http.MethodGet, "/api/mcp/servers", nil)
	getRR := httptest.NewRecorder()
	srv.handleMCPServers(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("GET /api/mcp/servers status = %d: %s", getRR.Code, getRR.Body.String())
	}
	var listResp struct {
		MCP     config.MCPConfig  `json:"mcp"`
		Servers []MCPServerStatus `json:"servers"`
	}
	if err := json.Unmarshal(getRR.Body.Bytes(), &listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Servers) != 3 || !listResp.Servers[0].Connected {
		t.Fatalf("GET /api/mcp/servers unexpected: %+v", listResp.Servers)
	}

	// Direct CallTool error propagation check (isError: true)
	c, err := connectMCPClient(context.Background(), "test", helperStdioServerConfig("ok"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.CallTool(context.Background(), "fail_always", nil); err == nil || !strings.Contains(err.Error(), "boom from helper") {
		t.Fatalf("expected error from fail_always, got %v", err)
	}
}

func TestMCPLegacySSEFallback(t *testing.T) {
	var msgURL string
	var sseMu sync.Mutex
	var sseWriter http.ResponseWriter
	var sseFlusher http.Flusher
	sseReady := make(chan struct{})
	sseDone := make(chan struct{})

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sse" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/event-stream")
			fl, _ := w.(http.Flusher)
			sseMu.Lock()
			sseWriter = w
			sseFlusher = fl
			sseMu.Unlock()
			_, _ = fmt.Fprintf(w, "event: endpoint\ndata: /messages?session=legacy1\n\n")
			fl.Flush()
			close(sseReady)
			<-sseDone
			return
		}
		if r.URL.Path == "/sse" && r.Method == http.MethodPost {
			http.Error(w, "use SSE", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path == "/messages" && r.Method == http.MethodPost {
			<-sseReady
			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			msgURL = r.URL.String()
			if len(bytes.TrimSpace(req.ID)) == 0 {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			w.WriteHeader(http.StatusAccepted)
			reply := map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(req.ID)}
			if req.Method == "initialize" {
				reply["result"] = map[string]any{
					"protocolVersion": "2024-11-05",
					"serverInfo":      map[string]any{"name": "legacy-sse-mcp", "version": "1.0"},
				}
			} else if req.Method == "tools/list" {
				reply["result"] = map[string]any{
					"tools": []map[string]any{{"name": "ping_legacy", "description": "Legacy SSE tool"}},
				}
			}
			data, _ := json.Marshal(reply)
			sseMu.Lock()
			_, _ = fmt.Fprintf(sseWriter, "event: message\ndata: %s\n\n", string(data))
			sseFlusher.Flush()
			sseMu.Unlock()
			return
		}
		http.NotFound(w, r)
	}))
	defer func() {
		close(sseDone)
		srv.Close()
	}()

	client, err := connectMCPClient(context.Background(), "test", config.MCPServerConfig{
		URL: srv.URL + "/sse",
	})
	if err != nil {
		t.Fatalf("connectMCPClient legacy fallback: %v", err)
	}
	defer func() { _ = client.Close() }()
	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "ping_legacy" || msgURL == "" {
		t.Fatalf("unexpected tools=%+v msgURL=%q", tools, msgURL)
	}
}

func TestChatMCPSelectionIsolationAndExecution(t *testing.T) {
	fake := newFakeOllamaRAGAgent()
	defer fake.Close()
	srv := newTestServer(t, fake.srv.URL)
	t.Chdir(t.TempDir())

	unselectedHTTP, unselectedCalls := newFakeStreamableHTTPMCPServer(t, false)
	defer unselectedHTTP.Close()

	srv.cfgMu.Lock()
	srv.cfg.MCP = config.MCPConfig{
		MCPServers: map[string]config.MCPServerConfig{
			"notes":      helperStdioServerConfig("note"),
			"unselected": {URL: unselectedHTTP.URL},
		},
	}
	srv.cfgMu.Unlock()

	fake.chatSteps = []ragAgentChatStep{
		{toolName: "mcp__notes__echo_note", toolArgs: map[string]any{"text": "hola-mcp"}},
		{content: "The MCP tool echoed note:hola-mcp."},
	}

	// Only enable "notes"; "unselected" must NOT be connected or exposed.
	code, sse := postChat(t, srv, map[string]any{
		"model":       "chat-model:latest",
		"messages":    []map[string]any{{"role": "user", "content": "Call echo_note"}},
		"mcp":         true,
		"mcp_servers": []string{"notes"},
	})
	if code != http.StatusOK {
		t.Fatalf("POST /api/chat status = %d: %s", code, sse)
	}
	if *unselectedCalls != 0 {
		t.Fatalf("unselected MCP server was called %d times, want 0", *unselectedCalls)
	}
	if !strings.Contains(sse, `"mcp_server":"notes"`) || !strings.Contains(sse, `"mcp_tool":"echo_note"`) {
		t.Fatalf("SSE tool events missing mcp metadata: %s", sse)
	}
	if !strings.Contains(sse, "note:hola-mcp") {
		t.Fatalf("SSE missing tool preview / answer output: %s", sse)
	}

	reqs := fake.chatRequests()
	if len(reqs) != 2 {
		t.Fatalf("chat requests = %d, want 2", len(reqs))
	}
	names := ragAgentToolNames(reqs[0])
	if !names["mcp__notes__echo_note"] || !names["mcp__notes__fail_always"] {
		t.Fatalf("missing notes MCP tools: %v", names)
	}
	if names["mcp__unselected__lookup_ticket"] {
		t.Fatalf("unselected MCP server leaked into model tools: %v", names)
	}
	if gotToolOut := lastToolContent(reqs[1], "mcp__notes__echo_note"); gotToolOut != "note:hola-mcp" {
		t.Fatalf("tool output = %q, want %q", gotToolOut, "note:hola-mcp")
	}

	// Artifact mode + MCP before create_artifact must not create artifacts/ dir prematurely,
	// and persistent session must record mcp_server / mcp_tool on ToolLog.
	fake.mu.Lock()
	fake.chatReqs = nil
	fake.chatSteps = nil
	fake.mu.Unlock()

	var artifactsExistedBeforeCreate bool
	step := 0
	fake.stepFn = func(req ollama.ChatRequest) ragAgentChatStep {
		cur := step
		step++
		switch cur {
		case 0:
			return ragAgentChatStep{toolName: "mcp__notes__echo_note", toolArgs: map[string]any{"text": "from-artifact-turn"}}
		case 1:
			_, err := os.Stat("artifacts")
			artifactsExistedBeforeCreate = !os.IsNotExist(err)
			return ragAgentChatStep{content: "Done with MCP in artifact session."}
		default:
			return ragAgentChatStep{content: "Done"}
		}
	}

	st := srv.chatSessions
	sess := st.Create("chat-model:latest", SessionSettings{
		Artifacts:  true,
		MCP:        true,
		MCPServers: []string{"notes"},
	})
	if !st.AppendUser(sess.ID, "Use notes MCP without creating files", nil) {
		t.Fatal("AppendUser failed")
	}
	srv.runSessionTurn(context.Background(), sess.ID)

	got := st.Get(sess.ID)
	if got.Status != chatSessionIdle {
		t.Fatalf("session status = %q (err=%q), want idle", got.Status, got.Error)
	}
	if artifactsExistedBeforeCreate {
		t.Fatalf("calling MCP tool before create_artifact created artifacts/ prematurely")
	}
	if _, err := os.Stat(filepath.Join("artifacts")); !os.IsNotExist(err) {
		t.Fatalf("MCP-only turn in artifact session should not leave empty artifacts dir")
	}
	if len(got.Messages) != 2 || len(got.Messages[1].ToolLog) != 1 {
		t.Fatalf("unexpected tool log: %+v", got.Messages)
	}
	entry := got.Messages[1].ToolLog[0]
	if entry.MCPServer != "notes" || entry.MCPTool != "echo_note" || entry.Status != "ok" {
		t.Fatalf("session ToolLog missing MCP metadata: %+v", entry)
	}
}
