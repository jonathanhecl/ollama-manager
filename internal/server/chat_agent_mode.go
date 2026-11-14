package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gense/ollama-manager/internal/ollama"
)

const maxArtifactRounds = 30

// artifactBrowserToolNames are the tools that cannot do any work on the server:
// they register a request channel and wait for the artifact preview panel in a
// browser to answer.
var artifactBrowserToolNames = map[string]bool{
	"take_artifact_screenshot": true,
	"eval_artifact_js":         true,
}

// filterArtifactToolDefinitions drops the browser-dependent tools (or keeps
// only them, when keep is true). It leaves every other schema untouched.
func filterArtifactToolDefinitions(tools []any, keep bool) []any {
	out := make([]any, 0, len(tools))
	for _, tool := range tools {
		fn, _ := tool.(map[string]any)["function"].(map[string]any)
		name, _ := fn["name"].(string)
		if artifactBrowserToolNames[name] == keep {
			out = append(out, tool)
		}
	}
	return out
}

// artifactWorkspaceToolDefinitions returns the core agent workspace tools
// (file creation, targeted edits, reading, directory listing, and command
// execution) that are available immediately when Agent Mode is enabled.
func artifactWorkspaceToolDefinitions() []any {
	return []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "write_file",
				"description": "Create or overwrite a file in the agent workspace directory. Use this for scripts, code, data files, markdown, or HTML/CSS/JS web artifact files.",
				"parameters": map[string]any{
					"type":     "object",
					"required": []string{"path", "content"},
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "Relative path inside the workspace (e.g. script.py, notes.md, index.html, src/app.js)",
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
							"description": "Relative path inside the workspace (e.g. index.html, styles.css, main.py)",
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
				"description": "Read the contents of a file in the agent workspace directory.",
				"parameters": map[string]any{
					"type":     "object",
					"required": []string{"path"},
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "Relative path inside the workspace",
						},
					},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "list_dir",
				"description": "List files and directories in the agent workspace directory.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{
							"type":        "string",
							"description": "Relative path inside the workspace (default '.')",
						},
					},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "exec",
				"description": "Execute a shell command inside the isolated workspace directory. The working directory (CWD) is already set to the workspace root ('.'). Never use cd or reference absolute host paths.",
				"parameters": map[string]any{
					"type":     "object",
					"required": []string{"command"},
					"properties": map[string]any{
						"command": map[string]any{
							"type":        "string",
							"description": "Shell command to run directly in the workspace root directory (e.g. 'ls', 'python3 script.py', 'npm test'). Do not use cd.",
						},
					},
				},
			},
		},
	}
}

// artifactInitToolDefinition returns the create_artifact schema used to explicitly
// name and open a live web artifact preview panel.
func artifactInitToolDefinition() map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        "create_artifact",
			"description": "Initialize and open a named live web preview panel for an HTML/CSS/JS artifact. Call this only when building an interactive web page, dashboard, game, animation, or visualization that needs a live preview — NOT for scripts, CLI commands, or general workspace files.",
			"parameters": map[string]any{
				"type":     "object",
				"required": []string{"name"},
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Display name for the web artifact",
					},
					"description": map[string]any{
						"type":        "string",
						"description": "Short description of what the web artifact does",
					},
				},
			},
		},
	}
}

// artifactPreviewToolDefinitions returns tools for inspecting an active HTML artifact preview.
func artifactPreviewToolDefinitions(hasVision bool) []any {
	tools := []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "get_artifact_console",
				"description": "Retrieve the console logs, outputs, and javascript runtime errors captured from the active artifact preview. Use this to verify your code execution or debug runtime issues if the user reports that the app is blank, not working, or has errors.",
				"parameters": map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			},
		},
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "eval_artifact_js",
				"description": "Execute JavaScript directly inside the live artifact preview iframe. Use this to interact with UI elements (click buttons, fill input fields, submit forms, trigger events) or inspect live DOM state, values, and test interactive user flows.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"code": map[string]any{
							"type":        "string",
							"description": "JavaScript code or expression to run in the iframe context. Supports async/await, 'return <value>', direct expressions, and DOM APIs like document.querySelector.",
						},
					},
					"required": []string{"code"},
				},
			},
		},
	}
	if hasVision {
		tools = append(tools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        "take_artifact_screenshot",
				"description": "Capture a visual screenshot of the current rendered artifact web page from the user's live browser preview. Use this to inspect the UI, visual layout, color contrast, styling, element positioning, and see what the user is seeing.",
				"parameters": map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			},
		})
	}
	return tools
}

// artifactOperationalToolDefinitions returns all filesystem, execution, preview,
// and vision tool schemas.
func artifactOperationalToolDefinitions(hasVision bool) []any {
	tools := artifactWorkspaceToolDefinitions()
	return append(tools, artifactPreviewToolDefinitions(hasVision)...)
}

// artifactSystemPrompt returns the system prompt injected when Agent Mode is enabled for a new workspace.
func artifactSystemPrompt(hasVision bool) string {
	visionNote := ""
	if hasVision {
		visionNote = "\n- 'take_artifact_screenshot': Capture a visual screenshot of the rendered web page in the user's browser to inspect UI, layout, styling, and visual rendering."
	}
	return `You are an autonomous Agent Mode assistant operating inside an isolated per-session workspace directory ('.').
You already have direct access to workspace agent tools:
- 'write_file': Create or overwrite a file in the workspace (relative path, e.g. 'script.py', 'notes.md', 'index.html').
- 'replace_in_file': Replace a specific snippet or block in an existing file.
- 'read_file': Read the contents of an existing file.
- 'list_dir': List files and subdirectories inside the workspace (default '.').
- 'exec': Run shell commands directly inside the workspace root directory (CWD is already '.'). Never use 'cd' with absolute host paths.
- 'create_artifact': Optionally initialize and name a live web preview panel when building an HTML/CSS/JS web artifact.

HOW TO CHOOSE YOUR WORKFLOW:
1. **General Agent Tasks (scripts, CLI commands, coding, analysis, file editing)**: Use 'write_file', 'replace_in_file', 'read_file', 'list_dir', and 'exec' directly in the workspace. Do NOT call 'create_artifact' unless building a web UI/page that should open a browser preview panel.
2. **Live Web Artifacts (web apps, dashboards, games, animations, HTML visualizations)**: Call 'create_artifact' with 'name' and 'description' to open the preview panel, then write 'index.html' (or write 'index.html' directly with 'write_file'). Once a web artifact preview is active, preview inspection tools ('get_artifact_console', 'eval_artifact_js'` + visionNote + `) become available. Keep web artifacts self-contained (inline CSS/JS or CDN libraries).
3. **Ordinary Conversation (greetings, simple questions, explanations, planning before editing)**: Respond directly in chat without calling tools unless they add clear value. Never create workspace files unnecessarily when the user is just asking a question.

ISOLATED WORKSPACE ENVIRONMENT:
1. Treat this workspace directory as your complete root workspace ('.').
2. The current working directory (CWD) for all tools and shell commands ('exec') is ALREADY this workspace directory.
3. NEVER use 'cd' with absolute host paths and NEVER reference host paths (e.g. /Users/..., /home/..., C:\...).
4. All file paths MUST be relative to the workspace root (e.g. 'main.py', 'data/out.json', 'index.html').

UI/CONVERSATION RULES:
1. Do NOT repeat or dump large file contents in chat after writing/editing them via tools.
2. Report clearly what commands you executed, files you created/edited, and observed outputs or results.`
}

// artifactExistingSystemPrompt returns the system prompt injected when continuing in an existing Agent Mode workspace.
func artifactExistingSystemPrompt(hasVision bool, hasPreview bool) string {
	previewNote := "If you create an 'index.html' (or call 'create_artifact'), the live web preview panel will open automatically."
	previewTools := ""
	if hasPreview {
		visionLine := ""
		if hasVision {
			visionLine = "\n- 'take_artifact_screenshot': Capture a visual screenshot of the rendered preview in the user's browser to inspect UI, visual layout, styling, and colors."
		}
		previewNote = "A live web artifact preview is active; any changes to web files will refresh it."
		previewTools = "\n- 'get_artifact_console': Retrieve the console logs, outputs, and javascript runtime errors from the active preview.\n- 'eval_artifact_js': Execute JavaScript directly in the rendered preview iframe to interact with the UI (click, input, submit) or inspect DOM state." + visionLine
	}
	return `You are an autonomous Agent Mode assistant continuing in an ACTIVE isolated workspace directory ('.').
All workspace tools are ALREADY AVAILABLE for you to use:
- 'write_file': Create or overwrite a file in the workspace. Required arguments: 'path' (relative), 'content' (full file content).
- 'replace_in_file': Replace a specific text snippet or code block in an existing file. Required arguments: 'path', 'old_string', 'new_string'.
- 'read_file': Read the contents of an existing file. Required arguments: 'path'.
- 'list_dir': List files and folders in a directory. Optional argument: 'path' (default '.').
- 'exec': Run a shell command in the workspace directory. The CWD is already the workspace root. Do not use cd or absolute host paths. Required argument: 'command'.` + previewTools + `

ISOLATED WORKSPACE ENVIRONMENT:
1. Treat this workspace directory as your complete root workspace ('.').
2. The current working directory (CWD) for all tools and shell commands ('exec') is ALREADY this workspace directory.
3. NEVER use 'cd' with absolute host paths and NEVER reference host paths (e.g. /Users/..., /home/..., C:\...).
4. All file paths MUST be relative to the workspace root.

WORKFLOW:
1. When asked to inspect, modify, run commands, or fix files, directly use 'read_file', 'list_dir', 'write_file', 'replace_in_file', or 'exec'.
2. ` + previewNote + `
3. Not every message requires a tool call. If the user is just chatting or asking a question that needs no workspace action, answer directly in chat.`
}

// buildArtifactSystemPrompt returns the system prompt, including a listing of
// existing files when continuing in a previously created workspace.
func buildArtifactSystemPrompt(artifactDir string, hasVision bool) string {
	if artifactDir == "" {
		return artifactSystemPrompt(hasVision)
	}
	hasPreview := hasArtifactPreview(artifactDir)
	entries, err := os.ReadDir(artifactDir)
	if err != nil {
		return artifactExistingSystemPrompt(hasVision, hasPreview)
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
		return artifactExistingSystemPrompt(hasVision, hasPreview)
	}
	return artifactExistingSystemPrompt(hasVision, hasPreview) + "\n\n" + fmt.Sprintf(
		"The following files are already present in the active workspace:\n  %s\n"+
			"Use read_file to inspect current files before making changes, or write_file / replace_in_file / exec to work with them.",
		strings.Join(files, "\n  "))
}

var reHTMLTitle = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

func hasArtifactPreview(artifactDir string) bool {
	if strings.TrimSpace(artifactDir) == "" {
		return false
	}
	if info, err := os.Stat(filepath.Join(artifactDir, "index.html")); err == nil && !info.IsDir() {
		return true
	}
	if info, err := os.Stat(filepath.Join(artifactDir, ".artifact.json")); err == nil && !info.IsDir() {
		return true
	}
	return false
}

// ensureArtifactPreviewMetadata returns the artifact display name for an
// active preview, reading .artifact.json if present or creating it from the
// HTML <title> (falling back to "Artifact") when index.html was written directly.
func ensureArtifactPreviewMetadata(artifactDir string) string {
	metaFile := filepath.Join(artifactDir, ".artifact.json")
	if b, err := os.ReadFile(metaFile); err == nil {
		var meta struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(b, &meta) == nil && strings.TrimSpace(meta.Name) != "" {
			return strings.TrimSpace(meta.Name)
		}
	}
	name := ""
	if b, err := os.ReadFile(filepath.Join(artifactDir, "index.html")); err == nil {
		if m := reHTMLTitle.FindStringSubmatch(string(b)); len(m) > 1 {
			candidate := strings.TrimSpace(html.UnescapeString(m[1]))
			candidate = strings.Join(strings.Fields(candidate), " ")
			if candidate != "" {
				name = truncateToolPreview(candidate, 80)
			}
		}
	}
	if name == "" {
		name = "Artifact"
	}
	metaBytes, _ := json.MarshalIndent(map[string]any{
		"name":        name,
		"description": "",
		"created_at":  time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	_ = os.WriteFile(metaFile, metaBytes, 0o644)
	return name
}

func truncateToolPreview(s string, maxRunes int) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" || maxRunes <= 0 {
		return ""
	}
	if utf8.RuneCountInString(trimmed) > maxRunes {
		return string([]rune(trimmed)[:maxRunes]) + "…"
	}
	return trimmed
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
			p["command"] = truncateToolPreview(cmd, 120)
		}
	case "eval_artifact_js":
		if code, _ := m["code"].(string); strings.TrimSpace(code) != "" {
			p["code"] = truncateToolPreview(code, 120)
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

func resolveArtifactSafePath(artifactDir, userPath string) (string, error) {
	if strings.TrimSpace(artifactDir) == "" {
		return "", errors.New("workspace not initialized")
	}
	clean := strings.TrimSpace(userPath)
	if clean == "" {
		return "", errors.New("missing path")
	}
	clean = strings.TrimPrefix(clean, "/")
	clean = strings.TrimPrefix(clean, "\\")
	full := filepath.Join(artifactDir, filepath.Clean(clean))
	if !isPathSafe(artifactDir, full) {
		return "", errors.New("path escapes project directory")
	}
	return full, nil
}

// runArtifactTool executes a single tool against the artifact directory.
func (s *Server) runArtifactTool(ctx context.Context, artifactDir, name string, args json.RawMessage) (string, error) {
	m := parseToolArgs(args)
	switch name {
	case "write_file":
		path, _ := m["path"].(string)
		content, _ := m["content"].(string)
		full, err := resolveArtifactSafePath(artifactDir, path)
		if err != nil {
			return "Error: " + err.Error(), nil
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
		if oldStr == "" {
			return "Error: missing old_string for replace_in_file", nil
		}
		full, err := resolveArtifactSafePath(artifactDir, path)
		if err != nil {
			return "Error: " + err.Error(), nil
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
		if strings.TrimSpace(artifactDir) == "" {
			if strings.TrimSpace(path) == "" {
				return "Error: missing path", nil
			}
			return "Error: file not found", nil
		}
		full, err := resolveArtifactSafePath(artifactDir, path)
		if err != nil {
			return "Error: " + err.Error(), nil
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
		if strings.TrimSpace(artifactDir) == "" {
			clean := filepath.Clean(strings.TrimSpace(path))
			if clean == "." {
				return "(empty)", nil
			}
			if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || filepath.IsAbs(path) {
				return "Error: path escapes project directory", nil
			}
			return "Error: directory not found", nil
		}
		full, err := resolveArtifactSafePath(artifactDir, path)
		if err != nil {
			return "Error: " + err.Error(), nil
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
		if hasArtifactPreview(artifactDir) {
			return "Error: an artifact preview is already active in this session. Do NOT call create_artifact again. You already have full access to write_file, replace_in_file, read_file, list_dir, exec, eval_artifact_js, and get_artifact_console.", nil
		}
		// No I/O — the loop handles sending the SSE artifact event.
		return "Artifact preview workspace initialized. You can write index.html and other project files inside the workspace root directory ('.').", nil

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

	cmdStr := strings.TrimSpace(command)
	// Strip redundant "cd <dir> && " or "cd <dir> ; " if the model attempts to cd into the artifact directory
	if strings.HasPrefix(cmdStr, "cd ") {
		absDir, _ := filepath.Abs(dir)
		for _, sep := range []string{"&&", ";"} {
			if idx := strings.Index(cmdStr, sep); idx != -1 {
				cdTarget := strings.TrimSpace(strings.TrimPrefix(cmdStr[:idx], "cd "))
				cdTargetClean := filepath.Clean(cdTarget)
				if cdTargetClean == "." || cdTargetClean == dir || (absDir != "" && cdTargetClean == absDir) || strings.Contains(cdTargetClean, "artifacts") {
					cmdStr = strings.TrimSpace(cmdStr[idx+len(sep):])
					break
				}
			}
		}
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/c", cmdStr)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", cmdStr)
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
// It streams chunks to the sink (either the live SSE response or a detached
// chat session), executes tools, and sends an "artifact" event when the model
// calls create_artifact.
func (s *Server) runArtifactAgentLoop(ctx context.Context, sink chatSink, body chatRequestBody) {
	send := sink.Send

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

	hasVision := s.sessionModelCaps(ctx, body.Model).HasVision

	sysPrompt := buildArtifactSystemPrompt(artifactDir, hasVision)
	comfyOn := s.comfyToolEnabled(body)
	ragOn := s.ragAgentEnabled(ctx, body)
	skills := s.selectedSkillsForChat(body)
	skillsOn := len(skills) > 0
	webOn := body.WebTools != nil && *body.WebTools
	var mcpTurn *mcpTurnSession
	if s.mcpEnabled(body) {
		var mcpErr error
		mcpTurn, mcpErr = s.prepareMCPForChat(ctx, body)
		if mcpTurn != nil {
			defer mcpTurn.Close()
		}
		if mcpErr != nil && !mcpTurn.HasTools() {
			log.Printf("[artifact] MCP connect warning: %v", mcpErr)
		}
	}
	if ragOn {
		sysPrompt += "\n\n" + ragAgentSystemInstruction
	}
	if skillsOn {
		sysPrompt += "\n\n" + skillsSystemPromptSection(skills, true, webOn)
	}
	if mcpTurn != nil {
		if sec := mcpTurn.SystemPrompt(); sec != "" {
			sysPrompt += "\n\n" + sec
		}
	}
	if comfyOn {
		sysPrompt += "\n\n" + comfySystemPromptSection(s.comfyWorkflows.ListEnabled(), hasVision, body.ComfyWorkflow)
	}
	if webOn {
		sysPrompt += "\n\n" + webToolSystemInstruction
	}
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
	hasExistingIndex := false
	if artifactDir != "" {
		if info, err := os.Stat(filepath.Join(artifactDir, "index.html")); err == nil && !info.IsDir() {
			hasExistingIndex = true
		}
	}
	// For existing HTML artifacts the preview is already live, so subsequent writes
	// trigger reload events rather than a fresh loaded event.
	artifactLoaded := hasExistingIndex
	artifactPreviewActive := hasArtifactPreview(artifactDir)
	workspaceEventSent := false

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

		tools := artifactWorkspaceToolDefinitions()
		if !artifactPreviewActive {
			// Offer create_artifact until a preview is active so the model can still
			// initialize and name a live web preview panel whenever needed, while keeping
			// all workspace tools immediately usable from round 0.
			tools = append(tools, artifactInitToolDefinition())
		} else {
			previewTools := artifactPreviewToolDefinitions(hasVision)
			if !body.browserToolsAllowed() {
				// Nobody is watching this run in a browser, so the two tools
				// that rendezvous with the artifact preview panel would only burn
				// their 8-10s timeout before erroring out. Keep them out of the
				// tool list until a tab subscribes again, which is why the
				// condition is re-checked on every round rather than once.
				previewTools = filterArtifactToolDefinitions(previewTools, false)
			}
			tools = append(tools, previewTools...)
		}
		if body.WebTools != nil && *body.WebTools {
			tools = append(tools, webToolDefinitions()...)
		}
		if ragOn {
			tools = append(tools, s.ragToolDefinitions(body)...)
		}
		if skillsOn {
			tools = append(tools, skillToolDefinitions()...)
		}
		if mcpTurn != nil && mcpTurn.HasTools() {
			tools = append(tools, mcpTurn.Tools()...)
		}
		if comfyOn {
			// Re-read every round: the user can register or disable a workflow
			// from Settings while a turn is running.
			tools = append(tools, s.comfyToolDefinitions(body.ComfyWorkflow)...)
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

		err := s.chatWithModel(ctx, req, func(ev ollama.ChatChunk) error {
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
					var path, cmd, code, artName, skill string
					if partial != nil {
						path, _ = partial["path"].(string)
						if rawCmd, _ := partial["command"].(string); rawCmd != "" {
							cmd = truncateToolPreview(rawCmd, 120)
						}
						if rawCode, _ := partial["code"].(string); rawCode != "" {
							code = truncateToolPreview(rawCode, 120)
						}
						if name == "create_artifact" {
							artName, _ = partial["name"].(string)
						} else if isSkillTool(name) {
							skill, _ = partial["skill"].(string)
							if skill == "" && name == "load_skill" {
								skill, _ = partial["name"].(string)
							}
						}
					}

					state, exists := sentTools[i]
					if !exists {
						state = &toolSentState{name: name}
						sentTools[i] = state
						p := map[string]any{"phase": "generating", "name": name, "tool_index": i}
						if mcpSrv, mcpTool, ok := mcpTurn.LookupTool(name); ok {
							p["mcp_server"] = mcpSrv
							p["mcp_tool"] = mcpTool
						}
						if path != "" {
							p["path"] = path
							state.path = path
						}
						if cmd != "" {
							p["command"] = cmd
							state.command = cmd
						}
						if code != "" {
							p["code"] = code
							state.code = code
						}
						if artName != "" {
							p["artifact_name"] = artName
							state.artifactName = artName
						}
						if skill != "" {
							p["skill"] = skill
						}
						send("tool", p)
					} else {
						updated := false
						p := map[string]any{"phase": "generating", "name": name, "tool_index": i}
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
						if code != "" && code != state.code {
							p["code"] = code
							state.code = code
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
			emptyFinal := strings.TrimSpace(assistant.Content) == "" && strings.TrimSpace(assistant.Thinking) == ""
			if emptyFinal {
				log.Printf("[artifact] round %d: empty response (no content, no tool calls), stopping", round)
				// Nothing was answered: the reported eval tokens and their
				// near-zero duration are not real throughput, so this run does
				// not count as usage nor as a tok/s record.
				accComp -= last.EvalCount
				accEvalNS -= last.EvalDuration
				if accComp < 0 {
					accComp = 0
				}
				if accEvalNS < 0 {
					accEvalNS = 0
				}
			} else {
				s.recordModelUsage(body.Model, accComp, accEvalNS, last.PromptEvalCount, time.Now())
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

		for tcIdx, tc := range assistant.ToolCalls {
			n := tc.Function.Name
			if n == "" {
				continue
			}

			// Use artifact-aware payload for artifact tools, web payload for web/rag/skill/mcp tools.
			mcpSrv, mcpTool, isMCP := mcpTurn.LookupTool(n)
			startPayload := artifactToolStartPayload(n, tc.Function.Arguments)
			if isMCP {
				startPayload = mcpToolStartPayload(mcpSrv, mcpTool, n, tc.Function.Arguments)
			} else if isWebTool(n) || isRAGTool(n) || isSkillTool(n) {
				startPayload = toolStartPayload(n, tc.Function.Arguments)
			}
			if n == comfyToolName {
				startPayload = comfyToolStartPayload(startPayload, tc.Function.Arguments,
					s.comfyWorkflowLabelFor(s.comfyConfiguredWorkflowID(body.ComfyWorkflow), tc.Function.Arguments))
			}
			startPayload["tool_index"] = tcIdx
			send("tool", startPayload)

			var out string
			var toolErr error
			var toolImageBase64 string
			var toolImages []string
			var toolMedia []ChatMedia
			toolWorkflow := ""
			if isMCP {
				out, mcpSrv, mcpTool, _, toolErr = mcpTurn.Call(ctx, n, tc.Function.Arguments)
			} else if n == comfyToolName {
				res, runErr := s.executeComfyTool(ctx, body, tc.Function.Arguments, func(status string) {
					send("tool", map[string]any{"phase": "running", "name": n, "run_status": status})
				})
				out, toolErr, toolImages, toolMedia = res.Text, runErr, res.Images, res.Media
				toolWorkflow = res.WorkflowName
			} else if n == "take_artifact_screenshot" {
				reqID := fmt.Sprintf("ss-%d-%d", time.Now().UnixNano(), round)
				ch := make(chan artifactScreenshotResponse, 1)
				s.artifactScreenshotMu.Lock()
				s.artifactScreenshotCh[reqID] = ch
				s.artifactScreenshotMu.Unlock()

				defer func(id string) {
					s.artifactScreenshotMu.Lock()
					delete(s.artifactScreenshotCh, id)
					s.artifactScreenshotMu.Unlock()
				}(reqID)

				send("artifact_screenshot_request", map[string]any{
					"request_id": reqID,
					"timestamp":  ts,
				})

				select {
				case res := <-ch:
					if res.Error != "" {
						out = "Error capturing screenshot: " + res.Error
					} else if res.Image != "" {
						imgStr := strings.TrimPrefix(res.Image, "data:image/jpeg;base64,")
						imgStr = strings.TrimPrefix(imgStr, "data:image/png;base64,")
						toolImageBase64 = imgStr
						out = "Artifact screenshot captured successfully. The visual screenshot image of the rendered artifact is attached to this message."
					} else {
						out = "Error: empty screenshot data received from preview."
					}
				case <-time.After(8 * time.Second):
					out = "Error: screenshot request timed out. Make sure the artifact preview panel is currently open in your browser."
				case <-ctx.Done():
					out = "Error: context cancelled"
					toolErr = ctx.Err()
				}
			} else if n == "eval_artifact_js" {
				m := parseToolArgs(tc.Function.Arguments)
				code, _ := m["code"].(string)
				if strings.TrimSpace(code) == "" {
					out = "Error: missing 'code' argument for eval_artifact_js"
				} else {
					reqID := fmt.Sprintf("eval-%d-%d", time.Now().UnixNano(), round)
					ch := make(chan artifactEvalResponse, 1)
					s.artifactEvalMu.Lock()
					s.artifactEvalCh[reqID] = ch
					s.artifactEvalMu.Unlock()

					defer func(id string) {
						s.artifactEvalMu.Lock()
						delete(s.artifactEvalCh, id)
						s.artifactEvalMu.Unlock()
					}(reqID)

					send("artifact_eval_request", map[string]any{
						"request_id": reqID,
						"code":       code,
						"timestamp":  ts,
					})

					select {
					case res := <-ch:
						if res.Error != "" {
							out = "Error evaluating JavaScript: " + res.Error
						} else if res.Result != "" {
							out = res.Result
						} else {
							out = "undefined (executed successfully with no return value)"
						}
					case <-time.After(10 * time.Second):
						out = "Error: eval request timed out. Make sure the artifact preview panel is currently open in your browser."
					case <-ctx.Done():
						out = "Error: context cancelled"
						toolErr = ctx.Err()
					}
				}
			} else if isRAGTool(n) {
				var ragFile string
				out, ragFile, toolErr = s.runRAGTool(ctx, sink, body, n, tc.Function.Arguments)
				if toolErr == nil && ragFile != "" {
					send("rag_updated", map[string]any{"filename": ragFile})
				}
			} else if isSkillTool(n) {
				if !skillsOn {
					toolErr = errors.New("skills are not enabled for this chat")
				} else {
					out, toolErr = s.runSkillToolWithAllowed(ctx, n, tc.Function.Arguments, skills)
				}
			} else if isWebTool(n) {
				if body.WebTools == nil || !*body.WebTools {
					toolErr = errors.New("web tools are not enabled for this chat")
				} else {
					out, toolErr = s.runWebTool(ctx, n, tc.Function.Arguments)
				}
			} else if strings.HasPrefix(n, "mcp__") {
				toolErr = fmt.Errorf("MCP tool %q is not enabled for this chat", n)
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
			if !(isRAGTool(n) && toolErr == nil) {
				out = truncateRunes(out, maxToolResultRunes)
			}

			// Handle create_artifact: reveal the artifact panel.
			// If index.html is already present we can load the preview immediately;
			// otherwise show a loading screen until the entry point is written.
			if n == "create_artifact" && toolErr == nil && !strings.HasPrefix(out, "Error:") {
				artifactPreviewActive = true
				workspaceEventSent = true
				if artifactDir == "" {
					if err := ensureArtifactDir(); err != nil {
						toolErr = err
					}
				}
				if toolErr == nil {
					m := parseToolArgs(tc.Function.Arguments)
					artName, _ := m["name"].(string)
					artDesc, _ := m["description"].(string)
					if artName == "" {
						artName = "Artifact"
					}
					previewURL := fmt.Sprintf("/api/artifacts/%s/", ts)
					metaFile := filepath.Join(artifactDir, ".artifact.json")
					metaBytes, _ := json.MarshalIndent(map[string]any{
						"name":        artName,
						"description": artDesc,
						"created_at":  time.Now().UTC().Format(time.RFC3339),
					}, "", "  ")
					_ = os.WriteFile(metaFile, metaBytes, 0o644)

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
					out = "Artifact preview initialized. Write index.html (and any supporting CSS/JS files) in the workspace root directory ('.')."
				}
			}
			// After workspace mutating tools:
			// - loaded: first time index.html is written (transition from loading screen, or direct index.html write)
			// - reload: subsequent writes once preview is loaded (refresh the live preview)
			// - workspace_only: first workspace file/command action without an HTML preview, so the session records ArtifactTS
			if (n == "write_file" || n == "replace_in_file") && toolErr == nil && !strings.HasPrefix(out, "Error:") {
				writePath, _ := parseToolArgs(tc.Function.Arguments)["path"].(string)
				normalizedPath := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(writePath)), "./")
				previewURL := fmt.Sprintf("/api/artifacts/%s/", ts)
				if !artifactLoaded && normalizedPath == "index.html" {
					artifactLoaded = true
					artifactPreviewActive = true
					workspaceEventSent = true
					artName := ensureArtifactPreviewMetadata(artifactDir)
					send("artifact", map[string]any{
						"url":       previewURL,
						"name":      artName,
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
			if (n == "write_file" || n == "replace_in_file" || n == "read_file" || n == "list_dir" || n == "exec") && toolErr == nil && !artifactPreviewActive && !workspaceEventSent && ts != "" {
				workspaceEventSent = true
				send("artifact", map[string]any{
					"timestamp":      ts,
					"workspace_only": true,
				})
			}

			toolMsg := ollama.ChatMessage{
				Role:     "tool",
				ToolName: n,
				Content:  out,
			}
			if toolImageBase64 != "" {
				toolMsg.Images = []string{toolImageBase64}
			}
			if len(toolImages) > 0 {
				toolMsg.Images = toolImages
			}
			msgs = append(msgs, toolMsg)

			done := map[string]any{
				"phase":      "done",
				"name":       n,
				"ok":         toolErr == nil,
				"tool_index": tcIdx,
			}
			for _, field := range []string{"path", "command", "code", "artifact_name"} {
				if v, ok := startPayload[field]; ok && v != "" {
					done[field] = v
				}
			}
			if mcpSrv != "" {
				done["mcp_server"] = mcpSrv
			}
			if mcpTool != "" {
				done["mcp_tool"] = mcpTool
			}
			if toolImageBase64 != "" {
				done["image"] = "data:image/jpeg;base64," + toolImageBase64
			}
			if toolWorkflow != "" {
				done["workflow"] = toolWorkflow
			}
			applyComfyMediaToToolEvent(done, toolImages, toolMedia)
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
	case comfyToolName:
		return "\n\nCorrect usage of '" + comfyToolName + "':\n" +
			"- Description: run a registered ComfyUI workflow and show the result in the chat.\n" +
			"- Required Arguments:\n" +
			"  * 'prompt': string, describing the image in plain language, including subject, style and lighting\n" +
			"- Optional Arguments: only the parameters the workflow exposes, for example " +
			"'negative_prompt', 'seed', 'steps', 'cfg', 'width', 'height', and 'workflow' to pick a different one.\n" +
			"Pass only what you want to change; anything you leave out keeps the workflow's current value.\n" +
			"If the tool reports an unknown parameter, call it again with only the parameters listed in its schema."
	default:
		if guide := skillToolUsageGuide(name); guide != "" {
			return guide
		}
		return ""
	}
}

type toolSentState struct {
	name         string
	path         string
	command      string
	code         string
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
// "sha256:" prefix) for Ollama models, or a sanitized model identifier for
// external models, or "" if modelName is empty.
func (s *Server) artifactModelDigest(ctx context.Context, modelName string) string {
	name := strings.TrimSpace(modelName)
	if name == "" {
		return ""
	}
	if s.ollama != nil {
		models, err := s.ollama.List(ctx)
		if err == nil {
			for _, m := range models {
				if m.Name == name || m.Model == name {
					if m.Digest != "" {
						return strings.TrimPrefix(m.Digest, "sha256:")
					}
				}
			}
			nameNorm := strings.ToLower(name)
			if !strings.Contains(nameNorm, ":") {
				nameNorm += ":latest"
			}
			for _, m := range models {
				mNorm := strings.ToLower(m.Name)
				if !strings.Contains(mNorm, ":") {
					mNorm += ":latest"
				}
				if mNorm == nameNorm || strings.ToLower(m.Model) == nameNorm {
					if m.Digest != "" {
						return strings.TrimPrefix(m.Digest, "sha256:")
					}
				}
			}
		}
	}
	return cleanModelName(name)
}

// artifactInfoForModel reports how many artifact projects belong to modelName
// and their total size on disk, using the model's unique digest as the key.
func (s *Server) artifactInfoForModel(ctx context.Context, modelName string) (count int, totalBytes int64) {
	digest := s.artifactModelDigest(ctx, modelName)
	if digest == "" {
		return 0, 0
	}
	entries, err := os.ReadDir(filepath.Join("artifacts", digest))
	if err != nil {
		return 0, 0
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		count++
		totalBytes += dirSize(filepath.Join("artifacts", digest, e.Name()))
	}
	return count, totalBytes
}

// deleteArtifactsForModel removes every artifact project created by modelName
// (folders under artifacts/<digest>/) and returns how many were deleted.
func (s *Server) deleteArtifactsForModel(ctx context.Context, modelName string) int {
	digest := s.artifactModelDigest(ctx, modelName)
	if digest == "" {
		return 0
	}
	root := filepath.Join("artifacts", digest)
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0
	}
	deleted := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, e.Name())); err == nil {
			deleted++
		}
	}
	os.Remove(root) // drop the now-empty digest folder
	return deleted
}

// dirSize sums the byte size of every regular file inside dir (recursively).
func dirSize(dir string) int64 {
	_, size := dirInfo(dir)
	return size
}

// dirInfo returns the number of regular files and the total byte size inside
// dir (recursively).
func dirInfo(dir string) (count int, size int64) {
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		count++
		if info, err := d.Info(); err == nil {
			size += info.Size()
		}
		return nil
	})
	return count, size
}

func cleanModelName(model string) string {
	s := strings.ReplaceAll(model, ":", "-")
	invalid := []string{"\\", "/", "*", "?", "\"", "<", ">", "|", " ", "\n", "\r", "\t"}
	for _, char := range invalid {
		s = strings.ReplaceAll(s, char, "-")
	}
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-._")
	if s == "" {
		s = "ext-model"
	}
	return s
}
