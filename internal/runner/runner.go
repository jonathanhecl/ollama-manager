// Package runner implements the test battery execution engine.
package runner

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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
	Thinking       string             `json:"thinking,omitempty"`
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
	Thinking       string      `json:"thinking,omitempty"`
	Error          string      `json:"error,omitempty"`
	CasesTotal     int         `json:"cases_total"`
	CasesPassed    int         `json:"cases_passed"`
	Points         float64     `json:"points"`
	MaxPoints      float64     `json:"max_points"`
	Score          float64     `json:"score"`
	SubResults     []SubResult `json:"sub_results,omitempty"`
}

// Progress tracks the current state of a battery run.
type Progress struct {
	RunID           string       `json:"run_id"`
	Model           string       `json:"model"`
	Models          []string     `json:"models,omitempty"`
	GroupID         string       `json:"group_id,omitempty"`
	GroupName       string       `json:"group_name,omitempty"`
	TestID          string       `json:"test_id"`
	TestName        string       `json:"test_name"`
	TestIndex       int          `json:"test_index"`
	TotalTests      int          `json:"total_tests"`
	CaseName        string       `json:"case_name,omitempty"`
	CaseIndex       int          `json:"case_index,omitempty"`
	TotalCases      int          `json:"total_cases,omitempty"`
	ActivePrompt    string       `json:"active_prompt,omitempty"`
	CompletedCases  []SubResult  `json:"completed_cases,omitempty"`
	IsThinking      bool         `json:"is_thinking"`
	PartialResponse string       `json:"partial_response,omitempty"`
	PartialThinking string       `json:"partial_thinking,omitempty"`
	// Per-stage live counters (thinking vs response, never summed).
	// Streaming time in each stage, in milliseconds.
	ThinkingMs int64 `json:"thinking_ms,omitempty"`
	ResponseMs int64 `json:"response_ms,omitempty"`
	// Accumulated rune counts per stage (token estimates ≈ chars/4).
	ThinkingChars int `json:"thinking_chars,omitempty"`
	ResponseChars int `json:"response_chars,omitempty"`
	Done            bool         `json:"done"`
	WaitingReview   bool         `json:"waiting_review,omitempty"`
	PendingReviews  int          `json:"pending_reviews,omitempty"`
	Error           string       `json:"error,omitempty"`
	Results         []TestResult `json:"results,omitempty"`
}

// Client wraps an Ollama client and executes tests.
type Client struct {
	ollama      *ollama.Client
	progressMu  sync.Mutex
	progress    map[string]*Progress
	cancelMu    sync.Mutex
	cancels     map[string]context.CancelFunc
	testCancels map[string]context.CancelCauseFunc
	// modelCancels holds the outer per-test cancel funcs (one live per run).
	// SkipCurrentModel cancels both the inner (step/case) and the outer
	// cancel so the run jumps to the next model instead of retrying.
	modelCancels map[string]context.CancelCauseFunc
	abortMu      sync.Mutex
	// abortMode records a pending abort choice per run ("discard" or
	// "save-completed"), applied to the run before onComplete fires.
	abortMode map[string]string
	// runExpected tracks tests expected per model per run, used to keep
	// only fully-completed models on abort with "save-completed".
	runExpected map[string]map[string]int
	// stageLimitsMu guards stageLimits, the optional per-stage auto-skip
	// limits applied to every turn (see StageLimits).
	stageLimitsMu sync.RWMutex
	stageLimits   StageLimits
}

// StageLimits holds optional automatic skip conditions for battery turns.
// Each limit applies per stage (thinking or response) individually, never
// to their sum. Zero disables the condition.
type StageLimits struct {
	// MaxTokens caps the approximate tokens of a single stage. Counts are
	// estimated (~4 chars per token) because Ollama only reports exact
	// token counts when generation finishes. 0 = no limit.
	MaxTokens int
	// MaxSeconds caps the streaming time spent in a single stage.
	// 0 = no limit.
	MaxSeconds int
	// Mode combines the enabled conditions: "any" (default) fires when
	// either trips; "all" only fires when every enabled condition trips
	// at once. With a single enabled condition both modes behave the same.
	Mode string
}

// stageSkipDecision evaluates the auto-skip conditions for one stage and
// reports whether the turn must be skipped plus a human-readable reason.
// Tokens are approximate (~4 chars/token); elapsed is streaming time in
// the stage.
func (l StageLimits) stageSkipDecision(stage string, chars int, elapsedMs int64) (bool, string) {
	enabled := 0
	met := 0
	var reasons []string
	if l.MaxTokens > 0 {
		enabled++
		if t := chars / 4; t > l.MaxTokens {
			met++
			reasons = append(reasons, fmt.Sprintf("exceeded %d tokens (~%d)", l.MaxTokens, t))
		}
	}
	if l.MaxSeconds > 0 {
		enabled++
		if s := elapsedMs / 1000; s > int64(l.MaxSeconds) {
			met++
			reasons = append(reasons, fmt.Sprintf("exceeded %ds (%ds)", l.MaxSeconds, s))
		}
	}
	if enabled == 0 {
		return false, ""
	}
	if l.Mode == "all" {
		if met != enabled {
			return false, ""
		}
	} else if met == 0 {
		return false, ""
	}
	return true, fmt.Sprintf("auto-skipped: %s stage %s", stage, strings.Join(reasons, " and "))
}

// NewClient creates a runner client.
func NewClient(ollamaClient *ollama.Client) *Client {
	return &Client{
		ollama:       ollamaClient,
		progress:     make(map[string]*Progress),
		cancels:      make(map[string]context.CancelFunc),
		testCancels:  make(map[string]context.CancelCauseFunc),
		modelCancels: make(map[string]context.CancelCauseFunc),
		abortMode:    make(map[string]string),
		runExpected:  make(map[string]map[string]int),
	}
}

// SetStageLimits replaces the per-stage auto-skip limits used by future
// turns. It is safe for concurrent use.
func (c *Client) SetStageLimits(l StageLimits) {
	c.stageLimitsMu.Lock()
	defer c.stageLimitsMu.Unlock()
	c.stageLimits = l
}

// getStageLimits returns a snapshot of the current per-stage limits.
func (c *Client) getStageLimits() StageLimits {
	c.stageLimitsMu.RLock()
	defer c.stageLimitsMu.RUnlock()
	return c.stageLimits
}

// estimateStageTokens approximates token counts from text (~4 chars per
// token, rune-based). Same convention as estimateTextTokens in the server
// package. Used for live per-stage enforcement while streaming, when exact
// counts are not available yet.
func estimateStageTokens(s string) int {
	if s == "" {
		return 0
	}
	n := len([]rune(s))
	if n < 4 {
		return 1
	}
	return n / 4
}

func (c *Client) setProgress(p Progress) {
	c.progressMu.Lock()
	defer c.progressMu.Unlock()
	if existing, ok := c.progress[p.RunID]; ok && existing != nil {
		if p.Results == nil && len(existing.Results) > 0 {
			p.Results = existing.Results
		}
		if p.Models == nil && len(existing.Models) > 0 {
			p.Models = existing.Models
		}
	}
	c.progress[p.RunID] = &p
}

// SetProgressForTest sets progress state for unit testing.
func (c *Client) SetProgressForTest(p Progress) {
	c.setProgress(p)
}

func (c *Client) updateProgressResults(runID string, results []TestResult) {
	c.progressMu.Lock()
	defer c.progressMu.Unlock()
	if p, ok := c.progress[runID]; ok && p != nil {
		p.Results = append([]TestResult(nil), results...)
	}
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

// GetActiveProgress returns the progress of the currently active run, if any.
func (c *Client) GetActiveProgress() (Progress, bool) {
	c.progressMu.Lock()
	defer c.progressMu.Unlock()
	for _, p := range c.progress {
		if p != nil && (!p.Done || p.WaitingReview) {
			return *p, true
		}
	}
	return Progress{}, false
}

// HasActiveRun returns true if there is a battery run currently in progress or awaiting human review.
func (c *Client) HasActiveRun() bool {
	_, ok := c.GetActiveProgress()
	return ok
}

// RateReviewResult records a rating or verdict for a test in progress and updates
// the pending reviews count. If no pending reviews remain, it marks the run Done: true
// and WaitingReview: false.
func (c *Client) RateReviewResult(runID, testID, model string, passed bool) (remaining int, wasWaiting bool) {
	c.progressMu.Lock()
	defer c.progressMu.Unlock()
	p, ok := c.progress[runID]
	if !ok || p == nil {
		return 0, false
	}
	wasWaiting = p.WaitingReview
	pending := 0
	for i := range p.Results {
		if p.Results[i].TestID == testID && p.Results[i].Model == model {
			p.Results[i].Passed = &passed
			if passed {
				p.Results[i].HumanRating = "good"
			} else {
				p.Results[i].HumanRating = "bad"
			}
		}
		if p.Results[i].Passed == nil && p.Results[i].Error == "" {
			pending++
		}
	}
	p.PendingReviews = pending
	if pending == 0 && p.WaitingReview {
		p.WaitingReview = false
		p.Done = true
	}
	return pending, wasWaiting
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
	expectedByModel := make(map[string]int, len(modelIDs))
	for _, model := range modelIDs {
		caps := modelCaps[model]
		for _, test := range activeTests {
			if hasAllCaps(caps, test.RequiredCaps) {
				total++
				expectedByModel[model]++
			}
		}
	}
	c.setRunExpected(run.ID, expectedByModel)

	c.setProgress(Progress{RunID: run.ID, TotalTests: total, GroupID: group.ID, GroupName: group.Name, Models: append([]string(nil), run.Models...)})

	runCtx, cancel := context.WithCancel(context.Background())
	c.cancelMu.Lock()
	c.cancels[run.ID] = cancel
	c.cancelMu.Unlock()

	go func() {
		defer func() {
			c.cancelMu.Lock()
			delete(c.cancels, run.ID)
			c.cancelMu.Unlock()
			c.applyAbortMode(run)
			if onComplete != nil {
				onComplete(run)
			}
		}()
		idx := 0
		var runErr string
	runModels:
		for _, model := range modelIDs {
			caps := modelCaps[model]
			for _, test := range activeTests {
				if !hasAllCaps(caps, test.RequiredCaps) {
					continue
				}
		idx++
		skipModel := false
		for {
			testCtx, testCancel := context.WithCancelCause(runCtx)
			c.setTestCancel(run.ID, testCancel)
			c.setModelCancel(run.ID, testCancel)
			res := c.runTest(testCtx, run.ID, model, test, idx, total)
			retry := errors.Is(context.Cause(testCtx), errManualRetry)
			if errors.Is(context.Cause(testCtx), errManualSkipModel) {
				skipModel = true
			}
			testCancel(nil)
			c.clearTestCancel(run.ID)
			c.clearModelCancel(run.ID)
			if retry {
				// Manual retry: discard the cancelled attempt and re-run the
				// same test from scratch (same idx, nothing appended).
				// Step/case-level retries are handled inside runTest;
				// reaching here with a retry cause means a whole-test retry
				// (single-prompt tests or a retry that landed outside any
				// step/case attempt).
				continue
			}
			run.Results = append(run.Results, res)
			c.updateProgressResults(run.ID, run.Results)
			break
		}
				if skipModel {
					// Manual model skip: the interrupted test was recorded
					// above; drop this model's remaining tests and continue
					// with the next model.
					break
				}
				if runCtx.Err() != nil {
					runErr = runCtx.Err().Error()
					break
				}
			}
			// Unload model from memory only after ALL tests for this model have completed.
			_ = c.ollama.Unload(runCtx, model)
			if runCtx.Err() != nil {
				break runModels
			}
		}
		pendingReviews := 0
		for _, res := range run.Results {
			if res.Passed == nil && res.Error == "" {
				pendingReviews++
			}
		}

		if runErr != "" {
			c.setProgress(Progress{
				RunID:      run.ID,
				Done:       true,
				Error:      runErr,
				TotalTests: total,
				Results:    run.Results,
				Models:     run.Models,
				GroupID:    run.GroupID,
				GroupName:  run.GroupName,
			})
		} else if pendingReviews > 0 {
			c.setProgress(Progress{
				RunID:          run.ID,
				Done:           false,
				WaitingReview:  true,
				PendingReviews: pendingReviews,
				TotalTests:     total,
				TestIndex:      total,
				Results:        run.Results,
				Models:         run.Models,
				GroupID:        run.GroupID,
				GroupName:      run.GroupName,
			})
		} else {
			c.setProgress(Progress{
				RunID:      run.ID,
				Done:       true,
				TotalTests: total,
				Results:    run.Results,
				Models:     run.Models,
				GroupID:    run.GroupID,
				GroupName:  run.GroupName,
			})
		}
	}()

	return run.ID
}

// CancelRun cancels an active battery run by its ID.
func (c *Client) CancelRun(runID string) bool {
	c.progressMu.Lock()
	if p, ok := c.progress[runID]; ok && p != nil && p.WaitingReview {
		p.WaitingReview = false
		p.Done = true
		c.progressMu.Unlock()
		return true
	}
	c.progressMu.Unlock()

	c.cancelMu.Lock()
	cancel, ok := c.cancels[runID]
	testCancel, hasTest := c.testCancels[runID]
	c.cancelMu.Unlock()
	if hasTest && testCancel != nil {
		testCancel(context.Canceled)
	}
	if ok && cancel != nil {
		cancel()
		return true
	}
	return false
}

func (c *Client) setTestCancel(runID string, cancel context.CancelCauseFunc) {
	c.cancelMu.Lock()
	defer c.cancelMu.Unlock()
	c.testCancels[runID] = cancel
}

func (c *Client) clearTestCancel(runID string) {
	c.cancelMu.Lock()
	defer c.cancelMu.Unlock()
	delete(c.testCancels, runID)
}

// RetryCurrentTest cancels the currently executing case (or step) of an active
// battery run so it restarts from the beginning. It mirrors SkipCurrentTest
// but uses the retry cause, which the run loops interpret as "discard this
// attempt and run it again" instead of "record and move on".
func (c *Client) RetryCurrentTest(runID string) bool {
	c.cancelMu.Lock()
	cancel, ok := c.testCancels[runID]
	c.cancelMu.Unlock()
	if ok && cancel != nil {
		cancel(errManualRetry)
		return true
	}
	return false
}

// SkipCurrentTest skips the currently executing case (or step) of an active
// battery run, recording it as skipped and continuing with the next case
// instead of aborting the whole test.
func (c *Client) SkipCurrentTest(runID string) bool {
	c.cancelMu.Lock()
	cancel, ok := c.testCancels[runID]
	c.cancelMu.Unlock()
	if ok && cancel != nil {
		cancel(errManualSkip)
		return true
	}
	return false
}

// SkipCurrentModel aborts the currently executing test and all remaining
// tests of the active model, continuing with the next model. Results
// completed so far (including the interrupted test, recorded as skipped)
// are kept.
func (c *Client) SkipCurrentModel(runID string) bool {
	c.cancelMu.Lock()
	inner, hasInner := c.testCancels[runID]
	outer, hasOuter := c.modelCancels[runID]
	c.cancelMu.Unlock()
	ok := false
	if hasInner && inner != nil {
		inner(errManualSkipModel)
		ok = true
	}
	if hasOuter && outer != nil {
		outer(errManualSkipModel)
		ok = true
	}
	return ok
}

// AbortRun cancels an active battery run, choosing what happens to the
// partial results. mode "discard" drops everything; mode "save-completed"
// keeps only results of models that completed all their expected tests.
// Returns false when there is no active run to abort.
func (c *Client) AbortRun(runID, mode string) bool {
	if mode != "save-completed" {
		mode = "discard"
	}
	c.abortMu.Lock()
	c.abortMode[runID] = mode
	c.abortMu.Unlock()
	return c.CancelRun(runID)
}

func (c *Client) setModelCancel(runID string, cancel context.CancelCauseFunc) {
	c.cancelMu.Lock()
	defer c.cancelMu.Unlock()
	c.modelCancels[runID] = cancel
}

func (c *Client) clearModelCancel(runID string) {
	c.cancelMu.Lock()
	defer c.cancelMu.Unlock()
	delete(c.modelCancels, runID)
}

func (c *Client) setRunExpected(runID string, expected map[string]int) {
	c.abortMu.Lock()
	defer c.abortMu.Unlock()
	c.runExpected[runID] = expected
}

// applyAbortMode rewrites the finished run according to a pending abort
// choice (if any) and releases per-run bookkeeping. Runs without an abort
// choice are left untouched.
func (c *Client) applyAbortMode(run *BatteryRun) {
	c.abortMu.Lock()
	mode := c.abortMode[run.ID]
	delete(c.abortMode, run.ID)
	expected := c.runExpected[run.ID]
	delete(c.runExpected, run.ID)
	c.abortMu.Unlock()

	switch mode {
	case "discard":
		run.Results = nil
		run.Models = nil
	case "save-completed":
		counts := make(map[string]int, len(run.Results))
		for _, res := range run.Results {
			counts[res.Model]++
		}
		kept := run.Results[:0]
		for _, res := range run.Results {
			if counts[res.Model] >= expected[res.Model] {
				kept = append(kept, res)
			}
		}
		// Clear the tail so dropped results are not retained.
		for i := len(kept); i < len(run.Results); i++ {
			run.Results[i] = TestResult{}
		}
		run.Results = kept
		models := run.Models[:0]
		for _, m := range run.Models {
			if counts[m] >= expected[m] {
				models = append(models, m)
			}
		}
		run.Models = models
	}
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

// splitCaseMedia separates a case/step's attachments into image payloads
// (image + audio kinds, sent via the message Images field) and text document
// blocks (inlined into the prompt, since Ollama has no document input).
func splitCaseMedia(atts []tests.Attachment) (media []string, textBlocks []string) {
	for _, att := range atts {
		switch att.Kind {
		case "image", "audio":
			if att.Data != "" {
				media = append(media, att.Data)
			}
		case "text", "file":
			if txt, ok := decodeSidecarText(att); ok {
				textBlocks = append(textBlocks, "--- attached file: "+att.Name+" ---\n"+txt)
			}
		}
	}
	return media, textBlocks
}

// decodeSidecarText best-effort decodes a text attachment (base64 content,
// raw text fallback).
func decodeSidecarText(att tests.Attachment) (string, bool) {
	if att.Data == "" {
		return "", false
	}
	if b, err := base64.StdEncoding.DecodeString(att.Data); err == nil {
		if utf8.Valid(b) {
			return string(b), true
		}
		return "", false
	}
	if utf8.ValidString(att.Data) {
		return att.Data, true
	}
	return "", false
}

// applyCaseMedia merges a case/step's attachments into its prompt: text
// documents are inlined, images/audio are returned for the Images field.
func applyCaseMedia(prompt string, atts []tests.Attachment) (string, []string) {
	media, blocks := splitCaseMedia(atts)
	if len(blocks) > 0 {
		prompt += "\n\n" + strings.Join(blocks, "\n\n")
	}
	return prompt, media
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
		anySkippedOrLoop := false
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

			// Snapshot everything this step may mutate, so a manual
			// retry re-executes THIS step with the same history prefix
			// (previous steps are kept, the cancelled attempt is
			// discarded). Mirrors the per-case retry logic below.
			snapHistLen := len(history)
			snapSubLen := len(res.SubResults)
			snapSummaryLen := len(responsesSummary)
			snapEvalDuration := totalEvalDuration
			snapPromptTokens := res.PromptTokens
			snapEvalTokens := res.EvalTokens
			snapTotalTokens := res.TotalTokens
			snapReasoning := res.ReasoningUsed
			snapHasScored := hasScored
			snapAllPassed := allPassed
			snapSkippedOrLoop := anySkippedOrLoop
			snapResError := res.Error

			stepFailed := false
			for {
				if ctx.Err() != nil {
					res.Error = ctx.Err().Error()
					stepFailed = true
					break
				}

				c.setProgress(Progress{
					RunID:          runID,
					Model:          model,
					GroupID:        test.GroupID,
					GroupName:      test.GroupID,
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

				stepPrompt, stepMedia := applyCaseMedia(step.Prompt, step.Attachments)
				stepMsg := ollama.ChatMessage{Role: "user", Content: stepPrompt}
				if len(stepMedia) > 0 {
					stepMsg.Images = stepMedia
				}
				history = append(history, stepMsg)
				stepCtx, stepCancel := context.WithCancelCause(ctx)
				c.setTestCancel(runID, stepCancel)
				turn := c.execChatTurn(stepCtx, runID, model, history, optsFor(effStepOpts[i]), thinkFor(effStepOpts[i]))
				retry := errors.Is(context.Cause(stepCtx), errManualRetry)
				// A stage-limit auto-skip behaves like a manual case skip:
				// record the step as skipped and continue with the next
				// step instead of aborting the whole test.
				skipCase := errors.Is(context.Cause(stepCtx), errManualSkip) || errors.Is(turn.Error, errAutoSkipStage)
				skipModel := errors.Is(context.Cause(stepCtx), errManualSkipModel)
				stepCancel(nil)
				c.clearTestCancel(runID)
				if retry {
					// Manual retry: drop the cancelled attempt and run
					// the same step again from its snapshot.
					history = history[:snapHistLen]
					res.SubResults = res.SubResults[:snapSubLen]
					responsesSummary = responsesSummary[:snapSummaryLen]
					totalEvalDuration = snapEvalDuration
					res.PromptTokens = snapPromptTokens
					res.EvalTokens = snapEvalTokens
					res.TotalTokens = snapTotalTokens
					res.ReasoningUsed = snapReasoning
					hasScored = snapHasScored
					allPassed = snapAllPassed
					anySkippedOrLoop = snapSkippedOrLoop
					res.Error = snapResError
					continue
				}
				if skipModel {
					history = history[:snapHistLen]
					falseVal := false
					allPassed = false
					anySkippedOrLoop = true
					res.SubResults = append(res.SubResults, SubResult{
						Index:  i + 1,
						Name:   stepLabel,
						Prompt: step.Prompt,
						Passed: &falseVal,
						Error:  "manually skipped",
					})
					responsesSummary = append(responsesSummary, fmt.Sprintf("[SKIP] %s: (Error: manually skipped)", stepLabel))
					stepFailed = true
					break
				}
				if turn.Error != nil {
					history = history[:snapHistLen]
					if skipCase {
						// Skip Case: record this step as skipped and
						// continue with the next step instead of
						// aborting the whole test.
						falseVal := false
						allPassed = false
						anySkippedOrLoop = true
						errStr := "manually skipped"
						if errors.Is(turn.Error, errAutoSkipStage) {
							errStr = turn.Error.Error()
						}
						res.SubResults = append(res.SubResults, SubResult{
							Index:          i + 1,
							Name:           stepLabel,
							Prompt:         step.Prompt,
							SystemPrompt:   effStepSys[i],
							Options:        effStepOpts[i],
							Passed:         &falseVal,
							ResponseTimeMs: turn.ResponseTimeMs,
							TokensPerSec:   turn.TokensPerSec,
							PromptTokens:   turn.PromptTokens,
							EvalTokens:     turn.EvalTokens,
							TotalTokens:    turn.TotalTokens,
							ReasoningUsed:  turn.Thinking != "",
							ModelResponse:  turn.Content,
							Thinking:       turn.Thinking,
							Error:          errStr,
						})
						responsesSummary = append(responsesSummary, fmt.Sprintf("[SKIP] %s: (Error: %s)", stepLabel, errStr))
						break
					}
					res.Error = turn.Error.Error()
					falseVal := false
					allPassed = false
					if isLoopOrSkip(turn.Error.Error()) {
						anySkippedOrLoop = true
					}
					res.SubResults = append(res.SubResults, SubResult{
						Index:          i + 1,
						Name:           stepLabel,
						Prompt:         step.Prompt,
						SystemPrompt:   effStepSys[i],
						Options:        effStepOpts[i],
						Passed:         &falseVal,
						ResponseTimeMs: turn.ResponseTimeMs,
						TokensPerSec:   turn.TokensPerSec,
						PromptTokens:   turn.PromptTokens,
						EvalTokens:     turn.EvalTokens,
						TotalTokens:    turn.TotalTokens,
						ReasoningUsed:  turn.Thinking != "",
						ModelResponse:  turn.Content,
						Thinking:       turn.Thinking,
						Error:          turn.Error.Error(),
					})
					responsesSummary = append(responsesSummary, fmt.Sprintf("[FAIL] %s: %s (Error: %s)", stepLabel, strings.TrimSpace(turn.Content), turn.Error.Error()))
					stepFailed = true
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
					Thinking:       turn.Thinking,
				})
				break
			}
			if stepFailed {
				break
			}
		}

		res.ResponseTimeMs = time.Since(start).Milliseconds()
		res.ModelResponse = strings.Join(responsesSummary, "\n\n")
		if totalEvalDuration > 0 && res.EvalTokens > 0 {
			res.TokensPerSec = float64(res.EvalTokens) / (float64(totalEvalDuration) / 1e9)
		}
		if isLoopOrSkip(res.Error) || anySkippedOrLoop {
			falseVal := false
			res.Passed = &falseVal
		} else if hasScored && res.Error == "" {
			res.Passed = &allPassed
		}
		res.CasesTotal = 1
		if res.Passed != nil && *res.Passed {
			res.CasesPassed = 1
			res.Points = 2.0 // 1 case + 1 bonus
		} else {
			res.CasesPassed = 0
			res.Points = 0.0
		}
		res.MaxPoints = 2.0
		res.Score = math.Min(100.0, math.Max(0.0, (res.Points/res.MaxPoints)*100.0))
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
		anySkippedOrLoop := false
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

		type caseTurnOutcome int
		const (
			caseTurnPass caseTurnOutcome = iota
			caseTurnSkip
			caseTurnFail
			caseTurnAbort
		)

		// runCaseTurn executes one user turn inside the given history, scores it
		// and records the sub-result. It handles retry and skip specifically for
		// this subcase without rolling back or aborting prior/subsequent subcases.
		runCaseTurn := func(history []ollama.ChatMessage, unitName, prompt string, attachments []tests.Attachment, sys string, eval *tests.Evaluation, effOpts *tests.TestOptions) ([]ollama.ChatMessage, caseTurnOutcome) {
			if ctx.Err() != nil {
				res.Error = ctx.Err().Error()
				return history, caseTurnAbort
			}
			unitIdx++
			snapHistLen := len(history)
			snapSubLen := len(res.SubResults)
			snapSummaryLen := len(casesSummary)
			snapEvalDuration := totalEvalDuration
			snapPromptTokens := res.PromptTokens
			snapEvalTokens := res.EvalTokens
			snapTotalTokens := res.TotalTokens
			snapReasoning := res.ReasoningUsed
			snapHasScored := hasScored
			snapAllPassed := allPassed
			snapSkippedOrLoop := anySkippedOrLoop
			snapResError := res.Error

			for {
				if ctx.Err() != nil {
					res.Error = ctx.Err().Error()
					return history, caseTurnAbort
				}
				c.setProgress(Progress{
					RunID:          runID,
					Model:          model,
					GroupID:        test.GroupID,
					GroupName:      test.GroupID,
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

				casePrompt, caseMedia := applyCaseMedia(prompt, attachments)
				caseMsg := ollama.ChatMessage{Role: "user", Content: casePrompt}
				if len(caseMedia) > 0 {
					caseMsg.Images = caseMedia
				}
				history = append(history, caseMsg)
				turnCtx, turnCancel := context.WithCancelCause(ctx)
				c.setTestCancel(runID, turnCancel)
				turn := c.execChatTurn(turnCtx, runID, model, history, optsFor(effOpts), thinkFor(effOpts))
				retry := errors.Is(context.Cause(turnCtx), errManualRetry)
				skipCase := errors.Is(context.Cause(turnCtx), errManualSkip) || errors.Is(turn.Error, errAutoSkipStage)
				skipModel := errors.Is(context.Cause(turnCtx), errManualSkipModel)
				turnCancel(nil)
				c.clearTestCancel(runID)

				if retry {
					// Manual retry: drop this turn attempt and run THIS subcase again.
					history = history[:snapHistLen]
					res.SubResults = res.SubResults[:snapSubLen]
					casesSummary = casesSummary[:snapSummaryLen]
					totalEvalDuration = snapEvalDuration
					res.PromptTokens = snapPromptTokens
					res.EvalTokens = snapEvalTokens
					res.TotalTokens = snapTotalTokens
					res.ReasoningUsed = snapReasoning
					hasScored = snapHasScored
					allPassed = snapAllPassed
					anySkippedOrLoop = snapSkippedOrLoop
					res.Error = snapResError
					continue
				}

				if skipModel {
					history = history[:snapHistLen]
					falseVal := false
					allPassed = false
					anySkippedOrLoop = true
					res.SubResults = append(res.SubResults, SubResult{
						Index:          unitIdx,
						Name:           unitName,
						Prompt:         prompt,
						SystemPrompt:   sys,
						Options:        effOpts,
						Passed:         &falseVal,
						ResponseTimeMs: turn.ResponseTimeMs,
						TokensPerSec:   turn.TokensPerSec,
						PromptTokens:   turn.PromptTokens,
						EvalTokens:     turn.EvalTokens,
						TotalTokens:    turn.TotalTokens,
						ReasoningUsed:  turn.Thinking != "",
						ModelResponse:  turn.Content,
						Thinking:       turn.Thinking,
						Error:          "manually skipped",
					})
					casesSummary = append(casesSummary, fmt.Sprintf("[SKIP] %s: (Error: manually skipped)", unitName))
					return history, caseTurnAbort
				}

				if turn.Error != nil {
					history = history[:snapHistLen]
					if ctx.Err() != nil {
						res.Error = ctx.Err().Error()
						return history, caseTurnAbort
					}
					if skipCase {
						falseVal := false
						allPassed = false
						anySkippedOrLoop = true
						errStr := "manually skipped"
						if errors.Is(turn.Error, errAutoSkipStage) {
							errStr = turn.Error.Error()
						}
						res.SubResults = append(res.SubResults, SubResult{
							Index:          unitIdx,
							Name:           unitName,
							Prompt:         prompt,
							SystemPrompt:   sys,
							Options:        effOpts,
							Passed:         &falseVal,
							ResponseTimeMs: turn.ResponseTimeMs,
							TokensPerSec:   turn.TokensPerSec,
							PromptTokens:   turn.PromptTokens,
							EvalTokens:     turn.EvalTokens,
							TotalTokens:    turn.TotalTokens,
							ReasoningUsed:  turn.Thinking != "",
							ModelResponse:  turn.Content,
							Thinking:       turn.Thinking,
							Error:          errStr,
						})
						casesSummary = append(casesSummary, fmt.Sprintf("[SKIP] %s: (Error: %s)", unitName, errStr))
						return history, caseTurnSkip
					}

					falseVal := false
					allPassed = false
					if isLoopOrSkip(turn.Error.Error()) {
						anySkippedOrLoop = true
					}
					casesSummary = append(casesSummary, fmt.Sprintf("[FAIL] %s: %s (Error: %s)", unitName, strings.TrimSpace(turn.Content), turn.Error.Error()))
					res.SubResults = append(res.SubResults, SubResult{
						Index:          unitIdx,
						Name:           unitName,
						Prompt:         prompt,
						SystemPrompt:   sys,
						Options:        effOpts,
						Passed:         &falseVal,
						ResponseTimeMs: turn.ResponseTimeMs,
						TokensPerSec:   turn.TokensPerSec,
						PromptTokens:   turn.PromptTokens,
						EvalTokens:     turn.EvalTokens,
						TotalTokens:    turn.TotalTokens,
						ReasoningUsed:  turn.Thinking != "",
						ModelResponse:  turn.Content,
						Thinking:       turn.Thinking,
						Error:          turn.Error.Error(),
					})
					return history, caseTurnFail
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
					Thinking:       turn.Thinking,
				})
				return history, caseTurnPass
			}
		}

		casePassedFlags := make([]bool, len(test.Cases))
		for i, tc := range test.Cases {
			if ctx.Err() != nil {
				res.Error = ctx.Err().Error()
				break
			}

			caseLabel := tc.Name
			if caseLabel == "" {
				caseLabel = fmt.Sprintf("Case %d", i+1)
			}
			caseSys := tests.EffectiveSystemPrompt(test.SystemPrompt, tc.SystemPrompt)
			caseOptsMerged := tests.MergeOptions(test.Options, tc.Options)

			subStart := len(res.SubResults)
			var history []ollama.ChatMessage
			if caseSys != "" {
				history = append(history, ollama.ChatMessage{Role: "system", Content: caseSys})
			}

			if len(tc.Steps) == 0 {
				_, outcome := runCaseTurn(history, caseLabel, tc.Prompt, tc.Attachments, caseSys, tc.Evaluation, caseOptsMerged)
				if outcome == caseTurnAbort {
					break
				}
				subEnd := len(res.SubResults)
				thisCaseOK := subEnd > subStart
				for sIdx := subStart; sIdx < subEnd; sIdx++ {
					sub := res.SubResults[sIdx]
					if sub.Error != "" || sub.Passed == nil || !*sub.Passed {
						thisCaseOK = false
						break
					}
				}
				casePassedFlags[i] = thisCaseOK
				continue
			}

			// Multi-turn case: chained steps sharing one case-scoped history.
			// An optional case-level prompt is sent first as the opening turn
			// (scored with the case-level evaluation when set).
			stopped := false
			var lastStepIdx int = -1
			if tc.Prompt != "" {
				var outcome caseTurnOutcome
				history, outcome = runCaseTurn(history, caseLabel+" › context", tc.Prompt, tc.Attachments, caseSys, tc.Evaluation, caseOptsMerged)
				if outcome == caseTurnAbort {
					break
				}
				if outcome == caseTurnFail {
					stopped = true
				}
			}

			if !stopped {
				stepOverrides := make([]string, len(tc.Steps))
				stepOptOverrides := make([]*tests.TestOptions, len(tc.Steps))
				for j, st := range tc.Steps {
					stepOverrides[j] = st.SystemPrompt
					stepOptOverrides[j] = st.Options
				}
				effStepSys := effectiveChainSystems(caseSys, stepOverrides)
				effStepOpts := effectiveChainOptions(caseOptsMerged, stepOptOverrides)
				for j, st := range tc.Steps {
					lastStepIdx = j
					stepLabel := st.Name
					if stepLabel == "" {
						stepLabel = fmt.Sprintf("Step %d", j+1)
					}
					turnLabel := caseLabel + " › " + stepLabel
					if len(tc.Steps) == 1 && tc.Prompt == "" && (st.Name == "" || st.Name == fmt.Sprintf("Step %d", j+1)) {
						turnLabel = caseLabel
					}
					if st.SystemPrompt != "" {
						history = setSystemPrompt(history, st.SystemPrompt)
					}
					var outcome caseTurnOutcome
					history, outcome = runCaseTurn(history, turnLabel, st.Prompt, st.Attachments, effStepSys[j], st.Evaluation, effStepOpts[j])
					if outcome == caseTurnAbort {
						stopped = true
						break
					}
					if outcome == caseTurnFail {
						stopped = true
						break
					}
					// If outcome == caseTurnSkip: this step was skipped, but we DO NOT stop the case!
					// We continue with the next step!
				}
			}

			if ctx.Err() != nil {
				res.Error = ctx.Err().Error()
				break
			}

			if stopped && lastStepIdx >= 0 && lastStepIdx < len(tc.Steps) {
				remainingErr := "skipped due to case failure"
				for nextJ := lastStepIdx + 1; nextJ < len(tc.Steps); nextJ++ {
					st := tc.Steps[nextJ]
					stepLabel := st.Name
					if stepLabel == "" {
						stepLabel = fmt.Sprintf("Step %d", nextJ+1)
					}
					turnLabel := caseLabel + " › " + stepLabel
					unitIdx++
					falseVal := false
					res.SubResults = append(res.SubResults, SubResult{
						Index:  unitIdx,
						Name:   turnLabel,
						Prompt: st.Prompt,
						Passed: &falseVal,
						Error:  remainingErr,
					})
					casesSummary = append(casesSummary, fmt.Sprintf("[SKIP] %s: (Error: %s)", turnLabel, remainingErr))
				}
			}

			subEnd := len(res.SubResults)
			thisCaseOK := subEnd > subStart
			for sIdx := subStart; sIdx < subEnd; sIdx++ {
				sub := res.SubResults[sIdx]
				if sub.Error != "" || sub.Passed == nil || !*sub.Passed {
					thisCaseOK = false
					break
				}
			}
			casePassedFlags[i] = thisCaseOK
		}

		res.ResponseTimeMs = time.Since(start).Milliseconds()
		res.ModelResponse = strings.Join(casesSummary, "\n\n")
		if totalEvalDuration > 0 && res.EvalTokens > 0 {
			res.TokensPerSec = float64(res.EvalTokens) / (float64(totalEvalDuration) / 1e9)
		}
		if ctx.Err() != nil {
			res.Error = ctx.Err().Error()
		}
		if isLoopOrSkip(res.Error) || anySkippedOrLoop {
			falseVal := false
			res.Passed = &falseVal
		} else if hasScored && res.Error == "" {
			res.Passed = &allPassed
		}
		casesTotal := len(test.Cases)
		casesPassed := 0
		for _, ok := range casePassedFlags {
			if ok {
				casesPassed++
			}
		}
		res.CasesTotal = casesTotal
		res.CasesPassed = casesPassed
		bonus := 0.0
		if allPassed && hasScored && res.Error == "" && !anySkippedOrLoop && casesPassed == casesTotal {
			bonus = 1.0
		}
		res.Points = float64(casesPassed) + bonus
		res.MaxPoints = float64(casesTotal) + 1.0
		if res.MaxPoints > 0 {
			res.Score = math.Min(100.0, math.Max(0.0, (res.Points/res.MaxPoints)*100.0))
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

	// Attach sidecars (simple tests only): images/audio go to Images[],
	// text documents are appended to the user prompt.
	sidecarMedia, sidecarBlocks := splitCaseMedia(test.Sidecars)
	if len(sidecarMedia) > 0 || len(sidecarBlocks) > 0 {
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Role == "user" {
				if len(sidecarBlocks) > 0 {
					messages[i].Content += "\n\n" + strings.Join(sidecarBlocks, "\n\n")
				}
				if len(sidecarMedia) > 0 {
					messages[i].Images = append(messages[i].Images, sidecarMedia...)
				}
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
		GroupID:      test.GroupID,
		GroupName:    test.GroupID,
		TestID:       test.ID,
		TestName:     test.Name,
		TestIndex:    idx,
		TotalTests:   total,
		CaseIndex:    1,
		TotalCases:   1,
		ActivePrompt: promptText,
	})

	turn := c.execChatTurn(ctx, runID, model, messages, optsFor(test.Options), thinkFor(test.Options))
	res.ResponseTimeMs = turn.ResponseTimeMs
	if turn.Error != nil {
		res.Error = turn.Error.Error()
		if isLoopOrSkip(res.Error) {
			falseVal := false
			res.Passed = &falseVal
		}
		res.ModelResponse = turn.Content
		return res
	}

	res.ModelResponse = turn.Content
	res.Thinking = turn.Thinking
	res.ReasoningUsed = turn.Thinking != ""
	res.TokensPerSec = turn.TokensPerSec
	res.PromptTokens = turn.PromptTokens
	res.EvalTokens = turn.EvalTokens
	res.TotalTokens = turn.TotalTokens

	passed := scoreEval(test.Evaluation, test.EvaluationType, test.EvaluationConfig, res.ModelResponse)
	if passed != nil {
		res.Passed = passed
	}
	res.CasesTotal = 1
	if res.Passed != nil && *res.Passed && res.Error == "" {
		res.CasesPassed = 1
		res.Points = 2.0 // 1 case + 1 bonus
	} else {
		res.CasesPassed = 0
		res.Points = 0.0
	}
	res.MaxPoints = 2.0
	res.Score = math.Min(100.0, math.Max(0.0, (res.Points/res.MaxPoints)*100.0))

	return res
}

func hasOpenThinkTag(s string) bool {
	lower := strings.ToLower(s)
	tags := []string{"think", "thinking", "stitching", "throat"}
	for _, tag := range tags {
		openTag := "<" + tag
		closeTag := "</" + tag + ">"
		openIdx := strings.LastIndex(lower, openTag)
		if openIdx != -1 {
			after := lower[openIdx+len(openTag):]
			if len(after) > 0 && (after[0] == '>' || after[0] == ' ' || after[0] == '\n' || after[0] == '\r' || after[0] == '\t') {
				closeIdx := strings.LastIndex(lower, closeTag)
				if closeIdx < openIdx {
					return true
				}
			}
		}
	}
	return false
}

func (c *Client) execChatTurn(ctx context.Context, runID, model string, messages []ollama.ChatMessage, opts map[string]any, think *ollama.ThinkLevel) turnResult {
	req := ollama.ChatRequest{
		Model:    model,
		Messages: messages,
		Options:  opts,
		Think:    think,
		Stream:   true,
	}

	var fullContent strings.Builder
	var fullThinking strings.Builder
	var chunkMeta *ollama.ChatChunk
	isThinking := false
	start := time.Now()
	var chatErr error
	limits := c.getStageLimits()

retryLoop:
	for attempt := 0; attempt <= 3; attempt++ {
		if attempt > 0 {
			if errors.Is(chatErr, errRepetitionLoop) || errors.Is(chatErr, errManualSkip) || errors.Is(chatErr, errManualSkipModel) || errors.Is(chatErr, errAutoSkipStage) || ctx.Err() != nil {
				break retryLoop
			}
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

		thinkChars := 0
		respChars := 0
		var thinkMs, respMs int64
		lastT := time.Now()
		chatErr = c.ollama.Chat(ctx, req, func(chunk ollama.ChatChunk) error {
			now := time.Now()
			deltaMs := now.Sub(lastT).Milliseconds()
			if deltaMs < 0 {
				deltaMs = 0
			}
			lastT = now
			if chunk.Message.Content != "" {
				fullContent.WriteString(chunk.Message.Content)
			}
			if chunk.Message.Thinking != "" {
				fullThinking.WriteString(chunk.Message.Thinking)
				thinkChars += len([]rune(chunk.Message.Thinking))
			}
			content := fullContent.String()

			// Determine whether the turn is currently in the thinking stage:
			// 1. Native reasoning chunks are streaming (or thinking started and response content has not arrived yet).
			// 2. Content is currently inside an unclosed inline thinking tag (<think>, <thinking>, etc.).
			chunkHasThinking := chunk.Message.Thinking != ""
			nativeThinkingActive := chunkHasThinking || (fullThinking.Len() > 0 && fullContent.Len() == 0)
			isThinking = nativeThinkingActive || hasOpenThinkTag(content)

			if n := len([]rune(chunk.Message.Content)); n > 0 {
				if isThinking {
					thinkChars += n
				} else {
					respChars += n
				}
			}
			if isThinking {
				thinkMs += deltaMs
			} else {
				respMs += deltaMs
			}
			c.updateProgressStream(runID, isThinking, content, fullThinking.String(), thinkMs, respMs, thinkChars, respChars)
			if chunk.Done {
				chunkMeta = &chunk
			}
			if fire, reason := limits.stageSkipDecision("thinking", thinkChars, thinkMs); fire {
				return fmt.Errorf("%s: %w", reason, errAutoSkipStage)
			}
			if fire, reason := limits.stageSkipDecision("response", respChars, respMs); fire {
				return fmt.Errorf("%s: %w", reason, errAutoSkipStage)
			}
			if isLoop, _ := detectRepetitionLoop(content); isLoop {
				return errRepetitionLoop
			}
			if isLoop, _ := detectRepetitionLoop(fullThinking.String()); isLoop {
				return errRepetitionLoop
			}
			return nil
		})

		if cause := context.Cause(ctx); errors.Is(cause, errManualSkip) {
			chatErr = errManualSkip
		} else if errors.Is(cause, errManualSkipModel) {
			chatErr = errManualSkipModel
		} else if errors.Is(cause, errManualRetry) {
			chatErr = errManualRetry
		}
		if chatErr != nil {
			break
		}
		if strings.TrimSpace(fullContent.String()) != "" {
			break
		}
	}

	if cause := context.Cause(ctx); errors.Is(cause, errManualSkip) {
		chatErr = errManualSkip
	} else if errors.Is(cause, errManualSkipModel) {
		chatErr = errManualSkipModel
	} else if errors.Is(cause, errManualRetry) {
		chatErr = errManualRetry
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

func (c *Client) updateProgressStream(runID string, thinking bool, content, reasoning string, thinkMs, respMs int64, thinkChars, respChars int) {
	c.progressMu.Lock()
	defer c.progressMu.Unlock()
	if p, ok := c.progress[runID]; ok && p != nil {
		p.IsThinking = thinking
		p.PartialResponse = content
		p.PartialThinking = reasoning
		p.ThinkingMs = thinkMs
		p.ResponseMs = respMs
		p.ThinkingChars = thinkChars
		p.ResponseChars = respChars
	}
}

// thinkFor resolves the Ollama think flag for a turn from the effective
// options. Empty or "auto" returns nil (model default, no flag sent) so
// models without thinking support are unaffected; any other valid level is
// forwarded as-is.
func thinkFor(o *tests.TestOptions) *ollama.ThinkLevel {
	if o == nil {
		return nil
	}
	lvl := strings.ToLower(strings.TrimSpace(o.ThinkLevel))
	if lvl == "" || lvl == "auto" {
		return nil
	}
	think := ollama.ThinkLevel(lvl)
	return &think
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

// BatteryEvaluationTypes are the evaluation types a battery run can score.
// Anything else scores a silent false (see scoreEval), so launches validate
// upfront instead.
var BatteryEvaluationTypes = []string{"exact_match", "contains", "contains_list", "regex", "json_schema", "human_review", "not_contains", "all_of"}

func isKnownEvalType(t string) bool {
	if t == "" || t == "agent" {
		return true // empty = legacy default; agent tests are filtered at execution
	}
	for _, known := range BatteryEvaluationTypes {
		if t == known {
			return true
		}
	}
	return false
}

// ValidateTestsForBattery reports unknown evaluation types before a run
// starts, so a typo surfaces as a 400 instead of silent failures.
func ValidateTestsForBattery(testsList []tests.Test) error {
	for _, t := range testsList {
		if t.Options != nil && !tests.IsValidThinkLevel(t.Options.ThinkLevel) {
			return fmt.Errorf("test %q uses invalid think_level %q (use auto, off, low, medium, high or max)", t.Name, t.Options.ThinkLevel)
		}
		if t.EvaluationType != "" && !isKnownEvalType(t.EvaluationType) {
			return fmt.Errorf("test %q uses unknown evaluation type %q", t.Name, t.EvaluationType)
		}
		if t.Evaluation != nil && !isKnownEvalType(t.Evaluation.Type) {
			return fmt.Errorf("test %q uses unknown evaluation type %q", t.Name, t.Evaluation.Type)
		}
		for i, tc := range t.Cases {
			if tc.Evaluation != nil && !isKnownEvalType(tc.Evaluation.Type) {
				return fmt.Errorf("test %q case %d uses unknown evaluation type %q", t.Name, i+1, tc.Evaluation.Type)
			}
			if tc.Options != nil && !tests.IsValidThinkLevel(tc.Options.ThinkLevel) {
				return fmt.Errorf("test %q case %d uses invalid think_level %q (use auto, off, low, medium, high or max)", t.Name, i+1, tc.Options.ThinkLevel)
			}
			for j, cs := range tc.Steps {
				if cs.Evaluation != nil && !isKnownEvalType(cs.Evaluation.Type) {
					return fmt.Errorf("test %q case %d step %d uses unknown evaluation type %q", t.Name, i+1, j+1, cs.Evaluation.Type)
				}
				if cs.Options != nil && !tests.IsValidThinkLevel(cs.Options.ThinkLevel) {
					return fmt.Errorf("test %q case %d step %d uses invalid think_level %q (use auto, off, low, medium, high or max)", t.Name, i+1, j+1, cs.Options.ThinkLevel)
				}
			}
		}
		for _, st := range t.Steps {
			if st.Evaluation != nil && !isKnownEvalType(st.Evaluation.Type) {
				return fmt.Errorf("test %q step %d uses unknown evaluation type %q", t.Name, st.Step, st.Evaluation.Type)
			}
			if st.Options != nil && !tests.IsValidThinkLevel(st.Options.ThinkLevel) {
				return fmt.Errorf("test %q step %d uses invalid think_level %q (use auto, off, low, medium, high or max)", t.Name, st.Step, st.Options.ThinkLevel)
			}
		}
	}
	return nil
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
