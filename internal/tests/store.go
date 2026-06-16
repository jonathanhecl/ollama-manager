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
	"path/filepath"
	"sort"
	"strings"
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

// Attachment is a file attached to a test (image or audio stored as base64).
type Attachment struct {
	ID   string `json:"id"`
	Kind string `json:"kind"` // "image" or "audio"
	Name string `json:"name"` // original filename
	Mime string `json:"mime"` // MIME type
	Data string `json:"data"` // base64 content
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
	Attachments      []Attachment    `json:"attachments,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

// persistFileV1 is the old on-disk format for tests.json.
type persistFileV1 struct {
	Groups []Group `json:"groups"`
	Tests  []Test  `json:"tests"`
}

// groupsFile is the on-disk format for tests.json (groups only).
type groupsFile struct {
	Groups []Group `json:"groups"`
}

// Store holds groups and tests in memory and persists them to disk.
// Groups live in tests.json; each group's tests live in tests-{groupID}.json.
type Store struct {
	mu         sync.Mutex
	groups     map[string]*Group
	tests      map[string]*Test
	groupsPath string // e.g. /data/tests.json
	dir        string // e.g. /data
}

// New creates an empty store backed by dir/tests.json.
func New(path string) *Store {
	return &Store{
		groups:     make(map[string]*Group),
		tests:      make(map[string]*Test),
		groupsPath: path,
		dir:        filepath.Dir(path),
	}
}

// Load reads groups from tests.json and tests from per-group files.
// If tests.json uses the old v1 format (contains "tests" key), it auto-migrates.
func (s *Store) Load() error {
	if s.groupsPath == "" {
		return nil
	}
	data, err := os.ReadFile(s.groupsPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", s.groupsPath, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.groups = make(map[string]*Group)
	s.tests = make(map[string]*Test)

	// Try old v1 format first (contains both groups and tests).
	var v1 persistFileV1
	if err := json.Unmarshal(data, &v1); err == nil && len(v1.Tests) > 0 {
		for i := range v1.Groups {
			g := v1.Groups[i]
			if g.ID == "" {
				continue
			}
			gg := g
			s.groups[g.ID] = &gg
		}
		for i := range v1.Tests {
			t := v1.Tests[i]
			if t.ID == "" {
				continue
			}
			tt := t
			s.tests[t.ID] = &tt
		}
		// Migrate: write per-group test files and rewrite groups-only file.
		if err := s.saveGroupsLocked(); err != nil {
			return fmt.Errorf("migrate groups file: %w", err)
		}
		for gid := range s.groups {
			if err := s.saveTestsLocked(gid); err != nil {
				return fmt.Errorf("migrate tests file %s: %w", gid, err)
			}
		}
		// Also save ungrouped tests if any.
		if err := s.saveTestsLocked(""); err != nil {
			return fmt.Errorf("migrate ungrouped tests: %w", err)
		}
		_ = os.Rename(s.groupsPath, s.groupsPath+".bak")
		return nil
	}

	// New format: groups-only file.
	var gf groupsFile
	if err := json.Unmarshal(data, &gf); err != nil {
		return fmt.Errorf("parse %s: %w", s.groupsPath, err)
	}
	for i := range gf.Groups {
		g := gf.Groups[i]
		if g.ID == "" {
			continue
		}
		gg := g
		s.groups[g.ID] = &gg
	}

	// Load per-group test files.
	entries, _ := os.ReadDir(s.dir)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "tests-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		// Extract group ID from tests-{id}.json
		gid := name[len("tests-") : len(name)-len(".json")]
		fpath := filepath.Join(s.dir, name)
		b, err := os.ReadFile(fpath)
		if err != nil {
			continue
		}
		var tlist []Test
		if err := json.Unmarshal(b, &tlist); err != nil {
			continue
		}
		for i := range tlist {
			t := tlist[i]
			if t.ID == "" {
				continue
			}
			// Ensure GroupID matches file name for consistency.
			if gid != "_" {
				t.GroupID = gid
			} else {
				t.GroupID = ""
			}
			tt := t
			s.tests[t.ID] = &tt
		}
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

// CreateTest adds a new test and persists to its group's file.
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
		Attachments:      in.Attachments,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	s.mu.Lock()
	s.tests[id] = &t
	if err := s.saveTestsLocked(in.GroupID); err != nil {
		s.mu.Unlock()
		return Test{}, err
	}
	s.mu.Unlock()
	return t, nil
}

// UpdateTest modifies an existing test and persists to the relevant group file(s).
func (s *Store) UpdateTest(id string, in Test) (Test, error) {
	s.mu.Lock()
	t, ok := s.tests[id]
	if !ok || t == nil {
		s.mu.Unlock()
		return Test{}, errors.New("test not found")
	}
	oldGroup := t.GroupID
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
	t.Attachments = in.Attachments
	t.UpdatedAt = time.Now().UTC()
	cp := *t

	// Save new group file.
	if err := s.saveTestsLocked(in.GroupID); err != nil {
		s.mu.Unlock()
		return Test{}, err
	}
	// If group changed, also save old group file (to remove the test from there).
	if oldGroup != in.GroupID {
		if err := s.saveTestsLocked(oldGroup); err != nil {
			s.mu.Unlock()
			return Test{}, err
		}
	}
	s.mu.Unlock()
	return cp, nil
}

// DeleteTest removes a test by id and persists its group's file.
func (s *Store) DeleteTest(id string) error {
	s.mu.Lock()
	t, ok := s.tests[id]
	if !ok {
		s.mu.Unlock()
		return errors.New("test not found")
	}
	groupID := t.GroupID
	delete(s.tests, id)
	if err := s.saveTestsLocked(groupID); err != nil {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	return nil
}

// ReorderTests bulk-updates the Order field for tests.
func (s *Store) ReorderTest(updates map[string]int) error {
	s.mu.Lock()
	affected := make(map[string]struct{})
	for id, order := range updates {
		if t, ok := s.tests[id]; ok {
			t.Order = order
			t.UpdatedAt = time.Now().UTC()
			affected[t.GroupID] = struct{}{}
		}
	}
	for gid := range affected {
		if err := s.saveTestsLocked(gid); err != nil {
			s.mu.Unlock()
			return err
		}
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

// CreateGroup adds a new group and persists groups file.
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
	if err := s.saveGroupsLocked(); err != nil {
		s.mu.Unlock()
		return Group{}, err
	}
	s.mu.Unlock()
	return g, nil
}

// UpdateGroup modifies an existing group and persists groups file.
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
	if err := s.saveGroupsLocked(); err != nil {
		s.mu.Unlock()
		return Group{}, err
	}
	s.mu.Unlock()
	return cp, nil
}

// DeleteGroup removes a group. Any tests belonging to it are reassigned
// to an empty group_id (unassigned) and moved to the ungrouped file.
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
	// Remove the group's test file.
	_ = os.Remove(s.testsFile(id))
	if err := s.saveGroupsLocked(); err != nil {
		s.mu.Unlock()
		return err
	}
	if err := s.saveTestsLocked(""); err != nil {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	return nil
}

// saveGroupsLocked persists only groups to tests.json.
// Must be called with s.mu held.
func (s *Store) saveGroupsLocked() error {
	if s.groupsPath == "" {
		return nil
	}
	gf := groupsFile{
		Groups: make([]Group, 0, len(s.groups)),
	}
	for _, g := range s.groups {
		gf.Groups = append(gf.Groups, *g)
	}
	data, err := json.MarshalIndent(gf, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := s.groupsPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.groupsPath)
}

// saveTestsLocked persists tests for a specific group to tests-{groupID}.json.
// Must be called with s.mu held.
func (s *Store) saveTestsLocked(groupID string) error {
	if s.dir == "" {
		return nil
	}
	path := s.testsFile(groupID)
	var list []Test
	for _, t := range s.tests {
		if t.GroupID == groupID {
			list = append(list, *t)
		}
	}
	// If no tests remain for this group, delete the file.
	if len(list) == 0 {
		_ = os.Remove(path)
		return nil
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// testsFile returns the on-disk path for a group's tests.
// Empty groupID maps to tests-_.json.
func (s *Store) testsFile(groupID string) string {
	if groupID == "" {
		groupID = "_"
	}
	return filepath.Join(s.dir, "tests-"+groupID+".json")
}

// newID returns a short random hex id.
func newID() (string, error) {
	buf := make([]byte, 10)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
