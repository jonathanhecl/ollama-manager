package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gense/ollama-manager/internal/ollama"
)

const maxArtifactRounds = 30

// artifactOperationalToolDefinitions returns filesystem and execution tool schemas.
// These are available once an artifact has been initialized.
func artifactOperationalToolDefinitions() []any {
	return []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "write_file",
				"description": "Create or overwrite a file in the artifact project directory. Use this to create HTML, CSS, JS, and other project files.",
				"parameters": map[string]any{
					"type":     "object",
					"required": []string{"path", "content"},
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "Relative path inside the project (e.g. index.html, styles.css, js/app.js)",
						},
						"content": map[string]any{
							"type":        "string",
							"description": "Full file content",
						},
					},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "replace_in_file",
				"description": "Replace a specific text snippet or code block in an existing file. Use this for targeted edits instead of overwriting the whole file.",
				"parameters": map[string]any{
					"type":     "object",
					"required": []string{"path", "old_string", "new_string"},
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "Relative path inside the project (e.g. index.html, styles.css, js/app.js)",
						},
						"old_string": map[string]any{
							"type":        "string",
							"description": "Exact text or code block to search for and replace",
						},
						"new_string": map[string]any{
							"type":        "string",
							"description": "New text or code block to insert in place of old_string",
						},
					},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "read_file",
				"description": "Read the contents of a file in the artifact project directory.",
				"parameters": map[string]any{
					"type":     "object",
					"required": []string{"path"},
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "Relative path inside the project",
						},
					},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "list_dir",
				"description": "List files and directories in the artifact project directory.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "Relative path inside the project (default '.')",
						},
					},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "exec",
				"description": "Execute a shell command in the artifact project directory. Use for installing dependencies, building, or running the project.",
				"parameters": map[string]any{
					"type":     "object",
					"required": []string{"command"},
					"properties": map[string]any{
						"command": map[string]any{
							"type":        "string",
							"description": "Shell command to run",
						},
					},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "get_artifact_console",
				"description": "Retrieve the console logs, outputs, and javascript runtime errors captured from the active artifact preview. Use this to verify your code execution or debug runtime issues if the user reports that the app is blank, not working, or has errors.",
				"parameters": map[string]any{
					"type": "object",
				},
			},
		},
	}
}

// artifactSystemPrompt returns the system prompt injected when artifacts mode is on for a new project.
func artifactSystemPrompt() string {
	return `You are a helpful coding and web assistant. You have access to artifact tools to create and build interactive web applications and projects.
To start building a project or web application, you MUST first call the tool 'create_artifact' with 'name' and 'description'.
Calling 'create_artifact' initializes the project workspace and immediately unlocks all project filesystem tools ('write_file', 'replace_in_file', 'read_file', 'list_dir', 'exec', and 'get_artifact_console') for you to use in the subsequent steps.

WORKFLOW:
1. Call 'create_artifact' with the project name.
2. In the next turn, create 'index.html' (and any CSS/JS files) using 'write_file'. The preview runs live in a sandboxed iframe.
3. Keep projects self-contained (inline CSS/JS or use CDN links for libraries like React, Tailwind, Lucide, KaTeX, Three.js, etc.).
4. All file paths are relative to the project root (e.g. 'index.html', 'style.css', 'app.js'). Do NOT use absolute paths.

UI/CONVERSATION RULES:
1. Do NOT repeat or dump code blocks in your chat response when you write or edit them using the tools. The user sees the code and live preview in the preview panel automatically.
2. Keep conversational text minimal (1-2 brief sentences max), prioritizing tool calls.
3. Your primary goal is to build and implement the artifact in the workspace.`
}

// artifactExistingSystemPrompt returns the system prompt injected when modifying an active existing project.
func artifactExistingSystemPrompt() string {
	return `You are a helpful coding and web assistant working on an ACTIVE artifact project workspace.
The artifact session is ALREADY ACTIVE and initialized. You do NOT need to call 'create_artifact' — all project tools are ALREADY AVAILABLE for you to use:
- 'write_file': Create or overwrite a file in the project. Required arguments: 'path' (relative, e.g. 'index.html'), 'content' (full file content).
- 'replace_in_file': Replace a specific text snippet or code block in an existing file. Required arguments: 'path', 'old_string', 'new_string'.
- 'read_file': Read the contents of an existing file. Required arguments: 'path'.
- 'list_dir': List files and folders in a directory. Optional argument: 'path' (default '.').
- 'exec': Run a shell command in the project directory. Required argument: 'command'.
- 'get_artifact_console': Retrieve the console logs, outputs, and javascript runtime errors from the active preview.

WORKFLOW:
1. When asked to modify, update, add features, or fix the project, directly use 'write_file', 'replace_in_file', or 'read_file'.
2. The user will see your changes instantly reflected in the live preview.
3. If the user reports a bug, error, blank screen, or unexpected behavior, call 'get_artifact_console' or 'read_file' to diagnose and fix it.
4. Keep all file paths relative to the project root.

UI/CONVERSATION RULES:
1. Do NOT repeat or dump code blocks in your chat response when writing/editing with tools.
2. Keep conversational text minimal (1-2 brief sentences max), prioritizing tool calls.`
}

// buildArtifactSystemPrompt returns the system prompt, including a listing of
// existing files when iterating on a previously created artifact.
func buildArtifactSystemPrompt(artifactDir string) string {
	if artifactDir == "" {
		return artifactSystemPrompt()
	}
	entries, err := os.ReadDir(artifactDir)
	if err != nil {
		return artifactExistingSystemPrompt()
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			files = append(files, e.Name()+"/")
		} else {
			if e.Name() == "prompt.txt" {
				continue
			}
			files = append(files, e.Name())
		}
	}
	if len(files) == 0 {
		return artifactExistingSystemPrompt()
	}
	return artifactExistingSystemPrompt() + "\n\n" + fmt.Sprintf(
		"The following files are already present in the active workspace:\n  %s\n"+
			"Use read_file to inspect current files before making changes, or write_file / replace_in_file to update them.",
		strings.Join(files, "\n  "))
}

// artifactToolStartPayload builds the SSE "tool start" payload for artifact tools.
func artifactToolStartPayload(name string, args json.RawMessage) map[string]any {
	p := map[string]any{"phase": "start", "name": name}
	m := parseToolArgs(args)
	switch name {
	case "write_file", "replace_in_file":
		if path, _ := m["path"].(string); strings.TrimSpace(path) != "" {
			p["path"] = path
		}
	case "read_file":
		if path, _ := m["path"].(string); strings.TrimSpace(path) != "" {
			p["path"] = path
		}
	case "list_dir":
		if path, _ := m["path"].(string); strings.TrimSpace(path) != "" {
			p["path"] = path
		} else {
			p["path"] = "."
		}
	case "exec":
		if cmd, _ := m["command"].(string); strings.TrimSpace(cmd) != "" {
			preview := cmd
			if utf8.RuneCountInString(preview) > 120 {
				preview = string([]rune(preview)[:120]) + "…"
			}
			p["command"] = preview
		}
	case "create_artifact":
		if n, _ := m["name"].(string); n != "" {
			p["artifact_name"] = n
		}
		if d, _ := m["description"].(string); d != "" {
			p["description"] = d
		}
	}
	return p
}

// runArtifactTool executes a single tool against the artifact directory.
func (s *Server) runArtifactTool(ctx context.Context, artifactDir, name string, args json.RawMessage) (string, error) {
	m := parseToolArgs(args)
	switch name {
	case "write_file":
		path, _ := m["path"].(string)
		content, _ := m["content"].(string)
		if strings.TrimSpace(path) == "" {
			return "Error: missing path for write_file", nil
		}
		full := filepath.Join(artifactDir, path)
		if !isPathSafe(artifactDir, full) {
			return "Error: path escapes project directory", nil
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			return "", err
		}
		return fmt.Sprintf("wrote %s (%d bytes)", path, len(content)), nil

	case "replace_in_file":
		path, _ := m["path"].(string)
		oldStr, _ := m["old_string"].(string)
		newStr, _ := m["new_string"].(string)
		if strings.TrimSpace(path) == "" {
			return "Error: missing path for replace_in_file", nil
		}
		if oldStr == "" {
			return "Error: missing old_string for replace_in_file", nil
		}
		full := filepath.Join(artifactDir, path)
		if !isPathSafe(artifactDir, full) {
			return "Error: path escapes project directory", nil
		}
		b, err := os.ReadFile(full)
		if err != nil {
			return "", err
		}
		content := string(b)
		if !strings.Contains(content, oldStr) {
			return fmt.Sprintf("Error: old_string not found in %s", path), nil
		}
		count := strings.Count(content, oldStr)
		if count > 1 {
			return fmt.Sprintf("Error: old_string matches %d occurrences in %s. Provide more surrounding context to uniquely identify the block.", count, path), nil
		}
		updated := strings.Replace(content, oldStr, newStr, 1)
		if err := os.WriteFile(full, []byte(updated), 0o644); err != nil {
			return "", err
		}
		return fmt.Sprintf("replaced text in %s (%d bytes)", path, len(updated)), nil

	case "read_file":
		path, _ := m["path"].(string)
		if strings.TrimSpace(path) == "" {
			return "Error: missing path for read_file", nil
		}
		full := filepath.Join(artifactDir, path)
		if !isPathSafe(artifactDir, full) {
			return "Error: path escapes project directory", nil
		}
		if filepath.Clean(full) == filepath.Clean(filepath.Join(artifactDir, "prompt.txt")) {
			return "Error: file not found", nil
		}
		b, err := os.ReadFile(full)
		if err != nil {
			return "", err
		}
		return string(b), nil

	case "list_dir":
		path, _ := m["path"].(string)
		if path == "" {
			path = "."
		}
		full := filepath.Join(artifactDir, path)
		if !isPathSafe(artifactDir, full) {
			return "Error: path escapes project directory", nil
		}
		entries, err := os.ReadDir(full)
		if err != nil {
			return "", err
		}
		var lines []string
		for _, e := range entries {
			if !e.IsDir() && e.Name() == "prompt.txt" {
				continue
			}
			kind := "file"
			if e.IsDir() {
				kind = "dir"
			}
			lines = append(lines, fmt.Sprintf("%s %s", kind, e.Name()))
		}
		if len(lines) == 0 {
			return "(empty)", nil
		}
		return strings.Join(lines, "\n"), nil

	case "exec":
		command, _ := m["command"].(string)
		if strings.TrimSpace(command) == "" {
			return "Error: missing command for exec", nil
		}
		return execInDir(ctx, artifactDir, command)

	case "create_artifact":
		if artifactDir != "" {
			return "Error: an artifact workspace is already active in this session. Do NOT call create_artifact again. You already have full access to write_file, replace_in_file, read_file, list_dir, exec, and get_artifact_console.", nil
		}
		// No I/O — the loop handles sending the SSE artifact event.
		return "Artifact workspace initialized successfully. Operational tools (write_file, replace_in_file, read_file, list_dir, exec, get_artifact_console) are now available.", nil

	case "get_artifact_console":
		ts := artifactRelID(artifactDir)
		s.artifactConsoleMu.RLock()
		logs := s.artifactConsoleLogs[ts]
		s.artifactConsoleMu.RUnlock()
		if len(logs) == 0 {
			return "No console logs or javascript errors captured yet.", nil
		}
		start := 0
		if len(logs) > 50 {
			start = len(logs) - 50
		}
		return strings.Join(logs[start:], "\n"), nil

	default:
		return fmt.Sprintf("Error: tool %q is not implemented", name), nil
	}
}

// isPathSafe checks that target is inside base (no path traversal).
func isPathSafe(base, target string) bool {
	cleanBase := filepath.Clean(base)
	cleanTarget := filepath.Clean(target)
	return cleanTarget == cleanBase || strings.HasPrefix(cleanTarget, cleanBase+string(os.PathSeparator))
}

// execInDir runs a shell command in dir with a 120-second timeout.
// parentCtx allows cancellation to propagate from the request context.
func execInDir(parentCtx context.Context, dir, command string) (string, error) {
	ctx, cancel := context.WithTimeout(parentCtx, 120*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/c", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	cmd.Dir = dir

	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	log.Printf("[artifact] exec: %q in %s", command, dir)
	if err := cmd.Start(); err != nil {
		return "", err
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		killProcessTree(cmd)
		<-done
		return outBuf.String(), fmt.Errorf("exec timed out or was cancelled after 120s")
	case err := <-done:
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				output := outBuf.String()
				if errBuf.String() != "" {
					output += "\n[stderr]\n" + errBuf.String()
				}
				output += fmt.Sprintf("\n[exit:%d]", exitErr.ExitCode())
				return output, nil
			}
			return outBuf.String(), err
		}
		output := outBuf.String()
		if errBuf.String() != "" {
			output += "\n[stderr]\n" + errBuf.String()
		}
		output += "\n[exit:0]"
		return output, nil
	}
}

func killProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return
	}
	if runtime.GOOS == "windows" {
		_ = exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(pid)).Run()
	} else {
		_ = exec.Command("kill", "-9", fmt.Sprintf("-%d", pid)).Run()
		_ = cmd.Process.Kill()
	}
}

// runArtifactAgentLoop is the main agent loop for artifact creation.
// It streams chunks to the browser via SSE, executes tools, and sends
// an "artifact" event when the model calls create_artifact.
func (s *Server) runArtifactAgentLoop(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, body chatRequestBody) {
	send := func(event string, payload any) {
		buf, _ := json.Marshal(payload)
		if event != "" {
			fmt.Fprintf(w, "event: %s\n", event)
		}
		fmt.Fprintf(w, "data: %s\n\n", buf)
		flusher.Flush()
	}

	startedAt := time.Now()

	// Use existing artifact directory if provided. For new requests, do NOT create
	// a directory yet — it will be created on-demand when the agent actually
	// writes a file. This avoids leaving empty artifact folders for messages
	// that never create anything.
	var artifactDir string
	var ts string
	if body.ArtifactDir != "" {
		candidate := filepath.Join("artifacts", filepath.Clean(body.ArtifactDir))
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			artifactDir = candidate
			ts = artifactRelID(artifactDir)
			log.Printf("[artifact] reusing existing dir: %s", artifactDir)
		}
	}

	// Lazy directory creation helper: only makes the artifacts/<digest>/<ts>/
	// folder when the agent is about to write something for the first time.
	// The digest uniquely identifies the model, so artifacts are grouped per
	// model; the timestamp identifies each artifact.
	ensureArtifactDir := func() error {
		if artifactDir != "" {
			return nil
		}
		digest := s.artifactModelDigest(ctx, body.Model)
		if digest == "" {
			digest = cleanModelName(body.Model)
		}
		ts = filepath.Join(digest, time.Now().Format("2006-01-02_15-04-05"))
		artifactDir = filepath.Join("artifacts", ts)
		if err := os.MkdirAll(artifactDir, 0o755); err != nil {
			return fmt.Errorf("create artifact dir: %w", err)
		}
		log.Printf("[artifact] created dir on demand: %s", artifactDir)

		// Save initial user prompt to prompt.txt inside the new folder.
		var initialUserPrompt string
		for i := len(body.Messages) - 1; i >= 0; i-- {
			if body.Messages[i].Role == "user" {
				initialUserPrompt = body.Messages[i].Content
				break
			}
		}
		if initialUserPrompt != "" {
			promptPath := filepath.Join(artifactDir, "prompt.txt")
			if err := os.WriteFile(promptPath, []byte(initialUserPrompt), 0o644); err != nil {
				log.Printf("[artifact] warning: failed to write prompt.txt: %v", err)
			} else {
				log.Printf("[artifact] saved initial user prompt to prompt.txt")
			}
		}

		return nil
	}

	// Prepare system prompt and messages cleanly
	var customSystem string
	filteredMessages := make([]ollama.ChatMessage, 0, len(body.Messages))
	for _, m := range body.Messages {
		if m.Role == "system" {
			if strings.TrimSpace(m.Content) != "" {
				if customSystem != "" {
					customSystem += "\n\n"
				}
				customSystem += m.Content
			}
		} else {
			filteredMessages = append(filteredMessages, m)
		}
	}

	sysPrompt := buildArtifactSystemPrompt(artifactDir)
	if customSystem != "" {
		sysPrompt += "\n\nAdditional User System Instructions:\n" + customSystem
	}

	msgs := make([]ollama.ChatMessage, 0, len(filteredMessages)+1)
	msgs = append(msgs, ollama.ChatMessage{
		Role:    "system",
		Content: sysPrompt,
	})
	msgs = append(msgs, filteredMessages...)

	accComp := 0
	var accEvalNS int64
	// For existing artifacts the preview is already live, so subsequent writes
	// should trigger reload events rather than a fresh loaded event.
	artifactLoaded := artifactDir != ""
	createArtifactCalled := artifactDir != ""

	for round := 0; round < maxArtifactRounds; round++ {
		if ctx.Err() != nil {
			log.Printf("[artifact] context cancelled at round %d", round)
			return
		}
		imgCount := 0
		for _, m := range msgs {
			imgCount += len(m.Images)
		}
		log.Printf("[artifact] round %d start, messages: %d, images: %d", round, len(msgs), imgCount)

		var tools []any
		if createArtifactCalled {
			tools = artifactOperationalToolDefinitions()
		} else {
			// Initially, only expose create_artifact tool.
			// This forces the agent to call create_artifact first before it gets files tools.
			tools = []any{
				map[string]any{
					"type": "function",
					"function": map[string]any{
						"name":        "create_artifact",
						"description": "Initialize a new artifact project/workspace. Call this first when you want to build a web project, app, dashboard, or other runnable code.",
						"parameters": map[string]any{
							"type":     "object",
							"required": []string{"name"},
							"properties": map[string]any{
								"name": map[string]any{
									"type":        "string",
									"description": "Display name for the artifact",
								},
								"description": map[string]any{
									"type":        "string",
									"description": "Short description of what the artifact does",
								},
							},
						},
					},
				},
			}
		}
		if body.WebTools != nil && *body.WebTools {
			tools = append(tools, webToolDefinitions()...)
		}

		req := ollama.ChatRequest{
			Model:    body.Model,
			Messages: msgs,
			Stream:   true,
			Think:    body.Think,
			Options:  body.Options,
			Tools:    tools,
		}
		var last ollama.ChatChunk
		var acc ollama.ChatMessage
		acc.Role = "assistant"
		sentTools := make(map[int]*toolSentState)

		err := s.ollama.Chat(ctx, req, func(ev ollama.ChatChunk) error {
			last = ev
			m := ev.Message
			if m.Thinking != "" {
				acc.Thinking += m.Thinking
			}
			if m.Content != "" {
				acc.Content += m.Content
			}
			if len(m.ToolCalls) > 0 {
				acc.ToolCalls = m.ToolCalls
				for i, tc := range m.ToolCalls {
					name := tc.Function.Name
					if name == "" {
						continue
					}
					partial := parseToolArgs(tc.Function.Arguments)
					var path, cmd, artName string
					if partial != nil {
						path, _ = partial["path"].(string)
						cmd, _ = partial["command"].(string)
						artName, _ = partial["name"].(string)
					}

					state, exists := sentTools[i]
					if !exists {
						state = &toolSentState{name: name}
						sentTools[i] = state
						p := map[string]any{"phase": "generating", "name": name}
						if path != "" {
							p["path"] = path
							state.path = path
						}
						if cmd != "" {
							p["command"] = cmd
							state.command = cmd
						}
						if artName != "" {
							p["artifact_name"] = artName
							state.artifactName = artName
						}
						send("tool", p)
					} else {
						updated := false
						p := map[string]any{"phase": "generating", "name": name}
						if path != "" && path != state.path {
							p["path"] = path
							state.path = path
							updated = true
						}
						if cmd != "" && cmd != state.command {
							p["command"] = cmd
							state.command = cmd
							updated = true
						}
						if artName != "" && artName != state.artifactName {
							p["artifact_name"] = artName
							state.artifactName = artName
							updated = true
						}
						if updated {
							send("tool", p)
						}
					}
				}
			}
			send("chunk", ev)
			return nil
		})
		if err != nil {
			send("error", map[string]any{"error": err.Error()})
			return
		}
		if acc.Role == "" {
			acc.Role = "assistant"
		}

		assistant := acc
		if len(assistant.ToolCalls) == 0 && len(last.Message.ToolCalls) > 0 {
			assistant.ToolCalls = last.Message.ToolCalls
		}
		msgs = append(msgs, assistant)
		accComp += last.EvalCount
		accEvalNS += last.EvalDuration

		if len(assistant.ToolCalls) == 0 {
			if strings.TrimSpace(assistant.Content) == "" && strings.TrimSpace(assistant.Thinking) == "" {
				log.Printf("[artifact] round %d: empty response (no content, no tool calls), stopping", round)
			}
			send("done", map[string]any{
				"elapsed_ms":         time.Since(startedAt).Milliseconds(),
				"prompt_tokens":      last.PromptEvalCount,
				"completion_tokens":  accComp,
				"total_tokens":       last.PromptEvalCount + accComp,
				"prompt_duration_ns": last.PromptEvalDuration,
				"eval_duration_ns":   accEvalNS,
				"total_duration_ns":  last.TotalDuration,
				"done_reason":        last.DoneReason,
			})
			return
		}

		for _, tc := range assistant.ToolCalls {
			n := tc.Function.Name
			if n == "" {
				continue
			}

			// Use artifact-aware payload for artifact tools, web payload for web tools.
			startPayload := artifactToolStartPayload(n, tc.Function.Arguments)
			if isWebTool(n) {
				startPayload = toolStartPayload(n, tc.Function.Arguments)
			}
			send("tool", startPayload)

			var out string
			var toolErr error
			if isWebTool(n) {
				out, toolErr = s.runWebTool(ctx, n, tc.Function.Arguments)
			} else {
				// Only create the artifacts directory when the agent is actually
				// about to write or run a command in the project.
				if n == "write_file" || n == "replace_in_file" || n == "exec" {
					if err := ensureArtifactDir(); err != nil {
						toolErr = err
					}
				}
				if toolErr == nil {
					out, toolErr = s.runArtifactTool(ctx, artifactDir, n, tc.Function.Arguments)
				}
			}
			if toolErr != nil {
				out = "Error: " + toolErr.Error()
			}
			if toolErr != nil || strings.HasPrefix(out, "Error:") {
				if guide := toolUsageGuide(n); guide != "" {
					out += guide
				}
			}
			out = truncateRunes(out, maxToolResultRunes)

			// Handle create_artifact: reveal the artifact panel.
			// If index.html is already present we can load the preview immediately;
			// otherwise show a loading screen until the entry point is written.
			if n == "create_artifact" {
				createArtifactCalled = true
				if artifactDir == "" {
					_ = ensureArtifactDir()
				}
				m := parseToolArgs(tc.Function.Arguments)
				artName, _ := m["name"].(string)
				artDesc, _ := m["description"].(string)
				if artName == "" {
					artName = "Artifact"
				}
				previewURL := fmt.Sprintf("/api/artifacts/%s/", ts)
				indexPath := filepath.Join(artifactDir, "index.html")
				hasIndex := false
				if info, err := os.Stat(indexPath); err == nil && !info.IsDir() {
					hasIndex = true
				}
				event := map[string]any{
					"url":         previewURL,
					"name":        artName,
					"description": artDesc,
					"timestamp":   ts,
				}
				if !artifactLoaded && hasIndex {
					artifactLoaded = true
					event["loaded"] = true
				} else if !artifactLoaded {
					event["generating"] = true
				}
				send("artifact", event)
				absPath, err := filepath.Abs(artifactDir)
				if err != nil {
					absPath = artifactDir
				}
				out = fmt.Sprintf("Artifact project created at absolute path '%s'. You now have access to the project tools: write_file, read_file, list_dir, exec, and get_artifact_console.", absPath)
			}
			// After write_file or replace_in_file on an artifact, send the appropriate event:
			// - loaded: first time index.html is written (transition from loading screen)
			// - reload: subsequent writes (refresh the live preview)
			if (n == "write_file" || n == "replace_in_file") && toolErr == nil {
				writePath, _ := parseToolArgs(tc.Function.Arguments)["path"].(string)
				normalizedPath := strings.TrimPrefix(strings.ToLower(writePath), "./")
				previewURL := fmt.Sprintf("/api/artifacts/%s/", ts)
				if !artifactLoaded && normalizedPath == "index.html" {
					artifactLoaded = true
					send("artifact", map[string]any{
						"url":       previewURL,
						"loaded":    true,
						"timestamp": ts,
					})
				} else if artifactLoaded {
					send("artifact", map[string]any{
						"url":       previewURL,
						"reload":    true,
						"timestamp": ts,
					})
				}
			}

			msgs = append(msgs, ollama.ChatMessage{
				Role:     "tool",
				ToolName: n,
				Content:  out,
			})

			done := map[string]any{"phase": "done", "name": n, "ok": toolErr == nil}
			if toolErr != nil {
				done["error"] = toolErr.Error()
			} else if out != "" {
				done["result_preview"] = truncateRunes(out, 320)
				done["result_runes"] = utf8.RuneCountInString(out)
			}
			send("tool", done)
		}
	}
	log.Printf("[artifact] reached max rounds (%d), stopping", maxArtifactRounds)
	send("done", map[string]any{
		"elapsed_ms":        time.Since(startedAt).Milliseconds(),
		"completion_tokens": accComp,
		"total_tokens":      accComp,
	})
	send("error", map[string]any{"error": "artifacts: too many tool rounds"})
}

// isWebTool returns true for the web tools (web_search, web_fetch).
func isWebTool(name string) bool {
	return name == "web_search" || name == "web_fetch"
}

func toolUsageGuide(name string) string {
	switch name {
	case "write_file":
		return "\n\nCorrect usage of 'write_file':\n" +
			"- Description: Create or overwrite a file in the project.\n" +
			"- Required Arguments:\n" +
			"  * 'path': string (relative path inside the project, e.g. 'index.html', 'js/app.js')\n" +
			"  * 'content': string (complete file content)"
	case "replace_in_file":
		return "\n\nCorrect usage of 'replace_in_file':\n" +
			"- Description: Replace a specific text snippet or code block in a file.\n" +
			"- Required Arguments:\n" +
			"  * 'path': string (relative path, e.g. 'index.html')\n" +
			"  * 'old_string': string (exact text/code block to find)\n" +
			"  * 'new_string': string (replacement text/code block)"
	case "read_file":
		return "\n\nCorrect usage of 'read_file':\n" +
			"- Description: Read the contents of a file.\n" +
			"- Required Arguments:\n" +
			"  * 'path': string (relative path of the file, e.g. 'index.html')"
	case "list_dir":
		return "\n\nCorrect usage of 'list_dir':\n" +
			"- Description: List files and folders in a directory.\n" +
			"- Optional Arguments:\n" +
			"  * 'path': string (relative directory path, e.g. '.' or 'css')"
	case "exec":
		return "\n\nCorrect usage of 'exec':\n" +
			"- Description: Execute a shell command in the project directory.\n" +
			"- Required Arguments:\n" +
			"  * 'command': string (shell command to run, e.g. 'npm install')"
	case "create_artifact":
		return "\n\nCorrect usage of 'create_artifact':\n" +
			"- Description: Initialize a new project space.\n" +
			"- Required Arguments:\n" +
			"  * 'name': string (display name)\n" +
			"- Optional Arguments:\n" +
			"  * 'description': string"
	case "web_search":
		return "\n\nCorrect usage of 'web_search':\n" +
			"- Description: Search the public web for information.\n" +
			"- Required Arguments:\n" +
			"  * 'query': string (search query)\n" +
			"- Optional Arguments:\n" +
			"  * 'max_results': integer"
	case "web_fetch":
		return "\n\nCorrect usage of 'web_fetch':\n" +
			"- Description: Fetch main text content of a URL.\n" +
			"- Required Arguments:\n" +
			"  * 'url': string (http/https URL)"
	default:
		return ""
	}
}

type toolSentState struct {
	name         string
	path         string
	command      string
	artifactName string
}

// artifactRelID returns the artifact folder's identifier relative to the
// artifacts/ directory, e.g. "<digest>/<timestamp>". This is the value sent
// to the browser as the artifact timestamp and used as the console-log key.
func artifactRelID(artifactDir string) string {
	if rel, err := filepath.Rel("artifacts", artifactDir); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return filepath.Base(artifactDir)
}

// artifactModelDigest returns the unique digest of modelName (without the
// "sha256:" prefix), or "" if it cannot be resolved. Artifact folders are
// grouped under this digest because it uniquely identifies the model.
func (s *Server) artifactModelDigest(ctx context.Context, modelName string) string {
	models, err := s.ollama.List(ctx)
	if err != nil {
		return ""
	}
	for _, m := range models {
		if m.Name == modelName || m.Model == modelName {
			return strings.TrimPrefix(m.Digest, "sha256:")
		}
	}
	return ""
}

func cleanModelName(model string) string {
	s := strings.ReplaceAll(model, ":", "-")
	invalid := []string{"\\", "/", "*", "?", "\"", "<", ">", "|", " ", "\n", "\r", "\t"}
	for _, char := range invalid {
		s = strings.ReplaceAll(s, char, "-")
	}
	if len(s) > 32 {
		s = s[:32]
	}
	return strings.Trim(s, "-")
}
