package server

import (
	"encoding/json"
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
		{"Qwen3.8-27B-Uncensored-HauhauCS-Aggressive-FastMTP-32K.gguf", "AUXILIARY"},
		{"custom_model.gguf", "OTHER"},
		{"gguf/gemma-3-12b-it-heretic-v2-Q4_K_M.gguf", "Q4_K_M"},
		{"quantized/sub/model-IQ3_XS.gguf", "IQ3_XS"},
		// Base-model version tokens ("Qwen3.8" abbreviated "Q3.8") must not be
		// mistaken for a "Q3" quant (DavidAU repos use this naming).
		{"LFM2.5-2.6B-Q3.8-TBrilliance-NEO-IQ4_XS.gguf", "IQ4_XS"},
		{"LFM2.5-2.6B-Q3.8-TBrilliance-NEO-MAX-Q6_K.gguf", "Q6_K"},
		{"LFM2.5-2.6B-Q3.8-TBrilliance-NEO-MAX-Q8_0.gguf", "Q8_0"},
		{"Qwen3.8-27b-abliterated-Q4_K_M.gguf", "Q4_K_M"},
		{"model-Q3.8-IVORY.gguf", "OTHER"},
		// UD- (unsloth dynamic) prefix on a K-quant.
		{"model-UD-Q4_K_XL.gguf", "UD-Q4_K_XL"},
		// MXFP4 (gpt-oss) is a scheme without a variant suffix.
		{"gpt-oss-20b-MXFP4.gguf", "MXFP4"},
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

func TestHFDedupeQuants(t *testing.T) {
	lfm := []HFQuantFile{
		{Filename: "LFM2.5-2.6B-Q3.8-TBrilliance-NEO-IQ4_XS.gguf", Quant: "IQ4_XS", SizeBytes: 1517841056},
		{Filename: "LFM2.5-2.6B-Q3.8-TBrilliance-NEO-MAX-IQ4_XS.gguf", Quant: "IQ4_XS", SizeBytes: 1827089056},
		{Filename: "LFM2.5-2.6B-Q3.8-TBrilliance-NEO-MAX-Q6_K.gguf", Quant: "Q6_K", SizeBytes: 2530896544},
		{Filename: "LFM2.5-2.6B-Q3.8-TBrilliance-NEO-MAX-Q8_0.gguf", Quant: "Q8_0", SizeBytes: 3120573088},
		{Filename: "LFM2.5-2.6B-Q3.8-TBrilliance-NEO-Q6_K.gguf", Quant: "Q6_K", SizeBytes: 2221648544},
		{Filename: "LFM2.5-2.6B-Q3.8-TBrilliance-NEO-Q8_0.gguf", Quant: "Q8_0", SizeBytes: 2874813088},
	}

	got := hfDedupeQuants(lfm)
	if len(got) != 3 {
		t.Fatalf("got %d options, want 3: %+v", len(got), got)
	}
	// One row per scheme; the alphabetically-first variant is kept because that
	// is the file HF's registry serves for the bare `:scheme` tag. The other
	// variant is preserved as read-only detail.
	want := []struct {
		quant    string
		filename string
		size     int64
		extra    []string
	}{
		{"IQ4_XS", "LFM2.5-2.6B-Q3.8-TBrilliance-NEO-IQ4_XS.gguf", 1517841056,
			[]string{"LFM2.5-2.6B-Q3.8-TBrilliance-NEO-MAX-IQ4_XS.gguf"}},
		{"Q6_K", "LFM2.5-2.6B-Q3.8-TBrilliance-NEO-MAX-Q6_K.gguf", 2530896544,
			[]string{"LFM2.5-2.6B-Q3.8-TBrilliance-NEO-Q6_K.gguf"}},
		{"Q8_0", "LFM2.5-2.6B-Q3.8-TBrilliance-NEO-MAX-Q8_0.gguf", 3120573088,
			[]string{"LFM2.5-2.6B-Q3.8-TBrilliance-NEO-Q8_0.gguf"}},
	}
	for i, w := range want {
		if got[i].Quant != w.quant || got[i].Filename != w.filename || got[i].SizeBytes != w.size {
			t.Errorf("option %d = {%s %s %d}, want {%s %s %d}",
				i, got[i].Quant, got[i].Filename, got[i].SizeBytes, w.quant, w.filename, w.size)
		}
		if len(got[i].ExtraVariants) != len(w.extra) {
			t.Fatalf("option %d has %d extra variants, want %d", i, len(got[i].ExtraVariants), len(w.extra))
		}
		for j, name := range w.extra {
			if got[i].ExtraVariants[j].Filename != name {
				t.Errorf("option %d extra %d = %s, want %s", i, j, got[i].ExtraVariants[j].Filename, name)
			}
		}
	}

	// A repo with a single file per scheme keeps every scheme and no extras.
	multi := hfDedupeQuants([]HFQuantFile{
		{Filename: "model-Q4_K_M.gguf", Quant: "Q4_K_M"},
		{Filename: "model-Q8_0.gguf", Quant: "Q8_0"},
		{Filename: "model-f16.gguf", Quant: "F16"},
	})
	if len(multi) != 3 {
		t.Errorf("distinct schemes: got %d options, want 3", len(multi))
	}
	for _, opt := range multi {
		if len(opt.ExtraVariants) != 0 {
			t.Errorf("%s: unexpected extra variants %+v", opt.Quant, opt.ExtraVariants)
		}
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
		// Vendor-prefixed speculative-decoding files.
		{"Qwen3.8-27B-Uncensored-HauhauCS-Aggressive-FastMTP-32K.gguf", true},
		{"Qwen3.8-27B-Uncensored-HauhauCS-Aggressive-FastMTP32K.gguf", true},
		{"model-EagleMTP.gguf", true},
		{"model-specdraft-q4_0.gguf", true},
		{"qwen2.5-coder-7b-instruct-q4_k_m.gguf", false},
		{"model.gguf", false},
		{"gguf/sub/model.gguf", false},
		{"gguf/sub/model.imatrix.gguf", true},
		// "mtp"/"draft" must stay delimited: these are ordinary models.
		{"promptbench-7b-q4_k_m.gguf", false},
		{"attempt-tuned-13b-q8_0.gguf", false},
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
		{"gguf/qwen2-vl-7b-instruct-mmproj-f16.gguf", true},
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

func TestParseHFGated(t *testing.T) {
	tests := []struct {
		raw     string
		isGated bool
		label   string
	}{
		{`false`, false, ""},
		{`null`, false, ""},
		{`"manual"`, true, "manual"},
		{`"auto"`, true, "auto"},
		{`"false"`, false, ""},
		{`true`, true, "true"},
		{``, false, ""},
	}
	for _, tt := range tests {
		var raw json.RawMessage
		if tt.raw != "" {
			raw = json.RawMessage(tt.raw)
		}
		got, label := parseHFGated(raw)
		if got != tt.isGated || label != tt.label {
			t.Errorf("parseHFGated(%s) = (%v,%q), want (%v,%q)", tt.raw, got, label, tt.isGated, tt.label)
		}
	}
}
