// Package runner implements the test battery execution engine.
package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

// SubResult holds the detailed outcome and analytics of a single step or case within a test.
type SubResult struct {
	Index          int                `json:"index"`
	Name           string             `json:"name,omitempty"`
	Prompt         string             `json:"prompt,omitempty"`
	SystemPrompt   string             `json:"system_prompt,omitempty"`
	Options        *tests.TestOptions `json:"options,omitempty"`
	Passed         *bool              `json:"passed,omitempty"`
	ResponseTimeMs int64              `json:"response_time_ms"`
	TokensPerSec   float64            `json:"tokens_per_sec,omitempty"`
	PromptTokens   int                `json:"prompt_tokens,omitempty"`
	EvalTokens     int                `json:"eval_tokens,omitempty"`
	TotalTokens    int                `json:"total_tokens,omitempty"`
	ReasoningUsed  bool               `json:"reasoning_used"`
	ModelResponse  string             `json:"model_response,omitempty"`
	Error          string             `json:"error,omitempty"`
}

// TestResult holds the outcome of a single test for a single model.
type TestResult struct {
	TestID         string      `json:"test_id"`
	TestName       string      `json:"test_name"`
	Model          string      `json:"model"`
	Passed         *bool       `json:"passed,omitempty"`
	ResponseTimeMs int64       `json:"response_time_ms"`
	TokensPerSec   float64     `json:"tokens_per_sec,omitempty"`
	PromptTokens   int         `json:"prompt_tokens,omitempty"`
	EvalTokens     int         `json:"eval_tokens,omitempty"`
	TotalTokens    int         `json:"total_tokens,omitempty"`
	ReasoningUsed  bool        `json:"reasoning_used"`
	HumanRating    string      `json:"human_rating,omitempty"` // "bad", "regular", "good"
	ModelResponse  string      `json:"model_response,omitempty"`
	Error          string      `json:"error,omitempty"`
	SubResults     []SubResult `json:"sub_results,omitempty"`
}

// Progress tracks the current state of a battery run.
type Progress struct {
	RunID           string      `json:"run_id"`
	Model           string      `json:"model"`
	TestID          string      `json:"test_id"`
	TestName        string      `json:"test_name"`
	TestIndex       int         `json:"test_index"`
	TotalTests      int         `json:"total_tests"`
	CaseName        string      `json:"case_name,omitempty"`
	CaseIndex       int         `json:"case_index,omitempty"`
	TotalCases      int         `json:"total_cases,omitempty"`
	ActivePrompt    string      `json:"active_prompt,omitempty"`
	CompletedCases  []SubResult `json:"completed_cases,omitempty"`
	IsThinking      bool        `json:"is_thinking"`
	PartialResponse string      `json:"partial_response,omitempty"`
	PartialThinking string      `json:"partial_thinking,omitempty"`
	Done            bool        `json:"done"`
	Error           string      `json:"error,omitempty"`
}

// Client wraps an Ollama client and executes tests.
type Client struct {
	ollama     *ollama.Client
	progressMu sync.Mutex
	progress   map[string]*Progress
	cancelMu   sync.Mutex
	cancels    map[string]context.CancelFunc
}

// NewClient creates a runner client.
func NewClient(ollamaClient *ollama.Client) *Client {
	return &Client{
		ollama:   ollamaClient,
		progress: make(map[string]*Progress),
		cancels:  make(map[string]context.CancelFunc),
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

	// Filter active non-agent tests.
	var activeTests []tests.Test
	for _, t := range testsList {
		if group.ID != "" && group.ID != "all" && len(testsList) > 1 && t.GroupID != group.ID {
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

	runCtx, cancel := context.WithCancel(context.Background())
	c.cancelMu.Lock()
	c.cancels[run.ID] = cancel
	c.cancelMu.Unlock()

	go func() {
		defer func() {
			c.cancelMu.Lock()
			delete(c.cancels, run.ID)
			c.cancelMu.Unlock()
			if onComplete != nil {
				onComplete(run)
			}
		}()
		idx := 0
		var runErr string
		for _, model := range modelIDs {
			caps := modelCaps[model]
			for _, test := range activeTests {
				if !hasAllCaps(caps, test.RequiredCaps) {
					continue
				}
				idx++
				res := c.runTest(runCtx, run.ID, model, test, idx, total)
				run.Results = append(run.Results, res)
				if runCtx.Err() != nil {
					runErr = runCtx.Err().Error()
					break
				}
			}
			// Unload model from memory only after ALL tests for this model have completed.
			_ = c.ollama.Unload(runCtx, model)
			if runCtx.Err() != nil {
				break
			}
		}
		if runErr != "" {
			c.setProgress(Progress{RunID: run.ID, Done: true, Error: runErr, TotalTests: total})
		} else {
			c.setProgress(Progress{RunID: run.ID, Done: true, TotalTests: total})
		}
	}()

	return run.ID
}

// CancelRun cancels an active battery run by its ID.
func (c *Client) CancelRun(runID string) bool {
	c.cancelMu.Lock()
	cancel, ok := c.cancels[runID]
	c.cancelMu.Unlock()
	if ok && cancel != nil {
		cancel()
		return true
	}
	return false
}

type turnResult struct {
	Content            string
	Thinking           string
	TokensPerSec       float64
	PromptTokens       int
	EvalTokens         int
	TotalTokens        int
	ResponseTimeMs     int64
	PromptEvalDuration int64
	EvalDuration       int64
	Error              error
}

func (c *Client) runTest(ctx context.Context, runID string, model string, test tests.Test, idx, total int) TestResult {
	res := TestResult{
		TestID:   test.ID,
		TestName: test.Name,
		Model:    model,
	}

	if ctx.Err() != nil {
		res.Error = ctx.Err().Error()
		return res
	}

	start := time.Now()

	// Multi-step interactive sequential test
	if len(test.Steps) > 0 {
		var history []ollama.ChatMessage
		if test.SystemPrompt != "" {
			history = append(history, ollama.ChatMessage{Role: "system", Content: test.SystemPrompt})
		}

		allPassed := true
		hasScored := false
		var responsesSummary []string
		var totalEvalDuration int64
		stepOverrides := make([]string, len(test.Steps))
		stepOptOverrides := make([]*tests.TestOptions, len(test.Steps))
		for i, s := range test.Steps {
			stepOverrides[i] = s.SystemPrompt
			stepOptOverrides[i] = s.Options
		}
		effStepSys := effectiveChainSystems(test.SystemPrompt, stepOverrides)
		effStepOpts := effectiveChainOptions(test.Options, stepOptOverrides)

		for i, step := range test.Steps {
			if ctx.Err() != nil {
				res.Error = ctx.Err().Error()
				break
			}

			stepLabel := step.Name
			if stepLabel == "" {
				stepLabel = fmt.Sprintf("Step %d", step.Step)
			}
			if step.SystemPrompt != "" {
				history = setSystemPrompt(history, step.SystemPrompt)
			}

			c.setProgress(Progress{
				RunID:          runID,
				Model:          model,
				TestID:         test.ID,
				TestName:       test.Name,
				TestIndex:      idx,
				TotalTests:     total,
				CaseName:       stepLabel,
				CaseIndex:      i + 1,
				TotalCases:     len(test.Steps),
				ActivePrompt:   step.Prompt,
				CompletedCases: append([]SubResult(nil), res.SubResults...),
			})

			history = append(history, ollama.ChatMessage{Role: "user", Content: step.Prompt})
			turn := c.execChatTurn(ctx, runID, model, history, optsFor(effStepOpts[i]))
			if turn.Error != nil {
				res.Error = turn.Error.Error()
				break
			}

			if turn.Thinking != "" {
				res.ReasoningUsed = true
			}
			res.PromptTokens += turn.PromptTokens
			res.EvalTokens += turn.EvalTokens
			res.TotalTokens += turn.TotalTokens
			totalEvalDuration += turn.EvalDuration

			history = append(history, ollama.ChatMessage{Role: "assistant", Content: turn.Content})

			stepPassed := scoreEval(step.Evaluation, test.EvaluationType, test.EvaluationConfig, turn.Content)
			status := "PASS"
			if stepPassed != nil {
				hasScored = true
				if !*stepPassed {
					allPassed = false
					status = "FAIL"
				}
			} else {
				status = "REVIEW"
			}
			responsesSummary = append(responsesSummary, fmt.Sprintf("[%s] %s: %s", status, stepLabel, strings.TrimSpace(turn.Content)))

			res.SubResults = append(res.SubResults, SubResult{
				Index:          i + 1,
				Name:           stepLabel,
				Prompt:         step.Prompt,
				SystemPrompt:   effStepSys[i],
				Options:        effStepOpts[i],
				Passed:         stepPassed,
				ResponseTimeMs: turn.ResponseTimeMs,
				TokensPerSec:   turn.TokensPerSec,
				PromptTokens:   turn.PromptTokens,
				EvalTokens:     turn.EvalTokens,
				TotalTokens:    turn.TotalTokens,
				ReasoningUsed:  turn.Thinking != "",
				ModelResponse:  turn.Content,
			})
		}

		res.ResponseTimeMs = time.Since(start).Milliseconds()
		res.ModelResponse = strings.Join(responsesSummary, "\n\n")
		if totalEvalDuration > 0 && res.EvalTokens > 0 {
			res.TokensPerSec = float64(res.EvalTokens) / (float64(totalEvalDuration) / 1e9)
		}
		if hasScored && res.Error == "" {
			res.Passed = &allPassed
		}
		return res
	}

	// Multi-case test suite.
	// Each case runs in isolation (fresh conversation history) and may override
	// the test-level system prompt (empty = inherit) and inference options.
	// A case is either single-turn (prompt + evaluation) or multi-turn
	// (ordered steps sharing one history scoped to the case). When steps are
	// present, an optional case-level prompt is sent first as the opening turn
	// (scored with the case-level evaluation when set). Within a chain the
	// system is sticky: a step's system_prompt replaces the active system from
	// that step onward, an empty one keeps the active system. Options fold the
	// same way field by field over the active options.
	if len(test.Cases) > 0 {
		allPassed := true
		hasScored := false
		var casesSummary []string
		var totalEvalDuration int64

		totalUnits := 0
		for _, tc := range test.Cases {
			totalUnits += len(tc.Steps)
			if len(tc.Steps) == 0 || tc.Prompt != "" {
				totalUnits++
			}
		}
		unitIdx := 0

		// runCaseTurn executes one user turn inside the given history, scores it
		// and records the sub-result. It returns false when execution must stop.
		runCaseTurn := func(history []ollama.ChatMessage, unitName, prompt, sys string, eval *tests.Evaluation, effOpts *tests.TestOptions) ([]ollama.ChatMessage, bool) {
			if ctx.Err() != nil {
				res.Error = ctx.Err().Error()
				return history, false
			}
			unitIdx++
			c.setProgress(Progress{
				RunID:          runID,
				Model:          model,
				TestID:         test.ID,
				TestName:       test.Name,
				TestIndex:      idx,
				TotalTests:     total,
				CaseName:       unitName,
				CaseIndex:      unitIdx,
				TotalCases:     totalUnits,
				ActivePrompt:   prompt,
				CompletedCases: append([]SubResult(nil), res.SubResults...),
			})

			history = append(history, ollama.ChatMessage{Role: "user", Content: prompt})
			turn := c.execChatTurn(ctx, runID, model, history, optsFor(effOpts))
			if turn.Error != nil {
				res.Error = turn.Error.Error()
				return history, false
			}

			if turn.Thinking != "" {
				res.ReasoningUsed = true
			}
			res.PromptTokens += turn.PromptTokens
			res.EvalTokens += turn.EvalTokens
			res.TotalTokens += turn.TotalTokens
			totalEvalDuration += turn.EvalDuration

			history = append(history, ollama.ChatMessage{Role: "assistant", Content: turn.Content})

			passed := scoreEval(eval, test.EvaluationType, test.EvaluationConfig, turn.Content)
			status := "PASS"
			if passed != nil {
				hasScored = true
				if !*passed {
					allPassed = false
					status = "FAIL"
				}
			} else {
				status = "REVIEW"
			}
			casesSummary = append(casesSummary, fmt.Sprintf("[%s] %s: %s", status, unitName, strings.TrimSpace(turn.Content)))

			res.SubResults = append(res.SubResults, SubResult{
				Index:          unitIdx,
				Name:           unitName,
				Prompt:         prompt,
				SystemPrompt:   sys,
				Options:        effOpts,
				Passed:         passed,
				ResponseTimeMs: turn.ResponseTimeMs,
				TokensPerSec:   turn.TokensPerSec,
				PromptTokens:   turn.PromptTokens,
				EvalTokens:     turn.EvalTokens,
				TotalTokens:    turn.TotalTokens,
				ReasoningUsed:  turn.Thinking != "",
				ModelResponse:  turn.Content,
			})
			return history, true
		}

		for i, tc := range test.Cases {
			caseLabel := tc.Name
			if caseLabel == "" {
				caseLabel = fmt.Sprintf("Case %d", i+1)
			}
			caseSys := tests.EffectiveSystemPrompt(test.SystemPrompt, tc.SystemPrompt)
			caseOptsMerged := tests.MergeOptions(test.Options, tc.Options)

			var history []ollama.ChatMessage
			if caseSys != "" {
				history = append(history, ollama.ChatMessage{Role: "system", Content: caseSys})
			}

			if len(tc.Steps) == 0 {
				var ok bool
				if _, ok = runCaseTurn(history, caseLabel, tc.Prompt, caseSys, tc.Evaluation, caseOptsMerged); !ok {
					break
				}
				continue
			}

			// Multi-turn case: chained steps sharing one case-scoped history.
			// An optional case-level prompt is sent first as the opening turn
			// (scored with the case-level evaluation when set).
			if tc.Prompt != "" {
				var ok bool
				if history, ok = runCaseTurn(history, caseLabel+" › context", tc.Prompt, caseSys, tc.Evaluation, caseOptsMerged); !ok {
					break
				}
			}
			stepOverrides := make([]string, len(tc.Steps))
			stepOptOverrides := make([]*tests.TestOptions, len(tc.Steps))
			for j, st := range tc.Steps {
				stepOverrides[j] = st.SystemPrompt
				stepOptOverrides[j] = st.Options
			}
			effStepSys := effectiveChainSystems(caseSys, stepOverrides)
			effStepOpts := effectiveChainOptions(caseOptsMerged, stepOptOverrides)
			stopped := false
			for j, st := range tc.Steps {
				stepLabel := st.Name
				if stepLabel == "" {
					stepLabel = fmt.Sprintf("Step %d", j+1)
				}
				if st.SystemPrompt != "" {
					history = setSystemPrompt(history, st.SystemPrompt)
				}
				var ok bool
				if history, ok = runCaseTurn(history, caseLabel+" › "+stepLabel, st.Prompt, effStepSys[j], st.Evaluation, effStepOpts[j]); !ok {
					stopped = true
					break
				}
			}
			if stopped {
				break
			}
		}

		res.ResponseTimeMs = time.Since(start).Milliseconds()
		res.ModelResponse = strings.Join(casesSummary, "\n\n")
		if totalEvalDuration > 0 && res.EvalTokens > 0 {
			res.TokensPerSec = float64(res.EvalTokens) / (float64(totalEvalDuration) / 1e9)
		}
		if hasScored && res.Error == "" {
			res.Passed = &allPassed
		}
		return res
	}

	// Standard single prompt / messages test
	var messages []ollama.ChatMessage
	if len(test.Messages) > 0 {
		for _, m := range test.Messages {
			messages = append(messages, ollama.ChatMessage{
				Role:    m.Role,
				Content: m.Content,
				Images:  m.Images,
			})
		}
	} else {
		messages = []ollama.ChatMessage{
			{Role: "system", Content: test.SystemPrompt},
			{Role: "user", Content: test.Prompt},
		}
		if messages[0].Content == "" {
			messages = messages[1:]
		}
	}

	// Attach media
	var media []string
	for _, att := range test.Attachments {
		if att.Kind == "image" || att.Kind == "audio" {
			media = append(media, att.Data)
		}
	}
	if len(media) > 0 {
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == "user" {
				messages[i].Images = append(messages[i].Images, media...)
				break
			}
		}
	}

	promptText := test.Prompt
	if promptText == "" && len(messages) > 0 {
		promptText = messages[len(messages)-1].Content
	}
	c.setProgress(Progress{
		RunID:        runID,
		Model:        model,
		TestID:       test.ID,
		TestName:     test.Name,
		TestIndex:    idx,
		TotalTests:   total,
		CaseIndex:    1,
		TotalCases:   1,
		ActivePrompt: promptText,
	})

	turn := c.execChatTurn(ctx, runID, model, messages, optsFor(test.Options))
	res.ResponseTimeMs = turn.ResponseTimeMs
	if turn.Error != nil {
		res.Error = turn.Error.Error()
		return res
	}

	res.ModelResponse = turn.Content
	res.ReasoningUsed = turn.Thinking != ""
	res.TokensPerSec = turn.TokensPerSec
	res.PromptTokens = turn.PromptTokens
	res.EvalTokens = turn.EvalTokens
	res.TotalTokens = turn.TotalTokens

	passed := scoreEval(test.Evaluation, test.EvaluationType, test.EvaluationConfig, res.ModelResponse)
	if passed != nil {
		res.Passed = passed
	}

	return res
}

func (c *Client) execChatTurn(ctx context.Context, runID, model string, messages []ollama.ChatMessage, opts map[string]any) turnResult {
	req := ollama.ChatRequest{
		Model:    model,
		Messages: messages,
		Options:  opts,
		Stream:   true,
	}

	var fullContent strings.Builder
	var fullThinking strings.Builder
	var chunkMeta *ollama.ChatChunk
	isThinking := false
	start := time.Now()
	var chatErr error

retryLoop:
	for attempt := 0; attempt <= 3; attempt++ {
		if attempt > 0 {
			if loaded, psErr := c.isModelLoaded(ctx, model); psErr == nil && !loaded {
				select {
				case <-time.After(2 * time.Second):
				case <-ctx.Done():
					chatErr = ctx.Err()
					break retryLoop
				}
			}
			select {
			case <-time.After(3 * time.Second):
			case <-ctx.Done():
				chatErr = ctx.Err()
				break retryLoop
			}
			if chatErr != nil {
				break retryLoop
			}
			fullContent.Reset()
			fullThinking.Reset()
			chunkMeta = nil
			isThinking = false
		}

		chatErr = c.ollama.Chat(ctx, req, func(chunk ollama.ChatChunk) error {
			if chunk.Message.Content != "" {
				fullContent.WriteString(chunk.Message.Content)
			}
			if chunk.Message.Thinking != "" {
				fullThinking.WriteString(chunk.Message.Thinking)
			}
			content := fullContent.String()
			if strings.Contains(content, "<thinking>") || strings.Contains(content, "<stitching>") || strings.Contains(content, "<throat>") {
				isThinking = true
			}
			if isThinking && (strings.Contains(content, "</thinking>") || strings.Contains(content, "</stitching>") || strings.Contains(content, "</throat>")) {
				isThinking = false
			}
			c.updateProgressStream(runID, isThinking, content, fullThinking.String())
			if chunk.Done {
				chunkMeta = &chunk
			}
			return nil
		})

		if chatErr != nil {
			break
		}
		if strings.TrimSpace(fullContent.String()) != "" {
			break
		}
	}

	elapsed := time.Since(start).Milliseconds()
	res := turnResult{
		Content:        fullContent.String(),
		Thinking:       fullThinking.String(),
		ResponseTimeMs: elapsed,
		Error:          chatErr,
	}

	if chunkMeta != nil {
		res.PromptTokens = chunkMeta.PromptEvalCount
		res.EvalTokens = chunkMeta.EvalCount
		res.TotalTokens = chunkMeta.PromptEvalCount + chunkMeta.EvalCount
		res.PromptEvalDuration = chunkMeta.PromptEvalDuration
		res.EvalDuration = chunkMeta.EvalDuration
		if chunkMeta.EvalCount > 0 && chunkMeta.EvalDuration > 0 {
			res.TokensPerSec = float64(chunkMeta.EvalCount) / (float64(chunkMeta.EvalDuration) / 1e9)
		}
	}

	return res
}

func (c *Client) isModelLoaded(ctx context.Context, model string) (bool, error) {
	running, err := c.ollama.PS(ctx)
	if err != nil {
		return false, err
	}
	for _, rm := range running {
		if rm.Name == model || rm.Model == model {
			return true, nil
		}
	}
	return false, nil
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

// optsFor converts TestOptions into the Ollama request options map.
func optsFor(o *tests.TestOptions) map[string]any {
	if o == nil {
		return nil
	}
	opts := make(map[string]any)
	if o.Temperature != nil {
		opts["temperature"] = *o.Temperature
	}
	if o.TopP != nil {
		opts["top_p"] = *o.TopP
	}
	if o.MaxTokens != nil {
		opts["num_predict"] = *o.MaxTokens
	}
	return opts
}

// effectiveChainSystems resolves the sticky system prompt for each turn of a
// conversation chain. base is the system active before the first turn
// (test-level, or the case-level override); each non-empty override replaces
// the active system from its turn onward, an empty value keeps the active one.
func effectiveChainSystems(base string, overrides []string) []string {
	out := make([]string, len(overrides))
	active := base
	for i, o := range overrides {
		if o != "" {
			active = o
		}
		out[i] = active
	}
	return out
}

// effectiveChainOptions resolves the sticky inference options for each turn
// of a conversation chain. base is the options active before the first turn
// (test-level merged with the case-level override); each step's set fields
// replace the active ones from its turn onward, nil fields keep them.
func effectiveChainOptions(base *tests.TestOptions, overrides []*tests.TestOptions) []*tests.TestOptions {
	out := make([]*tests.TestOptions, len(overrides))
	active := base
	for i, o := range overrides {
		active = tests.MergeOptions(active, o)
		out[i] = active
	}
	return out
}

// setSystemPrompt sets (or adds) the system message at the head of history.
func setSystemPrompt(history []ollama.ChatMessage, sys string) []ollama.ChatMessage {
	if sys == "" {
		return history
	}
	for i := range history {
		if history[i].Role == "system" {
			history[i].Content = sys
			return history
		}
	}
	return append([]ollama.ChatMessage{{Role: "system", Content: sys}}, history...)
}

func scoreEval(eval *tests.Evaluation, defaultType string, defaultCfg json.RawMessage, response string) *bool {
	evalType := defaultType
	cfgBytes := defaultCfg
	var directExpected any
	var directPattern string
	var directSchema any

	if eval != nil {
		if eval.Type != "" {
			evalType = eval.Type
		}
		if len(eval.Config) > 0 {
			cfgBytes = eval.Config
		}
		directExpected = eval.Expected
		directPattern = eval.Pattern
		directSchema = eval.Schema
	}

	switch evalType {
	case "exact_match":
		expected := ""
		if s, ok := directExpected.(string); ok {
			expected = s
		} else if directExpected != nil {
			expected = fmt.Sprintf("%v", directExpected)
		} else if len(cfgBytes) > 0 {
			var cfg struct {
				Expected string `json:"expected"`
			}
			_ = json.Unmarshal(cfgBytes, &cfg)
			expected = cfg.Expected
		}
		v := strings.TrimSpace(response) == strings.TrimSpace(expected)
		return &v

	case "contains":
		v := containsText(response, resolveExpected(directExpected, cfgBytes))
		return &v

	case "not_contains":
		// Negation of contains. If pattern is set, the response must NOT
		// match the regex (this covers what RE2 lookahead would do).
		// Otherwise the expected substring must be absent. An empty
		// pattern/expected fails closed (misconfigured check).
		if directPattern != "" {
			re, err := regexp.Compile(directPattern)
			if err != nil {
				v := false
				return &v
			}
			v := !re.MatchString(response)
			return &v
		}
		expected := resolveExpected(directExpected, cfgBytes)
		if expected == "" {
			v := false
			return &v
		}
		v := !containsText(response, expected)
		return &v

	case "contains_list":
		var expectedList []string
		if list, ok := directExpected.([]any); ok {
			for _, it := range list {
				expectedList = append(expectedList, fmt.Sprintf("%v", it))
			}
		} else if list, ok := directExpected.([]string); ok {
			expectedList = list
		} else if len(cfgBytes) > 0 {
			var cfg struct {
				Expected []string `json:"expected"`
			}
			_ = json.Unmarshal(cfgBytes, &cfg)
			expectedList = cfg.Expected
		}
		normResponse := normalizeForContains(response)
		for _, exp := range expectedList {
			normExpected := normalizeForContains(exp)
			if strings.Contains(normExpected, "\n") || strings.Contains(normExpected, "\t") {
				if strings.Contains(strings.ToLower(stripWhitespace(normResponse)), strings.ToLower(stripWhitespace(normExpected))) {
					v := true
					return &v
				}
			} else {
				if strings.Contains(strings.ToLower(normResponse), strings.ToLower(normExpected)) {
					v := true
					return &v
				}
			}
		}
		v := false
		return &v

	case "regex":
		pattern := directPattern
		if pattern == "" && len(cfgBytes) > 0 {
			var cfg struct {
				Pattern string `json:"pattern"`
			}
			_ = json.Unmarshal(cfgBytes, &cfg)
			pattern = cfg.Pattern
		}
		if pattern == "" {
			v := false
			return &v
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			v := false
			return &v
		}
		v := re.MatchString(response)
		return &v

	case "json_schema":
		var cfg struct {
			Schema struct {
				Type     string   `json:"type"`
				Required []string `json:"required"`
				MinItems int      `json:"minItems"`
				MaxItems int      `json:"maxItems"`
				Items    struct {
					Type string `json:"type"`
				} `json:"items"`
			} `json:"schema"`
		}
		if directSchema != nil {
			b, _ := json.Marshal(map[string]any{"schema": directSchema})
			_ = json.Unmarshal(b, &cfg)
		} else if len(cfgBytes) > 0 {
			_ = json.Unmarshal(cfgBytes, &cfg)
		}
		var raw any
		if err := json.Unmarshal([]byte(response), &raw); err != nil {
			v := false
			return &v
		}
		switch cfg.Schema.Type {
		case "array":
			arr, ok := raw.([]any)
			if !ok {
				v := false
				return &v
			}
			if cfg.Schema.MinItems > 0 && len(arr) < cfg.Schema.MinItems {
				v := false
				return &v
			}
			if cfg.Schema.MaxItems > 0 && len(arr) > cfg.Schema.MaxItems {
				v := false
				return &v
			}
			if cfg.Schema.Items.Type == "string" {
				for _, item := range arr {
					if _, ok := item.(string); !ok {
						v := false
						return &v
					}
				}
			}
			v := true
			return &v
		default:
			obj, ok := raw.(map[string]any)
			if !ok {
				v := false
				return &v
			}
			for _, key := range cfg.Schema.Required {
				if _, ok := obj[key]; !ok {
					v := false
					return &v
				}
			}
			v := true
			return &v
		}

	case "all_of":
		// Every sub-evaluation must pass. Three-valued logic: a single
		// failure fails fast, an unscored sub (e.g. human_review) marks the
		// whole check as needing review when everything else passes, and an
		// empty list fails closed.
		subs := eval.Evaluations
		if len(subs) == 0 {
			v := false
			return &v
		}
		needsReview := false
		for _, sub := range subs {
			r := scoreEval(sub, "", nil, response)
			if r == nil {
				needsReview = true
				continue
			}
			if !*r {
				v := false
				return &v
			}
		}
		if needsReview {
			return nil
		}
		v := true
		return &v

	case "human_review":
		return nil

	default:
		v := false
		return &v
	}
}

// resolveExpected extracts the expected substring from a direct value,
// falling back to the JSON evaluation config.
func resolveExpected(directExpected any, cfgBytes json.RawMessage) string {
	if s, ok := directExpected.(string); ok {
		return s
	}
	if directExpected != nil {
		return fmt.Sprintf("%v", directExpected)
	}
	if len(cfgBytes) > 0 {
		var cfg struct {
			Expected string `json:"expected"`
		}
		_ = json.Unmarshal(cfgBytes, &cfg)
		return cfg.Expected
	}
	return ""
}

// containsText reports whether response contains expected, using the same
// normalization as the contains check (case-insensitive, formatting-tolerant).
func containsText(response, expected string) bool {
	normResponse := normalizeForContains(response)
	normExpected := normalizeForContains(expected)
	if strings.Contains(normExpected, "\n") || strings.Contains(normExpected, "\t") {
		normResponse = stripWhitespace(normResponse)
		normExpected = stripWhitespace(normExpected)
	}
	return strings.Contains(strings.ToLower(normResponse), strings.ToLower(normExpected))
}

// normalizeForContains strips LaTeX/markdown/JSON formatting so that
// e.g. \frac{3}{4} becomes 3/4 for easier substring matching.
func normalizeForContains(s string) string {
	// Handle \frac{a}{b} -> a/b
	s = regexp.MustCompile(`\\frac\{([^}]*)\}\{([^}]*)\}`).ReplaceAllString(s, "$1/$2")
	// Remove common markdown.
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "*", "")
	s = strings.ReplaceAll(s, "`", "")
	// Strip quotes so quoted responses don't fail contains checks.
	s = strings.ReplaceAll(s, `"`, "")
	s = strings.ReplaceAll(s, `'`, "")
	// Strip literal escaped newlines/tabs that appear in JSON/tool_call strings.
	s = strings.ReplaceAll(s, `\n`, "")
	s = strings.ReplaceAll(s, `\t`, "")
	// Strip tool_call wrappers so JSON-embedded code can be evaluated.
	s = strings.ReplaceAll(s, "<tool_call>", "")
	s = strings.ReplaceAll(s, "</tool_call>", "")
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
