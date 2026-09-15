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

func TestScoreContainsAllAndAny(t *testing.T) {
	evalAll := &tests.Evaluation{
		Type: "contains",
		All:  []any{"VALKYRIE-9021-OMEGA", "Tier-3"},
	}
	passResp := "Here is VALKYRIE-9021-OMEGA with Tier-3 protocol."
	failResp := "Here is VALKYRIE-9021-OMEGA only."
	if got := scoreEval(evalAll, "", nil, passResp); got == nil || !*got {
		t.Fatalf("evalAll with both should pass, got %v", got)
	}
	if got := scoreEval(evalAll, "", nil, failResp); got == nil || *got {
		t.Fatalf("evalAll missing one should fail, got %v", got)
	}

	evalAny := &tests.Evaluation{
		Type: "contains",
		Any:  []any{"apple", "banana"},
	}
	if got := scoreEval(evalAny, "", nil, "I like banana."); got == nil || !*got {
		t.Fatalf("evalAny with one match should pass, got %v", got)
	}
	if got := scoreEval(evalAny, "", nil, "I like orange."); got == nil || *got {
		t.Fatalf("evalAny with no match should fail, got %v", got)
	}
}

func TestScoreExactMatchTolerant(t *testing.T) {
	eval := evalOf("exact_match", "knave", "")

	validResponses := []string{
		"knave",
		"Knave",
		"KNAVE",
		"Knave.",
		"knave!",
		"**Knave**",
		"`knave`",
		`"knave"`,
		`"Knave."`,
		"  knave \n",
		"```\nknave\n```",
	}
	for _, resp := range validResponses {
		got := scoreEval(eval, "", nil, resp)
		if got == nil || !*got {
			t.Errorf("exact_match for %q should pass, got %v", resp, got)
		}
	}

	invalidResponses := []string{
		"knight",
		"Knight",
		"A is a knave",
		"not knave",
	}
	for _, resp := range invalidResponses {
		got := scoreEval(eval, "", nil, resp)
		if got == nil || *got {
			t.Errorf("exact_match for %q should fail, got %v", resp, got)
		}
	}
}

func TestScoreContainsAndExactMatchWithList(t *testing.T) {
	// contains with any
	evalAny := &tests.Evaluation{
		Type: "contains",
		Any:  []any{"avergonza", "vergüenza", "apenad"},
	}
	if got := scoreEval(evalAny, "", nil, "En realidad, estoy tan avergonzado ahora mismo."); got == nil || !*got {
		t.Fatalf("expected avergonzado to match avergonza")
	}
	if got := scoreEval(evalAny, "", nil, "Estoy embarazada"); got == nil || *got {
		t.Fatalf("expected embarazada to fail")
	}

	// contains with expected as slice
	evalSlice := &tests.Evaluation{
		Type:     "contains",
		Expected: []string{"avergonza", "vergüenza"},
	}
	if got := scoreEval(evalSlice, "", nil, "Tengo mucha vergüenza."); got == nil || !*got {
		t.Fatalf("expected slice to match")
	}

	// exact_match with list
	evalExactList := &tests.Evaluation{
		Type:     "exact_match",
		Expected: []string{"knight", "knave"},
	}
	if got := scoreEval(evalExactList, "", nil, "Knave"); got == nil || !*got {
		t.Fatalf("expected Knave to match exact_match list")
	}
	if got := scoreEval(evalExactList, "", nil, "Knight"); got == nil || !*got {
		t.Fatalf("expected Knight to match exact_match list")
	}
	if got := scoreEval(evalExactList, "", nil, "Villain"); got == nil || *got {
		t.Fatalf("expected Villain to fail")
	}

	// diacritics / accents: biol vs bióloga
	evalBiol := evalOf("contains", "biol", "")
	if got := scoreEval(evalBiol, "", nil, "Es una bióloga marina que estudia pulpos."); got == nil || !*got {
		t.Fatalf("expected biol to match bióloga")
	}
	if got := scoreEval(evalBiol, "", nil, "Es un biólogo marino."); got == nil || !*got {
		t.Fatalf("expected biol to match biólogo")
	}

	// exact_match with diacritic differences
	evalFrio := evalOf("exact_match", "frio", "")
	if got := scoreEval(evalFrio, "", nil, "frío"); got == nil || !*got {
		t.Fatalf("expected frío to match exact_match frio")
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

func evalSchema(schema map[string]any) *tests.Evaluation {
	return &tests.Evaluation{Type: "json_schema", Schema: schema}
}

func TestScoreJSONSchemaIgnoresInlineReasoning(t *testing.T) {
	person := evalSchema(map[string]any{
		"type":     "object",
		"required": []string{"name", "age", "job", "city"},
	})
	cases := []struct {
		name     string
		eval     *tests.Evaluation
		response string
		want     bool
	}{
		{
			name:     "plain object",
			eval:     person,
			response: `{"name": "John Doe", "age": 34, "job": "engineer", "city": "Berlin"}`,
			want:     true,
		},
		{
			name:     "think block before object",
			eval:     person,
			response: "<think>\nReasoning about the fields.\n</think>\n\n{\"name\": \"John Doe\", \"age\": 34, \"job\": \"engineer\", \"city\": \"Berlin\"}",
			want:     true,
		},
		{
			name:     "duplicated answer with stray closing tag",
			eval:     person,
			response: "{\"name\": \"Ada\", \"age\": 36, \"job\": \"scientist\", \"city\": \"London\"}\n</think>\n\n{\"name\": \"Ada\", \"age\": 36, \"job\": \"scientist\", \"city\": \"London\"}",
			want:     true,
		},
		{
			name:     "missing required field fails",
			eval:     person,
			response: `<think>thinking</think>{"name": "Ada", "age": 36}`,
			want:     false,
		},
		{
			name: "array with reasoning",
			eval: evalSchema(map[string]any{
				"type":     "array",
				"minItems": 3,
				"maxItems": 5,
				"items":    map[string]any{"type": "string"},
			}),
			response: "<think>Pick fruits.</think>\n[\"apple\", \"banana\", \"orange\"]",
			want:     true,
		},
		{
			name:     "markdown fences still fail",
			eval:     person,
			response: "```json\n{\"name\": \"Ada\", \"age\": 36, \"job\": \"x\", \"city\": \"y\"}\n```",
			want:     false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scoreEval(tc.eval, "", nil, tc.response)
			if got == nil || *got != tc.want {
				t.Fatalf("scoreEval = %v, want %v", got, tc.want)
			}
		})
	}
}

func boolPtr(b bool) *bool { return &b }

func TestScoreHumanReview(t *testing.T) {
	eval := &tests.Evaluation{Type: "human_review"}

	t.Run("empty response fails without review", func(t *testing.T) {
		got := scoreEval(eval, "", nil, "")
		if got == nil || *got != false {
			t.Fatalf("expected false for empty response, got %v", got)
		}
	})

	t.Run("whitespace only response fails without review", func(t *testing.T) {
		got := scoreEval(eval, "", nil, "   \n\t  ")
		if got == nil || *got != false {
			t.Fatalf("expected false for whitespace response, got %v", got)
		}
	})

	t.Run("non-empty response leaves verdict nil for human review", func(t *testing.T) {
		got := scoreEval(eval, "", nil, "Paris")
		if got != nil {
			t.Fatalf("expected nil for valid response, got %v", got)
		}
	})
}

func TestHasReviewableOutput(t *testing.T) {
	if HasReviewableOutput(TestResult{ModelResponse: ""}) {
		t.Fatal("expected false for empty ModelResponse")
	}
	if HasReviewableOutput(TestResult{ModelResponse: "   \n"}) {
		t.Fatal("expected false for whitespace ModelResponse")
	}
	if !HasReviewableOutput(TestResult{ModelResponse: "Hello"}) {
		t.Fatal("expected true for non-empty ModelResponse")
	}

	subEmpty := TestResult{
		SubResults: []SubResult{
			{ModelResponse: ""},
			{ModelResponse: "   "},
		},
	}
	if HasReviewableOutput(subEmpty) {
		t.Fatal("expected false for all empty SubResults")
	}

	subMixed := TestResult{
		SubResults: []SubResult{
			{ModelResponse: ""},
			{ModelResponse: "Paris"},
		},
	}
	if !HasReviewableOutput(subMixed) {
		t.Fatal("expected true for SubResults with at least one non-empty response")
	}
}

func TestSanitizeEmptyReviewResults(t *testing.T) {
	passVal := true
	results := []TestResult{
		{
			TestID:        "t1",
			Passed:        nil,
			ModelResponse: "",
		},
		{
			TestID:        "t2",
			Passed:        nil,
			ModelResponse: "some answer",
		},
		{
			TestID:        "t3",
			Passed:        &passVal,
			ModelResponse: "already passed",
		},
		{
			TestID: "t4",
			Passed: nil,
			SubResults: []SubResult{
				{ModelResponse: ""},
				{ModelResponse: "   "},
			},
		},
	}

	SanitizeEmptyReviewResults(results)

	if results[0].Passed == nil || *results[0].Passed != false {
		t.Fatalf("expected results[0].Passed == false, got %v", results[0].Passed)
	}
	if results[1].Passed != nil {
		t.Fatalf("expected results[1].Passed == nil, got %v", results[1].Passed)
	}
	if results[2].Passed == nil || !*results[2].Passed {
		t.Fatalf("expected results[2].Passed == true, got %v", results[2].Passed)
	}
	if results[3].Passed == nil || *results[3].Passed != false {
		t.Fatalf("expected results[3].Passed == false, got %v", results[3].Passed)
	}
	for i, sub := range results[3].SubResults {
		if sub.Passed == nil || *sub.Passed != false {
			t.Fatalf("expected sub[%d].Passed == false, got %v", i, sub.Passed)
		}
	}
}
