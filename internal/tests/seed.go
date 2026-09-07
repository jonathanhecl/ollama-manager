package tests

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"time"
)

// Seed catalog IDs for default template examples.
var seedExampleIDs = map[string]struct{}{
	"example-arithmetic":   {},
	"example-weather-tool": {},
	"example-multi-turn":   {},
	"example-exact-math":   {},
	"example-codegen":      {},
	"example-json-output":  {},
	"example-instructions": {},
	"example-format-regex": {},
	"example-memory":       {},
	"example-human-review": {},
	"example-vision-cases": {},
}

// backfillSeedIDs are seeds added after the initial catalog: Load() creates
// the ones missing on disk (existing installs get them automatically). The
// original three are never backfilled so a deliberate deletion sticks.
var backfillSeedIDs = []string{
	"example-exact-math",
	"example-codegen",
	"example-json-output",
	"example-instructions",
	"example-format-regex",
	"example-memory",
	"example-human-review",
	"example-vision-cases",
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
	case "example-exact-math":
		return Test{
			ID:           "example-exact-math",
			Name:         "Exact Math (EN/ES)",
			Description:  "Bilingual exact-match arithmetic: the answer must be exactly the number.",
			GroupID:      "examples",
			Active:       true,
			Order:        3,
			SystemPrompt: "Reply with only the final number. No words, no punctuation.",
			Cases: []TestCase{
				{
					Name:   "Multiplication EN",
					Prompt: "Reply with only the number: 7 * 6",
					Evaluation: &Evaluation{
						Type:     "exact_match",
						Expected: "42",
					},
				},
				{
					Name:   "Multiplicación ES",
					Prompt: "Responde solo con el número: 12 * 12",
					Evaluation: &Evaluation{
						Type:     "exact_match",
						Expected: "144",
					},
				},
			},
			Filename:  "exact_math.yaml",
			CreatedAt: now,
			UpdatedAt: now,
		}, true
	case "example-codegen":
		return Test{
			ID:           "example-codegen",
			Name:         "Code Generation (EN/ES)",
			Description:  "Bilingual code-writing check via keyword presence.",
			GroupID:      "examples",
			Active:       true,
			Order:        4,
			SystemPrompt: "Reply with code only, no explanations.",
			Cases: []TestCase{
				{
					Name:   "Python function",
					Prompt: "Write a Python function add(a, b) that returns their sum.",
					Evaluation: &Evaluation{
						Type:     "contains",
						Expected: "def add",
					},
				},
				{
					Name:   "Función JavaScript",
					Prompt: "Escribe una función JavaScript sumar(a, b) que devuelva la suma.",
					Evaluation: &Evaluation{
						Type:     "contains",
						Expected: "function",
					},
				},
			},
			Filename:  "codegen.yaml",
			CreatedAt: now,
			UpdatedAt: now,
		}, true
	case "example-json-output":
		return Test{
			ID:           "example-json-output",
			Name:         "Structured JSON Output (EN/ES)",
			Description:  "Checks the model follows a JSON schema exactly.",
			GroupID:      "examples",
			Active:       true,
			Order:        5,
			SystemPrompt: "Reply with valid JSON only, no prose.",
			Cases: []TestCase{
				{
					Name:   "Fruit array",
					Prompt: "Reply with a JSON array of exactly 3 fruit names.",
					Evaluation: &Evaluation{
						Type:   "json_schema",
						Schema: map[string]any{"type": "array", "minItems": 3, "maxItems": 3, "items": map[string]any{"type": "string"}},
					},
				},
				{
					Name:   "Objeto persona",
					Prompt: "Responde con un objeto JSON con las claves obligatorias nombre y edad.",
					Evaluation: &Evaluation{
						Type:   "json_schema",
						Schema: map[string]any{"type": "object", "required": []string{"nombre", "edad"}},
					},
				},
			},
			Filename:  "json_output.yaml",
			CreatedAt: now,
			UpdatedAt: now,
		}, true
	case "example-format-regex":
		return Test{
			ID:           "example-format-regex",
			Name:         "Format Compliance (EN/ES)",
			Description:  "Bilingual regex check for exact-format compliance.",
			GroupID:      "examples",
			Active:       true,
			Order:        6,
			SystemPrompt: "Follow the format instruction exactly.",
			Cases: []TestCase{
				{
					Name:   "Ticket code",
					Prompt: "Reply with exactly: OK-1234 (nothing else)",
					Evaluation: &Evaluation{
						Type:    "regex",
						Pattern: `^OK-\d{4}$`,
					},
				},
				{
					Name:   "Código de parte",
					Prompt: "Responde exactamente: PIEZA-77 (nada más)",
					Evaluation: &Evaluation{
						Type:    "regex",
						Pattern: `^PIEZA-\d{2}$`,
					},
				},
			},
			Filename:  "instructions_regex.yaml",
			CreatedAt: now,
			UpdatedAt: now,
		}, true
	case "example-memory":
		return Test{
			ID:           "example-memory",
			Name:         "Bilingual Memory Chain",
			Description:  "Multi-step memory across languages: fact stated in English, recalled in Spanish.",
			GroupID:      "examples",
			Active:       true,
			Order:        7,
			SystemPrompt: "Be concise.",
			Steps: []Step{
				{
					Step:   1,
					Name:   "Set fact EN",
					Prompt: "My sister Clara is a marine biologist who studies octopuses. Remember this.",
					Evaluation: &Evaluation{
						Type:     "contains",
						Expected: "Clara",
					},
				},
				{
					Step:   2,
					Name:   "Recall ES",
					Prompt: "¿A qué se dedica mi hermana? Responde en una frase corta.",
					Evaluation: &Evaluation{
						Type:     "contains",
						Expected: "biol",
					},
				},
			},
			Filename:  "memory_bilingual.yaml",
			CreatedAt: now,
			UpdatedAt: now,
		}, true
	case "example-human-review":
		return Test{
			ID:           "example-human-review",
			Name:         "Creative Writing (human review)",
			Description:  "Open-ended bilingual writing sample for manual rating with good/regular/bad.",
			GroupID:      "examples",
			Active:       true,
			Order:        8,
			Cases: []TestCase{
				{
					Name:   "Haiku",
					Prompt: "Write a haiku about a lighthouse in winter.",
					Evaluation: &Evaluation{
						Type: "human_review",
					},
				},
				{
					Name:   "Microcuento",
					Prompt: "Escribe un microcuento de tres líneas sobre un faro en invierno.",
					Evaluation: &Evaluation{
						Type: "human_review",
					},
				},
			},
			Filename:  "human_review.yaml",
			CreatedAt: now,
			UpdatedAt: now,
		}, true
	case "example-vision-cases":
		return Test{
			ID:           "example-vision-cases",
			Name:         "Vision per Case (sidecars)",
			Description:  "Each case carries its own file: vision-cases-1.png and vision-cases-2.png (images) plus vision-cases-3.txt (inlined text). Requires a vision-capable model.",
			GroupID:      "examples",
			Active:       true,
			Order:        9,
			RequiredCaps: []string{"vision"},
			Cases: []TestCase{
				{
					Name:   "Red circle",
					Prompt: "What color is the circle in the image? Reply with only the color name in English.",
					Evaluation: &Evaluation{
						Type:     "contains",
						Expected: "red",
					},
				},
				{
					Name:   "Blue square",
					Prompt: "What shape is the blue figure in the image? Reply with only the shape name in English.",
					Evaluation: &Evaluation{
						Type:     "contains",
						Expected: "square",
					},
				},
				{
					Name:   "Attached note",
					Prompt: "According to the attached note, what is the capital mentioned? Reply with only the city name.",
					Evaluation: &Evaluation{
						Type:     "contains",
						Expected: "Lisboa",
					},
				},
			},
			Filename:  "vision_cases.yaml",
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
							Options:      &TestOptions{Temperature: &tempHigh},
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

// seedSidecarContent generates the binary content of a seed sidecar file so
// releases (binary-only installs) get the same fixtures as git checkouts.
// vision_cases-1.png: red circle on white; vision_cases-2.png: blue square
// on white; vision_cases-3.txt: short note naming a capital.
func seedSidecarContent(name string) ([]byte, bool) {
	switch name {
	case "vision_cases-1.png":
		return drawSeedShape("circle", color.RGBA{220, 30, 30, 255}), true
	case "vision_cases-2.png":
		return drawSeedShape("square", color.RGBA{30, 90, 220, 255}), true
	case "vision_cases-3.txt":
		return []byte("Field note: the expedition reached Lisboa at dawn. The harbor was quiet.\n"), true
	}
	return nil, false
}

func drawSeedShape(shape string, fg color.RGBA) []byte {
	const size = 200
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	white := color.RGBA{255, 255, 255, 255}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.Set(x, y, white)
		}
	}
	cx, cy := float64(size)/2, float64(size)/2
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx, dy := float64(x)-cx, float64(y)-cy
			inside := false
			switch shape {
			case "circle":
				inside = dx*dx+dy*dy <= 70*70
			case "square":
				inside = dx >= -60 && dx <= 60 && dy >= -60 && dy <= 60
			}
			if inside {
				img.Set(x, y, fg)
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}
