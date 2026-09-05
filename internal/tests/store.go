// Package tests implements a filesystem-backed store for YAML/JSON test scripts and test categories.
//
// Categories are represented as subdirectories inside the testing root folder.
// Tests are individual .yaml/.yml or .json script files within each category directory.
// All mutations are protected by a mutex and persisted directly to disk.
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

	"gopkg.in/yaml.v3"
)

// Group is a collection of related tests (corresponds to a category directory).
type Group struct {
	ID           string   `json:"id" yaml:"id"`
	Name         string   `json:"name" yaml:"name"`
	Description  string   `json:"description,omitempty" yaml:"description,omitempty"`
	RequiredCaps []string `json:"required_caps,omitempty" yaml:"required_caps,omitempty"`
	Order        int      `json:"order" yaml:"order"`
}

// Attachment is a file attached to a test (image, audio, or document).
type Attachment struct {
	ID   string `json:"id" yaml:"id"`
	Kind string `json:"kind" yaml:"kind"` // "image", "audio", "file"
	Name string `json:"name" yaml:"name"` // original filename
	Mime string `json:"mime" yaml:"mime"` // MIME type
	Data string `json:"data" yaml:"data"` // base64 content or relative file path
}

// Message represents a single chat turn in a multi-message test script.
type Message struct {
	Role      string   `json:"role" yaml:"role"`
	Content   string   `json:"content" yaml:"content"`
	Images    []string `json:"images,omitempty" yaml:"images,omitempty"`
	ToolCalls any      `json:"tool_calls,omitempty" yaml:"tool_calls,omitempty"`
}

// Evaluation specifies the evaluation strategy and parameters.
type Evaluation struct {
	Type     string          `json:"type" yaml:"type"`
	Expected any             `json:"expected,omitempty" yaml:"expected,omitempty"`
	Pattern  string          `json:"pattern,omitempty" yaml:"pattern,omitempty"`
	Schema   any             `json:"schema,omitempty" yaml:"schema,omitempty"`
	Config   json.RawMessage `json:"config,omitempty" yaml:"config,omitempty"`
}

// Step represents one turn in a sequential multi-step interactive test.
// SystemPrompt is sticky within the chain: a non-empty value replaces the
// active system from that step onward; empty keeps the active one.
// Options folds the same way field by field over the active options.
type Step struct {
	Step         int          `json:"step" yaml:"step"`
	Name         string       `json:"name,omitempty" yaml:"name,omitempty"`
	Prompt       string       `json:"prompt" yaml:"prompt"`
	Evaluation   *Evaluation  `json:"evaluation,omitempty" yaml:"evaluation,omitempty"`
	SystemPrompt string       `json:"system_prompt,omitempty" yaml:"system_prompt,omitempty"`
	Options      *TestOptions `json:"options,omitempty" yaml:"options,omitempty"`
}

// CaseStep is a single chained turn inside a multi-turn test case.
// Steps share one conversation history scoped to their parent case:
// each user prompt is sent in order, keeping prior turns in context,
// so instruction-following across turns can be evaluated.
//
// SystemPrompt is sticky within the chain: a non-empty value replaces the
// active system from that step onward; an empty value keeps whatever system
// is currently active (the case-level system, or the test-level one).
// Options behaves the same way field by field: set fields replace the active
// ones from that step onward, nil fields keep the active values.
type CaseStep struct {
	Name         string       `json:"name,omitempty" yaml:"name,omitempty"`
	Prompt       string       `json:"prompt" yaml:"prompt"`
	Evaluation   *Evaluation  `json:"evaluation,omitempty" yaml:"evaluation,omitempty"`
	SystemPrompt string       `json:"system_prompt,omitempty" yaml:"system_prompt,omitempty"`
	Options      *TestOptions `json:"options,omitempty" yaml:"options,omitempty"`
}

// TestCase represents an individual case in a batch/matrix test suite.
// Each case runs in isolation (fresh conversation history).
type TestCase struct {
	Name         string       `json:"name,omitempty" yaml:"name,omitempty"`
	Prompt       string       `json:"prompt,omitempty" yaml:"prompt,omitempty"`
	Evaluation   *Evaluation  `json:"evaluation,omitempty" yaml:"evaluation,omitempty"`
	SystemPrompt string       `json:"system_prompt,omitempty" yaml:"system_prompt,omitempty"`
	Options      *TestOptions `json:"options,omitempty" yaml:"options,omitempty"`
	Steps        []CaseStep   `json:"steps,omitempty" yaml:"steps,omitempty"`
}

// TestOptions represents optional inference parameters.
type TestOptions struct {
	Temperature *float64 `json:"temperature,omitempty" yaml:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty" yaml:"top_p,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty" yaml:"max_tokens,omitempty"`
}

// MergeOptions returns the effective inference options for a case or step:
// fields set in override win, nil fields fall back to base.
// A nil override returns base unchanged.
func MergeOptions(base, override *TestOptions) *TestOptions {
	if override == nil {
		return base
	}
	if base == nil {
		return override
	}
	out := *base
	if override.Temperature != nil {
		out.Temperature = override.Temperature
	}
	if override.TopP != nil {
		out.TopP = override.TopP
	}
	if override.MaxTokens != nil {
		out.MaxTokens = override.MaxTokens
	}
	return &out
}

// EffectiveSystemPrompt resolves which system prompt applies to a case or step:
// a non-empty override wins, otherwise the parent (test-level) prompt is used.
func EffectiveSystemPrompt(parent, override string) string {
	if override != "" {
		return override
	}
	return parent
}

// Test is an individual evaluation test script.
type Test struct {
	ID               string          `json:"id" yaml:"id"`
	Name             string          `json:"name" yaml:"name"`
	Description      string          `json:"description,omitempty" yaml:"description,omitempty"`
	GroupID          string          `json:"group_id" yaml:"group_id"`
	Active           bool            `json:"active" yaml:"active"`
	Order            int             `json:"order" yaml:"order"`
	SystemPrompt     string          `json:"system_prompt,omitempty" yaml:"system_prompt,omitempty"`
	Prompt           string          `json:"prompt,omitempty" yaml:"prompt,omitempty"`
	Messages         []Message       `json:"messages,omitempty" yaml:"messages,omitempty"`
	Steps            []Step          `json:"steps,omitempty" yaml:"steps,omitempty"`
	Cases            []TestCase      `json:"cases,omitempty" yaml:"cases,omitempty"`
	Evaluation       *Evaluation     `json:"evaluation,omitempty" yaml:"evaluation,omitempty"`
	EvaluationType   string          `json:"evaluation_type,omitempty" yaml:"evaluation_type,omitempty"`
	EvaluationConfig json.RawMessage `json:"evaluation_config,omitempty" yaml:"evaluation_config,omitempty"`
	RequiredCaps     []string        `json:"required_caps,omitempty" yaml:"required_caps,omitempty"`
	Attachments      []Attachment    `json:"attachments,omitempty" yaml:"attachments,omitempty"`
	Options          *TestOptions    `json:"options,omitempty" yaml:"options,omitempty"`
	Filename         string          `json:"filename,omitempty" yaml:"filename,omitempty"`
	CreatedAt        time.Time       `json:"created_at" yaml:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at" yaml:"updated_at"`
}

// Store holds groups and tests in memory and syncs them to the filesystem directory.
type Store struct {
	mu     sync.Mutex
	groups map[string]*Group
	tests  map[string]*Test
	dir    string // e.g. /path/to/testing
}

// New creates an empty store backed by the given testing directory.
func New(pathOrDir string) *Store {
	dir := pathOrDir
	if strings.HasSuffix(strings.ToLower(dir), ".json") || strings.HasSuffix(strings.ToLower(dir), ".yaml") || strings.HasSuffix(strings.ToLower(dir), ".yml") {
		dir = filepath.Dir(dir)
		if filepath.Base(dir) != "testing" {
			dir = filepath.Join(dir, "testing")
		}
	}
	return &Store{
		groups: make(map[string]*Group),
		tests:  make(map[string]*Test),
		dir:    dir,
	}
}

// Dir returns the root testing directory path.
func (s *Store) Dir() string {
	return s.dir
}

// Load scans the testing directory, discovering categories and test script files.
func (s *Store) Load() error {
	if s.dir == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Ensure testing directory exists
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("create testing dir %s: %w", s.dir, err)
	}

	// Run backup / migration for legacy root tests files if any exist
	s.migrateLegacyFilesLocked()

	s.groups = make(map[string]*Group)
	s.tests = make(map[string]*Test)

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("read testing dir: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirName := entry.Name()
		// Ignore hidden directories (.backup, .history, etc.) and private prefixes (_*)
		if strings.HasPrefix(dirName, ".") || strings.HasPrefix(dirName, "_") {
			continue
		}

		catPath := filepath.Join(s.dir, dirName)
		group := s.loadCategoryLocked(dirName, catPath)
		s.groups[group.ID] = group

		// Load test files in category directory
		catEntries, err := os.ReadDir(catPath)
		if err != nil {
			continue
		}

		for _, testEntry := range catEntries {
			if testEntry.IsDir() {
				continue
			}
			tName := testEntry.Name()
			lower := strings.ToLower(tName)
			if strings.HasPrefix(tName, ".") || strings.HasPrefix(tName, "_") ||
				strings.EqualFold(tName, "history.json") ||
				strings.HasSuffix(lower, "._history.json") ||
				strings.HasSuffix(lower, ".history.json") ||
				strings.HasSuffix(lower, "_history.json") {
				continue
			}

			isYaml := strings.HasSuffix(strings.ToLower(tName), ".yaml") || strings.HasSuffix(strings.ToLower(tName), ".yml")
			isJson := strings.HasSuffix(strings.ToLower(tName), ".json")
			if !isYaml && !isJson {
				continue
			}

			filePath := filepath.Join(catPath, tName)
			data, err := os.ReadFile(filePath)
			if err != nil {
				continue
			}

			var t Test
			if isYaml {
				if err := yaml.Unmarshal(data, &t); err != nil {
					continue
				}
			} else {
				if err := json.Unmarshal(data, &t); err != nil {
					continue
				}
			}

			if t.Name == "" && t.Prompt == "" && len(t.Cases) == 0 && len(t.Steps) == 0 && len(t.Messages) == 0 {
				continue
			}

			if t.ID == "" {
				t.ID = strings.TrimSuffix(tName, filepath.Ext(tName))
			}
			t.GroupID = group.ID
			t.Filename = tName

			// Normalize Evaluation
			t.normalizeEvaluation()

			// Sync messages vs prompt
			if len(t.Messages) > 0 && t.Prompt == "" {
				for i := len(t.Messages) - 1; i >= 0; i-- {
					if t.Messages[i].Role == "user" {
						t.Prompt = t.Messages[i].Content
						break
					}
				}
			}

			tt := t
			s.tests[t.ID] = &tt
		}
	}

	return nil
}

func (t *Test) normalizeEvaluation() {
	if t.Evaluation != nil {
		if t.EvaluationType == "" {
			t.EvaluationType = t.Evaluation.Type
		}
		if len(t.EvaluationConfig) == 0 && t.Evaluation.Config != nil {
			t.EvaluationConfig = t.Evaluation.Config
		} else if len(t.EvaluationConfig) == 0 {
			cfgMap := make(map[string]any)
			if t.Evaluation.Expected != nil {
				cfgMap["expected"] = t.Evaluation.Expected
			}
			if t.Evaluation.Pattern != "" {
				cfgMap["pattern"] = t.Evaluation.Pattern
			}
			if t.Evaluation.Schema != nil {
				cfgMap["schema"] = t.Evaluation.Schema
			}
			if len(cfgMap) > 0 {
				t.EvaluationConfig, _ = json.Marshal(cfgMap)
			}
		}
	} else if t.EvaluationType != "" {
		t.Evaluation = &Evaluation{
			Type:   t.EvaluationType,
			Config: t.EvaluationConfig,
		}
	}
}

func (s *Store) loadCategoryLocked(dirName, catPath string) *Group {
	// Try _category.yaml first, then _category.json
	for _, fn := range []string{"_category.yaml", "_category.yml", "_category.json"} {
		catMetaPath := filepath.Join(catPath, fn)
		if data, err := os.ReadFile(catMetaPath); err == nil {
			var g Group
			var parseErr error
			if strings.HasSuffix(fn, ".json") {
				parseErr = json.Unmarshal(data, &g)
			} else {
				parseErr = yaml.Unmarshal(data, &g)
			}
			if parseErr == nil {
				if g.ID == "" {
					g.ID = dirName
				}
				if g.Name == "" {
					g.Name = humanizeName(dirName)
				}
				return &g
			}
		}
	}

	return &Group{
		ID:          dirName,
		Name:        humanizeName(dirName),
		Description: "",
		Order:       len(s.groups),
	}
}

// List returns all groups and tests, sorted by Order then Name.
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

// CreateTest adds a new test and writes its individual .yaml file.
func (s *Store) CreateTest(in Test) (Test, error) {
	if in.Name == "" {
		return Test{}, errors.New("test name is required")
	}
	if in.Prompt == "" && len(in.Messages) == 0 && len(in.Steps) == 0 && len(in.Cases) == 0 {
		return Test{}, errors.New("test prompt, messages, steps, or cases are required")
	}
	for i, c := range in.Cases {
		if c.Prompt == "" && len(c.Steps) == 0 {
			return Test{}, fmt.Errorf("case %d (%s) needs a prompt or steps", i+1, c.Name)
		}
		for j, s := range c.Steps {
			if s.Prompt == "" {
				return Test{}, fmt.Errorf("case %d (%s) step %d (%s) needs a prompt", i+1, c.Name, j+1, s.Name)
			}
		}
	}

	evalType := in.EvaluationType
	if evalType == "" && in.Evaluation != nil {
		evalType = in.Evaluation.Type
	}
	if evalType == "" && len(in.Steps) == 0 && len(in.Cases) == 0 {
		evalType = "contains"
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	groupID := sanitizeDirname(in.GroupID)
	if groupID == "" {
		groupID = "default"
	}
	if _, ok := s.groups[groupID]; !ok {
		s.groups[groupID] = &Group{
			ID:    groupID,
			Name:  humanizeName(groupID),
			Order: len(s.groups),
		}
		_ = s.saveCategoryLocked(s.groups[groupID])
	}

	id := in.ID
	if id == "" {
		var err error
		id, err = newID()
		if err != nil {
			return Test{}, err
		}
	}

	now := time.Now().UTC()
	t := in
	t.ID = id
	t.GroupID = groupID
	t.EvaluationType = evalType
	if t.Evaluation == nil && evalType != "" {
		t.Evaluation = &Evaluation{
			Type:   evalType,
			Config: in.EvaluationConfig,
		}
	}
	t.CreatedAt = now
	t.UpdatedAt = now

	// Determine unique filename (.yaml by default)
	baseFilename := sanitizeFilename(t.Name)
	filename := baseFilename + ".yaml"
	targetDir := filepath.Join(s.dir, groupID)
	_ = os.MkdirAll(targetDir, 0o755)

	count := 1
	for {
		targetPath := filepath.Join(targetDir, filename)
		if _, err := os.Stat(targetPath); errors.Is(err, os.ErrNotExist) {
			break
		}
		filename = fmt.Sprintf("%s_%d.yaml", baseFilename, count)
		count++
	}
	t.Filename = filename

	if err := s.saveTestLocked(&t); err != nil {
		return Test{}, err
	}

	s.tests[id] = &t
	return t, nil
}

// UpdateTest modifies an existing test and rewrites its file.
func (s *Store) UpdateTest(id string, in Test) (Test, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.tests[id]
	if !ok || t == nil {
		return Test{}, errors.New("test not found")
	}

	oldGroup := t.GroupID
	oldFilename := t.Filename
	newGroup := sanitizeDirname(in.GroupID)
	if newGroup == "" {
		newGroup = oldGroup
	}

	if in.Name != "" {
		t.Name = in.Name
	}
	t.Description = in.Description
	t.GroupID = newGroup
	t.Active = in.Active
	t.Order = in.Order
	t.Prompt = in.Prompt
	t.SystemPrompt = in.SystemPrompt
	if in.Messages != nil {
		t.Messages = in.Messages
	}
	if in.Steps != nil {
		t.Steps = in.Steps
	}
	if in.Cases != nil {
		t.Cases = in.Cases
	}
	if in.EvaluationType != "" {
		t.EvaluationType = in.EvaluationType
	}
	if in.Evaluation != nil {
		t.Evaluation = in.Evaluation
		t.EvaluationType = in.Evaluation.Type
		t.EvaluationConfig = in.Evaluation.Config
	} else if in.EvaluationType != "" {
		t.Evaluation = &Evaluation{
			Type:   in.EvaluationType,
			Config: in.EvaluationConfig,
		}
	}
	t.EvaluationConfig = in.EvaluationConfig
	t.RequiredCaps = in.RequiredCaps
	t.Attachments = in.Attachments
	t.Options = in.Options
	t.UpdatedAt = time.Now().UTC()

	// If group changed or filename missing, handle file movement
	if oldGroup != newGroup || t.Filename == "" {
		oldPath := filepath.Join(s.dir, oldGroup, oldFilename)
		_ = os.Remove(oldPath)

		ext := filepath.Ext(oldFilename)
		if ext == "" {
			ext = ".yaml"
		}
		baseFilename := sanitizeFilename(t.Name)
		filename := baseFilename + ext
		targetDir := filepath.Join(s.dir, newGroup)
		_ = os.MkdirAll(targetDir, 0o755)

		count := 1
		for {
			targetPath := filepath.Join(targetDir, filename)
			if _, err := os.Stat(targetPath); errors.Is(err, os.ErrNotExist) {
				break
			}
			filename = fmt.Sprintf("%s_%d%s", baseFilename, count, ext)
			count++
		}
		t.Filename = filename

		// If an exercise history file existed for the old filename, move it to the new filename
		oldHistBase := strings.TrimSuffix(oldFilename, filepath.Ext(oldFilename))
		newHistBase := strings.TrimSuffix(filename, filepath.Ext(filename))
		if oldHistBase != "" && newHistBase != "" {
			oldHistPath := filepath.Join(s.dir, oldGroup, oldHistBase+"._history.json")
			newHistPath := filepath.Join(targetDir, newHistBase+"._history.json")
			if _, err := os.Stat(oldHistPath); err == nil {
				_ = os.Rename(oldHistPath, newHistPath)
			}
		}
	}

	if err := s.saveTestLocked(t); err != nil {
		return Test{}, err
	}

	cp := *t
	return cp, nil
}

// DeleteTestResult describes the outcome of deleting a test.
type DeleteTestResult struct {
	Reseeded bool `json:"reseeded"`
}

// DeleteTest removes a test file from its category directory.
func (s *Store) DeleteTest(id string) (DeleteTestResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if strings.HasPrefix(id, ".") || strings.HasPrefix(id, "_") {
		return DeleteTestResult{}, errors.New("cannot delete internal files")
	}

	t, ok := s.tests[id]
	if !ok || t == nil {
		return DeleteTestResult{}, errors.New("test not found")
	}

	filePath := filepath.Join(s.dir, t.GroupID, t.Filename)
	_ = os.Remove(filePath)
	delete(s.tests, id)

	return DeleteTestResult{Reseeded: false}, nil
}

// ReorderTest bulk-updates the Order field for tests.
func (s *Store) ReorderTest(updates map[string]int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, order := range updates {
		if t, ok := s.tests[id]; ok && t != nil {
			t.Order = order
			t.UpdatedAt = time.Now().UTC()
			_ = s.saveTestLocked(t)
		}
	}
	return nil
}

// GetGroup returns a category by id.
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

// CreateGroup adds a new category and creates its directory and _category.yaml.
func (s *Store) CreateGroup(in Group) (Group, error) {
	if in.Name == "" {
		return Group{}, errors.New("group name is required")
	}
	id := sanitizeDirname(in.ID)
	if id == "" {
		id = sanitizeDirname(in.Name)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	g := Group{
		ID:           id,
		Name:         in.Name,
		Description:  in.Description,
		RequiredCaps: in.RequiredCaps,
		Order:        in.Order,
	}

	catDir := filepath.Join(s.dir, id)
	if err := os.MkdirAll(catDir, 0o755); err != nil {
		return Group{}, err
	}

	if err := s.saveCategoryLocked(&g); err != nil {
		return Group{}, err
	}

	s.groups[id] = &g
	return g, nil
}

// UpdateGroup modifies an existing category.
func (s *Store) UpdateGroup(id string, in Group) (Group, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	g, ok := s.groups[id]
	if !ok || g == nil {
		return Group{}, errors.New("group not found")
	}

	if in.Name != "" {
		g.Name = in.Name
	}
	g.Description = in.Description
	g.RequiredCaps = in.RequiredCaps
	g.Order = in.Order

	if err := s.saveCategoryLocked(g); err != nil {
		return Group{}, err
	}

	cp := *g
	return cp, nil
}

// DeleteGroup removes a category directory and all tests within it.
func (s *Store) DeleteGroup(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.groups[id]
	if !ok {
		return errors.New("group not found")
	}

	for tid, t := range s.tests {
		if t.GroupID == id {
			delete(s.tests, tid)
		}
	}
	delete(s.groups, id)

	catDir := filepath.Join(s.dir, id)
	return os.RemoveAll(catDir)
}

func (s *Store) saveTestLocked(t *Test) error {
	catDir := filepath.Join(s.dir, t.GroupID)
	if err := os.MkdirAll(catDir, 0o755); err != nil {
		return err
	}

	if t.Filename == "" {
		t.Filename = sanitizeFilename(t.Name) + ".yaml"
	}
	targetPath := filepath.Join(catDir, t.Filename)

	var data []byte
	var err error
	if strings.HasSuffix(strings.ToLower(t.Filename), ".json") {
		data, err = json.MarshalIndent(t, "", "  ")
		data = append(data, '\n')
	} else {
		data, err = yaml.Marshal(t)
	}
	if err != nil {
		return err
	}

	tmp := targetPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, targetPath)
}

func (s *Store) saveCategoryLocked(g *Group) error {
	catDir := filepath.Join(s.dir, g.ID)
	if err := os.MkdirAll(catDir, 0o755); err != nil {
		return err
	}
	targetPath := filepath.Join(catDir, "_category.yaml")

	data, err := yaml.Marshal(g)
	if err != nil {
		return err
	}

	tmp := targetPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, targetPath)
}

// PopulateSeed creates the initial 3 example tests in YAML format in testing/examples if testing dir is empty.
func (s *Store) PopulateSeed() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.groups) > 0 {
		return nil
	}

	examplesGroup := &Group{
		ID:          "examples",
		Name:        "Examples",
		Description: "Reference test suites and evaluation templates in YAML",
		Order:       0,
	}

	if err := s.saveCategoryLocked(examplesGroup); err != nil {
		return fmt.Errorf("create examples category: %w", err)
	}
	s.groups[examplesGroup.ID] = examplesGroup

	now := time.Now().UTC()
	seedExamples := []Test{
		{
			ID:           "example-arithmetic",
			Name:         "Math Suite (Multiple Exercises)",
			Description:  "Evaluates multiple diverse arithmetic and mathematical operations in a single test.",
			GroupID:      "examples",
			Active:       true,
			Order:        0,
			SystemPrompt: "You are a concise calculator. Reply with only the final numerical answer or expression.",
			Cases: []TestCase{
				{
					Name:   "Basic order of operations",
					Prompt: "What is 2 + 3 * 4? Return only the final number.",
					Evaluation: &Evaluation{
						Type:     "contains",
						Expected: "14",
					},
				},
				{
					Name:   "Fraction simplification",
					Prompt: "Simplify the fraction 18/24 to its lowest terms. Answer with plain text only.",
					Evaluation: &Evaluation{
						Type:     "contains",
						Expected: "3/4",
					},
				},
				{
					Name:   "Exponentiation",
					Prompt: "What is 2 raised to the power of 8 (2^8)? Return only the number.",
					Evaluation: &Evaluation{
						Type:     "contains",
						Expected: "256",
					},
				},
				{
					Name:   "Percentages",
					Prompt: "What is 15% of 200? Return only the number.",
					Evaluation: &Evaluation{
						Type:     "contains",
						Expected: "30",
					},
				},
			},
			Filename:  "arithmetic.yaml",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:           "example-weather-tool",
			Name:         "Weather Tool Call",
			Description:  "One-shot tool call evaluation for weather query.",
			GroupID:      "examples",
			Active:       true,
			Order:        1,
			RequiredCaps: []string{"tools"},
			SystemPrompt: "You have access to the following tool:\nget_weather(location: string) -> {temperature: number, condition: string}\nWhen the user asks about weather, respond ONLY with the tool call. Example:\nget_weather(\"London\")\nDo not add any other text.",
			Prompt:       "What is the weather like in Paris right now?",
			Evaluation: &Evaluation{
				Type:    "regex",
				Pattern: `(?i)get_weather\s*\(\s*"Paris"\s*\)`,
			},
			Filename:  "weather_tool.yaml",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:           "example-multi-turn",
			Name:         "Sequential Dialogue Chain",
			Description:  "Multi-step interactive chain testing sequential context retention across turns.",
			GroupID:      "examples",
			Active:       true,
			Order:        2,
			SystemPrompt: "You are a helpful and concise programming assistant.",
			Steps: []Step{
				{
					Step:   1,
					Name:   "Initial context inquiry",
					Prompt: "I am learning Python for data analysis and machine learning. What is the primary library used for dataframes?",
					Evaluation: &Evaluation{
						Type:     "contains",
						Expected: "pandas",
					},
				},
				{
					Step:   2,
					Name:   "Contextual follow-up",
					Prompt: "What programming language did I mention I was learning in my previous message? Reply with just the language name.",
					Evaluation: &Evaluation{
						Type:     "contains",
						Expected: "Python",
					},
				},
			},
			Filename:  "multi_turn.yaml",
			CreatedAt: now,
			UpdatedAt: now,
		},
		func() Test {
			t, _ := GetSeedTest("example-instructions", now)
			return t
		}(),
	}

	for i := range seedExamples {
		t := seedExamples[i]
		t.normalizeEvaluation()
		if err := s.saveTestLocked(&t); err != nil {
			return fmt.Errorf("save seed test %s: %w", t.ID, err)
		}
		s.tests[t.ID] = &t
	}

	return nil
}

// migrateLegacyFilesLocked finds old root tests JSON files, backs them up to testing/.backup,
// and clears them from root so they no longer clutter the project.
func (s *Store) migrateLegacyFilesLocked() {
	rootDir := filepath.Dir(s.dir)
	if rootDir == "" || rootDir == "." {
		return
	}

	legacyFiles := []string{
		"tests.json",
		"tests-core.json",
		"tests-multimodal.json",
		"tests-structured.json",
		"tests-tools.json",
		"tests-agent.json",
	}

	hasLegacy := false
	for _, fn := range legacyFiles {
		if _, err := os.Stat(filepath.Join(rootDir, fn)); err == nil {
			hasLegacy = true
			break
		}
	}
	if !hasLegacy {
		return
	}

	backupDir := filepath.Join(s.dir, ".backup")
	_ = os.MkdirAll(backupDir, 0o755)

	// Read groups from root tests.json
	groupsMap := make(map[string]Group)
	if gData, err := os.ReadFile(filepath.Join(rootDir, "tests.json")); err == nil {
		var gf struct {
			Groups []Group `json:"groups"`
			Tests  []Test  `json:"tests"`
		}
		if err := json.Unmarshal(gData, &gf); err == nil {
			for _, g := range gf.Groups {
				groupsMap[g.ID] = g
			}
			for _, t := range gf.Tests {
				gid := t.GroupID
				if gid == "" {
					gid = "general"
				}
				saveBackupTest(backupDir, gid, t)
			}
		}
	}

	// Read per-group legacy files
	entries, _ := os.ReadDir(rootDir)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "tests-") || !strings.HasSuffix(name, ".json") || name == "tests-history.json" {
			continue
		}
		gid := name[len("tests-") : len(name)-len(".json")]
		if gid == "_" {
			gid = "general"
		}
		b, err := os.ReadFile(filepath.Join(rootDir, name))
		if err != nil {
			continue
		}
		var tlist []Test
		if err := json.Unmarshal(b, &tlist); err == nil {
			for _, t := range tlist {
				if t.GroupID == "" {
					t.GroupID = gid
				}
				saveBackupTest(backupDir, gid, t)
			}
		}
		// Save category metadata in backup
		if g, ok := groupsMap[gid]; ok {
			catDir := filepath.Join(backupDir, gid)
			_ = os.MkdirAll(catDir, 0o755)
			cData, _ := yaml.Marshal(g)
			_ = os.WriteFile(filepath.Join(catDir, "_category.yaml"), cData, 0o644)
		}
	}

	// Remove legacy test files from root
	for _, fn := range legacyFiles {
		_ = os.Remove(filepath.Join(rootDir, fn))
	}

	// Migrate tests-history.json if present
	legacyHistory := filepath.Join(rootDir, "tests-history.json")
	targetHistory := filepath.Join(s.dir, ".history.json")
	if _, err := os.Stat(legacyHistory); err == nil {
		if _, err := os.Stat(targetHistory); errors.Is(err, os.ErrNotExist) {
			_ = os.Rename(legacyHistory, targetHistory)
		} else {
			_ = os.Remove(legacyHistory)
		}
	}
}

func saveBackupTest(backupDir, groupID string, t Test) {
	catDir := filepath.Join(backupDir, groupID)
	_ = os.MkdirAll(catDir, 0o755)
	fn := sanitizeFilename(t.Name) + ".yaml"
	target := filepath.Join(catDir, fn)
	count := 1
	for {
		if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
			break
		}
		fn = fmt.Sprintf("%s_%d.yaml", sanitizeFilename(t.Name), count)
		target = filepath.Join(catDir, fn)
		count++
	}
	t.Filename = fn
	data, err := yaml.Marshal(t)
	if err == nil {
		_ = os.WriteFile(target, data, 0o644)
	}
}

func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "test"
	}
	illegal := []string{"<", ">", ":", "\"", "/", "\\", "|", "?", "*", "\x00"}
	for _, char := range illegal {
		name = strings.ReplaceAll(name, char, "-")
	}
	name = strings.TrimSpace(name)
	name = strings.Trim(name, ".")
	if name == "" {
		name = "test"
	}
	return strings.ToLower(strings.ReplaceAll(name, " ", "_"))
}

func sanitizeDirname(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "group"
	}
	illegal := []string{"<", ">", ":", "\"", "/", "\\", "|", "?", "*", "\x00", "."}
	for _, char := range illegal {
		name = strings.ReplaceAll(name, char, "_")
	}
	name = strings.Trim(name, "_")
	if name == "" {
		name = "group"
	}
	return strings.ToLower(strings.ReplaceAll(name, " ", "_"))
}

func humanizeName(slug string) string {
	slug = strings.ReplaceAll(slug, "_", " ")
	slug = strings.ReplaceAll(slug, "-", " ")
	parts := strings.Fields(slug)
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

func newID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
