package server

import (
	"testing"
)

func TestExtractQuantization(t *testing.T) {
	tests := []struct {
		filename string
		expected string
	}{
		{"qwen2.5-coder-7b-instruct-q4_k_m.gguf", "Q4_K_M"},
		{"model-q8_0.gguf", "Q8_0"},
		{"llama-3.2-3b-instruct-iq3_xxs.gguf", "IQ3_XXS"},
		{"deepseek-r1-distill-qwen-14b-ud-iq3_xxs.gguf", "UD-IQ3_XXS"},
		{"meta-llama-3-8b-f16.gguf", "F16"},
		{"phi-3.5-mini-instruct-q5_k_s.gguf", "Q5_K_S"},
		{"mmproj-model-f16.gguf", "MMPROJ"},
		{"SuperQwen3.8-27b-abliterated.imatrix.gguf", "AUXILIARY"},
		{"mtp-SuperQwen3.8-27b-abliterated-Q4_0.gguf", "AUXILIARY"},
		{"custom_model.gguf", "OTHER"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got := ExtractQuantization(tt.filename)
			if got != tt.expected {
				t.Errorf("ExtractQuantization(%q) = %q, want %q", tt.filename, got, tt.expected)
			}
		})
	}
}

func TestIsAuxiliaryGGUF(t *testing.T) {
	tests := []struct {
		filename string
		expected bool
	}{
		{"SuperQwen3.8-27b-abliterated.imatrix.dat", true},
		{"SuperQwen3.8-27b-abliterated.imatrix.gguf", true},
		{"mtp-SuperQwen3.8-27b-abliterated-Q4_0.gguf", true},
		{"SuperQwen3.8-27b-abliterated-mtp-q4_0.gguf", true},
		{"draft-model-q4_k_m.gguf", true},
		{"model.imatrix", true},
		{"imatrix.dat", true},
		{"qwen2.5-coder-7b-instruct-q4_k_m.gguf", false},
		{"model.gguf", false},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got := IsAuxiliaryGGUF(tt.filename)
			if got != tt.expected {
				t.Errorf("IsAuxiliaryGGUF(%q) = %v, want %v", tt.filename, got, tt.expected)
			}
		})
	}
}

func TestIsVisionProjector(t *testing.T) {
	tests := []struct {
		filename string
		expected bool
	}{
		{"mmproj-model-f16.gguf", true},
		{"model-mmproj.gguf", true},
		{"qwen2-vl-7b-instruct-mmproj-f16.gguf", true},
		{"qwen2.5-coder-7b-instruct-q4_k_m.gguf", false},
		{"model.gguf", false},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got := IsVisionProjector(tt.filename)
			if got != tt.expected {
				t.Errorf("IsVisionProjector(%q) = %v, want %v", tt.filename, got, tt.expected)
			}
		})
	}
}
