package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gense/ollama-manager/internal/config"
)

const (
	mcpProtocolVersion    = "2025-03-26"
	mcpDefaultTimeout     = 30 * time.Second
	mcpMaxToolListPages   = 10
	mcpMaxStderrBytes     = 8 * 1024
	mcpMaxQualifiedLen    = 64
	mcpMaxSummaryArgChars = 120
)

// MCPToolInfo describes a single tool exposed by an MCP server via tools/list.
type MCPToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

// MCPServerStatus reports configuration and last/live connection status for an MCP server.
type MCPServerStatus struct {
	Name            string        `json:"name"`
	Transport       string        `json:"transport"`
	Command         string        `json:"command,omitempty"`
	Args            []string      `json:"args,omitempty"`
	URL             string        `json:"url,omitempty"`
	Disabled        bool          `json:"disabled,omitempty"`
	Configured      bool          `json:"configured"`
	Connected       bool          `json:"connected"`
	ServerName      string        `json:"server_name,omitempty"`
	ServerVersion   string        `json:"server_version,omitempty"`
	ProtocolVersion string        `json:"protocol_version,omitempty"`
	Tools           []MCPToolInfo `json:"tools,omitempty"`
	ToolCount       int           `json:"tool_count"`
	Error           string        `json:"error,omitempty"`
	CheckedAt       string        `json:"checked_at,omitempty"`
}

type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonRPCNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type mcpInitResult struct {
	ProtocolVersion string `json:"protocolVersion"`
	Capabilities    any    `json:"capabilities,omitempty"`
	ServerInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

type mcpListToolsResult struct {
	Tools      []MCPToolInfo `json:"tools"`
	NextCursor string        `json:"nextCursor,omitempty"`
}

type mcpCallToolResult struct {
	Content           []mcpContentBlock `json:"content"`
	StructuredContent any               `json:"structuredContent,omitempty"`
	IsError           bool              `json:"isError,omitempty"`
}

type mcpContentBlock struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Resource *struct {
		URI      string `json:"uri,omitempty"`
		MimeType string `json:"mimeType,omitempty"`
		Text     string `json:"text,omitempty"`
		Blob     string `json:"blob,omitempty"`
	} `json:"resource,omitempty"`
}

type mcpTransport interface {
	Request(ctx context.Context, method string, params any, out any) error
	Notify(ctx context.Context, method string, params any) error
	SetProtocolVersion(version string)
	Close() error
}

type mcpClient struct {
	transport mcpTransport
	init      mcpInitResult
}

func normalizeJSONRPCID(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	if strings.HasPrefix(s, "\"") {
		var str string
		if err := json.Unmarshal(raw, &str); err == nil {
			return str
		}
	}
	return s
}

func mcpServerTimeout(cfg config.MCPServerConfig) time.Duration {
	if cfg.TimeoutSeconds > 0 {
		return time.Duration(cfg.TimeoutSeconds) * time.Second
	}
	return mcpDefaultTimeout
}

func connectMCPClient(ctx context.Context, clientVersion string, cfg config.MCPServerConfig) (*mcpClient, error) {
	transportKind := cfg.EffectiveTransport()
	if transportKind == "" {
		return nil, errors.New("MCP server requires 'command' (stdio) or 'url' (http/sse)")
	}
	var tr mcpTransport
	var err error
	switch transportKind {
	case "stdio":
		tr, err = newMCPStdioTransport(cfg)
	case "http", "sse":
		tr, err = newMCPHTTPTransport(ctx, cfg, transportKind == "sse")
	default:
		return nil, fmt.Errorf("unsupported MCP transport %q", transportKind)
	}
	if err != nil {
		return nil, err
	}

	c := &mcpClient{transport: tr}
	if strings.TrimSpace(clientVersion) == "" {
		clientVersion = "dev"
	}
	initCtx, cancel := context.WithTimeout(ctx, mcpServerTimeout(cfg))
	defer cancel()

	initParams := map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "ollama-manager",
			"version": clientVersion,
		},
	}
	if err := tr.Request(initCtx, "initialize", initParams, &c.init); err != nil {
		_ = tr.Close()
		return nil, fmt.Errorf("MCP initialize: %w", err)
	}
	if c.init.ProtocolVersion != "" {
		tr.SetProtocolVersion(c.init.ProtocolVersion)
	} else {
		tr.SetProtocolVersion(mcpProtocolVersion)
	}
	if err := tr.Notify(initCtx, "notifications/initialized", map[string]any{}); err != nil {
		// Some HTTP/SSE servers return 204 or 202; only hard transport errors fail here.
		_ = tr.Close()
		return nil, fmt.Errorf("MCP notifications/initialized: %w", err)
	}
	return c, nil
}

func (c *mcpClient) Close() error {
	if c == nil || c.transport == nil {
		return nil
	}
	return c.transport.Close()
}

func (c *mcpClient) ListTools(ctx context.Context) ([]MCPToolInfo, error) {
	var all []MCPToolInfo
	cursor := ""
	for page := 0; page < mcpMaxToolListPages; page++ {
		var params any
		if cursor != "" {
			params = map[string]any{"cursor": cursor}
		} else {
			params = map[string]any{}
		}
		var res mcpListToolsResult
		if err := c.transport.Request(ctx, "tools/list", params, &res); err != nil {
			return nil, fmt.Errorf("tools/list: %w", err)
		}
		all = append(all, res.Tools...)
		if strings.TrimSpace(res.NextCursor) == "" || res.NextCursor == cursor {
			break
		}
		cursor = res.NextCursor
	}
	return all, nil
}

func (c *mcpClient) CallTool(ctx context.Context, name string, rawArgs json.RawMessage) (string, error) {
	args := map[string]any{}
	if parsed := parseToolArgs(rawArgs); parsed != nil {
		args = parsed
	}
	var res mcpCallToolResult
	err := c.transport.Request(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	}, &res)
	if err != nil {
		return "", err
	}
	text := formatMCPToolResult(res)
	if res.IsError {
		if text == "" {
			text = fmt.Sprintf("MCP tool %q reported an error", name)
		}
		return "", errors.New(text)
	}
	return text, nil
}

func formatMCPToolResult(res mcpCallToolResult) string {
	var parts []string
	for _, block := range res.Content {
		switch strings.ToLower(strings.TrimSpace(block.Type)) {
		case "text":
			if block.Text != "" {
				parts = append(parts, block.Text)
			}
		case "image", "audio":
			mime := block.MimeType
			if mime == "" {
				mime = block.Type
			}
			parts = append(parts, fmt.Sprintf("[%s content (%s), %d encoded chars]", block.Type, mime, len(block.Data)))
		case "resource":
			if block.Resource != nil {
				if block.Resource.Text != "" {
					header := block.Resource.URI
					if header != "" {
						parts = append(parts, fmt.Sprintf("[resource %s]\n%s", header, block.Resource.Text))
					} else {
						parts = append(parts, block.Resource.Text)
					}
				} else if block.Resource.URI != "" {
					parts = append(parts, fmt.Sprintf("[resource %s (%s)]", block.Resource.URI, block.Resource.MimeType))
				}
			}
		default:
			if block.Text != "" {
				parts = append(parts, block.Text)
			} else if raw, err := json.Marshal(block); err == nil {
				parts = append(parts, string(raw))
			}
		}
	}
	if res.StructuredContent != nil {
		if raw, err := json.MarshalIndent(res.StructuredContent, "", "  "); err == nil {
			s := string(raw)
			if len(parts) == 0 {
				parts = append(parts, s)
			} else if !strings.Contains(strings.Join(parts, "\n"), s) {
				parts = append(parts, s)
			}
		}
	}
	if len(parts) == 0 {
		return "(no output)"
	}
	return strings.Join(parts, "\n\n")
}

// --- Stdio transport --------------------------------------------------------

type cappedStderrBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *cappedStderrBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	rem := mcpMaxStderrBytes - b.buf.Len()
	if rem > 0 {
		if len(p) > rem {
			_, _ = b.buf.Write(p[:rem])
		} else {
			_, _ = b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *cappedStderrBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.TrimSpace(b.buf.String())
}

type mcpStdioTransport struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	stdin  io.WriteCloser
	stderr *cappedStderrBuffer

	writeMu sync.Mutex
	nextID  atomic.Int64

	pendMu  sync.Mutex
	pending map[string]chan jsonRPCResponse
	readErr error
	doneCh  chan struct{}
}

func mergedMCPEnv(extra map[string]string) []string {
	if len(extra) == 0 {
		return os.Environ()
	}
	base := os.Environ()
	idxMap := make(map[string]int, len(base))
	out := make([]string, 0, len(base)+len(extra))
	for _, entry := range base {
		if k, _, ok := strings.Cut(entry, "="); ok {
			idxMap[k] = len(out)
		}
		out = append(out, entry)
	}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		line := k + "=" + extra[k]
		if idx, exists := idxMap[k]; exists {
			out[idx] = line
		} else {
			idxMap[k] = len(out)
			out = append(out, line)
		}
	}
	return out
}

func newMCPStdioTransport(cfg config.MCPServerConfig) (*mcpStdioTransport, error) {
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, errors.New("missing MCP stdio command")
	}
	runCtx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(runCtx, cfg.Command, cfg.Args...)
	cmd.Env = mergedMCPEnv(cfg.Env)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderrBuf := &cappedStderrBuffer{}
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start %q: %w", cfg.Command, err)
	}

	tr := &mcpStdioTransport{
		cmd:     cmd,
		cancel:  cancel,
		stdin:   stdin,
		stderr:  stderrBuf,
		pending: make(map[string]chan jsonRPCResponse),
		doneCh:  make(chan struct{}),
	}
	go tr.readLoop(stdout)
	return tr, nil
}

func (t *mcpStdioTransport) readLoop(r io.ReadCloser) {
	defer close(t.doneCh)
	reader := bufio.NewReader(r)
	var readErr error
	for {
		line, err := reader.ReadBytes('\n')
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) > 0 {
			if trimmed[0] == '{' {
				var msg jsonRPCResponse
				if uErr := json.Unmarshal(trimmed, &msg); uErr == nil {
					t.handleIncoming(msg)
				}
			} else if trimmed[0] == '[' {
				var batch []jsonRPCResponse
				if uErr := json.Unmarshal(trimmed, &batch); uErr == nil {
					for _, msg := range batch {
						t.handleIncoming(msg)
					}
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				readErr = err
			}
			break
		}
	}

	waitErr := t.cmd.Wait()
	if readErr == nil && waitErr != nil {
		readErr = waitErr
	}
	stderrText := t.stderr.String()
	if readErr == nil {
		if stderrText != "" {
			readErr = fmt.Errorf("MCP server closed stream: %s", stderrText)
		} else {
			readErr = errors.New("MCP server closed stream")
		}
	} else if stderrText != "" {
		readErr = fmt.Errorf("%w (%s)", readErr, stderrText)
	}

	t.pendMu.Lock()
	t.readErr = readErr
	for id, ch := range t.pending {
		delete(t.pending, id)
		close(ch)
	}
	t.pendMu.Unlock()
}

func (t *mcpStdioTransport) handleIncoming(msg jsonRPCResponse) {
	idKey := normalizeJSONRPCID(msg.ID)
	if idKey == "" {
		// Server-initiated notification (e.g. notifications/tools/list_changed): ignore cleanly.
		return
	}
	if msg.Method != "" && len(msg.Result) == 0 && msg.Error == nil {
		// Unexpected server->client request: reply method not found so server does not stall.
		t.sendRaw(map[string]any{
			"jsonrpc": "2.0",
			"id":      msg.ID,
			"error": map[string]any{
				"code":    -32601,
				"message": "Method not found",
			},
		})
		return
	}
	t.pendMu.Lock()
	ch, ok := t.pending[idKey]
	if ok {
		delete(t.pending, idKey)
	}
	t.pendMu.Unlock()
	if ok {
		ch <- msg
	}
}

func (t *mcpStdioTransport) sendRaw(payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	_, err = t.stdin.Write(data)
	return err
}

func (t *mcpStdioTransport) SetProtocolVersion(_ string) {}

func (t *mcpStdioTransport) Notify(_ context.Context, method string, params any) error {
	return t.sendRaw(jsonRPCNotification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	})
}

func (t *mcpStdioTransport) Request(ctx context.Context, method string, params any, out any) error {
	id := t.nextID.Add(1)
	idKey := strconv.FormatInt(id, 10)
	ch := make(chan jsonRPCResponse, 1)

	t.pendMu.Lock()
	if t.readErr != nil {
		err := t.readErr
		t.pendMu.Unlock()
		return err
	}
	t.pending[idKey] = ch
	t.pendMu.Unlock()

	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	if err := t.sendRaw(req); err != nil {
		t.pendMu.Lock()
		delete(t.pending, idKey)
		t.pendMu.Unlock()
		if stderrText := t.stderr.String(); stderrText != "" {
			return fmt.Errorf("%w (%s)", err, stderrText)
		}
		return err
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			t.pendMu.Lock()
			err := t.readErr
			t.pendMu.Unlock()
			if err != nil {
				return err
			}
			return errors.New("MCP server closed unexpectedly")
		}
		if resp.Error != nil {
			return fmt.Errorf("JSON-RPC %d: %s", resp.Error.Code, resp.Error.Message)
		}
		if out != nil && len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, out); err != nil {
				return fmt.Errorf("decode %s result: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		t.pendMu.Lock()
		delete(t.pending, idKey)
		t.pendMu.Unlock()
		return ctx.Err()
	}
}

func (t *mcpStdioTransport) Close() error {
	t.writeMu.Lock()
	_ = t.stdin.Close()
	t.writeMu.Unlock()

	select {
	case <-t.doneCh:
	case <-time.After(250 * time.Millisecond):
		t.cancel()
		select {
		case <-t.doneCh:
		case <-time.After(500 * time.Millisecond):
		}
	}
	t.cancel()
	return nil
}

// --- HTTP / Streamable HTTP / SSE transport ---------------------------------

type mcpHTTPTransport struct {
	endpointURL string
	headers     map[string]string
	httpClient  *http.Client

	mu              sync.Mutex
	sessionID       string
	protocolVersion string
	legacyPostURL   string
	nextID          atomic.Int64

	// Legacy SSE stream state (used only if transport is explicitly "sse" or
	// POST returns 405 Method Not Allowed on initialize).
	sseCancel context.CancelFunc
	sseDone   chan struct{}
	sseErr    error
	pendMu    sync.Mutex
	pending   map[string]chan jsonRPCResponse
}

func newMCPHTTPTransport(ctx context.Context, cfg config.MCPServerConfig, preferLegacySSE bool) (*mcpHTTPTransport, error) {
	rawURL := strings.TrimSpace(cfg.URL)
	if rawURL == "" {
		return nil, errors.New("missing MCP server URL")
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("invalid MCP server URL %q", rawURL)
	}
	tr := &mcpHTTPTransport{
		endpointURL: rawURL,
		headers:     cfg.Headers,
		httpClient:  &http.Client{},
		pending:     make(map[string]chan jsonRPCResponse),
	}
	if preferLegacySSE {
		if err := tr.startLegacySSE(ctx, mcpServerTimeout(cfg)); err != nil {
			return nil, err
		}
	}
	return tr, nil
}

func (t *mcpHTTPTransport) SetProtocolVersion(version string) {
	t.mu.Lock()
	t.protocolVersion = version
	t.mu.Unlock()
}

func (t *mcpHTTPTransport) applyCustomHeaders(req *http.Request) {
	for k, v := range t.headers {
		if strings.TrimSpace(k) != "" {
			req.Header.Set(k, v)
		}
	}
}

func (t *mcpHTTPTransport) startLegacySSE(ctx context.Context, timeout time.Duration) error {
	sseCtx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(sseCtx, http.MethodGet, t.endpointURL, nil)
	if err != nil {
		cancel()
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	t.applyCustomHeaders(req)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		cancel()
		return fmt.Errorf("connect SSE %s: %w", t.endpointURL, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		_ = resp.Body.Close()
		cancel()
		return fmt.Errorf("SSE HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	t.sseCancel = cancel
	t.sseDone = make(chan struct{})
	endpointReady := make(chan string, 1)
	errCh := make(chan error, 1)

	go t.readLegacySSE(resp.Body, endpointReady, errCh)

	waitCtx, waitCancel := context.WithTimeout(ctx, timeout)
	defer waitCancel()
	select {
	case ep := <-endpointReady:
		t.mu.Lock()
		t.legacyPostURL = ep
		t.mu.Unlock()
		return nil
	case err := <-errCh:
		_ = t.Close()
		return err
	case <-waitCtx.Done():
		_ = t.Close()
		return fmt.Errorf("timed out waiting for SSE endpoint event: %w", waitCtx.Err())
	}
}

func resolveSSERelativeEndpoint(baseURL, ep string) (string, error) {
	ep = strings.TrimSpace(ep)
	if ep == "" {
		return "", errors.New("empty SSE endpoint")
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(ep)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(ref).String(), nil
}

func scanSSEEvents(r io.Reader, handle func(eventType, data string) (stop bool)) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	eventType := ""
	var dataLines []string

	flush := func() bool {
		if len(dataLines) == 0 && eventType == "" {
			return false
		}
		data := strings.Join(dataLines, "\n")
		ev := eventType
		eventType = ""
		dataLines = dataLines[:0]
		if ev == "" && strings.TrimSpace(data) == "" {
			return false
		}
		return handle(ev, data)
	}

	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" {
			if flush() {
				return nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, val, hasColon := strings.Cut(line, ":")
		if hasColon {
			val = strings.TrimPrefix(val, " ")
		}
		switch field {
		case "event":
			eventType = val
		case "data":
			dataLines = append(dataLines, val)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	flush()
	return nil
}

func (t *mcpHTTPTransport) readLegacySSE(body io.ReadCloser, endpointReady chan<- string, initErr chan<- error) {
	defer close(t.sseDone)
	defer func() { _ = body.Close() }()

	gotEndpoint := false
	err := scanSSEEvents(body, func(eventType, data string) bool {
		if eventType == "endpoint" || (!gotEndpoint && !strings.HasPrefix(strings.TrimSpace(data), "{")) {
			resolved, rErr := resolveSSERelativeEndpoint(t.endpointURL, data)
			if rErr == nil && !gotEndpoint {
				gotEndpoint = true
				endpointReady <- resolved
				return false
			}
		}
		trimmed := strings.TrimSpace(data)
		if strings.HasPrefix(trimmed, "{") {
			var msg jsonRPCResponse
			if json.Unmarshal([]byte(trimmed), &msg) == nil {
				t.deliverSSEResponse(msg)
			}
		} else if strings.HasPrefix(trimmed, "[") {
			var batch []jsonRPCResponse
			if json.Unmarshal([]byte(trimmed), &batch) == nil {
				for _, msg := range batch {
					t.deliverSSEResponse(msg)
				}
			}
		}
		return false
	})
	if err == nil {
		err = errors.New("MCP SSE stream closed")
	}
	if !gotEndpoint {
		select {
		case initErr <- err:
		default:
		}
	}
	t.pendMu.Lock()
	t.sseErr = err
	for id, ch := range t.pending {
		delete(t.pending, id)
		close(ch)
	}
	t.pendMu.Unlock()
}

func (t *mcpHTTPTransport) deliverSSEResponse(msg jsonRPCResponse) {
	idKey := normalizeJSONRPCID(msg.ID)
	if idKey == "" {
		return
	}
	t.pendMu.Lock()
	ch, ok := t.pending[idKey]
	if ok {
		delete(t.pending, idKey)
	}
	t.pendMu.Unlock()
	if ok {
		ch <- msg
	}
}

func (t *mcpHTTPTransport) postJSON(ctx context.Context, payload any) (*http.Response, error) {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	targetURL := t.endpointURL
	if t.legacyPostURL != "" {
		targetURL = t.legacyPostURL
	}
	sessID := t.sessionID
	protoVer := t.protocolVersion
	t.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	t.applyCustomHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sessID != "" {
		req.Header.Set("Mcp-Session-Id", sessID)
	}
	if protoVer != "" {
		req.Header.Set("MCP-Protocol-Version", protoVer)
	}
	return t.httpClient.Do(req)
}

func (t *mcpHTTPTransport) Notify(ctx context.Context, method string, params any) error {
	resp, err := t.postJSON(ctx, jsonRPCNotification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d on %s", resp.StatusCode, method)
	}
	return nil
}

func (t *mcpHTTPTransport) Request(ctx context.Context, method string, params any, out any) error {
	id := t.nextID.Add(1)
	idKey := strconv.FormatInt(id, 10)

	t.mu.Lock()
	isLegacySSE := t.legacyPostURL != ""
	t.mu.Unlock()

	var ch chan jsonRPCResponse
	if isLegacySSE {
		ch = make(chan jsonRPCResponse, 1)
		t.pendMu.Lock()
		if t.sseErr != nil {
			err := t.sseErr
			t.pendMu.Unlock()
			return err
		}
		t.pending[idKey] = ch
		t.pendMu.Unlock()
	}

	reqPayload := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	resp, err := t.postJSON(ctx, reqPayload)
	if err != nil {
		if ch != nil {
			t.pendMu.Lock()
			delete(t.pending, idKey)
			t.pendMu.Unlock()
		}
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if sid := strings.TrimSpace(resp.Header.Get("Mcp-Session-Id")); sid != "" {
		t.mu.Lock()
		t.sessionID = sid
		t.mu.Unlock()
	}

	// Automatic fallback: if a user configured a legacy HTTP+SSE server URL
	// without setting transport:"sse", POST /sse returns 405 on initialize.
	if method == "initialize" && !isLegacySSE && resp.StatusCode == http.StatusMethodNotAllowed {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		if sseErr := t.startLegacySSE(ctx, mcpDefaultTimeout); sseErr == nil {
			return t.Request(ctx, method, params, out)
		}
		return fmt.Errorf("HTTP 405 Method Not Allowed on %s", t.endpointURL)
	}

	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		if ch != nil {
			t.pendMu.Lock()
			delete(t.pending, idKey)
			t.pendMu.Unlock()
		}
		msg := strings.TrimSpace(string(errBody))
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg)
	}

	if isLegacySSE && resp.StatusCode == http.StatusAccepted {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
		select {
		case rpcResp, ok := <-ch:
			if !ok {
				t.pendMu.Lock()
				err := t.sseErr
				t.pendMu.Unlock()
				if err != nil {
					return err
				}
				return errors.New("MCP SSE stream closed")
			}
			return decodeRPCResponse(method, rpcResp, out)
		case <-ctx.Done():
			t.pendMu.Lock()
			delete(t.pending, idKey)
			t.pendMu.Unlock()
			return ctx.Err()
		}
	}
	if ch != nil {
		t.pendMu.Lock()
		delete(t.pending, idKey)
		t.pendMu.Unlock()
	}

	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "text/event-stream") {
		rpcResp, err := readMatchingSSEResponse(resp.Body, idKey)
		if err != nil {
			return err
		}
		return decodeRPCResponse(method, rpcResp, out)
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return fmt.Errorf("read %s response: %w", method, err)
	}
	rpcResp, err := extractJSONRPCResponse(bodyBytes, idKey)
	if err != nil {
		return err
	}
	return decodeRPCResponse(method, rpcResp, out)
}

func readMatchingSSEResponse(r io.Reader, wantID string) (jsonRPCResponse, error) {
	var matched jsonRPCResponse
	var found bool
	err := scanSSEEvents(r, func(_ string, data string) bool {
		resp, err := extractJSONRPCResponse([]byte(data), wantID)
		if err == nil {
			matched = resp
			found = true
			return true
		}
		return false
	})
	if found {
		return matched, nil
	}
	if err != nil {
		return jsonRPCResponse{}, err
	}
	return jsonRPCResponse{}, fmt.Errorf("SSE response ended without matching JSON-RPC id %s", wantID)
}

func extractJSONRPCResponse(data []byte, wantID string) (jsonRPCResponse, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return jsonRPCResponse{}, errors.New("empty JSON-RPC response")
	}
	if trimmed[0] == '[' {
		var batch []jsonRPCResponse
		if err := json.Unmarshal(trimmed, &batch); err != nil {
			return jsonRPCResponse{}, err
		}
		for _, item := range batch {
			if normalizeJSONRPCID(item.ID) == wantID {
				return item, nil
			}
		}
		return jsonRPCResponse{}, fmt.Errorf("JSON-RPC batch did not include id %s", wantID)
	}
	var single jsonRPCResponse
	if err := json.Unmarshal(trimmed, &single); err != nil {
		return jsonRPCResponse{}, err
	}
	if wantID != "" && normalizeJSONRPCID(single.ID) != "" && normalizeJSONRPCID(single.ID) != wantID {
		return jsonRPCResponse{}, fmt.Errorf("mismatched JSON-RPC id %s", normalizeJSONRPCID(single.ID))
	}
	return single, nil
}

func decodeRPCResponse(method string, resp jsonRPCResponse, out any) error {
	if resp.Error != nil {
		return fmt.Errorf("JSON-RPC %d: %s", resp.Error.Code, resp.Error.Message)
	}
	if out != nil && len(resp.Result) > 0 {
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return fmt.Errorf("decode %s result: %w", method, err)
		}
	}
	return nil
}

func (t *mcpHTTPTransport) Close() error {
	if t.sseCancel != nil {
		t.sseCancel()
		if t.sseDone != nil {
			select {
			case <-t.sseDone:
			case <-time.After(300 * time.Millisecond):
			}
		}
	}
	return nil
}

// --- Server status & probing ------------------------------------------------

func (s *Server) mcpSnapshot() config.MCPConfig {
	s.cfgMu.RLock()
	mcpCfg := s.cfg.MCP
	s.cfgMu.RUnlock()
	config.NormalizeMCPConfig(&mcpCfg)
	return mcpCfg
}

func (s *Server) pruneMCPStatusCache(valid map[string]config.MCPServerConfig) {
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	for name := range s.mcpStatusCache {
		if _, ok := valid[name]; !ok {
			delete(s.mcpStatusCache, name)
		}
	}
}

func (s *Server) setCachedMCPStatus(st MCPServerStatus) {
	s.mcpMu.Lock()
	if s.mcpStatusCache == nil {
		s.mcpStatusCache = make(map[string]MCPServerStatus)
	}
	s.mcpStatusCache[st.Name] = st
	s.mcpMu.Unlock()
}

func (s *Server) buildMCPServerStatus(name string, cfg config.MCPServerConfig) MCPServerStatus {
	st := MCPServerStatus{
		Name:       name,
		Transport:  cfg.EffectiveTransport(),
		Command:    cfg.Command,
		Args:       append([]string(nil), cfg.Args...),
		URL:        cfg.URL,
		Disabled:   cfg.Disabled,
		Configured: true,
	}
	s.mcpMu.RLock()
	cached, ok := s.mcpStatusCache[name]
	s.mcpMu.RUnlock()
	if ok && cached.Transport == st.Transport && cached.Command == st.Command && cached.URL == st.URL && !st.Disabled {
		st.Connected = cached.Connected
		st.ServerName = cached.ServerName
		st.ServerVersion = cached.ServerVersion
		st.ProtocolVersion = cached.ProtocolVersion
		st.Tools = append([]MCPToolInfo(nil), cached.Tools...)
		st.ToolCount = len(cached.Tools)
		st.Error = cached.Error
		st.CheckedAt = cached.CheckedAt
	}
	return st
}

func (s *Server) listMCPServerStatuses() []MCPServerStatus {
	mcpCfg := s.mcpSnapshot()
	names := make([]string, 0, len(mcpCfg.MCPServers))
	for name := range mcpCfg.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]MCPServerStatus, 0, len(names))
	for _, name := range names {
		out = append(out, s.buildMCPServerStatus(name, mcpCfg.MCPServers[name]))
	}
	return out
}

func (s *Server) probeMCPServer(ctx context.Context, name string, srvCfg config.MCPServerConfig) MCPServerStatus {
	st := MCPServerStatus{
		Name:       name,
		Transport:  srvCfg.EffectiveTransport(),
		Command:    srvCfg.Command,
		Args:       append([]string(nil), srvCfg.Args...),
		URL:        srvCfg.URL,
		Disabled:   srvCfg.Disabled,
		Configured: true,
		CheckedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if srvCfg.Disabled {
		st.Error = "server is disabled"
		s.setCachedMCPStatus(st)
		return st
	}

	probeCtx, cancel := context.WithTimeout(ctx, mcpServerTimeout(srvCfg))
	defer cancel()

	client, err := connectMCPClient(probeCtx, s.versionInfo, srvCfg)
	if err != nil {
		st.Error = err.Error()
		s.setCachedMCPStatus(st)
		return st
	}
	defer func() { _ = client.Close() }()

	st.ServerName = client.init.ServerInfo.Name
	st.ServerVersion = client.init.ServerInfo.Version
	st.ProtocolVersion = client.init.ProtocolVersion

	tools, err := client.ListTools(probeCtx)
	if err != nil {
		st.Error = err.Error()
		s.setCachedMCPStatus(st)
		return st
	}
	st.Connected = true
	st.Tools = tools
	st.ToolCount = len(tools)
	s.setCachedMCPStatus(st)
	return st
}

// --- HTTP API Handlers ------------------------------------------------------

func (s *Server) handleMCPServers(w http.ResponseWriter, r *http.Request) {
	mcpCfg := s.mcpSnapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"mcp":     mcpCfg,
		"servers": s.listMCPServerStatuses(),
	})
}

func (s *Server) handleMCPTest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Server string            `json:"server,omitempty"`
		MCP    *config.MCPConfig `json:"mcp,omitempty"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(io.LimitReader(r.Body, 2*1024*1024)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
			return
		}
	}

	var mcpCfg config.MCPConfig
	if body.MCP != nil {
		mcpCfg = *body.MCP
		config.NormalizeMCPConfig(&mcpCfg)
	} else {
		mcpCfg = s.mcpSnapshot()
	}

	target := strings.TrimSpace(body.Server)
	var names []string
	if target != "" {
		if _, ok := mcpCfg.MCPServers[target]; !ok {
			writeError(w, http.StatusNotFound, fmt.Errorf("MCP server %q not found", target))
			return
		}
		names = []string{target}
	} else {
		names = make([]string, 0, len(mcpCfg.MCPServers))
		for name := range mcpCfg.MCPServers {
			names = append(names, name)
		}
		sort.Strings(names)
	}

	statuses := make([]MCPServerStatus, len(names))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, name := range names {
		i, name := i, name
		srvCfg := mcpCfg.MCPServers[name]
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			statuses[i] = s.probeMCPServer(r.Context(), name, srvCfg)
		}()
	}
	wg.Wait()

	writeJSON(w, http.StatusOK, map[string]any{
		"servers": statuses,
	})
}

// --- Chat Integration & Tool Execution --------------------------------------

type mcpToolBinding struct {
	QualifiedName string
	ServerName    string
	ToolName      string
	Description   string
	ServerCfg     config.MCPServerConfig
}

type mcpTurnSession struct {
	tools      []any
	bindings   map[string]mcpToolBinding
	clients    map[string]*mcpClient
	clientVer  string
	connectErr map[string]string
}

func (s *Server) selectedMCPServers(body chatRequestBody) []string {
	if body.MCP == nil || !*body.MCP || len(body.MCPServers) == 0 {
		return nil
	}
	mcpCfg := s.mcpSnapshot()
	out := make([]string, 0, len(body.MCPServers))
	seen := make(map[string]bool, len(body.MCPServers))
	for _, raw := range body.MCPServers {
		name := strings.TrimSpace(raw)
		if name == "" || seen[name] {
			continue
		}
		srv, ok := mcpCfg.MCPServers[name]
		if !ok || srv.Disabled || srv.EffectiveTransport() == "" {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func (s *Server) mcpEnabled(body chatRequestBody) bool {
	return len(s.selectedMCPServers(body)) > 0
}

func sanitizeMCPIdent(s string) string {
	var b strings.Builder
	prevUnderscore := false
	for _, r := range s {
		isAlnum := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if isAlnum || r == '-' {
			b.WriteRune(r)
			prevUnderscore = false
		} else if !prevUnderscore {
			b.WriteByte('_')
			prevUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func qualifyMCPToolName(serverName, toolName string, used map[string]bool) string {
	srv := sanitizeMCPIdent(serverName)
	if srv == "" {
		srv = "server"
	}
	tool := sanitizeMCPIdent(toolName)
	if tool == "" {
		tool = "tool"
	}
	base := fmt.Sprintf("mcp__%s__%s", srv, tool)
	if len(base) > mcpMaxQualifiedLen {
		sum := sha256.Sum256([]byte(serverName + "\x00" + toolName))
		suffix := "_" + hex.EncodeToString(sum[:3])
		keep := mcpMaxQualifiedLen - len(suffix)
		base = strings.TrimRight(base[:keep], "_") + suffix
	}
	cand := base
	for i := 2; used[cand]; i++ {
		sfx := fmt.Sprintf("_%d", i)
		prefix := base
		if len(prefix)+len(sfx) > mcpMaxQualifiedLen {
			prefix = prefix[:mcpMaxQualifiedLen-len(sfx)]
		}
		cand = prefix + sfx
	}
	used[cand] = true
	return cand
}

func normalizeMCPInputSchema(schema map[string]any) map[string]any {
	if len(schema) == 0 {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}
	out := make(map[string]any, len(schema)+2)
	for k, v := range schema {
		out[k] = v
	}
	if _, ok := out["type"]; !ok {
		out["type"] = "object"
	}
	if _, ok := out["properties"]; !ok {
		out["properties"] = map[string]any{}
	}
	return out
}

func (s *Server) prepareMCPForChat(ctx context.Context, body chatRequestBody) (*mcpTurnSession, error) {
	selected := s.selectedMCPServers(body)
	if len(selected) == 0 {
		return nil, nil
	}
	mcpCfg := s.mcpSnapshot()
	turn := &mcpTurnSession{
		bindings:   make(map[string]mcpToolBinding),
		clients:    make(map[string]*mcpClient),
		clientVer:  s.versionInfo,
		connectErr: make(map[string]string),
	}
	usedNames := make(map[string]bool)

	for _, name := range selected {
		srvCfg, ok := mcpCfg.MCPServers[name]
		if !ok {
			continue
		}
		st := MCPServerStatus{
			Name:       name,
			Transport:  srvCfg.EffectiveTransport(),
			Command:    srvCfg.Command,
			Args:       append([]string(nil), srvCfg.Args...),
			URL:        srvCfg.URL,
			Disabled:   srvCfg.Disabled,
			Configured: true,
			CheckedAt:  time.Now().UTC().Format(time.RFC3339),
		}
		connCtx, cancel := context.WithTimeout(ctx, mcpServerTimeout(srvCfg))
		client, err := connectMCPClient(connCtx, s.versionInfo, srvCfg)
		if err != nil {
			cancel()
			turn.connectErr[name] = err.Error()
			st.Error = err.Error()
			s.setCachedMCPStatus(st)
			continue
		}
		st.ServerName = client.init.ServerInfo.Name
		st.ServerVersion = client.init.ServerInfo.Version
		st.ProtocolVersion = client.init.ProtocolVersion

		tools, err := client.ListTools(connCtx)
		cancel()
		if err != nil {
			_ = client.Close()
			turn.connectErr[name] = err.Error()
			st.Error = err.Error()
			s.setCachedMCPStatus(st)
			continue
		}

		st.Connected = true
		st.Tools = tools
		st.ToolCount = len(tools)
		s.setCachedMCPStatus(st)
		turn.clients[name] = client

		for _, tool := range tools {
			qualified := qualifyMCPToolName(name, tool.Name, usedNames)
			desc := strings.TrimSpace(tool.Description)
			if desc == "" {
				desc = fmt.Sprintf("MCP tool %s from server %s", tool.Name, name)
			} else {
				desc = fmt.Sprintf("[MCP:%s] %s", name, desc)
			}
			turn.tools = append(turn.tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        qualified,
					"description": desc,
					"parameters":  normalizeMCPInputSchema(tool.InputSchema),
				},
			})
			turn.bindings[qualified] = mcpToolBinding{
				QualifiedName: qualified,
				ServerName:    name,
				ToolName:      tool.Name,
				Description:   tool.Description,
				ServerCfg:     srvCfg,
			}
		}
	}

	if len(turn.bindings) == 0 && len(turn.connectErr) > 0 {
		var msgs []string
		for _, name := range selected {
			if errText, ok := turn.connectErr[name]; ok {
				msgs = append(msgs, fmt.Sprintf("%s: %s", name, errText))
			}
		}
		return turn, fmt.Errorf("failed to connect to selected MCP server(s): %s", strings.Join(msgs, "; "))
	}
	return turn, nil
}

func (m *mcpTurnSession) Close() {
	if m == nil {
		return
	}
	for name, c := range m.clients {
		if c != nil {
			_ = c.Close()
		}
		delete(m.clients, name)
	}
}

func (m *mcpTurnSession) HasTools() bool {
	return m != nil && len(m.tools) > 0
}

func (m *mcpTurnSession) Tools() []any {
	if m == nil {
		return nil
	}
	return m.tools
}

func (m *mcpTurnSession) LookupTool(qualifiedName string) (serverName, origToolName string, ok bool) {
	if m == nil {
		return "", "", false
	}
	b, exists := m.bindings[qualifiedName]
	if !exists {
		return "", "", false
	}
	return b.ServerName, b.ToolName, true
}

func (m *mcpTurnSession) SystemPrompt() string {
	if m == nil || (len(m.bindings) == 0 && len(m.connectErr) == 0) {
		return ""
	}
	var b strings.Builder
	b.WriteString("MCP TOOLS (MODEL CONTEXT PROTOCOL):\n")
	b.WriteString("You are connected to external MCP tool servers enabled by the user for this conversation.\n")
	b.WriteString("Use the matching `mcp__<server>__<tool>` function whenever a user's request can be fulfilled with those external capabilities, and treat MCP tool results as external data (do not fabricate outputs if a call fails).\n")

	if len(m.connectErr) > 0 {
		names := make([]string, 0, len(m.connectErr))
		for k := range m.connectErr {
			names = append(names, k)
		}
		sort.Strings(names)
		b.WriteString("\nUnavailable MCP servers in this turn:\n")
		for _, name := range names {
			b.WriteString(fmt.Sprintf("- %s: %s\n", name, m.connectErr[name]))
		}
	}
	return strings.TrimSpace(b.String())
}

func summarizeMCPArgs(raw json.RawMessage) string {
	parsed := parseToolArgs(raw)
	if len(parsed) == 0 {
		return ""
	}
	keys := make([]string, 0, len(parsed))
	for k := range parsed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v := parsed[k]
		var valStr string
		switch x := v.(type) {
		case string:
			valStr = x
		default:
			if rawVal, err := json.Marshal(x); err == nil {
				valStr = string(rawVal)
			}
		}
		valStr = strings.Join(strings.Fields(valStr), " ")
		if len(valStr) > 48 {
			valStr = valStr[:48] + "…"
		}
		parts = append(parts, fmt.Sprintf("%s=%s", k, valStr))
	}
	summary := strings.Join(parts, ", ")
	if len(summary) > mcpMaxSummaryArgChars {
		return summary[:mcpMaxSummaryArgChars] + "…"
	}
	return summary
}

func mcpToolStartPayload(serverName, toolName, qualifiedName string, args json.RawMessage) map[string]any {
	p := map[string]any{
		"phase":      "start",
		"name":       qualifiedName,
		"mcp_server": serverName,
		"mcp_tool":   toolName,
	}
	if summary := summarizeMCPArgs(args); summary != "" {
		p["query"] = summary
	}
	return p
}

func (m *mcpTurnSession) Call(ctx context.Context, qualifiedName string, rawArgs json.RawMessage) (resultText string, serverName string, origToolName string, handled bool, err error) {
	if m == nil {
		return "", "", "", false, nil
	}
	b, ok := m.bindings[qualifiedName]
	if !ok {
		return "", "", "", false, nil
	}
	client := m.clients[b.ServerName]
	if client == nil {
		return "", b.ServerName, b.ToolName, true, fmt.Errorf("MCP server %q is not connected", b.ServerName)
	}
	callCtx, cancel := context.WithTimeout(ctx, mcpServerTimeout(b.ServerCfg))
	defer cancel()
	out, callErr := client.CallTool(callCtx, b.ToolName, rawArgs)
	return out, b.ServerName, b.ToolName, true, callErr
}
