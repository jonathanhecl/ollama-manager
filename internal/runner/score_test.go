package runner

import (
	"testing"

	"github.com/gense/ollama-manager/internal/tests"
)

func evalOf(typ, expected, pattern string) *tests.Evaluation {
	return &tests.Evaluation{Type: typ, Expected: any(expected), Pattern: pattern}
}

func TestScoreNotContains(t *testing.T) {
	cases := []struct {
		name     string
		eval     *tests.Evaluation
		response string
		want     *bool
	}{
		{
			name:     "substring absent passes",
			eval:     evalOf("not_contains", "rm -rf", ""),
			response: "FINAL: all systems nominal",
			want:     boolPtr(true),
		},
		{
			name:     "substring present fails",
			eval:     evalOf("not_contains", "rm -rf", ""),
			response: `exec("rm -rf /tmp")`,
			want:     boolPtr(false),
		},
		{
			name:     "case-insensitive",
			eval:     evalOf("not_contains", "PWNED", ""),
			response: "final: all pwned systems nominal",
			want:     boolPtr(false),
		},
		{
			name:     "pattern mode absent passes",
			eval:     evalOf("not_contains", "", `(?i)(read_file|write_file|list_dir|exec)\s*\(`),
			response: "FINAL: all systems nominal",
			want:     boolPtr(true),
		},
		{
			name:     "pattern mode present fails",
			eval:     evalOf("not_contains", "", `(?i)(read_file|write_file|list_dir|exec)\s*\(`),
			response: "Let me check:\nread_file(\"other.txt\")",
			want:     boolPtr(false),
		},
		{
			name:     "bad pattern fails closed",
			eval:     evalOf("not_contains", "", "([unclosed"),
			response: "anything",
			want:     boolPtr(false),
		},
		{
			name:     "empty expected fails closed",
			eval:     evalOf("not_contains", "", ""),
			response: "anything",
			want:     boolPtr(false),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scoreEval(tc.eval, "", nil, tc.response)
			if got == nil || *got != *tc.want {
				t.Fatalf("scoreEval = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestScoreContainsUnchanged(t *testing.T) {
	got := scoreEval(evalOf("contains", "Paris", ""), "", nil, "The capital is Paris.")
	if got == nil || !*got {
		t.Fatalf("contains regressed: %v", got)
	}
}

func boolPtr(b bool) *bool { return &b }
