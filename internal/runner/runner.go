// Package runner implements the test battery execution engine.
package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gense/ollama-manager/internal/ollama"
	"github.com/gense/ollama-manager/internal/tests"
)

// BatteryRun is the result of executing a group of tests against one or more models.
type BatteryRun struct {
	ID        string       `json:"id"`
	Timestamp time.Time    `json:"timestamp"`
	GroupID   string       `json:"group_id"`
	GroupName string       `json:"group_name"`
	Models    []string     `json:"models"`
	Results   []TestResult `json:"results"`
	SysInfo   SysInfo      `json:"sys_info,omitempty"`
}

// TestResult holds the outcome of a single test for a single model.
type TestResult struct {
	TestID         string  `json:"test_id"`
	TestName       string  `json:"test_name"`
	Model          string  `json:"model"`
	Passed         *bool   `json:"passed,omitempty"`
	ResponseTimeMs int64   `json:"response_time_ms"`
	TokensPerSec   float64 `json:"tokens_per_sec,omitempty"`
	ReasoningUsed  bool    `json:"reasoning_used"`
	HumanRating    string  `json:"human_rating,omitempty"` // "bad", "regular", "good"
	ModelResponse  string  `json:"model_response,omitempty"`
	Error          string  `json:"error,omitempty"`
}

// Progress tracks the current state of a battery run.
type Progress struct {
	RunID           string `json:"run_id"`
	Model           string `json:"model"`
	TestID          string `json:"test_id"`
	TestName        string `json:"test_name"`
	TestIndex       int    `json:"test_index"`
	TotalTests      int    `json:"total_tests"`
	IsThinking      bool   `json:"is_thinking"`
	PartialResponse string `json:"partial_response,omitempty"`
	PartialThinking string `json:"partial_thinking,omitempty"`
	Done            bool   `json:"done"`
	Error           string `json:"error,omitempty"`
}

// Client wraps an Ollama client and executes tests.
type Client struct {
	ollama     *ollama.Client
	progressMu sync.Mutex
	progress   map[string]*Progress
}

// NewClient creates a runner client.
func NewClient(ollamaClient *ollama.Client) *Client {
	return &Client{
		ollama:   ollamaClient,
		progress: make(map[string]*Progress),
	}
}

func (c *Client) setProgress(p Progress) {
	c.progressMu.Lock()
	defer c.progressMu.Unlock()
	c.progress[p.RunID] = &p
}

// GetProgress returns the current progress for a run.
func (c *Client) GetProgress(runID string) (Progress, bool) {
	c.progressMu.Lock()
	defer c.progressMu.Unlock()
	p, ok := c.progress[runID]
	if !ok || p == nil {
		return Progress{}, false
	}
	return *p, true
}

// ClearProgress removes progress tracking for a run.
func (c *Client) ClearProgress(runID string) {
	c.progressMu.Lock()
	defer c.progressMu.Unlock()
	delete(c.progress, runID)
}

// ExecuteBatteryAsync starts the battery run in a goroutine and returns the run ID immediately.
// The caller should poll GetProgress and then retrieve the run from the store when Done is true.
func (c *Client) ExecuteBatteryAsync(ctx context.Context, group tests.Group, testsList []tests.Test, modelIDs []string, modelCaps map[string][]string, sysInfo SysInfo, onComplete func(*BatteryRun)) string {
	run := &BatteryRun{
		ID:        newRunID(),
		Timestamp: time.Now().UTC(),
		GroupID:   group.ID,
		GroupName: group.Name,
		Models:    append([]string(nil), modelIDs...),
		SysInfo:   sysInfo,
	}

	// Filter active non-agent tests that belong to this group.
	var activeTests []tests.Test
	for _, t := range testsList {
		if t.GroupID != group.ID {
			continue
		}
		if !t.Active {
			continue
		}
		if t.EvaluationType == "agent" {
			continue
		}
		activeTests = append(activeTests, t)
	}

	total := 0
	for _, model := range modelIDs {
		caps := modelCaps[model]
		for _, test := range activeTests {
			if hasAllCaps(caps, test.RequiredCaps) {
				total++
			}
		}
	}

	c.setProgress(Progress{RunID: run.ID, TotalTests: total})

	go func() {
		idx := 0
		var runErr string
		for _, model := range modelIDs {
			caps := modelCaps[model]
			for _, test := range activeTests {
				if !hasAllCaps(caps, test.RequiredCaps) {
					continue
				}
				idx++
				c.setProgress(Progress{
					RunID:      run.ID,
					Model:      model,
					TestID:     test.ID,
					TestName:   test.Name,
					TestIndex:  idx,
					TotalTests: total,
				})
				res := c.runTest(ctx, run.ID, model, test)
				run.Results = append(run.Results, res)
			}
		}
		if runErr != "" {
			c.setProgress(Progress{RunID: run.ID, Done: true, Error: runErr, TotalTests: total})
		} else {
			c.setProgress(Progress{RunID: run.ID, Done: true, TotalTests: total})
		}
		if onComplete != nil {
			onComplete(run)
		}
	}()

	return run.ID
}

func (c *Client) runTest(ctx context.Context, runID string, model string, test tests.Test) TestResult {
	res := TestResult{
		TestID:   test.ID,
		TestName: test.Name,
		Model:    model,
	}

	messages := []ollama.ChatMessage{
		{Role: "system", Content: test.SystemPrompt},
		{Role: "user", Content: test.Prompt},
	}
	// Remove empty system message.
	if messages[0].Content == "" {
		messages = messages[1:]
	}

	// Attach images and audio if present.
	// Ollama puts both in the same `images` array; it does not distinguish
	// image vs audio at the field level.
	var media []string
	for _, att := range test.Attachments {
		if att.Kind == "image" || att.Kind == "audio" {
			media = append(media, att.Data)
		}
	}
	if len(media) > 0 {
		// Attach to the last user message.
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == "user" {
				messages[i].Images = append(messages[i].Images, media...)
				break
			}
		}
	}

	req := ollama.ChatRequest{
		Model:    model,
		Messages: messages,
		Stream:   true,
	}

	var fullContent strings.Builder
	var fullThinking strings.Builder
	var chunkMeta *ollama.ChatChunk
	isThinking := false
	start := time.Now()

	err := c.ollama.Chat(ctx, req, func(chunk ollama.ChatChunk) error {
		if chunk.Message.Content != "" {
			fullContent.WriteString(chunk.Message.Content)
		}
		if chunk.Message.Thinking != "" {
			fullThinking.WriteString(chunk.Message.Thinking)
		}
		// Detect thinking tags in real-time and update progress with partial content.
		content := fullContent.String()
		wasThinking := isThinking
		if strings.Contains(content, "<thinking>") || strings.Contains(content, "<stitching>") || strings.Contains(content, "<throat>") {
			isThinking = true
		}
		if isThinking && (strings.Contains(content, "</thinking>") || strings.Contains(content, "</stitching>") || strings.Contains(content, "</throat>")) {
			isThinking = false
		}
		c.updateProgressStream(runID, isThinking, content, fullThinking.String())
		_ = wasThinking
		if chunk.Done {
			chunkMeta = &chunk
		}
		return nil
	})

	elapsed := time.Since(start).Milliseconds()
	res.ResponseTimeMs = elapsed

	if err != nil {
		res.Error = err.Error()
		return res
	}

	res.ModelResponse = fullContent.String()
	res.ReasoningUsed = strings.TrimSpace(fullThinking.String()) != ""

	// Compute tokens per second from Ollama metadata.
	if chunkMeta != nil && chunkMeta.EvalCount > 0 && chunkMeta.EvalDuration > 0 {
		res.TokensPerSec = float64(chunkMeta.EvalCount) / (float64(chunkMeta.EvalDuration) / 1e9)
	}

	// Score based on evaluation type.
	passed := scoreTest(test, res.ModelResponse)
	if passed != nil {
		res.Passed = passed
	}
	// For human_review, passed stays nil and human_rating stays empty.

	return res
}

func (c *Client) updateProgressStream(runID string, thinking bool, content, reasoning string) {
	c.progressMu.Lock()
	defer c.progressMu.Unlock()
	if p, ok := c.progress[runID]; ok && p != nil {
		p.IsThinking = thinking
		p.PartialResponse = content
		p.PartialThinking = reasoning
	}
}

func scoreTest(test tests.Test, response string) *bool {
	switch test.EvaluationType {
	case "exact_match":
		var cfg struct {
			Expected string `json:"expected"`
		}
		_ = json.Unmarshal(test.EvaluationConfig, &cfg)
		v := strings.TrimSpace(response) == strings.TrimSpace(cfg.Expected)
		return &v
	case "contains":
		var cfg struct {
			Expected string `json:"expected"`
		}
		_ = json.Unmarshal(test.EvaluationConfig, &cfg)
		norm := normalizeForContains(response)
		expected := cfg.Expected
		// For code-like expected values, strip all whitespace for robust matching.
		if strings.Contains(expected, "\n") || strings.Contains(expected, "\t") {
			norm = stripWhitespace(norm)
			expected = stripWhitespace(expected)
		}
		v := strings.Contains(strings.ToLower(norm), strings.ToLower(expected))
		return &v
	case "regex":
		var cfg struct {
			Pattern string `json:"pattern"`
		}
		_ = json.Unmarshal(test.EvaluationConfig, &cfg)
		if cfg.Pattern == "" {
			v := false
			return &v
		}
		re, err := regexp.Compile(cfg.Pattern)
		if err != nil {
			v := false
			return &v
		}
		v := re.MatchString(response)
		return &v
	case "json_schema":
		var cfg struct {
			Required []string `json:"required"`
		}
		_ = json.Unmarshal(test.EvaluationConfig, &cfg)
		var obj map[string]any
		if err := json.Unmarshal([]byte(response), &obj); err != nil {
			v := false
			return &v
		}
		for _, key := range cfg.Required {
			if _, ok := obj[key]; !ok {
				v := false
				return &v
			}
		}
		v := true
		return &v
	case "human_review":
		return nil // no auto-score
	default:
		v := false
		return &v
	}
}

// normalizeForContains strips LaTeX/markdown formatting so that
// e.g. \frac{3}{4} becomes 3/4 for easier substring matching.
func normalizeForContains(s string) string {
	// Handle \frac{a}{b} -> a/b
	s = regexp.MustCompile(`\\frac\{([^}]*)\}\{([^}]*)\}`).ReplaceAllString(s, "$1/$2")
	// Remove remaining braces.
	s = strings.ReplaceAll(s, "{", "")
	s = strings.ReplaceAll(s, "}", "")
	// Remove common markdown.
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "*", "")
	s = strings.ReplaceAll(s, "`", "")
	return s
}

func stripWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func hasAllCaps(have, need []string) bool {
	if len(need) == 0 {
		return true
	}
	set := make(map[string]bool, len(have))
	for _, c := range have {
		set[c] = true
	}
	for _, c := range need {
		if !set[c] {
			return false
		}
	}
	return true
}

func newRunID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "run-" + hex.EncodeToString(b)
}
