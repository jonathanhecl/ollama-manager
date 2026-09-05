// Package tests implements a filesystem-backed store for YAML/JSON test scripts and test categories.
//
// Categories are represented as subdirectories inside the testing root folder.
// Tests are individual .yaml/.yml or .json script files within each category directory.
// All mutations are protected by a mutex and persisted directly to disk.
package tests

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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

// Attachment is a file attached to a single case or step (image, audio, or
// text document). Attachments are NEVER written to the test YAML: they live
// as sidecar files next to it named <base>-<N>.<ext> (N = 1-based case/step
// index) and are discovered at Load time into these runtime-only fields.
type Attachment struct {
	ID   string `json:"id" yaml:"-"`
	Kind string `json:"kind" yaml:"-"` // "image", "audio", "text"
	Name string `json:"name" yaml:"-"` // sidecar filename, e.g. vision-cases-1.png
	Mime string `json:"mime" yaml:"-"` // MIME type derived from the extension
	Data string `json:"data" yaml:"-"` // base64 file content
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
type Step struct {
	Step        int          `json:"step" yaml:"step"`
	Name        string       `json:"name,omitempty" yaml:"name,omitempty"`
	Prompt      string       `json:"prompt" yaml:"prompt"`
	Evaluation  *Evaluation  `json:"evaluation,omitempty" yaml:"evaluation,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty" yaml:"-"`
}

// TestCase represents an individual case in a batch/matrix test suite.
type TestCase struct {
	Name        string       `json:"name,omitempty" yaml:"name,omitempty"`
	Prompt      string       `json:"prompt" yaml:"prompt"`
	Evaluation  *Evaluation  `json:"evaluation,omitempty" yaml:"evaluation,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty" yaml:"-"`
}

// TestOptions represents optional inference parameters.
type TestOptions struct {
	Temperature *float64 `json:"temperature,omitempty" yaml:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty" yaml:"top_p,omitempty"`
	MaxTokens   *int     `json:"max_tokens,omitempty" yaml:"max_tokens,omitempty"`
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
	// Sidecars holds runtime-discovered attachments for simple (prompt-only)
	// tests, from the <base>-1.<ext> sidecar file. Never stored in YAML.
	Sidecars         []Attachment      `json:"sidecars,omitempty" yaml:"-"`
	Options          *TestOptions      `json:"options,omitempty" yaml:"options,omitempty"`
	Filename         string          `json:"filename,omitempty" yaml:"filename,omitempty"`
	CreatedAt        time.Time       `json:"created_at" yaml:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at" yaml:"updated_at"`
}

// MaxSidecarBytes caps sidecar files loaded into memory (10 MiB).
const MaxSidecarBytes = 10 << 20

// sidecarKind maps a sidecar file extension to its attachment kind and MIME.
type sidecarKind struct {
	kind string
	mime string
}

// SidecarKindForExt reports the attachment kind and MIME for a file extension
// (with or without leading dot, case-insensitive). ok=false means the
// extension is not a supported sidecar type.
func SidecarKindForExt(ext string) (kind, mime string, ok bool) {
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "png":
		return "image", "image/png", true
	case "jpg", "jpeg":
		return "image", "image/jpeg", true
	case "webp":
		return "image", "image/webp", true
	case "gif":
		return "image", "image/gif", true
	case "wav":
		return "audio", "audio/wav", true
	case "mp3":
		return "audio", "audio/mpeg", true
	case "ogg":
		return "audio", "audio/ogg", true
	case "txt":
		return "text", "text/plain", true
	case "md":
		return "text", "text/markdown", true
	}
	return "", "", false
}

// attachSidecarsLocked discovers <base>-<N>.<ext> sidecar files next to the
// test file and attaches them to case/step N (1-based), or — for simple
// prompt-only tests — the <base>-1.<ext> file to the test itself. It resets
// all runtime attachment fields first, so it can be re-run after uploads or
// deletions. Call with s.mu held.
func (s *Store) attachSidecarsLocked(catDir string, t *Test) {
	t.Sidecars = nil
	for i := range t.Cases {
		t.Cases[i].Attachments = nil
	}
	for i := range t.Steps {
		t.Steps[i].Attachments = nil
	}
	base := strings.TrimSuffix(t.Filename, filepath.Ext(t.Filename))
	if base == "" {
		return
	}
	entries, err := os.ReadDir(catDir)
	if err != nil {
		return
	}
	prefix := base + "-"
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		rest := strings.TrimPrefix(name, prefix) // "<N>.<ext>"
		dot := strings.LastIndex(rest, ".")
		if dot <= 0 {
			continue
		}
		n, err := strconv.Atoi(rest[:dot])
		if err != nil || n < 1 {
			continue
		}
		kind, mime, ok := SidecarKindForExt(rest[dot:])
		if !ok {
			continue
		}
		data, err := os.ReadFile(filepath.Join(catDir, name))
		if err != nil || len(data) == 0 || len(data) > MaxSidecarBytes {
			continue
		}
		att := Attachment{
			ID:   base + "-" + strconv.Itoa(n) + strings.ToLower(rest[dot:]),
			Kind: kind,
			Name: name,
			Mime: mime,
			Data: base64.StdEncoding.EncodeToString(data),
		}
		idx := n - 1
		switch {
		case len(t.Cases) > 0:
			if idx < len(t.Cases) {
				t.Cases[idx].Attachments = append(t.Cases[idx].Attachments, att)
			}
		case len(t.Steps) > 0:
			if idx < len(t.Steps) {
				t.Steps[idx].Attachments = append(t.Steps[idx].Attachments, att)
			}
		default:
			if n == 1 {
				t.Sidecars = append(t.Sidecars, att)
			}
		}
	}
}

// SaveSidecar stores an uploaded sidecar file for case/step index (1-based)
// as <base>-<index><ext>, replacing any other sidecar previously bound to
// that index. data is the raw file content.
func (s *Store) SaveSidecar(id string, index int, filename string, data []byte) (Attachment, error) {
	if index < 1 {
		return Attachment{}, errors.New("sidecar index must be >= 1")
	}
	if len(data) == 0 {
		return Attachment{}, errors.New("empty file")
	}
	if len(data) > MaxSidecarBytes {
		return Attachment{}, fmt.Errorf("file exceeds %d bytes", MaxSidecarBytes)
	}
	ext := strings.ToLower(filepath.Ext(filename))
	kind, mime, ok := SidecarKindForExt(ext)
	if !ok {
		return Attachment{}, fmt.Errorf("unsupported sidecar type %q (png/jpg/webp/gif/wav/mp3/ogg/txt/md)", ext)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.tests[id]
	if !ok || t == nil {
		return Attachment{}, errors.New("test not found")
	}
	catDir := filepath.Join(s.dir, t.GroupID)
	base := strings.TrimSuffix(t.Filename, filepath.Ext(t.Filename))
	// Drop any sidecar previously bound to this index (single file per case).
	if entries, err := os.ReadDir(catDir); err == nil {
		prefix := fmt.Sprintf("%s-%d.", base, index)
		for _, e := range entries {
			if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
				_ = os.Remove(filepath.Join(catDir, e.Name()))
			}
		}
	}
	name := fmt.Sprintf("%s-%d%s", base, index, ext)
	if err := os.WriteFile(filepath.Join(catDir, name), data, 0o644); err != nil {
		return Attachment{}, err
	}
	s.attachSidecarsLocked(catDir, t)
	return Attachment{
		ID:   base + "-" + strconv.Itoa(index) + ext,
		Kind: kind,
		Name: name,
		Mime: mime,
		Data: base64.StdEncoding.EncodeToString(data),
	}, nil
}

// DeleteSidecar removes every sidecar file bound to case/step index (1-based).
func (s *Store) DeleteSidecar(id string, index int) error {
	if index < 1 {
		return errors.New("sidecar index must be >= 1")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.tests[id]
	if !ok || t == nil {
		return errors.New("test not found")
	}
	catDir := filepath.Join(s.dir, t.GroupID)
	base := strings.TrimSuffix(t.Filename, filepath.Ext(t.Filename))
	removed := false
	if entries, err := os.ReadDir(catDir); err == nil {
		prefix := fmt.Sprintf("%s-%d.", base, index)
		for _, e := range entries {
			if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
				if _, _, ok := SidecarKindForExt(filepath.Ext(e.Name())); ok {
					_ = os.Remove(filepath.Join(catDir, e.Name()))
					removed = true
				}
			}
		}
	}
	if !removed {
		return errors.New("no sidecar for that index")
	}
	s.attachSidecarsLocked(catDir, t)
	return nil
}

// moveSidecarsLocked renames <oldBase>-* sidecar files when a test file moves.
// Call with s.mu held.
func (s *Store) moveSidecarsLocked(oldDir, oldBase, newDir, newBase string) {
	if oldBase == "" || newBase == "" || (oldDir == newDir && oldBase == newBase) {
		return
	}
	entries, err := os.ReadDir(oldDir)
	if err != nil {
		return
	}
	prefix := oldBase + "-"
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		rest := strings.TrimPrefix(e.Name(), prefix)
		dot := strings.LastIndex(rest, ".")
		if dot <= 0 {
			continue
		}
		if _, _, ok := SidecarKindForExt(rest[dot:]); !ok {
			continue
		}
		if _, err := strconv.Atoi(rest[:dot]); err != nil {
			continue
		}
		_ = os.MkdirAll(newDir, 0o755)
		_ = os.Rename(filepath.Join(oldDir, e.Name()), filepath.Join(newDir, newBase+"-"+rest))
	}
}

// removeSidecarsLocked deletes every sidecar file of a test. Call with s.mu held.
func (s *Store) removeSidecarsLocked(catDir, base string) {
	if base == "" {
		return
	}
	entries, err := os.ReadDir(catDir)
	if err != nil {
		return
	}
	prefix := base + "-"
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		rest := strings.TrimPrefix(e.Name(), prefix)
		dot := strings.LastIndex(rest, ".")
		if dot <= 0 {
			continue
		}
		if _, _, ok := SidecarKindForExt(rest[dot:]); !ok {
			continue
		}
		if _, err := strconv.Atoi(rest[:dot]); err != nil {
			continue
		}
		_ = os.Remove(filepath.Join(catDir, e.Name()))
	}
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
		s.attachSidecarsLocked(catPath, &tt)
		s.tests[t.ID] = &tt
		}
	}

	// Backfill newer seeds missing on disk (existing installs get them).
	s.backfillSeedsLocked()

	return nil
}

// backfillSeedsLocked creates the newer catalog seeds missing on disk.
// The original three seeds are never backfilled so a deliberate deletion
// sticks. Call with s.mu held.
func (s *Store) backfillSeedsLocked() {
	now := time.Now().UTC()
	for _, id := range backfillSeedIDs {
		if _, ok := s.tests[id]; ok {
			continue
		}
		t, ok := GetSeedTest(id, now)
		if !ok {
			continue
		}
		if _, ok := s.groups[t.GroupID]; !ok {
			s.groups[t.GroupID] = &Group{ID: t.GroupID, Name: humanizeName(t.GroupID), Order: len(s.groups)}
			_ = s.saveCategoryLocked(s.groups[t.GroupID])
		}
		// Don't clobber an unrelated file with the same name.
		if _, err := os.Stat(filepath.Join(s.dir, t.GroupID, t.Filename)); err == nil {
			continue
		}
		t.normalizeEvaluation()
		if err := s.saveTestLocked(&t); err != nil {
			continue
		}
		s.writeSeedSidecarsLocked(t.GroupID, t.Filename)
		s.attachSidecarsLocked(filepath.Join(s.dir, t.GroupID), &t)
		s.tests[t.ID] = &t
	}
}

// writeSeedSidecarsLocked generates known seed sidecar fixtures next to a
// test file (no-op for unknown names, never overwrites). Call with s.mu held.
func (s *Store) writeSeedSidecarsLocked(groupID, filename string) {
	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	if base == "" {
		return
	}
	catDir := filepath.Join(s.dir, groupID)
	for n := 1; n <= 9; n++ {
		for _, ext := range []string{".png", ".txt"} {
			name := fmt.Sprintf("%s-%d%s", base, n, ext)
			content, ok := seedSidecarContent(name)
			if !ok {
				continue
			}
			target := filepath.Join(catDir, name)
			if _, err := os.Stat(target); err == nil {
				continue
			}
			_ = os.WriteFile(target, content, 0o644)
		}
	}
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

	s.attachSidecarsLocked(targetDir, &t)
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
	if len(in.Messages) > 0 {
		t.Messages = in.Messages
	}
	if len(in.Steps) > 0 {
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
	// NOTE: case/step attachments are runtime-only (sidecar files); the
	// test-level Attachments field no longer exists.
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
		// Move sidecar files along with the test file.
		s.moveSidecarsLocked(filepath.Join(s.dir, oldGroup), oldHistBase, targetDir, newHistBase)
	}

	if err := s.saveTestLocked(t); err != nil {
		return Test{}, err
	}

	s.attachSidecarsLocked(filepath.Join(s.dir, t.GroupID), t)

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
	s.removeSidecarsLocked(filepath.Join(s.dir, t.GroupID), strings.TrimSuffix(t.Filename, filepath.Ext(t.Filename)))
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

	// Idempotent: only write the original seeds that are missing (the newer
	// catalog is backfilled by Load, which may run first and create the group).
	missing := false
	for _, id := range []string{"example-arithmetic", "example-weather-tool", "example-multi-turn"} {
		if _, ok := s.tests[id]; !ok {
			missing = true
			break
		}
	}
	if !missing {
		return nil
	}
	if _, ok := s.groups["examples"]; !ok {
		s.groups["examples"] = &Group{
			ID:          "examples",
			Name:        "Examples",
			Description: "Reference test suites and evaluation templates in YAML",
			Order:       len(s.groups),
		}
		if err := s.saveCategoryLocked(s.groups["examples"]); err != nil {
			return fmt.Errorf("create examples category: %w", err)
		}
	}

	now := time.Now().UTC()
	for _, id := range []string{"example-arithmetic", "example-weather-tool", "example-multi-turn"} {
		if _, ok := s.tests[id]; ok {
			continue
		}
		t, ok := GetSeedTest(id, now)
		if !ok {
			continue
		}
		t.normalizeEvaluation()
		if err := s.saveTestLocked(&t); err != nil {
			return fmt.Errorf("save seed test %s: %w", t.ID, err)
		}
		s.writeSeedSidecarsLocked(t.GroupID, t.Filename)
		s.attachSidecarsLocked(filepath.Join(s.dir, t.GroupID), &t)
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
