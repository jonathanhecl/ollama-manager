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

func TestScoreAllOf(t *testing.T) {
	allOf := func(subs ...*tests.Evaluation) *tests.Evaluation {
		return &tests.Evaluation{Type: "all_of", Evaluations: subs}
	}
	cases := []struct {
		name     string
		eval     *tests.Evaluation
		response string
		want     *bool
	}{
		{
			name: "all pass",
			eval: allOf(
				evalOf("regex", "", `(?i)FINAL:`),
				evalOf("not_contains", "", `(?i)exec\s*\(`),
			),
			response: "FINAL: all systems nominal",
			want:     boolPtr(true),
		},
		{
			name: "second fails",
			eval: allOf(
				evalOf("regex", "", `(?i)FINAL:`),
				evalOf("not_contains", "", `(?i)exec\s*\(`),
			),
			response: "FINAL: done\n" + `exec("rm -rf /")`,
			want:     boolPtr(false),
		},
		{
			name: "first fails fast",
			eval: allOf(
				evalOf("contains", "FINAL:", ""),
				evalOf("contains", "nominal", ""),
			),
			response: "all systems nominal",
			want:     boolPtr(false),
		},
		{
			name:     "empty list fails closed",
			eval:     allOf(),
			response: "anything",
			want:     boolPtr(false),
		},
		{
			name: "human sub with rest passing needs review",
			eval: allOf(
				evalOf("contains", "nominal", ""),
				&tests.Evaluation{Type: "human_review"},
			),
			response: "all systems nominal",
			want:     nil,
		},
		{
			name: "human sub with failure still fails",
			eval: allOf(
				evalOf("contains", "missing", ""),
				&tests.Evaluation{Type: "human_review"},
			),
			response: "all systems nominal",
			want:     boolPtr(false),
		},
		{
			name: "nested all_of",
			eval: allOf(
				allOf(evalOf("contains", "a", ""), evalOf("contains", "b", "")),
				evalOf("not_contains", "z", ""),
			),
			response: "a b c",
			want:     boolPtr(true),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scoreEval(tc.eval, "", nil, tc.response)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("scoreEval = %v, want nil", got)
				}
				return
			}
			if got == nil || *got != *tc.want {
				t.Fatalf("scoreEval = %v, want %v", got, tc.want)
			}
		})
	}
}

func boolPtr(b bool) *bool { return &b }
