// Package runner implements the test battery execution engine.
package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"
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
}

// TestResult holds the outcome of a single test for a single model.
type TestResult struct {
	TestID         string `json:"test_id"`
	TestName       string `json:"test_name"`
	Model          string `json:"model"`
	Passed         *bool  `json:"passed,omitempty"`
	ResponseTimeMs int64  `json:"response_time_ms"`
	ReasoningUsed  bool   `json:"reasoning_used"`
	HumanRating    string `json:"human_rating,omitempty"` // "bad", "regular", "good"
	ModelResponse  string `json:"model_response,omitempty"`
	Error          string `json:"error,omitempty"`
}

// Client wraps an Ollama client and executes tests.
type Client struct {
	ollama *ollama.Client
}

// NewClient creates a runner client.
func NewClient(ollamaClient *ollama.Client) *Client {
	return &Client{ollama: ollamaClient}
}

// ExecuteBattery runs all active non-agent tests in the given group against each model sequentially.
// modelCaps maps model name -> slice of capability strings.
func (c *Client) ExecuteBattery(ctx context.Context, group tests.Group, testsList []tests.Test, modelIDs []string, modelCaps map[string][]string) (*BatteryRun, error) {
	run := &BatteryRun{
		ID:        newRunID(),
		Timestamp: time.Now().UTC(),
		GroupID:   group.ID,
		GroupName: group.Name,
		Models:    append([]string(nil), modelIDs...),
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

	for _, model := range modelIDs {
		caps := modelCaps[model]
		for _, test := range activeTests {
			// Skip if model lacks required capabilities.
			if !hasAllCaps(caps, test.RequiredCaps) {
				continue
			}
			res := c.runTest(ctx, model, test)
			run.Results = append(run.Results, res)
		}
	}

	return run, nil
}

func (c *Client) runTest(ctx context.Context, model string, test tests.Test) TestResult {
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

	// Attach images if present.
	var images []string
	for _, att := range test.Attachments {
		if att.Kind == "image" {
			images = append(images, att.Data)
		}
	}
	if len(images) > 0 {
		// Attach to the last user message.
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == "user" {
				messages[i].Images = images
				break
			}
		}
	}

	req := ollama.ChatRequest{
		Model:    model,
		Messages: messages,
		Stream:   false,
	}

	start := time.Now()
	resp, err := c.ollama.ChatOnce(ctx, req)
	elapsed := time.Since(start).Milliseconds()
	res.ResponseTimeMs = elapsed

	if err != nil {
		res.Error = err.Error()
		return res
	}

	if resp == nil || resp.Message.Content == "" {
		res.ModelResponse = ""
	} else {
		res.ModelResponse = resp.Message.Content
	}

	// Detect reasoning via Thinking field.
	res.ReasoningUsed = resp != nil && strings.TrimSpace(resp.Message.Thinking) != ""

	// Score based on evaluation type.
	passed := scoreTest(test, res.ModelResponse)
	if passed != nil {
		res.Passed = passed
	}
	// For human_review, passed stays nil and human_rating stays empty.

	return res
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
		v := strings.Contains(strings.ToLower(response), strings.ToLower(cfg.Expected))
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
