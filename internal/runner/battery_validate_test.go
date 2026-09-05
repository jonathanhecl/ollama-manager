package runner

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/gense/ollama-manager/internal/tests"
)

func TestValidateTestsForBattery(t *testing.T) {
	ok := []tests.Test{{
		Name:           "fine",
		EvaluationType: "contains",
		Cases: []tests.TestCase{{
			Prompt:     "hi",
			Evaluation: &tests.Evaluation{Type: "regex", Pattern: "^hi$"},
		}},
		Steps: []tests.Step{{
			Step:       1,
			Prompt:     "yo",
			Evaluation: &tests.Evaluation{Type: "human_review"},
		}},
	}}
	if err := ValidateTestsForBattery(ok); err != nil {
		t.Fatalf("expected valid, got %v", err)
	}

	bad := []tests.Test{{
		Name:           "typo",
		EvaluationType: "containz",
	}}
	if err := ValidateTestsForBattery(bad); err == nil || !strings.Contains(err.Error(), "containz") {
		t.Fatalf("expected unknown-type error, got %v", err)
	}

	badCase := []tests.Test{{
		Name:  "badcase",
		Cases: []tests.TestCase{{Prompt: "x", Evaluation: &tests.Evaluation{Type: "fuzzy"}}},
	}}
	if err := ValidateTestsForBattery(badCase); err == nil {
		t.Fatal("expected error for bad case type")
	}
}

func TestSplitCaseMedia(t *testing.T) {
	atts := []tests.Attachment{
		{ID: "a-1.png", Kind: "image", Name: "a-1.png", Mime: "image/png", Data: "imgdata"},
		{ID: "a-2.txt", Kind: "text", Name: "a-2.txt", Mime: "text/plain", Data: base64.StdEncoding.EncodeToString([]byte("hello"))},
		{ID: "a-3.bin", Kind: "text", Name: "a-3.bin", Mime: "application/octet-stream", Data: "!!!not-base64!!!\xff"},
	}
	media, blocks := splitCaseMedia(atts)
	if len(media) != 1 || media[0] != "imgdata" {
		t.Fatalf("media wrong: %v", media)
	}
	if len(blocks) != 1 || !strings.Contains(blocks[0], "a-2.txt") || !strings.Contains(blocks[0], "hello") {
		t.Fatalf("text blocks wrong: %v", blocks)
	}
	out, _ := applyCaseMedia("prompt", atts)
	if !strings.HasPrefix(out, "prompt\n\n--- attached file: a-2.txt ---") {
		t.Fatalf("applyCaseMedia wrong: %q", out)
	}
}
