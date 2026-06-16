// Package tests implements a JSON-backed store for test templates and test groups.
//
// It is meant to be used by the HTTP layer directly (no goroutine worker).
// All mutations are protected by a mutex and persisted atomically.
package tests

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"
)

// Group is a collection of related tests.
type Group struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	RequiredCaps []string `json:"required_caps,omitempty"`
	Order        int      `json:"order"`
}

// Test is a single evaluation prompt template.
type Test struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Description      string          `json:"description,omitempty"`
	GroupID          string          `json:"group_id"`
	Active           bool            `json:"active"`
	Order            int             `json:"order"`
	Prompt           string          `json:"prompt"`
	SystemPrompt     string          `json:"system_prompt,omitempty"`
	EvaluationType   string          `json:"evaluation_type"`
	EvaluationConfig json.RawMessage `json:"evaluation_config,omitempty"`
	RequiredCaps     []string        `json:"required_caps,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

// persistFile is the on-disk format for tests.json.
type persistFile struct {
	Groups []Group `json:"groups"`
	Tests  []Test  `json:"tests"`
}

// Store holds groups and tests in memory and persists them to disk.
type Store struct {
	mu     sync.Mutex
	groups map[string]*Group
	tests  map[string]*Test
	path   string
}

// New creates an empty store backed by path.
func New(path string) *Store {
	return &Store{
		groups: make(map[string]*Group),
		tests:  make(map[string]*Test),
		path:   path,
	}
}

// Load reads tests.json from disk. If it does not exist, the store stays empty.
func (s *Store) Load() error {
	if s.path == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", s.path, err)
	}

	var pf persistFile
	if err := json.Unmarshal(data, &pf); err != nil {
		return fmt.Errorf("parse %s: %w", s.path, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.groups = make(map[string]*Group)
	s.tests = make(map[string]*Test)
	for i := range pf.Groups {
		g := pf.Groups[i]
		if g.ID == "" {
			continue
		}
		gg := g
		s.groups[g.ID] = &gg
	}
	for i := range pf.Tests {
		t := pf.Tests[i]
		if t.ID == "" {
			continue
		}
		tt := t
		s.tests[t.ID] = &tt
	}
	return nil
}

// List returns all groups and tests, each slice sorted by Order then Name.
func (s *Store) List() ([]Group, []Test) {
	s.mu.Lock()
	defer s.mu.Unlock()

	gs := make([]Group, 0, len(s.groups))
	for _, g := range s.groups {
		gs = append(gs, *g)
	}
	sort.Slice(gs, func(i, j int) bool {
		if gs[i].Order != gs[j].Order {
			return gs[i].Order < gs[j].Order
		}
		return gs[i].Name < gs[j].Name
	})

	ts := make([]Test, 0, len(s.tests))
	for _, t := range s.tests {
		ts = append(ts, *t)
	}
	sort.Slice(ts, func(i, j int) bool {
		if ts[i].GroupID != ts[j].GroupID {
			return ts[i].GroupID < ts[j].GroupID
		}
		if ts[i].Order != ts[j].Order {
			return ts[i].Order < ts[j].Order
		}
		return ts[i].Name < ts[j].Name
	})

	return gs, ts
}

// GetTest returns a test by id.
func (s *Store) GetTest(id string) (Test, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tests[id]
	if !ok || t == nil {
		return Test{}, false
	}
	cp := *t
	return cp, true
}

// CreateTest adds a new test and persists.
func (s *Store) CreateTest(in Test) (Test, error) {
	if in.Name == "" {
		return Test{}, errors.New("test name is required")
	}
	if in.Prompt == "" {
		return Test{}, errors.New("test prompt is required")
	}
	if in.EvaluationType == "" {
		return Test{}, errors.New("evaluation_type is required")
	}
	id, err := newID()
	if err != nil {
		return Test{}, err
	}
	now := time.Now().UTC()
	t := Test{
		ID:               id,
		Name:             in.Name,
		Description:      in.Description,
		GroupID:          in.GroupID,
		Active:           in.Active,
		Order:            in.Order,
		Prompt:           in.Prompt,
		SystemPrompt:     in.SystemPrompt,
		EvaluationType:   in.EvaluationType,
		EvaluationConfig: in.EvaluationConfig,
		RequiredCaps:     in.RequiredCaps,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	s.mu.Lock()
	s.tests[id] = &t
	if err := s.saveLocked(); err != nil {
		s.mu.Unlock()
		return Test{}, err
	}
	s.mu.Unlock()
	return t, nil
}

// UpdateTest modifies an existing test and persists.
func (s *Store) UpdateTest(id string, in Test) (Test, error) {
	s.mu.Lock()
	t, ok := s.tests[id]
	if !ok || t == nil {
		s.mu.Unlock()
		return Test{}, errors.New("test not found")
	}
	if in.Name != "" {
		t.Name = in.Name
	}
	t.Description = in.Description
	t.GroupID = in.GroupID
	t.Active = in.Active
	t.Order = in.Order
	if in.Prompt != "" {
		t.Prompt = in.Prompt
	}
	t.SystemPrompt = in.SystemPrompt
	if in.EvaluationType != "" {
		t.EvaluationType = in.EvaluationType
	}
	t.EvaluationConfig = in.EvaluationConfig
	t.RequiredCaps = in.RequiredCaps
	t.UpdatedAt = time.Now().UTC()
	cp := *t
	if err := s.saveLocked(); err != nil {
		s.mu.Unlock()
		return Test{}, err
	}
	s.mu.Unlock()
	return cp, nil
}

// DeleteTest removes a test by id and persists.
func (s *Store) DeleteTest(id string) error {
	s.mu.Lock()
	_, ok := s.tests[id]
	if !ok {
		s.mu.Unlock()
		return errors.New("test not found")
	}
	delete(s.tests, id)
	if err := s.saveLocked(); err != nil {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	return nil
}

// ReorderTests bulk-updates the Order field for tests.
func (s *Store) ReorderTest(updates map[string]int) error {
	s.mu.Lock()
	for id, order := range updates {
		if t, ok := s.tests[id]; ok {
			t.Order = order
			t.UpdatedAt = time.Now().UTC()
		}
	}
	if err := s.saveLocked(); err != nil {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	return nil
}

// GetGroup returns a group by id.
func (s *Store) GetGroup(id string) (Group, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.groups[id]
	if !ok || g == nil {
		return Group{}, false
	}
	cp := *g
	return cp, true
}

// CreateGroup adds a new group and persists.
func (s *Store) CreateGroup(in Group) (Group, error) {
	if in.Name == "" {
		return Group{}, errors.New("group name is required")
	}
	id, err := newID()
	if err != nil {
		return Group{}, err
	}
	g := Group{
		ID:           id,
		Name:         in.Name,
		Description:  in.Description,
		RequiredCaps: in.RequiredCaps,
		Order:        in.Order,
	}
	s.mu.Lock()
	s.groups[id] = &g
	if err := s.saveLocked(); err != nil {
		s.mu.Unlock()
		return Group{}, err
	}
	s.mu.Unlock()
	return g, nil
}

// UpdateGroup modifies an existing group and persists.
func (s *Store) UpdateGroup(id string, in Group) (Group, error) {
	s.mu.Lock()
	g, ok := s.groups[id]
	if !ok || g == nil {
		s.mu.Unlock()
		return Group{}, errors.New("group not found")
	}
	if in.Name != "" {
		g.Name = in.Name
	}
	g.Description = in.Description
	g.RequiredCaps = in.RequiredCaps
	g.Order = in.Order
	cp := *g
	if err := s.saveLocked(); err != nil {
		s.mu.Unlock()
		return Group{}, err
	}
	s.mu.Unlock()
	return cp, nil
}

// DeleteGroup removes a group. Any tests belonging to it are reassigned
// to an empty group_id (unassigned).
func (s *Store) DeleteGroup(id string) error {
	s.mu.Lock()
	_, ok := s.groups[id]
	if !ok {
		s.mu.Unlock()
		return errors.New("group not found")
	}
	for _, t := range s.tests {
		if t.GroupID == id {
			t.GroupID = ""
			t.UpdatedAt = time.Now().UTC()
		}
	}
	delete(s.groups, id)
	if err := s.saveLocked(); err != nil {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	return nil
}

// saveLocked persists current state to disk. Must be called with s.mu held.
func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	pf := persistFile{
		Groups: make([]Group, 0, len(s.groups)),
		Tests:  make([]Test, 0, len(s.tests)),
	}
	for _, g := range s.groups {
		pf.Groups = append(pf.Groups, *g)
	}
	for _, t := range s.tests {
		pf.Tests = append(pf.Tests, *t)
	}
	data, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// newID returns a short random hex id.
func newID() (string, error) {
	buf := make([]byte, 10)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
