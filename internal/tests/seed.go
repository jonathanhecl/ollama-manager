package tests

import (
	"encoding/json"
	"fmt"
	"time"
)

// SeedIfEmpty creates default groups and tests when the store has no data.
// It is safe to call multiple times — it only seeds when truly empty.
func (s *Store) SeedIfEmpty() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Already has groups or tests — skip.
	if len(s.groups) > 0 || len(s.tests) > 0 {
		return nil
	}

	now := time.Now().UTC()

	// ---------- Groups ----------
	gCore := &Group{
		ID:          "core",
		Name:        "Core Skills",
		Description: "Basic reasoning, math, and logic",
		Order:       0,
	}
	gTools := &Group{
		ID:           "tools",
		Name:         "One-Shot Tool Use",
		Description:  "Single-turn tool-calling capability tests",
		RequiredCaps: []string{"tools"},
		Order:        1,
	}
	gMulti := &Group{
		ID:          "multimodal",
		Name:        "Multimodal",
		Description: "Vision, audio, and file input tests",
		Order:       2,
	}
	gJSON := &Group{
		ID:          "structured",
		Name:        "Structured Output",
		Description: "JSON schema and structured format tests",
		Order:       3,
	}

	s.groups = map[string]*Group{
		gCore.ID:  gCore,
		gTools.ID: gTools,
		gMulti.ID: gMulti,
		gJSON.ID:  gJSON,
	}

	// ---------- Tests ----------
	seedTests := []Test{
		// === Core Skills ===
		{
			ID:             "t1",
			Name:           "Basic Arithmetic",
			Description:    "Evaluates whether the model can follow order of operations.",
			GroupID:        gCore.ID,
			Active:         true,
			Order:          0,
			Prompt:         "What is 2 + 3 * 4? Return only the final number.",
			EvaluationType: "contains",
			EvaluationConfig: mustJSON(map[string]any{
				"expected": "14",
			}),
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:             "t2",
			Name:           "Fraction Simplification",
			Description:    "Checks understanding of fraction reduction.",
			GroupID:        gCore.ID,
			Active:         true,
			Order:          1,
			Prompt:         "Simplify the fraction 18/24 to its lowest terms.",
			EvaluationType: "contains",
			EvaluationConfig: mustJSON(map[string]any{
				"expected": "3/4",
			}),
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:             "t3",
			Name:           "Logic Deduction",
			Description:    "Simple syllogism to verify reasoning chain.",
			GroupID:        gCore.ID,
			Active:         true,
			Order:          2,
			Prompt:         "All birds have wings. A penguin is a bird. What conclusion can you draw about penguins?",
			EvaluationType: "contains",
			EvaluationConfig: mustJSON(map[string]any{
				"expected": "wings",
			}),
			CreatedAt: now,
			UpdatedAt: now,
		},

		// === One-Shot Tool Use ===
		// In one-shot tests the system prompt defines available tools.
		// The evaluation checks that the model emits the tool call pattern.
		{
			ID:          "t4",
			Name:        "Weather Tool Call",
			Description: "One-shot: model must emit a get_weather call.",
			GroupID:     gTools.ID,
			Active:      true,
			Order:       0,
			SystemPrompt: `You have access to the following tool:
get_weather(location: string) -> {temperature: number, condition: string}
When the user asks about weather, respond ONLY with the tool call. Example:
get_weather("London")
Do not add any other text.`,
			Prompt:         "What is the weather like in Paris right now?",
			RequiredCaps:   []string{"tools"},
			EvaluationType: "regex",
			EvaluationConfig: mustJSON(map[string]any{
				"pattern": `(?i)get_weather\s*\(\s*"Paris"\s*\)`,
			}),
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:          "t5",
			Name:        "Calculator Tool Call",
			Description: "One-shot: model must use calculator tool for complex math.",
			GroupID:     gTools.ID,
			Active:      true,
			Order:       1,
			SystemPrompt: `You have access to the following tool:
calculator(expression: string) -> number
When a calculation is requested, respond ONLY with the tool call. Example:
calculator("12 * 34")
Do not add any other text.`,
			Prompt:         "Compute exactly 47 * 128 + 93.",
			RequiredCaps:   []string{"tools"},
			EvaluationType: "regex",
			EvaluationConfig: mustJSON(map[string]any{
				"pattern": `(?i)calculator\s*\(\s*".*47.*\*.*128.*\+.*93.*"\s*\)`,
			}),
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:          "t6",
			Name:        "Search Tool Call",
			Description: "One-shot: model must emit a web_search call.",
			GroupID:     gTools.ID,
			Active:      true,
			Order:       2,
			SystemPrompt: `You have access to the following tool:
web_search(query: string) -> [{title, snippet, url}]
When the user asks for current information, respond ONLY with the tool call. Example:
web_search("latest Mars rover news")
Do not add any other text.`,
			Prompt:         "Who won the last FIFA World Cup? I need up-to-date information.",
			RequiredCaps:   []string{"tools"},
			EvaluationType: "regex",
			EvaluationConfig: mustJSON(map[string]any{
				"pattern": `(?i)web_search\s*\(`,
			}),
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:          "t7",
			Name:        "Multi-Tool Routing",
			Description: "One-shot: model must choose the correct tool among several options.",
			GroupID:     gTools.ID,
			Active:      true,
			Order:       3,
			SystemPrompt: `You have two tools:
- get_stock_price(ticker: string) -> {price: number, currency: string}
- get_weather(location: string) -> {temperature: number, condition: string}
Respond ONLY with the correct tool call. No extra text.`,
			Prompt:         "I want to know the current share price of Apple (AAPL).",
			RequiredCaps:   []string{"tools"},
			EvaluationType: "regex",
			EvaluationConfig: mustJSON(map[string]any{
				"pattern": `(?i)get_stock_price\s*\(\s*"AAPL"\s*\)`,
			}),
			CreatedAt: now,
			UpdatedAt: now,
		},

		// === Multimodal (requires user-supplied attachments) ===
		{
			ID:             "t8",
			Name:           "Vision: Color Identification",
			Description:    "Attach an image and verify the model describes its dominant color.",
			GroupID:        gMulti.ID,
			Active:         true,
			Order:          0,
			Prompt:         "What is the dominant color in the attached image? Answer with a single color name.",
			RequiredCaps:   []string{"vision"},
			EvaluationType: "human_review",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
		{
			ID:             "t9",
			Name:           "Audio: Spoken Number",
			Description:    "Attach an audio file with a spoken number and verify transcription.",
			GroupID:        gMulti.ID,
			Active:         true,
			Order:          1,
			Prompt:         "Listen to the attached audio. What number is being said? Return only digits.",
			RequiredCaps:   []string{"audio"},
			EvaluationType: "human_review",
			CreatedAt:      now,
			UpdatedAt:      now,
		},

		// === Structured Output ===
		{
			ID:          "t10",
			Name:        "JSON Person Object",
			Description: "Model must return strictly valid JSON matching a schema.",
			GroupID:     gJSON.ID,
			Active:      true,
			Order:       0,
			Prompt: `Extract the person information from this text and return ONLY a JSON object:
"John Doe is 34 years old and works as a software engineer in Berlin."`,
			SystemPrompt:   "You must respond with valid JSON only. Do not wrap in markdown code blocks.",
			EvaluationType: "json_schema",
			EvaluationConfig: mustJSON(map[string]any{
				"schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{"type": "string"},
						"age":  map[string]any{"type": "number"},
						"job":  map[string]any{"type": "string"},
						"city": map[string]any{"type": "string"},
					},
					"required": []string{"name", "age", "job", "city"},
				},
			}),
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:             "t11",
			Name:           "JSON Array of Items",
			Description:    "Model must return a JSON array with exactly 3 items.",
			GroupID:        gJSON.ID,
			Active:         true,
			Order:          1,
			Prompt:         "List three programming languages invented before 1990. Return ONLY a JSON array of strings. No markdown.",
			SystemPrompt:   "Respond with a raw JSON array. No extra text.",
			EvaluationType: "json_schema",
			EvaluationConfig: mustJSON(map[string]any{
				"schema": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "string",
					},
					"minItems": 3,
					"maxItems": 3,
				},
			}),
			CreatedAt: now,
			UpdatedAt: now,
		},
	}

	for i := range seedTests {
		t := seedTests[i]
		s.tests[t.ID] = &t
	}

	// Persist to disk.
	if err := s.saveGroupsLocked(); err != nil {
		return fmt.Errorf("seed groups: %w", err)
	}
	for gid := range s.groups {
		if err := s.saveTestsLocked(gid); err != nil {
			return fmt.Errorf("seed tests %s: %w", gid, err)
		}
	}
	if err := s.saveTestsLocked(""); err != nil {
		return fmt.Errorf("seed ungrouped: %w", err)
	}
	return nil
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
