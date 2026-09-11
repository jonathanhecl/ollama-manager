package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gense/ollama-manager/internal/ollama"
	"github.com/gense/ollama-manager/internal/tests"
)

// inlineThinkServer streams a fixed assistant content once, as if a model
// without a native reasoning parser emitted its reasoning inline in content.
func inlineThinkServer(t *testing.T, content string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message":       map[string]any{"role": "assistant", "content": content},
			"done":          true,
			"eval_count":    12,
			"eval_duration": 1000000000,
		})
	}))
}

func TestRunTestMovesInlineReasoningOutOfContent(t *testing.T) {
	const answer = "R:extract"
	srv := inlineThinkServer(t, "<thinking>The user wants me to answer.\n</thinking>\n\n"+answer)
	defer srv.Close()

	c := NewClient(ollama.New(srv.URL))
	res := c.runTest(context.Background(), "run-1", "m1", singlePromptTest("t1", "extract"), 1, 1)

	if res.ModelResponse != answer {
		t.Fatalf("ModelResponse = %q, want %q", res.ModelResponse, answer)
	}
	if res.Thinking != "The user wants me to answer." {
		t.Fatalf("Thinking = %q, want reasoning text", res.Thinking)
	}
	if !res.ReasoningUsed {
		t.Fatalf("ReasoningUsed = false, want true")
	}
	if res.Passed == nil || !*res.Passed {
		t.Fatalf("Passed = %v, want true (answer should match exact_match)", res.Passed)
	}
}

func TestRunTestTreatsThinkLikeThinking(t *testing.T) {
	const answer = `{"name": "Ada", "age": 36}`
	srv := inlineThinkServer(t, "<think>reason</think>\n"+answer)
	defer srv.Close()

	test := tests.Test{
		ID:      "t-json",
		Name:    "json",
		GroupID: "g1",
		Active:  true,
		Prompt:  "extract",
		Evaluation: &tests.Evaluation{
			Type:   "json_schema",
			Schema: map[string]any{"type": "object", "required": []string{"name", "age"}},
		},
	}

	c := NewClient(ollama.New(srv.URL))
	res := c.runTest(context.Background(), "run-2", "m1", test, 1, 1)

	if res.ModelResponse != answer {
		t.Fatalf("ModelResponse = %q, want %q", res.ModelResponse, answer)
	}
	if res.Passed == nil || !*res.Passed {
		t.Fatalf("Passed = %v, want true", res.Passed)
	}
}
