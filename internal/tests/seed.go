package tests

import (
	"time"
)

// Seed catalog IDs for default template examples.
var seedExampleIDs = map[string]struct{}{
	"example-arithmetic":   {},
	"example-weather-tool": {},
	"example-multi-turn":   {},
	"example-instructions": {},
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
			Name:         "Math Suite (Multiple Exercises)",
			Description:  "Evaluates multiple diverse arithmetic and mathematical operations in a single test.",
			GroupID:      "examples",
			Active:       true,
			Order:        0,
			SystemPrompt: "You are a concise calculator. Reply with only the final numerical answer or expression.",
			Cases: []TestCase{
				{
					Name:   "Basic order of operations",
					Prompt: "What is 2 + 3 * 4? Return only the final number.",
					Evaluation: &Evaluation{
						Type:     "contains",
						Expected: "14",
					},
				},
				{
					Name:   "Fraction simplification",
					Prompt: "Simplify the fraction 18/24 to its lowest terms. Answer with plain text only.",
					Evaluation: &Evaluation{
						Type:     "contains",
						Expected: "3/4",
					},
				},
				{
					Name:   "Exponentiation",
					Prompt: "What is 2 raised to the power of 8 (2^8)? Return only the number.",
					Evaluation: &Evaluation{
						Type:     "contains",
						Expected: "256",
					},
				},
				{
					Name:   "Percentages",
					Prompt: "What is 15% of 200? Return only the number.",
					Evaluation: &Evaluation{
						Type:     "contains",
						Expected: "30",
					},
				},
			},
			Filename:  "arithmetic.yaml",
			CreatedAt: now,
			UpdatedAt: now,
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
			Evaluation: &Evaluation{
				Type:    "regex",
				Pattern: `(?i)get_weather\s*\(\s*"Paris"\s*\)`,
			},
			Filename:  "weather_tool.yaml",
			CreatedAt: now,
			UpdatedAt: now,
		}, true
	case "example-multi-turn":
		return Test{
			ID:           "example-multi-turn",
			Name:         "Sequential Dialogue Chain",
			Description:  "Multi-step interactive chain testing sequential context retention across turns.",
			GroupID:      "examples",
			Active:       true,
			Order:        2,
			SystemPrompt: "You are a helpful and concise programming assistant.",
			Steps: []Step{
				{
					Step:   1,
					Name:   "Initial context inquiry",
					Prompt: "I am learning Python for data analysis and machine learning. What is the primary library used for dataframes?",
					Evaluation: &Evaluation{
						Type:     "contains",
						Expected: "pandas",
					},
				},
				{
					Step:   2,
					Name:   "Contextual follow-up",
					Prompt: "What programming language did I mention I was learning in my previous message? Reply with just the language name.",
					Evaluation: &Evaluation{
						Type:     "contains",
						Expected: "Python",
					},
				},
			},
			Filename:  "multi_turn.yaml",
			CreatedAt: now,
			UpdatedAt: now,
		}, true
	case "example-instructions":
		tempLow := 0.2
		tempHigh := 0.9
		return Test{
			ID:           "example-instructions",
			Name:         "Instruction Following (Per-Case Config)",
			Description:  "Demonstrates per-case system prompts, per-case options and chained multi-turn steps within a case.",
			GroupID:      "examples",
			Active:       true,
			Order:        3,
			SystemPrompt: "You are a concise assistant. Always reply in English.",
			Options:      &TestOptions{Temperature: &tempLow},
			Cases: []TestCase{
				{
					Name:   "Inherits global system prompt",
					Prompt: `Say the word "apple" and nothing else.`,
					Evaluation: &Evaluation{
						Type:     "contains",
						Expected: "apple",
					},
				},
				{
					Name:         "Pirate voice override",
					Prompt:       "Say hello in one short sentence.",
					SystemPrompt: `You are a pirate. Every reply must contain the word "arr".`,
					Options:      &TestOptions{Temperature: &tempHigh},
					Evaluation: &Evaluation{
						Type:    "regex",
						Pattern: `(?i)\barr\b`,
					},
				},
				{
					Name:   "Follow a list of instructions",
					Prompt: "Memorize this list in order: red, green, blue. Reply with only the word OK.",
					Evaluation: &Evaluation{
						Type:     "contains",
						Expected: "OK",
					},
					Steps: []CaseStep{
						{
							Name:   "Recall second item",
							Prompt: "What was the second color of the list? Reply with just the color.",
							Evaluation: &Evaluation{
								Type:     "contains",
								Expected: "green",
							},
						},
						{
							Name:   "Repeat full list",
							Prompt: "Repeat the full list in order, comma-separated, with nothing else.",
							Evaluation: &Evaluation{
								Type:    "regex",
								Pattern: `(?i)red.*green.*blue`,
							},
						},
					},
				},
				{
					Name:   "Mid-chain voice switch",
					Prompt: `Say the word "start" and nothing else.`,
					Evaluation: &Evaluation{
						Type:     "contains",
						Expected: "start",
					},
					Steps: []CaseStep{
						{
							Name:   "Still the default voice",
							Prompt: `Say the word "middle" and nothing else.`,
							Evaluation: &Evaluation{
								Type:     "contains",
								Expected: "middle",
							},
						},
						{
							Name:         "Switch to pirate",
							Prompt:       "Say hello in one short sentence.",
							SystemPrompt: `You are a pirate. Every reply must contain the word "arr".`,
							Evaluation: &Evaluation{
								Type:    "regex",
								Pattern: `(?i)\barr\b`,
							},
						},
						{
							Name:   "Pirate voice sticks",
							Prompt: "Say goodbye in one short sentence.",
							Evaluation: &Evaluation{
								Type:    "regex",
								Pattern: `(?i)\barr\b`,
							},
						},
					},
				},
			},
			Filename:  "instructions.yaml",
			CreatedAt: now,
			UpdatedAt: now,
		}, true
	}
	return Test{}, false
}
