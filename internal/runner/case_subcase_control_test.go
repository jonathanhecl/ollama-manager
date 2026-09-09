package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gense/ollama-manager/internal/ollama"
	"github.com/gense/ollama-manager/internal/tests"
)

// TestRetryMultiCaseRestartsOnlyCurrentCase verifies that when a test has multiple
// cases (Case 1, Case 2, Case 3), retrying during Case 2 only restarts Case 2,
// keeping Case 1 intact and still executing Case 3.
func TestRetryMultiCaseRestartsOnlyCurrentCase(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	case2Inflight := make(chan struct{})
	var case2Once sync.Once

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		var req ollama.ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		lastUser := ""
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if req.Messages[i].Role == "user" {
				lastUser = req.Messages[i].Content
				break
			}
		}
		mu.Lock()
		hits[lastUser]++
		n := hits[lastUser]
		mu.Unlock()

		if lastUser == "prompt-case-2" && n == 1 {
			case2Once.Do(func() { close(case2Inflight) })
			select {
			case <-r.Context().Done():
				return
			case <-time.After(10 * time.Second):
				return
			}
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message":       map[string]any{"role": "assistant", "content": "R:" + lastUser},
			"done":          true,
			"eval_count":    5,
			"eval_duration": 1000000000,
		})
	}))
	defer srv.Close()

	c := NewClient(ollama.New(srv.URL))

	testDef := tests.Test{
		ID:      "t-multicase",
		Name:    "multi case test",
		GroupID: "g1",
		Active:  true,
		Cases: []tests.TestCase{
			{
				Name:       "case-1",
				Prompt:     "prompt-case-1",
				Evaluation: &tests.Evaluation{Type: "exact_match", Expected: "R:prompt-case-1"},
			},
			{
				Name:       "case-2",
				Prompt:     "prompt-case-2",
				Evaluation: &tests.Evaluation{Type: "exact_match", Expected: "R:prompt-case-2"},
			},
			{
				Name:       "case-3",
				Prompt:     "prompt-case-3",
				Evaluation: &tests.Evaluation{Type: "exact_match", Expected: "R:prompt-case-3"},
			},
		},
	}

	completed := make(chan *BatteryRun, 1)
	runID := c.ExecuteBatteryAsync(
		context.Background(),
		tests.Group{ID: "g1", Name: "g1"},
		[]tests.Test{testDef},
		[]string{"model-x"},
		map[string][]string{"model-x": nil},
		SysInfo{},
		func(run *BatteryRun) { completed <- run },
	)

	select {
	case <-case2Inflight:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for case 2 to start")
	}
	time.Sleep(200 * time.Millisecond)

	if !c.RetryCurrentTest(runID) {
		t.Fatalf("expected RetryCurrentTest to succeed while case 2 in flight")
	}

	var run *BatteryRun
	select {
	case run = <-completed:
	case <-time.After(20 * time.Second):
		t.Fatalf("timed out waiting for run to finish")
	}

	mu.Lock()
	defer mu.Unlock()
	if hits["prompt-case-1"] != 1 {
		t.Errorf("case 1 must run exactly once, got %d", hits["prompt-case-1"])
	}
	if hits["prompt-case-2"] != 2 {
		t.Errorf("case 2 must run twice (1 cancelled + 1 retry), got %d", hits["prompt-case-2"])
	}
	if hits["prompt-case-3"] != 1 {
		t.Errorf("case 3 must run exactly once, got %d", hits["prompt-case-3"])
	}
	if run == nil || len(run.Results) != 1 {
		t.Fatalf("expected 1 test result, got %+v", run)
	}
	res := run.Results[0]
	if len(res.SubResults) != 3 {
		t.Fatalf("expected 3 sub-results, got %d: %+v", len(res.SubResults), res.SubResults)
	}
	if res.CasesPassed != 3 {
		t.Errorf("expected all 3 cases to pass after retry, got %d", res.CasesPassed)
	}
}

// TestSkipMultiCaseSkipsOnlyCurrentCase verifies that when a test has multiple
// cases (Case 1, Case 2, Case 3), skipping during Case 2 only skips Case 2,
// and Case 3 still runs and finishes.
func TestSkipMultiCaseSkipsOnlyCurrentCase(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	case2Inflight := make(chan struct{})
	var case2Once sync.Once

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		var req ollama.ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		lastUser := ""
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if req.Messages[i].Role == "user" {
				lastUser = req.Messages[i].Content
				break
			}
		}
		mu.Lock()
		hits[lastUser]++
		mu.Unlock()

		if lastUser == "prompt-case-2" {
			case2Once.Do(func() { close(case2Inflight) })
			select {
			case <-r.Context().Done():
				return
			case <-time.After(10 * time.Second):
				return
			}
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message":       map[string]any{"role": "assistant", "content": "R:" + lastUser},
			"done":          true,
			"eval_count":    5,
			"eval_duration": 1000000000,
		})
	}))
	defer srv.Close()

	c := NewClient(ollama.New(srv.URL))

	testDef := tests.Test{
		ID:      "t-multicase-skip",
		Name:    "multi case skip test",
		GroupID: "g1",
		Active:  true,
		Cases: []tests.TestCase{
			{
				Name:       "case-1",
				Prompt:     "prompt-case-1",
				Evaluation: &tests.Evaluation{Type: "exact_match", Expected: "R:prompt-case-1"},
			},
			{
				Name:       "case-2",
				Prompt:     "prompt-case-2",
				Evaluation: &tests.Evaluation{Type: "exact_match", Expected: "R:prompt-case-2"},
			},
			{
				Name:       "case-3",
				Prompt:     "prompt-case-3",
				Evaluation: &tests.Evaluation{Type: "exact_match", Expected: "R:prompt-case-3"},
			},
		},
	}

	completed := make(chan *BatteryRun, 1)
	runID := c.ExecuteBatteryAsync(
		context.Background(),
		tests.Group{ID: "g1", Name: "g1"},
		[]tests.Test{testDef},
		[]string{"model-x"},
		map[string][]string{"model-x": nil},
		SysInfo{},
		func(run *BatteryRun) { completed <- run },
	)

	select {
	case <-case2Inflight:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for case 2 to start")
	}
	time.Sleep(200 * time.Millisecond)

	if !c.SkipCurrentTest(runID) {
		t.Fatalf("expected SkipCurrentTest to succeed while case 2 in flight")
	}

	var run *BatteryRun
	select {
	case run = <-completed:
	case <-time.After(20 * time.Second):
		t.Fatalf("timed out waiting for run to finish")
	}

	mu.Lock()
	defer mu.Unlock()
	if hits["prompt-case-1"] != 1 {
		t.Errorf("case 1 must run, got %d", hits["prompt-case-1"])
	}
	if hits["prompt-case-2"] != 1 {
		t.Errorf("case 2 must run once (skipped), got %d", hits["prompt-case-2"])
	}
	if hits["prompt-case-3"] != 1 {
		t.Errorf("case 3 must run after case 2 was skipped, got %d", hits["prompt-case-3"])
	}

	res := run.Results[0]
	if len(res.SubResults) != 3 {
		t.Fatalf("expected 3 sub-results, got %d", len(res.SubResults))
	}
	// Case 1 passed
	if res.SubResults[0].Passed == nil || !*res.SubResults[0].Passed {
		t.Errorf("case 1 should have passed, got %+v", res.SubResults[0])
	}
	// Case 2 skipped
	if !strings.Contains(res.SubResults[1].Error, "manually skipped") {
		t.Errorf("case 2 should be marked manually skipped, got %+v", res.SubResults[1])
	}
	// Case 3 passed
	if res.SubResults[2].Passed == nil || !*res.SubResults[2].Passed {
		t.Errorf("case 3 should have passed, got %+v", res.SubResults[2])
	}
	if res.CasesPassed != 2 {
		t.Errorf("expected 2 passed cases (case 1 and 3), got %d", res.CasesPassed)
	}
}

// TestSkipMultiStepCaseSkipsOnlyCurrentStep verifies that when a case has multiple
// steps (Step 1, Step 2, Step 3), skipping during Step 2 skips ONLY Step 2,
// and Step 3 still executes and passes.
func TestSkipMultiStepCaseSkipsOnlyCurrentStep(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	step2Inflight := make(chan struct{})
	var step2Once sync.Once

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		var req ollama.ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		lastUser := ""
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if req.Messages[i].Role == "user" {
				lastUser = req.Messages[i].Content
				break
			}
		}
		mu.Lock()
		hits[lastUser]++
		mu.Unlock()

		if lastUser == "prompt-step-2" {
			step2Once.Do(func() { close(step2Inflight) })
			select {
			case <-r.Context().Done():
				return
			case <-time.After(10 * time.Second):
				return
			}
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message":       map[string]any{"role": "assistant", "content": "R:" + lastUser},
			"done":          true,
			"eval_count":    5,
			"eval_duration": 1000000000,
		})
	}))
	defer srv.Close()

	c := NewClient(ollama.New(srv.URL))

	testDef := tests.Test{
		ID:      "t-steps-skip",
		Name:    "case with steps skip test",
		GroupID: "g1",
		Active:  true,
		Cases: []tests.TestCase{
			{
				Name: "case-with-steps",
				Steps: []tests.CaseStep{
					{
						Name:       "step-1",
						Prompt:     "prompt-step-1",
						Evaluation: &tests.Evaluation{Type: "exact_match", Expected: "R:prompt-step-1"},
					},
					{
						Name:       "step-2",
						Prompt:     "prompt-step-2",
						Evaluation: &tests.Evaluation{Type: "exact_match", Expected: "R:prompt-step-2"},
					},
					{
						Name:       "step-3",
						Prompt:     "prompt-step-3",
						Evaluation: &tests.Evaluation{Type: "exact_match", Expected: "R:prompt-step-3"},
					},
				},
			},
		},
	}

	completed := make(chan *BatteryRun, 1)
	runID := c.ExecuteBatteryAsync(
		context.Background(),
		tests.Group{ID: "g1", Name: "g1"},
		[]tests.Test{testDef},
		[]string{"model-x"},
		map[string][]string{"model-x": nil},
		SysInfo{},
		func(run *BatteryRun) { completed <- run },
	)

	select {
	case <-step2Inflight:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for step 2 to start")
	}
	time.Sleep(200 * time.Millisecond)

	if !c.SkipCurrentTest(runID) {
		t.Fatalf("expected SkipCurrentTest to succeed while step 2 in flight")
	}

	var run *BatteryRun
	select {
	case run = <-completed:
	case <-time.After(20 * time.Second):
		t.Fatalf("timed out waiting for run to finish")
	}

	mu.Lock()
	defer mu.Unlock()
	if hits["prompt-step-1"] != 1 {
		t.Errorf("step 1 must run, got %d", hits["prompt-step-1"])
	}
	if hits["prompt-step-2"] != 1 {
		t.Errorf("step 2 must run once (skipped), got %d", hits["prompt-step-2"])
	}
	if hits["prompt-step-3"] != 1 {
		t.Errorf("step 3 must run after step 2 was skipped, got %d", hits["prompt-step-3"])
	}

	res := run.Results[0]
	if len(res.SubResults) != 3 {
		t.Fatalf("expected 3 sub-results, got %d", len(res.SubResults))
	}
	// Step 1 passed
	if res.SubResults[0].Passed == nil || !*res.SubResults[0].Passed {
		t.Errorf("step 1 should have passed, got %+v", res.SubResults[0])
	}
	// Step 2 skipped
	if !strings.Contains(res.SubResults[1].Error, "manually skipped") {
		t.Errorf("step 2 should be marked manually skipped, got %+v", res.SubResults[1])
	}
	// Step 3 passed
	if res.SubResults[2].Passed == nil || !*res.SubResults[2].Passed {
		t.Errorf("step 3 should have passed, got %+v", res.SubResults[2])
	}
}

// TestRetryMultiStepCaseRestartsOnlyCurrentStep verifies that when a case has
// multiple steps, retrying during Step 2 only restarts Step 2, keeping Step 1
// in history and not re-running Step 1.
func TestRetryMultiStepCaseRestartsOnlyCurrentStep(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	var step2SecondAttemptMsgs []ollama.ChatMessage
	step2Inflight := make(chan struct{})
	var step2Once sync.Once

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		var req ollama.ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		lastUser := ""
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if req.Messages[i].Role == "user" {
				lastUser = req.Messages[i].Content
				break
			}
		}
		mu.Lock()
		hits[lastUser]++
		n := hits[lastUser]
		mu.Unlock()

		if lastUser == "prompt-step-2" && n == 1 {
			step2Once.Do(func() { close(step2Inflight) })
			select {
			case <-r.Context().Done():
				return
			case <-time.After(10 * time.Second):
				return
			}
		}

		if lastUser == "prompt-step-2" && n == 2 {
			mu.Lock()
			step2SecondAttemptMsgs = append([]ollama.ChatMessage(nil), req.Messages...)
			mu.Unlock()
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message":       map[string]any{"role": "assistant", "content": "R:" + lastUser},
			"done":          true,
			"eval_count":    5,
			"eval_duration": 1000000000,
		})
	}))
	defer srv.Close()

	c := NewClient(ollama.New(srv.URL))

	testDef := tests.Test{
		ID:      "t-steps-retry",
		Name:    "case with steps retry test",
		GroupID: "g1",
		Active:  true,
		Cases: []tests.TestCase{
			{
				Name: "case-with-steps",
				Steps: []tests.CaseStep{
					{
						Name:       "step-1",
						Prompt:     "prompt-step-1",
						Evaluation: &tests.Evaluation{Type: "exact_match", Expected: "R:prompt-step-1"},
					},
					{
						Name:       "step-2",
						Prompt:     "prompt-step-2",
						Evaluation: &tests.Evaluation{Type: "exact_match", Expected: "R:prompt-step-2"},
					},
					{
						Name:       "step-3",
						Prompt:     "prompt-step-3",
						Evaluation: &tests.Evaluation{Type: "exact_match", Expected: "R:prompt-step-3"},
					},
				},
			},
		},
	}

	completed := make(chan *BatteryRun, 1)
	runID := c.ExecuteBatteryAsync(
		context.Background(),
		tests.Group{ID: "g1", Name: "g1"},
		[]tests.Test{testDef},
		[]string{"model-x"},
		map[string][]string{"model-x": nil},
		SysInfo{},
		func(run *BatteryRun) { completed <- run },
	)

	select {
	case <-step2Inflight:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for step 2 to start")
	}
	time.Sleep(200 * time.Millisecond)

	if !c.RetryCurrentTest(runID) {
		t.Fatalf("expected RetryCurrentTest to succeed while step 2 in flight")
	}

	var run *BatteryRun
	select {
	case run = <-completed:
	case <-time.After(20 * time.Second):
		t.Fatalf("timed out waiting for run to finish")
	}

	mu.Lock()
	defer mu.Unlock()
	if hits["prompt-step-1"] != 1 {
		t.Errorf("step 1 must run exactly once, got %d", hits["prompt-step-1"])
	}
	if hits["prompt-step-2"] != 2 {
		t.Errorf("step 2 must run twice (1 cancelled + 1 retry), got %d", hits["prompt-step-2"])
	}
	if hits["prompt-step-3"] != 1 {
		t.Errorf("step 3 must run once, got %d", hits["prompt-step-3"])
	}

	res := run.Results[0]
	if len(res.SubResults) != 3 {
		t.Fatalf("expected 3 sub-results, got %d", len(res.SubResults))
	}
	if len(step2SecondAttemptMsgs) != 3 {
		t.Errorf("retried step 2 must keep step 1 in history (want 3 msgs), got %d: %+v", len(step2SecondAttemptMsgs), step2SecondAttemptMsgs)
	}
}

// TestSinglePromptTestRetry verifies that when a test has NO subcases (single prompt),
// RetryCurrentTest restarts the whole case/test.
func TestSinglePromptTestRetry(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	t1Inflight := make(chan struct{})
	var t1Once sync.Once

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		var req ollama.ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		lastUser := ""
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if req.Messages[i].Role == "user" {
				lastUser = req.Messages[i].Content
				break
			}
		}
		mu.Lock()
		hits[lastUser]++
		n := hits[lastUser]
		mu.Unlock()

		if lastUser == "single-prompt-1" && n == 1 {
			t1Once.Do(func() { close(t1Inflight) })
			select {
			case <-r.Context().Done():
				return
			case <-time.After(10 * time.Second):
				return
			}
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message":       map[string]any{"role": "assistant", "content": "R:" + lastUser},
			"done":          true,
			"eval_count":    5,
			"eval_duration": 1000000000,
		})
	}))
	defer srv.Close()

	c := NewClient(ollama.New(srv.URL))

	testDef := tests.Test{
		ID:      "t-single",
		Name:    "single prompt test",
		GroupID: "g1",
		Active:  true,
		Prompt:  "single-prompt-1",
		Evaluation: &tests.Evaluation{
			Type:     "exact_match",
			Expected: "R:single-prompt-1",
		},
	}

	completed := make(chan *BatteryRun, 1)
	runID := c.ExecuteBatteryAsync(
		context.Background(),
		tests.Group{ID: "g1", Name: "g1"},
		[]tests.Test{testDef},
		[]string{"model-x"},
		map[string][]string{"model-x": nil},
		SysInfo{},
		func(run *BatteryRun) { completed <- run },
	)

	select {
	case <-t1Inflight:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for t1 to start")
	}
	time.Sleep(200 * time.Millisecond)

	if !c.RetryCurrentTest(runID) {
		t.Fatalf("expected RetryCurrentTest to succeed while t1 in flight")
	}

	var run *BatteryRun
	select {
	case run = <-completed:
	case <-time.After(20 * time.Second):
		t.Fatalf("timed out waiting for run to finish")
	}

	mu.Lock()
	defer mu.Unlock()
	if hits["single-prompt-1"] != 2 {
		t.Errorf("single prompt test must run twice (1 cancelled + 1 retry), got %d", hits["single-prompt-1"])
	}
	if len(run.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(run.Results))
	}
	if run.Results[0].Passed == nil || !*run.Results[0].Passed {
		t.Errorf("expected retried single prompt test to pass, got %+v", run.Results[0])
	}
}

// TestSinglePromptTestSkip verifies that when a test has NO subcases (single prompt),
// SkipCurrentTest skips the whole test and advances to the next test.
func TestSinglePromptTestSkip(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	t1Inflight := make(chan struct{})
	var t1Once sync.Once

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		var req ollama.ChatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		lastUser := ""
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if req.Messages[i].Role == "user" {
				lastUser = req.Messages[i].Content
				break
			}
		}
		mu.Lock()
		hits[lastUser]++
		mu.Unlock()

		if lastUser == "single-prompt-1" {
			t1Once.Do(func() { close(t1Inflight) })
			select {
			case <-r.Context().Done():
				return
			case <-time.After(10 * time.Second):
				return
			}
		}

		w.Header().Set("Content-Type", "application/x-ndjson")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message":       map[string]any{"role": "assistant", "content": "R:" + lastUser},
			"done":          true,
			"eval_count":    5,
			"eval_duration": 1000000000,
		})
	}))
	defer srv.Close()

	c := NewClient(ollama.New(srv.URL))

	t1 := tests.Test{
		ID:      "t-single-1",
		Name:    "single prompt test 1",
		GroupID: "g1",
		Active:  true,
		Prompt:  "single-prompt-1",
		Evaluation: &tests.Evaluation{
			Type:     "exact_match",
			Expected: "R:single-prompt-1",
		},
	}
	t2 := tests.Test{
		ID:      "t-single-2",
		Name:    "single prompt test 2",
		GroupID: "g1",
		Active:  true,
		Prompt:  "single-prompt-2",
		Evaluation: &tests.Evaluation{
			Type:     "exact_match",
			Expected: "R:single-prompt-2",
		},
	}

	completed := make(chan *BatteryRun, 1)
	runID := c.ExecuteBatteryAsync(
		context.Background(),
		tests.Group{ID: "g1", Name: "g1"},
		[]tests.Test{t1, t2},
		[]string{"model-x"},
		map[string][]string{"model-x": nil},
		SysInfo{},
		func(run *BatteryRun) { completed <- run },
	)

	select {
	case <-t1Inflight:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for t1 to start")
	}
	time.Sleep(200 * time.Millisecond)

	if !c.SkipCurrentTest(runID) {
		t.Fatalf("expected SkipCurrentTest to succeed while t1 in flight")
	}

	var run *BatteryRun
	select {
	case run = <-completed:
	case <-time.After(20 * time.Second):
		t.Fatalf("timed out waiting for run to finish")
	}

	mu.Lock()
	defer mu.Unlock()
	if hits["single-prompt-1"] != 1 {
		t.Errorf("t1 must run once (skipped), got %d", hits["single-prompt-1"])
	}
	if hits["single-prompt-2"] != 1 {
		t.Errorf("t2 must run and finish, got %d", hits["single-prompt-2"])
	}
	if len(run.Results) != 2 {
		t.Fatalf("expected 2 results (1 skipped + 1 passed), got %d", len(run.Results))
	}
	if !strings.Contains(run.Results[0].Error, "manually skipped") {
		t.Errorf("expected t1 to be marked manually skipped, got error %q", run.Results[0].Error)
	}
	if run.Results[1].Passed == nil || !*run.Results[1].Passed {
		t.Errorf("expected t2 to pass, got %+v", run.Results[1])
	}
}

