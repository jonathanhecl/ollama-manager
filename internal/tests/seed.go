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
			Name:         "Math Suite (Multiple Exercises)",
			Description:  "Evaluates multiple diverse arithmetic and mathematical operations.",
			GroupID:      "examples",
			Active:       true,
			Order:        0,
			SystemPrompt: "Eres un calculador conciso. Responde únicamente con el número o resultado final.",
			Cases: []TestCase{
				{
					Name:   "Jerarquía de operaciones básica",
					Prompt: "¿Cuánto es 2 + 3 * 4? Devuelve solo el número.",
					Evaluation: &Evaluation{
						Type:     "contains",
						Expected: "14",
					},
				},
				{
					Name:   "Simplificación de fracción",
					Prompt: "Simplifica la fracción 18/24 a su mínima expresión. Responde solo con la fracción reducida.",
					Evaluation: &Evaluation{
						Type:     "contains",
						Expected: "3/4",
					},
				},
				{
					Name:   "Potenciación",
					Prompt: "¿Cuánto es 2 elevado a la potencia 8 (2^8)? Solo el número.",
					Evaluation: &Evaluation{
						Type:     "contains",
						Expected: "256",
					},
				},
				{
					Name:   "Porcentajes",
					Prompt: "¿Cuál es el 15% de 200? Devuelve únicamente el número.",
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
			Description:  "Multi-step interactive chain testing sequential retention across turns.",
			GroupID:      "examples",
			Active:       true,
			Order:        2,
			SystemPrompt: "Eres un asistente de programación conciso y servicial.",
			Steps: []Step{
				{
					Step:   1,
					Name:   "Pregunta de contexto inicial",
					Prompt: "Estoy aprendiendo Python para análisis de datos y machine learning. ¿Cuál es su biblioteca principal para tablas de datos?",
					Evaluation: &Evaluation{
						Type:     "contains",
						Expected: "pandas",
					},
				},
				{
					Step:   2,
					Name:   "Seguimiento contextual",
					Prompt: "¿Y qué lenguaje te mencioné que estoy aprendiendo en mi mensaje anterior? Responde solo con el nombre.",
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
	}
	return Test{}, false
}
