package tests

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Tiny 1x1 JPEG (base64) for seed vision test.
const seedImageB64 = "/9j/4AAQSkZJRgABAQEASABIAAD/2wBDAAEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQH/wAARCAABAAEDASIAAhEBAxEB/8QAFQABAQAAAAAAAAAAAAAAAAAAAAn/xAAUEAEAAAAAAAAAAAAAAAAAAAAA/8QAFQEBAQAAAAAAAAAAAAAAAAAABQf/xAAUEQEAAAAAAAAAAAAAAAAAAAAA/9oADAMBAAIRAxEAPwC3/9k="

// Tiny silent WAV (header-only, base64) for seed audio test.
const seedAudioB64 = "UklGRiQAAABXQVZFZm10IBAAAAABAAEAQB8AAEAfAAABAAgAZGF0YQAAAAA="

//go:embed seeddata/flag-argentina.b64
var seedFlagArgentinaB64 string

//go:embed seeddata/spoken-number-691.b64
var seedSpokenNumberB64 string

//go:embed seeddata/test-spanish-audio.b64
var seedSpanishAudioB64 string

//go:embed seeddata/code-png.b64
var seedCodePngB64 string

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
	gAgent := &Group{
		ID:           "agent",
		Name:         "Multi-Turn Agent",
		Description:  "Multi-turn agent capability tests with sandboxed file system",
		RequiredCaps: []string{"tools"},
		Order:        4,
	}

	s.groups = map[string]*Group{
		gCore.ID:  gCore,
		gTools.ID: gTools,
		gMulti.ID: gMulti,
		gJSON.ID:  gJSON,
		gAgent.ID: gAgent,
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
			Prompt:         "Simplify the fraction 18/24 to its lowest terms. Answer with plain text only (no LaTeX, no markdown).",
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

		// === Multimodal ===
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
			Attachments: []Attachment{
				{ID: "att-img-1", Kind: "image", Name: "bandera-argentina.png", Mime: "image/png", Data: strings.TrimSpace(seedFlagArgentinaB64)},
			},
			CreatedAt: now,
			UpdatedAt: now,
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
			EvaluationType: "exact_match",
			EvaluationConfig: mustJSON(map[string]any{
				"expected": "691",
			}),
			Attachments: []Attachment{
				{ID: "att-aud-1", Kind: "audio", Name: "test.wav", Mime: "audio/wav", Data: strings.TrimSpace(seedSpokenNumberB64)},
			},
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:             "t15",
			Name:           "Audio: Transcription Spanish",
			Description:    "Verify the model can transcribe Spanish speech from audio.",
			GroupID:        gMulti.ID,
			Active:         true,
			Order:          2,
			Prompt:         "Transcribe this audio",
			RequiredCaps:   []string{"audio"},
			EvaluationType: "contains",
			EvaluationConfig: mustJSON(map[string]any{
				"expected": "Esta es una prueba de audio",
			}),
			Attachments: []Attachment{
				{ID: "att-aud-sp", Kind: "audio", Name: "test.wav", Mime: "audio/wav", Data: strings.TrimSpace(seedSpanishAudioB64)},
			},
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:             "t16",
			Name:           "Vision: Copy Code",
			Description:    "Verify the model can transcribe source code from an image.",
			GroupID:        gMulti.ID,
			Active:         true,
			Order:          3,
			Prompt:         "Transcribe this code. Do not add anything else.",
			RequiredCaps:   []string{"vision"},
			EvaluationType: "contains",
			EvaluationConfig: mustJSON(map[string]any{
				"expected": "func hasAllCaps(have, need []string) bool {\n\tif len(need) == 0 {\n\t\treturn true\n\t}\n\tset := make(map[string]bool, len(have))\n\tfor _, c := range have {\n\t\tset[c] = true\n\t}\n\tfor _, c := range need {\n\t\tif !set[c] {\n\t\t\treturn false\n\t\t}\n\t}\n\treturn true\n}\n\nfunc newRunID() string {\n\tb := make([]byte, 6)\n\t_, _ = rand.Read(b)\n\treturn \"run-\" + hex.EncodeToString(b)\n}",
			}),
			Attachments: []Attachment{
				{ID: "att-img-code", Kind: "image", Name: "code.png", Mime: "image/png", Data: strings.TrimSpace(seedCodePngB64)},
			},
			CreatedAt: now,
			UpdatedAt: now,
		},

		// === Structured Output ===
		{
			ID:          "t10",
			Name:        "JSON Person Object",
			Description: "Model must return strictly valid JSON matching a schema.",
			GroupID:     gJSON.ID,
			Active:      true,
			Order:       0,
			Prompt: `Extract the person information from this text and return ONLY a raw JSON object. Do not wrap in markdown code blocks. No extra text.
Text: "John Doe is 34 years old and works as a software engineer in Berlin."`,
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
			Prompt:         "List exactly three programming languages invented before 1990. Return ONLY a raw JSON array of strings. Do not wrap in markdown code blocks. No extra text. Example format: [\"Fortran\", \"Lisp\", \"C\"]",
			SystemPrompt:   "Respond with a raw JSON array. No extra text. No markdown.",
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

		// === Multi-Turn Agent (human review) ===
		{
			ID:          "t12",
			Name:        "Animated Solar System",
			Description: "Build a complete animated solar system with HTML/CSS/JS. The model must create files, iterate, and accept human feedback.",
			GroupID:     gAgent.ID,
			Active:      true,
			Order:       0,
			Prompt: `Create a complete animated solar system using HTML, CSS, and JavaScript.
Requirements:
- The sun in the center with a glow effect
- At least 4 planets orbiting at different speeds (relative to real orbital periods)
- Pause / resume controls
- Zoom controls
- Smooth animations using CSS or Canvas

Use the available tools to create files in the sandbox. Start by creating index.html, style.css, and script.js.`,
			SystemPrompt: `You are a web developer agent. You have access to file tools (read_file, write_file, list_dir, exec).
Your task is to build a web project by creating and editing files in the sandbox.
When you need to make changes, use the tools directly. Do not ask for permission.
After each change, explain what you did.`,
			EvaluationType: "agent",
			EvaluationConfig: mustJSON(map[string]any{
				"max_turns": 15,
				"initial_files": []map[string]string{
					{"path": "index.html", "content": "<!DOCTYPE html>\n<html>\n<head><title>Solar System</title><link rel=\"stylesheet\" href=\"style.css\"></head>\n<body>\n<div id=\"solar-system\"></div>\n<script src=\"script.js\"></script>\n</body>\n</html>"},
					{"path": "style.css", "content": "body { margin: 0; background: #000; overflow: hidden; }\n#solar-system { width: 100vw; height: 100vh; position: relative; }"},
					{"path": "script.js", "content": "// Solar system logic goes here\n"},
				},
				"tools":        []string{"read_file", "write_file", "list_dir", "exec"},
				"human_review": true,
			}),
			RequiredCaps: []string{"tools"},
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			ID:          "t13",
			Name:        "Shoe Store Website",
			Description: "Build a single-page shoe store with product grid, cart, and responsive layout.",
			GroupID:     gAgent.ID,
			Active:      true,
			Order:       1,
			Prompt: `Build a single-page shoe store website.
Requirements:
- A product grid showing at least 6 different shoes with images (use placeholder URLs), names, and prices
- An "Add to cart" button on each product
- A shopping cart sidebar that shows added items, quantities, and total price
- A responsive layout that works on mobile and desktop
- Use vanilla HTML, CSS, and JavaScript (no external libraries)

Create the necessary files using the sandbox tools.`,
			SystemPrompt: `You are a web developer agent with access to file tools (read_file, write_file, list_dir, exec).
Build the requested web project by creating and editing files. Use the tools directly.
Explain your changes after each action.`,
			EvaluationType: "agent",
			EvaluationConfig: mustJSON(map[string]any{
				"max_turns": 12,
				"initial_files": []map[string]string{
					{"path": "index.html", "content": "<!DOCTYPE html>\n<html lang=\"en\">\n<head><meta charset=\"UTF-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1.0\"><title>Shoe Store</title><link rel=\"stylesheet\" href=\"style.css\"></head>\n<body>\n<header><h1>Shoe Store</h1></header>\n<main id=\"app\"></main>\n<script src=\"app.js\"></script>\n</body>\n</html>"},
					{"path": "style.css", "content": "* { box-sizing: border-box; margin: 0; padding: 0; }\nbody { font-family: sans-serif; background: #f5f5f5; }"},
					{"path": "app.js", "content": "// Shoe store logic goes here\n"},
				},
				"tools":        []string{"read_file", "write_file", "list_dir", "exec"},
				"human_review": true,
			}),
			RequiredCaps: []string{"tools"},
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			ID:          "t14",
			Name:        "To-Do App",
			Description: "Create a functional to-do list in a single HTML file with localStorage persistence.",
			GroupID:     gAgent.ID,
			Active:      true,
			Order:       2,
			Prompt: `Create a fully functional to-do list application in a single HTML file.
Requirements:
- Add new tasks with a text input and a button
- Mark tasks as complete (strikethrough or checkbox)
- Delete individual tasks
- Filter tasks by: All, Active, Completed
- Persist tasks in localStorage so they survive page reloads
- Clean, modern UI with CSS

Create index.html and implement everything inside it (HTML + CSS + JS).`,
			SystemPrompt: `You are a web developer agent with access to file tools (read_file, write_file, list_dir, exec).
Build the requested application by creating and editing files in the sandbox.
Use the tools directly. Explain your changes after each action.`,
			EvaluationType: "agent",
			EvaluationConfig: mustJSON(map[string]any{
				"max_turns": 10,
				"initial_files": []map[string]string{
					{"path": "index.html", "content": "<!DOCTYPE html>\n<html lang=\"en\">\n<head><meta charset=\"UTF-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1.0\"><title>To-Do App</title></head>\n<body>\n<div id=\"app\"></div>\n<script></script>\n</body>\n</html>"},
				},
				"tools":        []string{"read_file", "write_file", "list_dir", "exec"},
				"human_review": true,
			}),
			RequiredCaps: []string{"tools"},
			CreatedAt:    now,
			UpdatedAt:    now,
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
