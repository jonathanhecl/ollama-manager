package tests

import (
	"time"
)

// Seed catalog IDs for default template examples.
var seedExampleIDs = map[string]struct{}{
	"example-arithmetic":   {},
	"example-weather-tool": {},
	"example-multi-turn":   {},
}

// IsSeedTestID reports whether id belongs to the default example catalog.
func IsSeedTestID(id string) bool {
	_, ok := seedExampleIDs[id]
	return ok
}

// GetSeedTest returns a copy of a default example test definition.
func GetSeedTest(id string, now time.Time) (Test, bool) {
	switch id {
	case "example-arithmetic":
		return Test{
			ID:           "example-arithmetic",
			Name:         "Basic Arithmetic",
			Description:  "Evaluates whether the model can follow order of operations.",
			GroupID:      "examples",
			Active:       true,
			Order:        0,
			SystemPrompt: "You are a concise calculator. Reply with only the final numerical answer.",
			Prompt:       "What is 2 + 3 * 4? Return only the final number.",
			Messages: []Message{
				{Role: "system", Content: "You are a concise calculator. Reply with only the final numerical answer."},
				{Role: "user", Content: "What is 2 + 3 * 4? Return only the final number."},
			},
			Evaluation: &Evaluation{
				Type:   "contains",
				Config: mustJSON(map[string]any{"expected": "14"}),
			},
			EvaluationType:   "contains",
			EvaluationConfig: mustJSON(map[string]any{"expected": "14"}),
			Filename:         "arithmetic.json",
			CreatedAt:        now,
			UpdatedAt:        now,
		}, true
	case "example-weather-tool":
		return Test{
			ID:           "example-weather-tool",
			Name:         "Weather Tool Call",
			Description:  "One-shot tool call evaluation for weather query.",
			GroupID:      "examples",
			Active:       true,
			Order:        1,
			RequiredCaps: []string{"tools"},
			SystemPrompt: "You have access to the following tool:\nget_weather(location: string) -> {temperature: number, condition: string}\nWhen the user asks about weather, respond ONLY with the tool call. Example:\nget_weather(\"London\")\nDo not add any other text.",
			Prompt:       "What is the weather like in Paris right now?",
			Messages: []Message{
				{Role: "system", Content: "You have access to the following tool:\nget_weather(location: string) -> {temperature: number, condition: string}\nWhen the user asks about weather, respond ONLY with the tool call. Example:\nget_weather(\"London\")\nDo not add any other text."},
				{Role: "user", Content: "What is the weather like in Paris right now?"},
			},
			Evaluation: &Evaluation{
				Type:   "regex",
				Config: mustJSON(map[string]any{"pattern": `(?i)get_weather\s*\(\s*"Paris"\s*\)`}),
			},
			EvaluationType:   "regex",
			EvaluationConfig: mustJSON(map[string]any{"pattern": `(?i)get_weather\s*\(\s*"Paris"\s*\)`}),
			Filename:         "weather_tool.json",
			CreatedAt:        now,
			UpdatedAt:        now,
		}, true
	case "example-multi-turn":
		return Test{
			ID:           "example-multi-turn",
			Name:         "Multi-Turn Dialogue",
			Description:  "Tests multi-turn context retention and following previous instructions.",
			GroupID:      "examples",
			Active:       true,
			Order:        2,
			SystemPrompt: "You are a helpful programming assistant.",
			Prompt:       "What programming language was I asking about in my first question? Answer with just the language name.",
			Messages: []Message{
				{Role: "system", Content: "You are a helpful programming assistant."},
				{Role: "user", Content: "I am learning Python for data analysis. Is it a good choice?"},
				{Role: "assistant", Content: "Yes, Python is an excellent choice for data analysis due to libraries like pandas, numpy, and matplotlib."},
				{Role: "user", Content: "What programming language was I asking about in my first question? Answer with just the language name."},
			},
			Evaluation: &Evaluation{
				Type:   "contains",
				Config: mustJSON(map[string]any{"expected": "Python"}),
			},
			EvaluationType:   "contains",
			EvaluationConfig: mustJSON(map[string]any{"expected": "Python"}),
			Filename:         "multi_turn.json",
			CreatedAt:        now,
			UpdatedAt:        now,
		}, true
	}
	return Test{}, false
}
