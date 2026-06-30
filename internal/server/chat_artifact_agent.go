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
	}
}

// artifactSystemPrompt returns the system prompt injected when artifacts mode is on.
func artifactSystemPrompt() string {
	return `You are a helpful assistant. If the user wants you to create a web project, display a web page, design a window interface and show it, or explain/visualize something using a website, you MUST call the tool 'create_artifact' with a name and description to initialize the project space.
You must be highly proactive: when the user asks you to build, showcase, explain, or design something, do not just describe it or write code snippets in chat. Directly initialize the workspace via 'create_artifact' and implement the code files immediately.
Do not attempt to write files or execute commands before calling 'create_artifact'.
When building a web project, write the files starting with index.html as the entry point.
Keep projects self-contained (inline CSS/JS or use CDN links). The preview runs in a sandboxed iframe.
IMPORTANT: All file paths are relative to the project root. Do not use absolute paths.`
}

// artifactExistingSystemPrompt returns the system prompt injected when modifying an existing project.
func artifactExistingSystemPrompt() string {
	return `You are a helpful assistant. You are working on an EXISTING project workspace.
You must be highly proactive: when the user asks for changes, updates, additions, or bug fixes, directly apply those changes to the project files using the filesystem tools. Do not just describe the changes in conversation; implement them immediately in the workspace so the user can see the updated preview.
You must use the following tools to inspect, edit, and build the project:
- 'write_file': Create or overwrite a file in the project. Arguments:
  * 'path': Relative path inside the project (e.g. index.html, styles.css, js/app.js)
  * 'content': Full file content
- 'read_file': Read the contents of an existing file. Arguments:
  * 'path': Relative path inside the project
- 'list_dir': List files and folders in a directory. Arguments:
  * 'path': Relative path inside the project (default '.')
- 'exec': Run a shell command in the project directory (e.g., to install npm packages, compile code, etc.). Arguments:
  * 'command': The shell command to run

When building or updating a web project, write the files starting with index.html as the entry point.
Keep projects self-contained (inline CSS/JS or use CDN links). The preview runs in a sandboxed iframe.
IMPORTANT: All file paths are relative to the project root. Do not use absolute paths.`
}

// buildArtifactSystemPrompt returns the system prompt, including a listing of
// existing files when iterating on a previously created artifact.
func buildArtifactSystemPrompt(artifactDir string) string {
	if artifactDir == "" {
		return artifactSystemPrompt()
	}
	entries, err := os.ReadDir(artifactDir)
	if err != nil || len(entries) == 0 {
		return artifactSystemPrompt()
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			files = append(files, e.Name()+"/")
		} else {
			files = append(files, e.Name())
		}
	}
	return artifactExistingSystemPrompt() + "\n\n" + fmt.Sprintf(
		"The following files are already present in the workspace:\n  %s\n"+
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

	// Use existing artifact directory if provided. For new requests, do NOT create
	// a directory yet — it will be created on-demand when the agent actually
	// writes a file. This avoids leaving empty artifact folders for messages
	// that never create anything.
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

	// Lazy directory creation helper: only makes the artifacts/<ts>/ folder when
	// the agent is about to write something for the first time.
	ensureArtifactDir := func() error {
		if artifactDir != "" {
			return nil
		}
		ts = time.Now().Unix()
		artifactDir = filepath.Join("artifacts", fmt.Sprintf("%d", ts))
		if err := os.MkdirAll(artifactDir, 0o755); err != nil {
			return fmt.Errorf("create artifact dir: %w", err)
		}
		log.Printf("[artifact] created dir on demand: %s", artifactDir)
		return nil
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
				if n == "write_file" || n == "exec" {
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
				previewURL := fmt.Sprintf("/api/artifacts/%d/", ts)
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
				out = "Artifact project created. You now have access to the filesystem tools (write_file, read_file, list_dir, exec) to write your files."
			}
			// After write_file on an artifact, send the appropriate event:
			// - loaded: first time index.html is written (transition from loading screen)
			// - reload: subsequent writes (refresh the live preview)
			if n == "write_file" && toolErr == nil {
				writePath, _ := parseToolArgs(tc.Function.Arguments)["path"].(string)
				normalizedPath := strings.TrimPrefix(strings.ToLower(writePath), "./")
				previewURL := fmt.Sprintf("/api/artifacts/%d/", ts)
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
