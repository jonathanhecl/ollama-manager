// Package agent provides the multi-turn agent test sandbox, tools, and session engine.
package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Sandbox is an isolated file system directory for a single agent-test session.
type Sandbox struct {
	Path string // absolute path to the sandbox root
}

// sandboxStore tracks all sandboxes so we can list / clean up.
type sandboxStore struct {
	mu        sync.RWMutex
	baseDir   string
	sandboxes map[string]*Sandbox // key = sessionID
}

// NewSandboxStore creates a manager rooted at baseDir.
func NewSandboxStore(baseDir string) *sandboxStore {
	_ = os.MkdirAll(baseDir, 0o700)
	return &sandboxStore{
		baseDir:   baseDir,
		sandboxes: make(map[string]*Sandbox),
	}
}

// Create makes a fresh sandbox directory for the given session and model.
func (ss *sandboxStore) Create(sessionID, modelID string) (*Sandbox, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	safeModel := safeDirName(modelID)
	dir := filepath.Join(ss.baseDir, safeModel, sessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create sandbox: %w", err)
	}

	sb := &Sandbox{Path: dir}
	ss.sandboxes[sessionID] = sb
	return sb, nil
}

// Get returns the sandbox for a session, or nil.
func (ss *sandboxStore) Get(sessionID string) *Sandbox {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	return ss.sandboxes[sessionID]
}

// Destroy removes a sandbox directory and forgets it.
func (ss *sandboxStore) Destroy(sessionID string) error {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if sb, ok := ss.sandboxes[sessionID]; ok {
		_ = os.RemoveAll(sb.Path)
		delete(ss.sandboxes, sessionID)
	}
	return nil
}

// Reset empties the sandbox then recreates initial files.
func (ss *sandboxStore) Reset(sessionID string, initialFiles []InitialFile) error {
	ss.mu.Lock()
	sb, ok := ss.sandboxes[sessionID]
	ss.mu.Unlock()
	if !ok {
		return fmt.Errorf("sandbox not found for session %s", sessionID)
	}

	// Remove all contents but keep the directory itself.
	entries, err := os.ReadDir(sb.Path)
	if err != nil {
		return err
	}
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(sb.Path, e.Name()))
	}

	return ss.writeFiles(sb.Path, initialFiles)
}

// writeFiles creates files inside a sandbox directory.
func (ss *sandboxStore) writeFiles(dir string, files []InitialFile) error {
	for _, f := range files {
		path := filepath.Join(dir, f.Path)
		// Prevent escaping the sandbox.
		if !strings.HasPrefix(filepath.Clean(path), filepath.Clean(dir)+string(os.PathSeparator)) && filepath.Clean(path) != filepath.Clean(dir) {
			return fmt.Errorf("path escapes sandbox: %s", f.Path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(f.Content), 0o600); err != nil {
			return err
		}
	}
	return nil
}

// ListFiles returns the file tree inside the sandbox.
func (sb *Sandbox) ListFiles() ([]FileNode, error) {
	return listDirRecursive(sb.Path, sb.Path)
}

// ReadFile returns the content of a file inside the sandbox.
func (sb *Sandbox) ReadFile(relPath string) (string, error) {
	target := filepath.Join(sb.Path, relPath)
	if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(sb.Path)+string(os.PathSeparator)) && filepath.Clean(target) != filepath.Clean(sb.Path) {
		return "", fmt.Errorf("path escapes sandbox")
	}
	b, err := os.ReadFile(target)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// WriteFile writes content to a file inside the sandbox.
func (sb *Sandbox) WriteFile(relPath string, content string) error {
	target := filepath.Join(sb.Path, relPath)
	if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(sb.Path)+string(os.PathSeparator)) && filepath.Clean(target) != filepath.Clean(sb.Path) {
		return fmt.Errorf("path escapes sandbox")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	return os.WriteFile(target, []byte(content), 0o600)
}

// Exec runs a command inside the sandbox directory. Restricted to simple commands.
func (sb *Sandbox) Exec(command string) (stdout, stderr string, exitCode int, err error) {
	// For now, return a stub — real exec requires os/exec with timeouts.
	return "", "exec not yet implemented", 1, nil
}

// FileNode represents a file or directory in the sandbox tree.
type FileNode struct {
	Name     string     `json:"name"`
	Path     string     `json:"path"`
	IsDir    bool       `json:"is_dir"`
	Children []FileNode `json:"children,omitempty"`
}

// InitialFile seeds a sandbox with files at session start.
type InitialFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func listDirRecursive(root, prefix string) ([]FileNode, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var nodes []FileNode
	for _, e := range entries {
		rel, _ := filepath.Rel(prefix, filepath.Join(root, e.Name()))
		node := FileNode{
			Name:  e.Name(),
			Path:  rel,
			IsDir: e.IsDir(),
		}
		if e.IsDir() {
			children, err := listDirRecursive(filepath.Join(root, e.Name()), prefix)
			if err != nil {
				return nil, err
			}
			node.Children = children
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

func safeDirName(name string) string {
	// Replace filesystem-unfriendly characters.
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
	)
	s := replacer.Replace(name)
	if s == "" {
		s = "unknown"
	}
	return s
}
