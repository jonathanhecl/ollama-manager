package server

import (
	"testing"

	"github.com/gense/ollama-manager/internal/ollama"
)

func TestEstimateTextTokens(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"ab", 1},
		{"12345678", 2},
		{"this is a short ocr text of twenty four chars", 11},
	}
	for _, c := range cases {
		if got := estimateTextTokens(c.in); got != c.want {
			t.Errorf("estimateTextTokens(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestEstimatePromptTokensIncludesImages(t *testing.T) {
	body := chatRequestBody{
		Messages: []ollama.ChatMessage{
			{Role: "user", Content: "Extrae el texto", Images: []string{"AAAA", "BBBB"}},
		},
	}
	got := estimatePromptTokens(body)
	// estimatePromptTokens joins message contents with a trailing newline.
	want := estimateTextTokens("Extrae el texto\n") + 2*256
	if got != want {
		t.Errorf("estimatePromptTokens = %d, want %d", got, want)
	}
	if got <= 0 {
		t.Errorf("estimatePromptTokens should be > 0 for a vision request, got %d", got)
	}
}