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

// TestRetrySequentialStepRestartsOnlyCurrentStep verifies that RetryCurrentTest
// during a sequential multi-step test re-executes the in-flight step with the
// same history prefix: earlier steps are NOT re-run and no retry artifacts are
// recorded. It drives the production loop (ExecuteBatteryAsync) so the retry
// travels through the same cancel registration as in production.
func TestRetrySequentialStepRestartsOnlyCurrentStep(t *testing.T) {
	var mu sync.Mutex
	hits := map[string]int{}
	var step2SecondAttemptMsgs []ollama.ChatMessage
	step2Inflight := make(chan struct{})
	var step2InflightOnce sync.Once

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			// Unload and other endpoints: acknowledge so the run can finish.
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		var req ollama.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
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

		// First attempt of step 2 blocks until the client cancels (retry).
		if lastUser == "prompt-step-2" && n == 1 {
			step2InflightOnce.Do(func() { close(step2Inflight) })
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
		chunk := map[string]any{
			"message":       map[string]any{"role": "assistant", "content": "R:" + lastUser},
			"done":          true,
			"eval_count":    5,
			"eval_duration": 1000000000,
		}
		_ = json.NewEncoder(w).Encode(chunk)
	}))
	defer srv.Close()

	c := NewClient(ollama.New(srv.URL))

	testDef := tests.Test{
		ID:      "t-steps",
		Name:    "steps test",
		GroupID: "g1",
		Active:  true,
		Steps: []tests.Step{
			{
				Step:       1,
				Name:       "first",
				Prompt:     "prompt-step-1",
				Evaluation: &tests.Evaluation{Type: "exact_match", Expected: "R:prompt-step-1"},
			},
			{
				Step:       2,
				Name:       "second",
				Prompt:     "prompt-step-2",
				Evaluation: &tests.Evaluation{Type: "exact_match", Expected: "R:prompt-step-2"},
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

	// Wait until step 2 is in flight, then retry it.
	select {
	case <-step2Inflight:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for step 2 to start (hits=%v)", hits)
	}
	// Give the streaming read a moment to settle, then retry.
	time.Sleep(200 * time.Millisecond)
	if !c.RetryCurrentTest(runID) {
		t.Fatalf("expected RetryCurrentTest to succeed while step 2 in flight")
	}

	var run *BatteryRun
	select {
	case run = <-completed:
	case <-time.After(20 * time.Second):
		t.Fatalf("timed out waiting for run to finish (hits=%v)", hits)
	}

	mu.Lock()
	defer mu.Unlock()
	if hits["prompt-step-1"] != 1 {
		t.Errorf("step 1 must run exactly once (history prefix kept), got %d", hits["prompt-step-1"])
	}
	if hits["prompt-step-2"] != 2 {
		t.Errorf("step 2 must run twice (cancelled attempt + retry), got %d", hits["prompt-step-2"])
	}
	if run == nil || len(run.Results) != 1 {
		t.Fatalf("expected 1 test result, got %+v", run)
	}
	res := run.Results[0]
	if len(res.SubResults) != 2 {
		t.Fatalf("expected 2 sub-results, got %d: %+v", len(res.SubResults), res.SubResults)
	}
	for _, sr := range res.SubResults {
		if strings.Contains(sr.Error, "manually retried") {
			t.Errorf("retry artifact leaked into sub-results: %+v", sr)
		}
		if sr.Error != "" {
			t.Errorf("unexpected sub-result error: %+v", sr)
		}
	}
	if res.Error != "" {
		t.Errorf("expected empty result error after retry, got %q", res.Error)
	}
	if res.Passed == nil || !*res.Passed {
		t.Errorf("expected test to pass after step retry, got passed=%v err=%q", res.Passed, res.Error)
	}
	// The retried step must see the same history prefix (step 1 turn kept).
	if len(step2SecondAttemptMsgs) != 3 {
		t.Errorf("retried step must keep history prefix (want 3 msgs), got %d: %+v", len(step2SecondAttemptMsgs), step2SecondAttemptMsgs)
	} else if step2SecondAttemptMsgs[1].Content != "R:prompt-step-1" {
		t.Errorf("history prefix lost on retry, assistant msg=%q", step2SecondAttemptMsgs[1].Content)
	}
}
