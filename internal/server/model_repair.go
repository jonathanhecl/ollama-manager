package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/gense/ollama-manager/internal/ollama"
)

const repairFixedTag = "fixed"

var repairCaps = []string{"completion", "tools", "thinking", "vision", "audio", "embedding"}

type modelRepairRequest struct {
	Model             string   `json:"model"`
	Capabilities      []string `json:"capabilities"`
	TemplatePreset    string   `json:"template_preset"`
	ContextPreset     string   `json:"context_preset"`
	TemperaturePreset string   `json:"temperature_preset"`
	Confirm           bool     `json:"confirm"`
}

type modelRepairPreview struct {
	BaseName             string   `json:"base_name"`
	TargetName           string   `json:"target_name"`
	Modelfile            string   `json:"modelfile"`
	Warnings             []string `json:"warnings,omitempty"`
	DetectedCapabilities []string `json:"detected_capabilities,omitempty"`
	RequiresConfirmation bool     `json:"requires_confirmation"`
}

func buildModelRepairPreview(base string, show *ollama.ShowResponse, req modelRepairRequest) (*modelRepairPreview, error) {
	base = strings.TrimSpace(base)
	if base == "" {
		return nil, errors.New("missing base model")
	}
	if isFixedModelName(base) {
		return nil, errors.New("fixed models cannot be repaired; open the base model and apply a new fix")
	}
	if show == nil {
		show = &ollama.ShowResponse{}
	}

	caps, err := normalizeRepairCapabilities(req.Capabilities)
	if err != nil {
		return nil, err
	}
	templatePreset := normalizeRepairPreset(req.TemplatePreset, "generic")
	contextPreset := normalizeRepairPreset(req.ContextPreset, "safe")
	tempPreset := normalizeRepairPreset(req.TemperaturePreset, "keep")

	var b strings.Builder
	fmt.Fprintf(&b, "FROM %s\n\n", base)

	warnings := []string{
		"Only enable capabilities that the GGUF/model architecture actually supports. Wrong flags or templates can still fail after the model is created.",
	}
	arch := strings.ToLower(extractArchitecture(show))
	if strings.Contains(arch, "qwen35") || strings.Contains(arch, "qwen3.5") {
		warnings = append(warnings, "If Ollama reports an unknown qwen35/qwen35moe architecture, a Modelfile cannot patch missing runtime support.")
	}

	if hasRepairCap(caps, "vision") {
		warnings = append(warnings, "Vision fixes do not add ADAPTER/mmproj automatically. Use a GGUF with embedded vision tensors or an official multimodal Ollama model.")
	}
	if hasRepairCap(caps, "audio") {
		warnings = append(warnings, "Audio support depends on model/runtime support; this fix only changes the Modelfile metadata and chat template.")
	}

	template := repairTemplate(templatePreset, hasRepairCap(caps, "tools"), hasRepairCap(caps, "thinking"))
	if template == "" {
		return nil, fmt.Errorf("unknown template preset %q", req.TemplatePreset)
	}
	if template != "" {
		b.WriteString("TEMPLATE \"\"\"")
		b.WriteString(template)
		b.WriteString("\"\"\"\n\n")
	}

	if templatePreset == "qwen35" {
		b.WriteString("RENDERER qwen3.5\n")
		b.WriteString("PARSER qwen3.5\n\n")
	}

	switch contextPreset {
	case "safe":
		b.WriteString("PARAMETER num_ctx 2048\n")
	case "thinking":
		b.WriteString("PARAMETER num_ctx 16384\n")
	case "keep", "":
	default:
		return nil, fmt.Errorf("unknown context preset %q", req.ContextPreset)
	}

	switch tempPreset {
	case "tools":
		b.WriteString("PARAMETER temperature 0.0\n")
	case "low":
		b.WriteString("PARAMETER temperature 0.1\n")
	case "keep", "":
	default:
		return nil, fmt.Errorf("unknown temperature preset %q", req.TemperaturePreset)
	}

	for _, stop := range repairStops(templatePreset) {
		fmt.Fprintf(&b, "PARAMETER stop %q\n", stop)
	}

	modelfile := strings.TrimSpace(b.String()) + "\n"
	if len(modelfile) > 64*1024 {
		return nil, errors.New("generated Modelfile is too large")
	}

	return &modelRepairPreview{
		BaseName:             base,
		TargetName:           fixedModelName(base),
		Modelfile:            modelfile,
		Warnings:             warnings,
		DetectedCapabilities: append([]string(nil), show.Capabilities...),
		RequiresConfirmation: true,
	}, nil
}

func normalizeRepairCapabilities(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, c := range in {
		c = strings.ToLower(strings.TrimSpace(c))
		if c == "" {
			continue
		}
		if !slices.Contains(repairCaps, c) {
			return nil, fmt.Errorf("unknown capability %q", c)
		}
		if !seen[c] {
			out = append(out, c)
			seen[c] = true
		}
	}
	return out, nil
}

func normalizeRepairPreset(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return fallback
	}
	return value
}

func hasRepairCap(caps []string, cap string) bool {
	return slices.Contains(caps, cap)
}

func repairTemplate(preset string, tools, thinking bool) string {
	switch preset {
	case "qwen35", "qwen":
		var b strings.Builder
		b.WriteString(`{{- if .System }}<|im_start|>system
{{ .System }}<|im_end|>
{{ end -}}`)
		if tools {
			b.WriteString(`
{{- if .Tools }}<|im_start|>system
You may call tools. Available tools:
{{ range .Tools }}{{ . }}
{{ end }}<|im_end|>
{{ end -}}`)
		}
		b.WriteString(`
{{- range .Messages }}<|im_start|>{{ .Role }}
{{ .Content }}`)
		if tools {
			b.WriteString(`
{{- if .ToolCalls }}
{{ range .ToolCalls }}{{ . }}
{{ end }}{{ end -}}`)
		}
		if thinking {
			b.WriteString(`
{{- if .Thinking }}
<think>{{ .Thinking }}</think>
{{ end -}}`)
		}
		b.WriteString(`<|im_end|>
{{ end -}}<|im_start|>assistant
`)
		return b.String()
	case "llama3":
		return `{{- if .System }}<|start_header_id|>system<|end_header_id|>

{{ .System }}<|eot_id|>{{ end }}
{{- range .Messages }}<|start_header_id|>{{ .Role }}<|end_header_id|>

{{ .Content }}<|eot_id|>{{ end }}<|start_header_id|>assistant<|end_header_id|>

`
	case "generic", "chatml", "":
		return `{{- if .System }}<|im_start|>system
{{ .System }}<|im_end|>
{{ end -}}
{{- range .Messages }}<|im_start|>{{ .Role }}
{{ .Content }}<|im_end|>
{{ end -}}<|im_start|>assistant
`
	default:
		return ""
	}
}

func repairStops(preset string) []string {
	switch preset {
	case "llama3":
		return []string{"<|eot_id|>", "<|end_of_text|>"}
	case "qwen35", "qwen", "generic", "chatml", "":
		return []string{"<|im_end|>"}
	default:
		return nil
	}
}

func fixedModelName(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return ""
	}
	slash := strings.LastIndex(base, "/")
	colon := strings.LastIndex(base, ":")
	if colon > slash {
		return base[:colon] + ":" + repairFixedTag
	}
	return base + ":" + repairFixedTag
}

func fixedBaseName(name string) string {
	name = strings.TrimSpace(name)
	if !isFixedModelName(name) {
		return name
	}
	return strings.TrimSuffix(name, ":"+repairFixedTag)
}

func isFixedModelName(name string) bool {
	return strings.HasSuffix(strings.TrimSpace(name), ":"+repairFixedTag)
}

func extractArchitecture(show *ollama.ShowResponse) string {
	if show == nil || show.ModelInfo == nil {
		return ""
	}
	if raw, ok := show.ModelInfo["general.architecture"]; ok {
		var arch string
		if json.Unmarshal(raw, &arch) == nil {
			return arch
		}
	}
	return ""
}
