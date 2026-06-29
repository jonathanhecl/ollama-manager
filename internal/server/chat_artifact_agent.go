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
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gense/ollama-manager/internal/ollama"
)

const maxArtifactRounds = 30

// artifactToolDefinitions returns the tool schemas for artifact creation.
func artifactToolDefinitions() []any {
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
				"name":        "create_artifact",
				"description": "Mark the project as ready for preview. Call this AFTER creating all necessary files (especially index.html). The user will see a live preview of the project.",
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

// artifactSystemPrompt returns the system prompt injected when artifacts mode is on.
func artifactSystemPrompt() string {
	return `You have access to filesystem tools to create web projects. Use write_file to create files (HTML, CSS, JS, etc.). Use read_file and list_dir to inspect what you've created. Use exec to run commands if needed (e.g. installing dependencies).

When the project is ready, call create_artifact with a name and description to make it previewable by the user. The entry point for preview is index.html — always create one if the project is a web app.

Keep projects self-contained (inline CSS/JS or use CDN links). The preview runs in a sandboxed iframe.` + "\n\n" + `IMPORTANT: All file paths are relative to the project root. Do not use absolute paths.`
}

// buildArtifactSystemPrompt returns the system prompt, including a listing of
// existing files when iterating on a previously created artifact.
func buildArtifactSystemPrompt(artifactDir string) string {
	base := artifactSystemPrompt()
	entries, err := os.ReadDir(artifactDir)
	if err != nil || len(entries) == 0 {
		return base
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			files = append(files, e.Name()+"/")
		} else {
			files = append(files, e.Name())
		}
	}
	return base + "\n\n" + fmt.Sprintf(
		"You are working on an EXISTING project. The following files are already present:\n  %s\n"+
			"Use read_file to inspect current files before making changes. Edit files with write_file to update them. "+
			"Only recreate files that need changes — do not rewrite the entire project unless necessary.",
		strings.Join(files, "\n  "))
}

// artifactToolStartPayload builds the SSE "tool start" payload for artifact tools.
func artifactToolStartPayload(name string, args json.RawMessage) map[string]any {
	p := map[string]any{"phase": "start", "name": name}
	m := parseToolArgs(args)
	switch name {
	case "write_file":
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

	case "read_file":
		path, _ := m["path"].(string)
		if strings.TrimSpace(path) == "" {
			return "Error: missing path for read_file", nil
		}
		full := filepath.Join(artifactDir, path)
		if !isPathSafe(artifactDir, full) {
			return "Error: path escapes project directory", nil
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
		// No I/O — the loop handles sending the SSE artifact event.
		return "artifact ready", nil

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

// execInDir runs a shell command in dir with a 30-second timeout.
// parentCtx allows cancellation to propagate from the request context.
func execInDir(parentCtx context.Context, dir, command string) (string, error) {
	ctx, cancel := context.WithTimeout(parentCtx, 30*time.Second)
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
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return outBuf.String(), fmt.Errorf("exec timed out after 30s")
		}
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

	// Build tool list: artifact tools + web tools if web_tools is also on.
	tools := artifactToolDefinitions()
	if body.WebTools != nil && *body.WebTools {
		tools = append(tools, webToolDefinitions()...)
	}

	// Use existing artifact directory if provided, otherwise create a new one.
	var artifactDir string
	var ts int64
	if body.ArtifactDir != "" {
		candidate := filepath.Join("artifacts", filepath.Clean(body.ArtifactDir))
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			artifactDir = candidate
			// Extract timestamp from dir name for preview URL.
			if base := filepath.Base(artifactDir); base != "" {
				if parsed, err := strconv.ParseInt(base, 10, 64); err == nil {
					ts = parsed
				}
			}
			log.Printf("[artifact] reusing existing dir: %s", artifactDir)
		}
	}
	if artifactDir == "" {
		ts = time.Now().Unix()
		artifactDir = filepath.Join("artifacts", fmt.Sprintf("%d", ts))
		if err := os.MkdirAll(artifactDir, 0o755); err != nil {
			send("error", map[string]any{"error": fmt.Sprintf("create artifact dir: %v", err)})
			return
		}
	}

	// Inject system prompt for artifacts (with existing file listing if iterating).
	msgs := make([]ollama.ChatMessage, 0, len(body.Messages)+1)
	msgs = append(msgs, ollama.ChatMessage{
		Role:    "system",
		Content: buildArtifactSystemPrompt(artifactDir),
	})
	msgs = append(msgs, body.Messages...)

	accComp := 0
	var accEvalNS int64
	// If iterating on an existing artifact, mark as already sent so write_file
	// triggers reload events without needing create_artifact again.
	artifactSent := body.ArtifactDir != "" && artifactDir != ""

	for round := 0; round < maxArtifactRounds; round++ {
		if ctx.Err() != nil {
			log.Printf("[artifact] context cancelled at round %d", round)
			return
		}
		log.Printf("[artifact] round %d start, messages: %d", round, len(msgs))
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
		toolCallSeen := false

		// While the model is streaming, show a generic "using something" indicator
		// if it goes silent for a short period. This covers the gap before the
		// model emits the actual tool_call JSON.
		var pendingMu sync.Mutex
		var pendingSent bool
		var pendingTimer *time.Timer
		startPendingTimer := func() {
			pendingMu.Lock()
			defer pendingMu.Unlock()
			if pendingTimer != nil {
				pendingTimer.Stop()
			}
			if pendingSent {
				return
			}
			pendingTimer = time.AfterFunc(800*time.Millisecond, func() {
				pendingMu.Lock()
				if pendingSent {
					pendingMu.Unlock()
					return
				}
				pendingSent = true
				pendingMu.Unlock()
				send("tool", map[string]any{"phase": "pending"})
			})
		}
		stopPendingTimer := func() {
			pendingMu.Lock()
			defer pendingMu.Unlock()
			if pendingTimer != nil {
				pendingTimer.Stop()
				pendingTimer = nil
			}
		}
		startPendingTimer()
		defer stopPendingTimer()

		err := s.ollama.Chat(ctx, req, func(ev ollama.ChatChunk) error {
			last = ev
			m := ev.Message
			// Reset pending timer every time we receive a chunk. If the model goes
			// silent for 800ms, the frontend will see a generic "using something" hint.
			startPendingTimer()
			if m.Thinking != "" {
				acc.Thinking += m.Thinking
				pendingMu.Lock()
				pendingSent = true
				pendingMu.Unlock()
			}
			if m.Content != "" {
				acc.Content += m.Content
				pendingMu.Lock()
				pendingSent = true
				pendingMu.Unlock()
			}
			if len(m.ToolCalls) > 0 {
				pendingMu.Lock()
				pendingSent = true
				pendingMu.Unlock()
				acc.ToolCalls = m.ToolCalls
				if !toolCallSeen {
					toolCallSeen = true
					// Send a progress event so the frontend can show which tool is being generated.
					for _, tc := range m.ToolCalls {
						name := tc.Function.Name
						if name == "" {
							continue
						}
						p := map[string]any{"phase": "generating", "name": name}
						// Try to extract partial path/command for early feedback.
						partial := parseToolArgs(tc.Function.Arguments)
						if path, _ := partial["path"].(string); path != "" {
							p["path"] = path
						}
						if cmd, _ := partial["command"].(string); cmd != "" {
							p["command"] = cmd
						}
						if artName, _ := partial["name"].(string); artName != "" {
							p["artifact_name"] = artName
						}
						send("tool", p)
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
				out, toolErr = s.runArtifactTool(ctx, artifactDir, n, tc.Function.Arguments)
			}
			if toolErr != nil {
				out = "Error: " + toolErr.Error()
			}
			out = truncateRunes(out, maxToolResultRunes)

			// Handle create_artifact: send artifact event.
			if n == "create_artifact" && !artifactSent {
				artifactSent = true
				m := parseToolArgs(tc.Function.Arguments)
				artName, _ := m["name"].(string)
				artDesc, _ := m["description"].(string)
				if artName == "" {
					artName = "Artifact"
				}
				previewURL := fmt.Sprintf("/api/artifacts/%d/", ts)
				send("artifact", map[string]any{
					"url":         previewURL,
					"name":        artName,
					"description": artDesc,
					"timestamp":   ts,
				})
			}

			// After write_file on an existing artifact, send a reload event so
			// the frontend can refresh the iframe preview in real time.
			if n == "write_file" && toolErr == nil && artifactSent {
				previewURL := fmt.Sprintf("/api/artifacts/%d/", ts)
				send("artifact", map[string]any{
					"url":       previewURL,
					"reload":    true,
					"timestamp": ts,
				})
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
