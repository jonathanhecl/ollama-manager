package agent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SessionStatus represents the state of an agent-test session.
type SessionStatus string

const (
	StatusCreated      SessionStatus = "created"
	StatusRunning      SessionStatus = "running"
	StatusWaitingHuman SessionStatus = "waiting_human"
	StatusFinished     SessionStatus = "finished"
	StatusError        SessionStatus = "error"
)

// Role represents who sent a turn.
type Role string

const (
	RoleSystem Role = "system"
	RoleAgent  Role = "agent"
	RoleUser   Role = "user"
	RoleTool   Role = "tool"
)

// Turn is a single step in the conversation.
type Turn struct {
	Number      int           `json:"number"`
	Role        Role          `json:"role"`
	Content     string        `json:"content"`
	ToolCalls   []ToolCall    `json:"tool_calls,omitempty"`
	ToolResults []ToolResult  `json:"tool_results,omitempty"`
	CreatedAt   time.Time     `json:"created_at"`
}

// Session holds the full state of one agent-test run.
type Session struct {
	ID           string        `json:"id"`
	ModelID      string        `json:"model_id"`
	TestID       string        `json:"test_id"`
	Status       SessionStatus `json:"status"`
	MaxTurns     int           `json:"max_turns"`
	CurrentTurn  int           `json:"current_turn"`
	Turns        []Turn        `json:"turns"`
	SandboxPath  string        `json:"sandbox_path"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

// AgentConfig is the evaluation_config shape for agent tests.
type AgentConfig struct {
	MaxTurns     int            `json:"max_turns"`
	InitialFiles []InitialFile  `json:"initial_files,omitempty"`
	Tools        []string       `json:"tools,omitempty"`
	HumanReview  bool           `json:"human_review"`
}

// DefaultAgentConfig returns a sensible default configuration.
func DefaultAgentConfig() AgentConfig {
	return AgentConfig{
		MaxTurns:    10,
		Tools:       []string{"read_file", "write_file", "list_dir", "exec"},
		HumanReview: true,
	}
}

// SessionStore keeps sessions in memory and persists them to disk.
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	path     string // sessions.json
	sandboxes *sandboxStore
}

// NewSessionStore creates a store backed by a JSON file next to the config dir.
func NewSessionStore(dataDir string) *SessionStore {
	path := filepath.Join(dataDir, "agent_sessions.json")
	ss := &SessionStore{
		sessions:  make(map[string]*Session),
		path:      path,
		sandboxes: NewSandboxStore(filepath.Join(dataDir, "sandboxes")),
	}
	_ = ss.Load()
	return ss
}

// Create starts a new session: creates the sandbox, seeds initial files, persists.
func (ss *SessionStore) Create(modelID, testID string, cfg AgentConfig) (*Session, error) {
	id, err := newSessionID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()

	sb, err := ss.sandboxes.Create(id, modelID)
	if err != nil {
		return nil, err
	}
	if err := ss.sandboxes.writeFiles(sb.Path, cfg.InitialFiles); err != nil {
		return nil, fmt.Errorf("seed sandbox: %w", err)
	}

	sess := &Session{
		ID:          id,
		ModelID:     modelID,
		TestID:      testID,
		Status:      StatusCreated,
		MaxTurns:    cfg.MaxTurns,
		CurrentTurn: 0,
		Turns:       []Turn{},
		SandboxPath: sb.Path,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	ss.mu.Lock()
	ss.sessions[id] = sess
	err = ss.saveLocked()
	ss.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return sess, nil
}

// Get returns a session by ID.
func (ss *SessionStore) Get(id string) (Session, bool) {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	s, ok := ss.sessions[id]
	if !ok {
		return Session{}, false
	}
	return *s, true
}

// List returns all sessions, newest first.
func (ss *SessionStore) List() []Session {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	out := make([]Session, 0, len(ss.sessions))
	for _, s := range ss.sessions {
		out = append(out, *s)
	}
	// sort by created_at desc
	for i := 0; i < len(out)-1; i++ {
		for j := i + 1; j < len(out); j++ {
			if out[i].CreatedAt.Before(out[j].CreatedAt) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// AddTurn appends a turn and persists.
func (ss *SessionStore) AddTurn(id string, turn Turn) error {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s, ok := ss.sessions[id]
	if !ok {
		return errors.New("session not found")
	}
	s.Turns = append(s.Turns, turn)
	s.CurrentTurn = len(s.Turns)
	s.UpdatedAt = time.Now().UTC()
	return ss.saveLocked()
}

// UpdateStatus changes the session status.
func (ss *SessionStore) UpdateStatus(id string, status SessionStatus) error {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s, ok := ss.sessions[id]
	if !ok {
		return errors.New("session not found")
	}
	s.Status = status
	s.UpdatedAt = time.Now().UTC()
	return ss.saveLocked()
}

// Reset restores the sandbox to initial files and clears turns.
func (ss *SessionStore) Reset(id string, cfg AgentConfig) error {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s, ok := ss.sessions[id]
	if !ok {
		return errors.New("session not found")
	}
	if err := ss.sandboxes.Reset(id, cfg.InitialFiles); err != nil {
		return err
	}
	s.Turns = nil
	s.CurrentTurn = 0
	s.Status = StatusCreated
	s.UpdatedAt = time.Now().UTC()
	return ss.saveLocked()
}

// Destroy deletes a session and its sandbox.
func (ss *SessionStore) Destroy(id string) error {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if _, ok := ss.sessions[id]; !ok {
		return errors.New("session not found")
	}
	delete(ss.sessions, id)
	_ = ss.sandboxes.Destroy(id)
	return ss.saveLocked()
}

// Sandbox returns the sandbox for a session.
func (ss *SessionStore) Sandbox(id string) *Sandbox {
	return ss.sandboxes.Get(id)
}

// Load reads sessions.json from disk.
func (ss *SessionStore) Load() error {
	data, err := os.ReadFile(ss.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var sessions []Session
	if err := json.Unmarshal(data, &sessions); err != nil {
		return err
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	for i := range sessions {
		s := sessions[i]
		ss.sessions[s.ID] = &s
	}
	return nil
}

// saveLocked persists all sessions atomically. Must hold ss.mu.
func (ss *SessionStore) saveLocked() error {
	if ss.path == "" {
		return nil
	}
	list := make([]Session, 0, len(ss.sessions))
	for _, s := range ss.sessions {
		list = append(list, *s)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := ss.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, ss.path)
}

func newSessionID() (string, error) {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
