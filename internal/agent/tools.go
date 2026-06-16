package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ToolDefinition describes a tool available to the agent.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"` // JSON schema
}

// ToolCall is a request from the model to invoke a tool.
type ToolCall struct {
	Name   string          `json:"name"`
	ID     string          `json:"id,omitempty"`
	Params json.RawMessage `json:"params"`
}

// ToolResult is the outcome of a tool invocation.
type ToolResult struct {
	CallID  string `json:"call_id"`
	Success bool   `json:"success"`
	Output  string `json:"output"`
	Error   string `json:"error,omitempty"`
}

// Registry holds all available tools.
type Registry struct {
	tools map[string]func(sb *Sandbox, params json.RawMessage) ToolResult
}

// DefaultRegistry returns a registry with the built-in filesystem tools.
func DefaultRegistry() *Registry {
	r := &Registry{tools: make(map[string]func(sb *Sandbox, params json.RawMessage) ToolResult)}
	r.tools["read_file"] = toolReadFile
	r.tools["write_file"] = toolWriteFile
	r.tools["list_dir"] = toolListDir
	r.tools["exec"] = toolExec
	return r
}

// Run executes a tool call against a sandbox.
func (r *Registry) Run(sb *Sandbox, call ToolCall) ToolResult {
	fn, ok := r.tools[call.Name]
	if !ok {
		return ToolResult{CallID: call.ID, Success: false, Error: fmt.Sprintf("unknown tool: %s", call.Name)}
	}
	return fn(sb, call.Params)
}

// Definitions returns the JSON-schema definitions for all tools in the registry.
func (r *Registry) Definitions() []ToolDefinition {
	defs := []ToolDefinition{
		{
			Name:        "read_file",
			Description: "Read the contents of a file inside the sandbox.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative path inside the sandbox"}},"required":["path"]}`),
		},
		{
			Name:        "write_file",
			Description: "Create or overwrite a file inside the sandbox.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative path inside the sandbox"},"content":{"type":"string","description":"Full file content"}},"required":["path","content"]}`),
		},
		{
			Name:        "list_dir",
			Description: "List files and directories inside the sandbox.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative path inside the sandbox (default '.')"}}}`),
		},
		{
			Name:        "exec",
			Description: "Execute a shell command inside the sandbox directory.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","description":"Shell command to run"}},"required":["command"]}`),
		},
	}
	return defs
}

func toolReadFile(sb *Sandbox, params json.RawMessage) ToolResult {
	var args struct{ Path string `json:"path"` }
	if err := json.Unmarshal(params, &args); err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	content, err := sb.ReadFile(args.Path)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	return ToolResult{Success: true, Output: content}
}

func toolWriteFile(sb *Sandbox, params json.RawMessage) ToolResult {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	if err := sb.WriteFile(args.Path, args.Content); err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	return ToolResult{Success: true, Output: fmt.Sprintf("wrote %s", args.Path)}
}

func toolListDir(sb *Sandbox, params json.RawMessage) ToolResult {
	var args struct{ Path string `json:"path"` }
	_ = json.Unmarshal(params, &args) // optional, default "."
	if args.Path == "" {
		args.Path = "."
	}

	target := filepath.Join(sb.Path, args.Path)
	if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(sb.Path)+string(os.PathSeparator)) && filepath.Clean(target) != filepath.Clean(sb.Path) {
		return ToolResult{Success: false, Error: "path escapes sandbox"}
	}

	entries, err := os.ReadDir(target)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	var lines []string
	for _, e := range entries {
		kind := "file"
		if e.IsDir() {
			kind = "dir"
		}
		lines = append(lines, fmt.Sprintf("%s %s", kind, e.Name()))
	}
	return ToolResult{Success: true, Output: strings.Join(lines, "\n")}
}

func toolExec(sb *Sandbox, params json.RawMessage) ToolResult {
	var args struct{ Command string `json:"command"` }
	if err := json.Unmarshal(params, &args); err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	stdout, stderr, code, err := sb.Exec(args.Command)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	output := stdout
	if stderr != "" {
		output += "\n[stderr]\n" + stderr
	}
	output += fmt.Sprintf("\n[exit:%d]", code)
	return ToolResult{Success: code == 0, Output: output}
}
