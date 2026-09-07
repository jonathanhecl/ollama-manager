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

// blockingChatServer serves instant NDJSON answers for every prompt, except
// the blockHitN-th request of blockPrompt, which hangs until the client
// cancels. It reports hits per prompt and signals inflight when blocked.
type blockingChatServer struct {
	mu       sync.Mutex
	hits     map[string]int
	block    string
	blockHit int
	inflight chan struct{}
	once     sync.Once
	srv      *httptest.Server
}

func newBlockingChatServer(blockPrompt string, blockHit int) *blockingChatServer {
	b := &blockingChatServer{hits: map[string]int{}, block: blockPrompt, blockHit: blockHit, inflight: make(chan struct{})}
	b.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
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
		b.mu.Lock()
		b.hits[lastUser]++
		n := b.hits[lastUser]
		b.mu.Unlock()

		if b.block != "" && lastUser == b.block && n == b.blockHit {
			b.once.Do(func() { close(b.inflight) })
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
	return b
}

func (b *blockingChatServer) close() { b.srv.Close() }

func (b *blockingChatServer) hitCount(prompt string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.hits[prompt]
}

func singlePromptTest(id, prompt string) tests.Test {
	return tests.Test{
		ID:      id,
		Name:    id,
		GroupID: "g1",
		Active:  true,
		Prompt:  prompt,
		Evaluation: &tests.Evaluation{
			Type:     "exact_match",
			Expected: "R:" + prompt,
		},
	}
}

func resultsByModel(run *BatteryRun) map[string][]TestResult {
	out := map[string][]TestResult{}
	for _, res := range run.Results {
		out[res.Model] = append(out[res.Model], res)
	}
	return out
}

func waitRun(t *testing.T, completed chan *BatteryRun) *BatteryRun {
	t.Helper()
	select {
	case run := <-completed:
		return run
	case <-time.After(20 * time.Second):
		t.Fatalf("timed out waiting for run to finish")
		return nil
	}
}

func TestSkipCurrentModel(t *testing.T) {
	// m1-t1 instant; m1-t2 (1st hit of p-t2) blocks. Skip the model mid-t2.
	b := newBlockingChatServer("p-t2", 1)
	defer b.close()
	c := NewClient(ollama.New(b.srv.URL))

	testsList := []tests.Test{singlePromptTest("t1", "p-t1"), singlePromptTest("t2", "p-t2")}
	completed := make(chan *BatteryRun, 1)
	runID := c.ExecuteBatteryAsync(
		context.Background(),
		tests.Group{ID: "g1", Name: "g1"},
		testsList,
		[]string{"m1", "m2"},
		map[string][]string{"m1": nil, "m2": nil},
		SysInfo{},
		func(run *BatteryRun) { completed <- run },
	)

	select {
	case <-b.inflight:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for m1-t2 to start")
	}
	time.Sleep(200 * time.Millisecond)
	if !c.SkipCurrentModel(runID) {
		t.Fatalf("expected SkipCurrentModel to succeed")
	}

	run := waitRun(t, completed)
	byModel := resultsByModel(run)
	if len(byModel["m1"]) != 2 {
		t.Fatalf("m1 should keep both results (t1 pass + t2 skipped), got %+v", byModel["m1"])
	}
	if byModel["m1"][1].Passed == nil || *byModel["m1"][1].Passed {
		t.Errorf("m1-t2 should be recorded as failed/skipped, got %+v", byModel["m1"][1])
	}
	if !strings.Contains(byModel["m1"][1].Error, "manually skipped model") {
		t.Errorf("m1-t2 error should mention model skip, got %q", byModel["m1"][1].Error)
	}
	if len(byModel["m2"]) != 2 {
		t.Fatalf("m2 should run all its tests after the skip, got %+v", byModel["m2"])
	}
	for _, res := range byModel["m2"] {
		if res.Passed == nil || !*res.Passed {
			t.Errorf("m2 results should pass, got %+v", res)
		}
	}
	if b.hitCount("p-t2") != 2 {
		t.Errorf("t2 prompt should run twice (m1 attempt + m2), got %d", b.hitCount("p-t2"))
	}
}

func TestAbortRunSaveCompleted(t *testing.T) {
	// m1 completes both tests; abort lands on m2-t1 (2nd hit of p-t1).
	// Only m1 (fully completed) must survive in the saved run.
	b := newBlockingChatServer("p-t1", 2)
	defer b.close()
	c := NewClient(ollama.New(b.srv.URL))

	testsList := []tests.Test{singlePromptTest("t1", "p-t1"), singlePromptTest("t2", "p-t2")}
	completed := make(chan *BatteryRun, 1)
	runID := c.ExecuteBatteryAsync(
		context.Background(),
		tests.Group{ID: "g1", Name: "g1"},
		testsList,
		[]string{"m1", "m2"},
		map[string][]string{"m1": nil, "m2": nil},
		SysInfo{},
		func(run *BatteryRun) { completed <- run },
	)

	select {
	case <-b.inflight:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for m2-t1 to start")
	}
	time.Sleep(200 * time.Millisecond)
	if !c.AbortRun(runID, "save-completed") {
		t.Fatalf("expected AbortRun to succeed")
	}

	run := waitRun(t, completed)
	byModel := resultsByModel(run)
	if len(byModel["m1"]) != 2 {
		t.Errorf("m1 completed all tests and must be kept, got %+v", byModel["m1"])
	}
	if len(byModel["m2"]) != 0 {
		t.Errorf("m2 did not complete and must be pruned, got %+v", byModel["m2"])
	}
	if len(run.Models) != 1 || run.Models[0] != "m1" {
		t.Errorf("run models should be [m1], got %v", run.Models)
	}
}

func TestAbortRunDiscard(t *testing.T) {
	// Abort on the very first test: nothing must survive.
	b := newBlockingChatServer("p-t1", 1)
	defer b.close()
	c := NewClient(ollama.New(b.srv.URL))

	testsList := []tests.Test{singlePromptTest("t1", "p-t1"), singlePromptTest("t2", "p-t2")}
	completed := make(chan *BatteryRun, 1)
	runID := c.ExecuteBatteryAsync(
		context.Background(),
		tests.Group{ID: "g1", Name: "g1"},
		testsList,
		[]string{"m1", "m2"},
		map[string][]string{"m1": nil, "m2": nil},
		SysInfo{},
		func(run *BatteryRun) { completed <- run },
	)

	select {
	case <-b.inflight:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for m1-t1 to start")
	}
	time.Sleep(200 * time.Millisecond)
	if !c.AbortRun(runID, "discard") {
		t.Fatalf("expected AbortRun to succeed")
	}

	run := waitRun(t, completed)
	if len(run.Results) != 0 {
		t.Errorf("discarded run should have no results, got %+v", run.Results)
	}
	if len(run.Models) != 0 {
		t.Errorf("discarded run should have no models, got %v", run.Models)
	}
}
