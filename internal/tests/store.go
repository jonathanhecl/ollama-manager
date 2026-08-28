// Package tests implements a filesystem-backed store for test scripts and test categories.
//
// Categories are represented as subdirectories inside the testing root folder.
// Tests are individual .json script files within each category directory.
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
)

// Group is a collection of related tests (corresponds to a category directory).
type Group struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	RequiredCaps []string `json:"required_caps,omitempty"`
	Order        int      `json:"order"`
}

// Attachment is a file attached to a test (image, audio, or document).
type Attachment struct {
	ID   string `json:"id"`
	Kind string `json:"kind"` // "image", "audio", "file"
	Name string `json:"name"` // original filename
	Mime string `json:"mime"` // MIME type
	Data string `json:"data"` // base64 content or relative file path
}

// Message represents a single chat turn in a multi-message test script.
type Message struct {
	Role      string   `json:"role"`
	Content   string   `json:"content"`
	Images    []string `json:"images,omitempty"`
	ToolCalls any      `json:"tool_calls,omitempty"`
}

// Evaluation specifies the evaluation strategy and parameters.
type Evaluation struct {
	Type   string          `json:"type"`
	Config json.RawMessage `json:"config,omitempty"`
}

// TestOptions represents optional inference parameters.
type TestOptions struct {
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty"`
}

// Test is an individual evaluation test script.
type Test struct {
	ID               string          `json:"id"`
	Name             string          `json:"name"`
	Description      string          `json:"description,omitempty"`
	GroupID          string          `json:"group_id"`
	Active           bool            `json:"active"`
	Order            int             `json:"order"`
	Prompt           string          `json:"prompt,omitempty"`
	SystemPrompt     string          `json:"system_prompt,omitempty"`
	Messages         []Message       `json:"messages,omitempty"`
	Evaluation       *Evaluation     `json:"evaluation,omitempty"`
	EvaluationType   string          `json:"evaluation_type,omitempty"`
	EvaluationConfig json.RawMessage `json:"evaluation_config,omitempty"`
	RequiredCaps     []string        `json:"required_caps,omitempty"`
	Attachments      []Attachment    `json:"attachments,omitempty"`
	Options          *TestOptions    `json:"options,omitempty"`
	Filename         string          `json:"filename,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
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
	if strings.HasSuffix(strings.ToLower(dir), ".json") {
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
// It also migrates legacy root JSON test files if present.
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
		// Ignore hidden directories (.backup, .history, etc.)
		if strings.HasPrefix(dirName, ".") {
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
			if strings.HasPrefix(tName, ".") || strings.EqualFold(tName, "_category.json") {
				continue
			}
			if !strings.HasSuffix(strings.ToLower(tName), ".json") {
				continue
			}

			filePath := filepath.Join(catPath, tName)
			data, err := os.ReadFile(filePath)
			if err != nil {
				continue
			}

			var t Test
			if err := json.Unmarshal(data, &t); err != nil {
				continue
			}

			if t.ID == "" {
				t.ID = strings.TrimSuffix(tName, filepath.Ext(tName))
			}
			t.GroupID = group.ID
			t.Filename = tName

			// Sync Evaluation struct and legacy fields
			if t.Evaluation != nil {
				if t.EvaluationType == "" {
					t.EvaluationType = t.Evaluation.Type
				}
				if len(t.EvaluationConfig) == 0 {
					t.EvaluationConfig = t.Evaluation.Config
				}
			} else if t.EvaluationType != "" {
				t.Evaluation = &Evaluation{
					Type:   t.EvaluationType,
					Config: t.EvaluationConfig,
				}
			}

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

func (s *Store) loadCategoryLocked(dirName, catPath string) *Group {
	catMetaPath := filepath.Join(catPath, "_category.json")
	if data, err := os.ReadFile(catMetaPath); err == nil {
		var g Group
		if err := json.Unmarshal(data, &g); err == nil {
			if g.ID == "" {
				g.ID = dirName
			}
			if g.Name == "" {
				g.Name = humanizeName(dirName)
			}
			return &g
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

// CreateTest adds a new test and writes its individual .json file.
func (s *Store) CreateTest(in Test) (Test, error) {
	if in.Name == "" {
		return Test{}, errors.New("test name is required")
	}
	if in.Prompt == "" && len(in.Messages) == 0 {
		return Test{}, errors.New("test prompt or messages are required")
	}

	evalType := in.EvaluationType
	if evalType == "" && in.Evaluation != nil {
		evalType = in.Evaluation.Type
	}
	if evalType == "" {
		return Test{}, errors.New("evaluation_type is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	groupID := sanitizeDirname(in.GroupID)
	if groupID == "" {
		groupID = "default"
	}
	if _, ok := s.groups[groupID]; !ok {
		// Auto-create category if needed
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
	if t.Evaluation == nil {
		t.Evaluation = &Evaluation{
			Type:   evalType,
			Config: in.EvaluationConfig,
		}
	}
	t.CreatedAt = now
	t.UpdatedAt = now

	// Determine unique filename
	baseFilename := sanitizeFilename(t.Name)
	filename := baseFilename + ".json"
	targetDir := filepath.Join(s.dir, groupID)
	_ = os.MkdirAll(targetDir, 0o755)

	count := 1
	for {
		targetPath := filepath.Join(targetDir, filename)
		if _, err := os.Stat(targetPath); errors.Is(err, os.ErrNotExist) {
			break
		}
		filename = fmt.Sprintf("%s_%d.json", baseFilename, count)
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
	if in.Prompt != "" {
		t.Prompt = in.Prompt
	}
	t.SystemPrompt = in.SystemPrompt
	if len(in.Messages) > 0 {
		t.Messages = in.Messages
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

		baseFilename := sanitizeFilename(t.Name)
		filename := baseFilename + ".json"
		targetDir := filepath.Join(s.dir, newGroup)
		_ = os.MkdirAll(targetDir, 0o755)

		count := 1
		for {
			targetPath := filepath.Join(targetDir, filename)
			if _, err := os.Stat(targetPath); errors.Is(err, os.ErrNotExist) {
				break
			}
			filename = fmt.Sprintf("%s_%d.json", baseFilename, count)
			count++
		}
		t.Filename = filename
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

// CreateGroup adds a new category and creates its directory and _category.json.
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

	// Remove all tests belonging to this group
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
		t.Filename = sanitizeFilename(t.Name) + ".json"
	}
	targetPath := filepath.Join(catDir, t.Filename)

	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

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
	targetPath := filepath.Join(catDir, "_category.json")

	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp := targetPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, targetPath)
}

// PopulateSeed creates the initial 3 example tests in testing/examples if testing dir is empty.
func (s *Store) PopulateSeed() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.groups) > 0 {
		return nil
	}

	examplesGroup := &Group{
		ID:          "examples",
		Name:        "Examples",
		Description: "Reference tests and standard evaluation templates",
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
			Name:         "Basic Arithmetic",
			Description:  "Evaluates whether the model can follow order of operations.",
			GroupID:      "examples",
			Active:       true,
			Order:        0,
			SystemPrompt: "You are a concise calculator. Reply with only the final numerical answer.",
			Prompt:       "What is 2 + 3 * 4? Return only the final number.",
			Messages: []Message{
				{Role: "system", Content: "You are a concise calculator. Reply with only the final numerical answer."},
				{Role: "user", Content: "What is 2 + 3 * 4? Return only the final number."},
			},
			Evaluation: &Evaluation{
				Type:   "contains",
				Config: mustJSON(map[string]any{"expected": "14"}),
			},
			EvaluationType:   "contains",
			EvaluationConfig: mustJSON(map[string]any{"expected": "14"}),
			Filename:         "arithmetic.json",
			CreatedAt:        now,
			UpdatedAt:        now,
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
			Messages: []Message{
				{Role: "system", Content: "You have access to the following tool:\nget_weather(location: string) -> {temperature: number, condition: string}\nWhen the user asks about weather, respond ONLY with the tool call. Example:\nget_weather(\"London\")\nDo not add any other text."},
				{Role: "user", Content: "What is the weather like in Paris right now?"},
			},
			Evaluation: &Evaluation{
				Type:   "regex",
				Config: mustJSON(map[string]any{"pattern": `(?i)get_weather\s*\(\s*"Paris"\s*\)`}),
			},
			EvaluationType:   "regex",
			EvaluationConfig: mustJSON(map[string]any{"pattern": `(?i)get_weather\s*\(\s*"Paris"\s*\)`}),
			Filename:         "weather_tool.json",
			CreatedAt:        now,
			UpdatedAt:        now,
		},
		{
			ID:           "example-multi-turn",
			Name:         "Multi-Turn Dialogue",
			Description:  "Tests multi-turn context retention and following previous instructions.",
			GroupID:      "examples",
			Active:       true,
			Order:        2,
			SystemPrompt: "You are a helpful programming assistant.",
			Prompt:       "What programming language was I asking about in my first question? Answer with just the language name.",
			Messages: []Message{
				{Role: "system", Content: "You are a helpful programming assistant."},
				{Role: "user", Content: "I am learning Python for data analysis. Is it a good choice?"},
				{Role: "assistant", Content: "Yes, Python is an excellent choice for data analysis due to libraries like pandas, numpy, and matplotlib."},
				{Role: "user", Content: "What programming language was I asking about in my first question? Answer with just the language name."},
			},
			Evaluation: &Evaluation{
				Type:   "contains",
				Config: mustJSON(map[string]any{"expected": "Python"}),
			},
			EvaluationType:   "contains",
			EvaluationConfig: mustJSON(map[string]any{"expected": "Python"}),
			Filename:         "multi_turn.json",
			CreatedAt:        now,
			UpdatedAt:        now,
		},
	}

	for i := range seedExamples {
		t := seedExamples[i]
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
			cData, _ := json.MarshalIndent(g, "", "  ")
			_ = os.WriteFile(filepath.Join(catDir, "_category.json"), cData, 0o644)
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
	fn := sanitizeFilename(t.Name) + ".json"
	target := filepath.Join(catDir, fn)
	count := 1
	for {
		if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
			break
		}
		fn = fmt.Sprintf("%s_%d.json", sanitizeFilename(t.Name), count)
		target = filepath.Join(catDir, fn)
		count++
	}
	t.Filename = fn
	data, err := json.MarshalIndent(t, "", "  ")
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

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
