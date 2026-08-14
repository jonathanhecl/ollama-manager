package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
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
	Stops             []string `json:"stops"`
	Projector         string   `json:"projector"`
	FixLoad           bool     `json:"fix_load"`
	Modelfile         string   `json:"modelfile"`
	Confirm           bool     `json:"confirm"`
}

type modelRepairPreview struct {
	BaseName             string         `json:"base_name"`
	TargetName           string         `json:"target_name"`
	Modelfile            string         `json:"modelfile"`
	Warnings             []string       `json:"warnings,omitempty"`
	DetectedCapabilities []string       `json:"detected_capabilities,omitempty"`
	BaseStops            []string       `json:"base_stops,omitempty"`
	Projector            string         `json:"projector,omitempty"`
	RequiresConfirmation bool           `json:"requires_confirmation"`
	System               string         `json:"-"`
	Template             string         `json:"-"`
	Parameters           map[string]any `json:"-"`
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
	templateFallback := "generic"
	if strings.TrimSpace(show.Template) != "" {
		templateFallback = "keep"
	}
	templatePreset := normalizeRepairPreset(req.TemplatePreset, templateFallback)
	contextPreset := normalizeRepairPreset(req.ContextPreset, "keep")
	tempPreset := normalizeRepairPreset(req.TemperaturePreset, "keep")

	var b strings.Builder

	originalBlobs := extractBlobs(show.Modelfile)
	projector := strings.TrimSpace(req.Projector)
	if projector != "" && len(originalBlobs) == 0 {
		return nil, errors.New("cannot attach a vision projector: the base model Modelfile has no GGUF blob to build from")
	}
	useBlobFrom := (req.FixLoad && len(originalBlobs) > 0) || projector != ""

	if useBlobFrom {
		fmt.Fprintf(&b, "FROM %s\n", originalBlobs[0])
		for i := 1; i < len(originalBlobs); i++ {
			fmt.Fprintf(&b, "# FROM %s (stripped to fix load error)\n", originalBlobs[i])
		}
		if projector != "" {
			fmt.Fprintf(&b, "# PROJECTOR %s\n", projector)
		}
		b.WriteString("\n")
	} else {
		fmt.Fprintf(&b, "FROM %s\n\n", base)
	}

	warnings := []string{
		"Only enable capabilities that the GGUF/model architecture actually supports. Wrong flags or templates can still fail after the model is created.",
	}
	arch := strings.ToLower(extractArchitecture(show))
	if strings.Contains(arch, "qwen35") || strings.Contains(arch, "qwen3.5") {
		warnings = append(warnings, "If Ollama reports an unknown qwen35/qwen35moe architecture, a Modelfile cannot patch missing runtime support.")
	}
	if strings.Contains(arch, "gemma") {
		warnings = append(warnings, "Gemma models from Hugging Face often fail with Error 500 due to missing metadata. Use the 'Gemma' template preset and ensure 'Safe load' context is selected.")
	}
	if isLFM2Arch(arch) {
		warnings = append(warnings, "LFM2 models use special token characters in their template/stop parameters. The repair will preserve the original Modelfile exactly and only inject the PARSER directive.")
	}
	if hasRepairCap(caps, "vision") {
		warnings = append(warnings, "Vision fixes do not add ADAPTER/mmproj automatically. Use a GGUF with embedded vision tensors or an official multimodal Ollama model.")
	}
	if projector != "" {
		warnings = append(warnings, "A vision projector (mmproj) will be downloaded from Hugging Face and attached to this model. Make sure the file matches this model's architecture.")
	}
	if hasRepairCap(caps, "audio") {
		warnings = append(warnings, "Audio support depends on model/runtime support; this fix only changes the Modelfile metadata and chat template.")
	}

	system := repairSystem(caps)
	if system != "" {
		b.WriteString("SYSTEM \"\"\"")
		b.WriteString(system)
		b.WriteString("\"\"\"\n\n")
	}

	template := ""
	if templatePreset != "keep" {
		template = repairTemplate(templatePreset, hasRepairCap(caps, "tools"), hasRepairCap(caps, "thinking"))
		if template == "" {
			return nil, fmt.Errorf("unknown template preset %q", req.TemplatePreset)
		}
	}
	if template != "" {
		b.WriteString("TEMPLATE \"\"\"")
		b.WriteString(template)
		b.WriteString("\"\"\"\n\n")
	} else if strings.TrimSpace(show.Template) != "" {
		warnings = append(warnings, "Keeping the original template from the base model. The fixed model will inherit it and only add SYSTEM/PARAMETER changes.")
		if hasRepairCap(caps, "tools") {
			warnings = append(warnings, "Tool metadata may still require a template that renders .Tools and .ToolCalls. If the original template does not do that, choose an explicit template preset or edit the preview manually.")
		}
	}

	// Add RENDERER/PARSER directives for architectures that need them
	renderer := repairRenderer(templatePreset, arch)
	if renderer != "" && !isLFM2Arch(arch) {
		fmt.Fprintf(&b, "RENDERER %s\n", renderer)
		fmt.Fprintf(&b, "PARSER %s\n\n", renderer)
	}
	if req.FixLoad && renderer == "" {
		// If fix_load is on but we couldn't detect renderer, try from arch
		if r := rendererFromArch(arch); r != "" {
			fmt.Fprintf(&b, "RENDERER %s\n", r)
			fmt.Fprintf(&b, "PARSER %s\n\n", r)
		}
	}

	// LFM2 special path: when tools are requested, use a modern template so Ollama can
	// inject tool definitions natively. Otherwise preserve the exact original Modelfile.
	if isLFM2Arch(arch) {
		return buildLFM2RepairPreview(base, show, req, caps, arch, contextPreset, tempPreset, warnings, templatePreset)
	}

	parameters := make(map[string]any)
	switch contextPreset {
	case "safe":
		b.WriteString("PARAMETER num_ctx 2048\n")
		parameters["num_ctx"] = 2048
	case "thinking":
		b.WriteString("PARAMETER num_ctx 16384\n")
		parameters["num_ctx"] = 16384
	case "keep", "":
	default:
		return nil, fmt.Errorf("unknown context preset %q", req.ContextPreset)
	}

	switch tempPreset {
	case "tools":
		b.WriteString("PARAMETER temperature 0.0\n")
		parameters["temperature"] = 0.0
	case "low":
		b.WriteString("PARAMETER temperature 0.1\n")
		parameters["temperature"] = 0.1
	case "keep", "":
	default:
		return nil, fmt.Errorf("unknown temperature preset %q", req.TemperaturePreset)
	}

	stops, stopWarnings := resolveRepairStops(req.Stops, templatePreset, show.Modelfile)
	warnings = append(warnings, stopWarnings...)
	if stops != nil {
		parameters["stop"] = stops
	}
	for _, stop := range stops {
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
		BaseStops:            append([]string(nil), extractModelfileStops(show.Modelfile)...),
		Projector:            projector,
		RequiresConfirmation: true,
		System:               system,
		Template:             template,
		Parameters:           parameters,
	}, nil
}

func isLFM2Arch(arch string) bool {
	arch = strings.ToLower(arch)
	return strings.Contains(arch, "lfm2") || strings.Contains(arch, "lfm2moe")
}

// buildLFM2RepairPreview handles LFM2 models. When tools are requested it generates
// a modern template with .Tools/.Messages support so Ollama can inject tool
// definitions natively. Otherwise it preserves the exact original Modelfile.
func buildLFM2RepairPreview(base string, show *ollama.ShowResponse, req modelRepairRequest, caps []string, arch, contextPreset, tempPreset string, warnings []string, templatePreset string) (*modelRepairPreview, error) {
	parser := "lfm2"
	if hasRepairCap(caps, "thinking") {
		parser = "lfm2-thinking"
	}

	projector := strings.TrimSpace(req.Projector)
	hasVision := hasRepairCap(caps, "vision") || projector != ""
	useModernTemplate := hasRepairCap(caps, "tools") || hasVision || req.FixLoad || strings.Contains(show.Modelfile, "bos_token") || strings.Contains(show.Template, "bos_token")

	originalBlobs := extractBlobs(show.Modelfile)
	if projector != "" && len(originalBlobs) == 0 {
		return nil, errors.New("cannot attach a vision projector: the base model Modelfile has no GGUF blob to build from")
	}
	if projector != "" {
		warnings = append(warnings, "A vision projector (mmproj) will be downloaded from Hugging Face and attached to this model. Make sure the file matches this model's architecture.")
	}

	var b strings.Builder
	var lines []string
	var addedParser bool

	if useModernTemplate {
		// Use the clean approach for LFM2 models:
		// For text models: RENDERER/PARSER with TEMPLATE {{ .Prompt }}
		// For vision models: PARSER with full ChatML TEMPLATE so Ollama passes image attachments per turn
		if (projector != "" || req.FixLoad) && len(originalBlobs) > 0 {
			fmt.Fprintf(&b, "FROM %s\n", originalBlobs[0])
			for i := 1; i < len(originalBlobs); i++ {
				fmt.Fprintf(&b, "# FROM %s (stripped to fix load error)\n", originalBlobs[i])
			}
			if projector != "" {
				fmt.Fprintf(&b, "# PROJECTOR %s\n", projector)
			}
			b.WriteString("\n")
		} else {
			fmt.Fprintf(&b, "FROM %s\n\n", base)
		}

		if !hasVision {
			b.WriteString("RENDERER " + parser + "\n")
		}
		b.WriteString("PARSER " + parser + "\n\n")

		if hasVision || templatePreset != "keep" {
			tmpl := repairTemplate(templatePreset, hasRepairCap(caps, "tools"), hasRepairCap(caps, "thinking"))
			if tmpl == "" {
				tmpl = repairTemplate("lfm2", hasRepairCap(caps, "tools"), hasRepairCap(caps, "thinking"))
			}
			b.WriteString("TEMPLATE \"\"\"" + tmpl + "\"\"\"\n\n")
		} else {
			b.WriteString("TEMPLATE {{ .Prompt }}\n\n")
		}
	} else {
		// Preserve exact original Modelfile (critical for invisible token characters)
		original := strings.TrimSpace(show.Modelfile)
		if original == "" {
			return nil, errors.New("cannot repair LFM2 model: original Modelfile is empty")
		}
		for _, line := range strings.Split(original, "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			upper := strings.ToUpper(trimmed)
			if strings.HasPrefix(upper, "PARSER ") || strings.HasPrefix(upper, "RENDERER ") {
				continue
			}
			// When the user supplies a custom stop list, drop the original stop
			// lines so the fixed model re-declares them from scratch.
			if req.Stops != nil && strings.HasPrefix(upper, "PARAMETER STOP ") {
				continue
			}
			lines = append(lines, sanitizeOllamaTemplate(line))
			if !addedParser && strings.HasPrefix(upper, "FROM ") {
				lines = append(lines, "PARSER "+parser)
				addedParser = true
			}
		}
		if !addedParser {
			return nil, errors.New("cannot repair LFM2 model: original Modelfile has no FROM directive")
		}
		for _, line := range lines {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}

	// Inject SYSTEM if capabilities require it
	// For LFM2, use a specialized system prompt that teaches the native tool format
	system := repairLFM2System(caps)
	if system != "" {
		b.WriteString("SYSTEM \"\"\"")
		b.WriteString(system)
		b.WriteString("\"\"\"\n")
	}

	parameters := make(map[string]any)
	switch contextPreset {
	case "safe":
		b.WriteString("PARAMETER num_ctx 2048\n")
		parameters["num_ctx"] = 2048
	case "thinking":
		b.WriteString("PARAMETER num_ctx 16384\n")
		parameters["num_ctx"] = 16384
	}

	switch tempPreset {
	case "tools":
		b.WriteString("PARAMETER temperature 0.0\n")
		parameters["temperature"] = 0.0
	case "low":
		b.WriteString("PARAMETER temperature 0.1\n")
		parameters["temperature"] = 0.1
	}

	// Stop tokens: custom list if specified, otherwise declare standard ChatML stops
	if req.Stops != nil {
		var stops []string
		for _, s := range req.Stops {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			stops = append(stops, s)
			fmt.Fprintf(&b, "PARAMETER stop %q\n", s)
		}
		parameters["stop"] = stops
	} else if useModernTemplate {
		stops := []string{"<|im_end|>", "<|endoftext|>"}
		for _, s := range stops {
			fmt.Fprintf(&b, "PARAMETER stop %q\n", s)
		}
		parameters["stop"] = stops
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
		BaseStops:            append([]string(nil), extractModelfileStops(show.Modelfile)...),
		Projector:            projector,
		RequiresConfirmation: true,
		System:               system,
		Template:             show.Template,
		Parameters:           parameters,
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

func parseRepairModelfile(modelfile, expectedBase string, fallback *modelRepairPreview) (string, string, string, map[string]any, error) {
	modelfile = strings.TrimSpace(modelfile)
	if modelfile == "" {
		return "", "", "", nil, errors.New("missing edited Modelfile; generate a preview before creating the fixed model")
	}
	if len(modelfile) > 64*1024 {
		return "", "", "", nil, errors.New("edited Modelfile is too large")
	}

	from := ""
	for _, line := range strings.Split(modelfile, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, rest, ok := strings.Cut(line, " ")
		if ok && strings.EqualFold(key, "FROM") {
			from = strings.TrimSpace(rest)
			break
		}
	}
	if from == "" {
		return "", "", "", nil, errors.New("edited Modelfile must include FROM")
	}
	// Allow blob-based FROM when the user is fixing a load error
	isBlobFrom := strings.Contains(from, "sha256-")
	if from != expectedBase && !isBlobFrom {
		return "", "", "", nil, fmt.Errorf("edited Modelfile must keep FROM %s", expectedBase)
	}

	system, err := extractTripleQuotedDirective(modelfile, "SYSTEM")
	if err != nil {
		return "", "", "", nil, err
	}
	if system == "" && fallback != nil {
		system = fallback.System
	}

	template, err := extractTripleQuotedDirective(modelfile, "TEMPLATE")
	if err != nil {
		return "", "", "", nil, err
	}
	if template == "" {
		template = extractLineDirective(modelfile, "TEMPLATE")
	}
	if template == "" && fallback != nil {
		template = fallback.Template
	}
	template = sanitizeOllamaTemplate(template)
	system = sanitizeOllamaTemplate(system)

	parameters := make(map[string]any)
	for _, line := range strings.Split(modelfile, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, rest, ok := strings.Cut(line, " ")
		if !ok || !strings.EqualFold(key, "PARAMETER") {
			continue
		}
		name, value, ok := strings.Cut(strings.TrimSpace(rest), " ")
		if !ok {
			return "", "", "", nil, fmt.Errorf("invalid PARAMETER line %q", line)
		}
		name = strings.TrimSpace(name)
		parsed := parseRepairParameterValue(strings.TrimSpace(value))
		if name == "stop" {
			if existing, ok := parameters[name].([]string); ok {
				parameters[name] = append(existing, fmt.Sprint(parsed))
			} else {
				parameters[name] = []string{fmt.Sprint(parsed)}
			}
			continue
		}
		parameters[name] = parsed
	}
	if len(parameters) == 0 && fallback != nil {
		for k, v := range fallback.Parameters {
			parameters[k] = v
		}
	}
	return from, system, template, parameters, nil
}

func sanitizeOllamaTemplate(tmpl string) string {
	if tmpl == "" {
		return ""
	}
	replaces := []string{
		"{{- bos_token -}}", "",
		"{{- bos_token }}", "",
		"{{ bos_token -}}", "",
		"{{ bos_token }}", "",
		"{{- eos_token -}}", "",
		"{{- eos_token }}", "",
		"{{ eos_token -}}", "",
		"{{ eos_token }}", "",
	}
	res := tmpl
	for i := 0; i < len(replaces); i += 2 {
		res = strings.ReplaceAll(res, replaces[i], replaces[i+1])
	}
	// If template contains raw Python Jinja control flow {% ... %} that Go text/template cannot parse,
	// replace with a clean fallback {{ .Prompt }}
	if strings.Contains(res, "{%") || strings.Contains(res, "loop.") || strings.Contains(res, "raise_exception") {
		return "{{ .Prompt }}"
	}
	return res
}

func extractTripleQuotedDirective(modelfile, directive string) (string, error) {
	marker := directive + " \"\"\""
	upper := strings.ToUpper(modelfile)
	if i := strings.Index(upper, strings.ToUpper(marker)); i >= 0 {
		rest := modelfile[i+len(marker):]
		if j := strings.Index(rest, "\"\"\""); j >= 0 {
			return rest[:j], nil
		}
		return "", fmt.Errorf("edited Modelfile has an unterminated %s block", directive)
	}
	return "", nil
}

func extractLineDirective(modelfile, directive string) string {
	for _, line := range strings.Split(modelfile, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, rest, ok := strings.Cut(line, " ")
		if ok && strings.EqualFold(key, directive) {
			return strings.Trim(strings.TrimSpace(rest), `"`)
		}
	}
	return ""
}

func blobDigest(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	if strings.HasPrefix(ref, "sha256:") {
		return ref
	}
	if i := strings.LastIndex(ref, "sha256-"); i >= 0 {
		hex := strings.TrimSpace(ref[i+len("sha256-"):])
		if hex != "" {
			return "sha256:" + hex
		}
	}
	return ""
}

func parseRepairParameterValue(raw string) any {
	if unquoted, err := strconv.Unquote(raw); err == nil {
		return unquoted
	}
	if i, err := strconv.Atoi(raw); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil {
		return f
	}
	if b, err := strconv.ParseBool(raw); err == nil {
		return b
	}
	return raw
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

// repairLFM2System generates a specialized system prompt for LFM2 models.
// It teaches the model how to use tools in the native LFM2 format:
//
//	<|tool_call_start|>[function_name(arg1="value1", arg2="value2")]
//
// Tool definitions are expected to be provided dynamically in the system prompt
// as a JSON array under "List of tools:".
func repairLFM2System(caps []string) string {
	if len(caps) == 0 {
		return ""
	}
	var parts []string
	if hasRepairCap(caps, "tools") {
		parts = append(parts, "You are a helpful assistant. You have access to tools that can help answer user questions.")
		parts = append(parts, "")
		parts = append(parts, "When you need to use a tool, output your call in this exact format:")
		parts = append(parts, "<|tool_call_start|>[function_name(arg1=\"value1\", arg2=\"value2\")]")
		parts = append(parts, "")
		parts = append(parts, "Tool definitions will be provided in the system prompt as a JSON array under \"List of tools:\". Use the available tools whenever they can help answer the user's question.")
	}
	if hasRepairCap(caps, "thinking") {
		if len(parts) > 0 {
			parts = append(parts, "")
		}
		parts = append(parts, "This model supports structured reasoning. When thinking is enabled, reasoning traces will be separated from the final answer.")
	}
	if hasRepairCap(caps, "completion") && len(parts) == 0 {
		parts = append(parts, "You are a helpful assistant trained by Liquid AI.")
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n")
}

func repairSystem(caps []string) string {
	if len(caps) == 0 {
		return ""
	}
	var parts []string
	if hasRepairCap(caps, "tools") {
		parts = append(parts, "This model is expected to support tool use when the runtime provides tools. Use valid tool-call JSON only when a tool is required.")
	}
	if hasRepairCap(caps, "thinking") {
		parts = append(parts, "This model is expected to support structured reasoning traces when the runtime enables thinking.")
	}
	if hasRepairCap(caps, "vision") {
		parts = append(parts, "This model is expected to process image inputs when the runtime and model file support vision.")
	}
	if hasRepairCap(caps, "audio") {
		parts = append(parts, "This model is expected to process audio inputs when the runtime and model file support audio.")
	}
	if hasRepairCap(caps, "embedding") {
		parts = append(parts, "This model is expected to produce embeddings when called through embedding endpoints.")
	}
	if hasRepairCap(caps, "completion") {
		parts = append(parts, "This model is expected to support text completion/chat responses.")
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n")
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
	case "gemma":
		return `{{- if .System }}<start_of_turn>system
{{ .System }}<end_of_turn>
{{ end }}{{- range .Messages }}<start_of_turn>{{ .Role }}
{{ .Content }}<end_of_turn>
{{ end }}<start_of_turn>assistant
`
	case "gemma4":
		return "{{ .Prompt }}"
	case "gemma2_unsloth":
		return `{{- if .System }}<bos><|turn>system
{{ .System }}<turn|>
{{ end }}{{- range .Messages }}<|turn|>{{ .Role }}
{{ .Content }}<turn|>
{{ end }}<|turn>model
`
	case "lfm2":
		var b strings.Builder
		b.WriteString(`{{- if .System }}<|im_start|>system
{{ .System }}<|im_end|>
{{ end -}}`)
		if tools {
			b.WriteString(`
{{- if .Tools }}<|im_start|>system
List of tools: [{{ range $i, $t := .Tools }}{{ if $i }}, {{ end }}{{ $t }}{{ end }}]<|im_end|>
{{ end -}}`)
		}
		b.WriteString(`
{{- range .Messages }}<|im_start|>{{ .Role }}
{{ .Content }}<|im_end|>
{{ end -}}<|im_start|>assistant
`)
		return b.String()
	case "hf_generic":
		return `{{- if .System }}<|im_start|>system
{{ .System }}<|im_end|>
{{ end -}}
{{- range .Messages }}<|im_start|>{{ .Role }}
{{ .Content }}<|im_end|>
{{ end -}}<|im_start|>assistant
`
	case "muse_glimmer", "muse-glimmer", "glimmer", "muse":
		return `{{- if .System }}<|start|>system<|message|>
{{ .System }}<|eot|>
{{ end -}}
{{- range .Messages }}<|start|>{{ .Role }}<|message|>
{{ .Content }}<|eot|>
{{ end -}}<|start|>assistant<|message|>
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
	case "gemma":
		return []string{"<end_of_turn>", "<eos>"}
	case "gemma4":
		return nil // renderer handles stops
	case "gemma2_unsloth":
		return []string{"<bos>", "<|turn|>", "<turn|>", "<|turn|>user"}
	case "lfm2":
		return []string{"<|im_end|>", "<|endoftext|>"}
	case "muse_glimmer", "muse-glimmer", "glimmer", "muse":
		return []string{"<|eot|>", "<|start|>user<|message|>"}
	case "hf_generic":
		return []string{"<|im_end|>", "<|endoftext|>", "<|file_separator|>"}
	case "qwen35", "qwen", "generic", "chatml", "":
		return []string{"<|im_end|>"}
	default:
		return nil
	}
}

// resolveRepairStops returns the stop sequences to declare in the fixed model.
// When the user supplied a custom list (non-nil) it is used verbatim — this is
// fully generic and independent of the model architecture or template preset.
// Otherwise the existing heuristic applies: preset stops first, then the base
// model's stops with Markdown-punctuation sequences filtered out.
func resolveRepairStops(custom []string, templatePreset, modelfile string) (stops []string, warnings []string) {
	if custom != nil {
		for _, s := range custom {
			s = strings.TrimSpace(s)
			if s != "" {
				stops = append(stops, s)
			}
		}
		if markdown, _ := splitMarkdownStops(stops); len(markdown) > 0 {
			warnings = append(warnings, fmt.Sprintf("The custom stop list contains plain Markdown punctuation (%s) that can cut off responses containing horizontal rules or headings.", strings.Join(markdown, ", ")))
		}
		return stops, warnings
	}

	stops = repairStops(templatePreset)
	markdownStops, keptBaseStops := splitMarkdownStops(extractModelfileStops(modelfile))
	if len(stops) > 0 {
		if len(markdownStops) > 0 {
			warnings = append(warnings, fmt.Sprintf("The base model declares stop sequences made of plain Markdown punctuation (%s) that cut off responses containing horizontal rules or headings. The stops declared by the selected template preset replace them.", strings.Join(markdownStops, ", ")))
		}
		return stops, warnings
	}
	if len(markdownStops) > 0 {
		if len(keptBaseStops) > 0 {
			warnings = append(warnings, fmt.Sprintf("The base model declares stop sequences made of plain Markdown punctuation (%s) that cut off responses containing horizontal rules or headings. The fixed model re-declares the stop list without them.", strings.Join(markdownStops, ", ")))
			return keptBaseStops, warnings
		}
		warnings = append(warnings, fmt.Sprintf("The base model declares only stop sequences made of plain Markdown punctuation (%s) that cut off responses containing horizontal rules or headings. They cannot be removed automatically; edit the preview manually to declare replacement stops.", strings.Join(markdownStops, ", ")))
		return nil, warnings
	}
	return nil, warnings
}

// repairRenderer returns the RENDERER/PARSER value for a given template preset.
func repairRenderer(preset, arch string) string {
	switch preset {
	case "qwen35", "qwen":
		return "qwen3.5"
	case "gemma4":
		return "gemma4"
	case "gemma", "gemma2_unsloth":
		if strings.Contains(arch, "gemma4") {
			return "gemma4"
		}
		if strings.Contains(arch, "gemma3") {
			return "gemma3"
		}
		return ""
	case "lfm2":
		if hasRepairCap([]string{"thinking"}, "thinking") {
			return "lfm2-thinking"
		}
		return "lfm2"
	default:
		return ""
	}
}

// rendererFromArch auto-detects the renderer from the model's architecture.
// Used as a fallback when fix_load is on but the template preset didn't set a renderer.
func rendererFromArch(arch string) string {
	if strings.Contains(arch, "gemma4") {
		return "gemma4"
	}
	if strings.Contains(arch, "gemma3") {
		return "gemma3"
	}
	if strings.Contains(arch, "gemma2") {
		return "gemma2"
	}
	if strings.Contains(arch, "gemma") {
		return "gemma"
	}
	if strings.Contains(arch, "llama") {
		return "llama"
	}
	if strings.Contains(arch, "qwen3.5") || strings.Contains(arch, "qwen35") {
		return "qwen3.5"
	}
	if strings.Contains(arch, "qwen") {
		return "qwen"
	}
	if strings.Contains(arch, "lfm2moe") || strings.Contains(arch, "lfm2") {
		return "lfm2-thinking"
	}
	return ""
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

// extractModelfileStops parses PARAMETER stop lines from a Modelfile.
func extractModelfileStops(modelfile string) []string {
	var stops []string
	for _, line := range strings.Split(modelfile, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, rest, ok := strings.Cut(line, " ")
		if !ok || !strings.EqualFold(key, "PARAMETER") {
			continue
		}
		name, value, ok := strings.Cut(strings.TrimSpace(rest), " ")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), "stop") {
			continue
		}
		parsed := parseRepairParameterValue(strings.TrimSpace(value))
		stops = append(stops, fmt.Sprint(parsed))
	}
	return stops
}

// isMarkdownPunctuationStop reports whether a stop sequence is made solely of
// Markdown punctuation characters (e.g. "###", "---", "***", "___"). Such stops
// truncate responses that contain horizontal rules or headings. Stops with any
// other content ("### Response:", "<user>") are considered legitimate.
func isMarkdownPunctuationStop(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch r {
		case '#', '-', '*', '_':
		default:
			return false
		}
	}
	return true
}

// splitMarkdownStops partitions stops into Markdown-punctuation stops and the rest.
func splitMarkdownStops(stops []string) (markdown []string, kept []string) {
	for _, s := range stops {
		if isMarkdownPunctuationStop(s) {
			markdown = append(markdown, s)
		} else {
			kept = append(kept, s)
		}
	}
	return markdown, kept
}

func extractBlobs(modelfile string) []string {
	var blobs []string
	for _, line := range strings.Split(modelfile, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		upperLine := strings.ToUpper(line)
		if strings.HasPrefix(upperLine, "FROM ") || strings.HasPrefix(upperLine, "ADAPTER ") {
			parts := strings.SplitN(line, " ", 2)
			if len(parts) == 2 {
				val := strings.TrimSpace(parts[1])
				// Support any path format as long as it's a blob
				if strings.Contains(val, "sha256-") {
					blobs = append(blobs, val)
				}
			}
		}
	}
	return blobs
}

// resolveProjectorURL normalizes a user-provided vision projector reference into
// a direct Hugging Face download URL. Accepted forms:
//   - full URL:  https://huggingface.co/<user>/<repo>/resolve/<rev>/<file>.gguf
//   - blob URL:  https://huggingface.co/<user>/<repo>/blob/<rev>/<file>.gguf
//   - shorthand: hf.co/<user>/<repo>/<file>
//   - shorthand: <user>/<repo>/<file>
func resolveProjectorURL(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", errors.New("empty projector reference")
	}
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		u, err := url.Parse(ref)
		if err != nil {
			return "", fmt.Errorf("invalid projector URL: %w", err)
		}
		if !isHuggingFaceHost(u.Host) {
			return "", errors.New("projector URL must point to huggingface.co or hf.co")
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		for i, p := range parts {
			if p == "blob" || p == "resolve" {
				parts[i] = "resolve"
				break
			}
		}
		u.Path = "/" + strings.Join(parts, "/")
		u.RawQuery = ""
		u.Fragment = ""
		return u.String(), nil
	}

	ref = strings.TrimPrefix(ref, "hf.co/")
	parts := strings.SplitN(ref, "/", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", errors.New("projector reference must be a URL or <user>/<repo>/<file>")
	}
	return "https://huggingface.co/" + parts[0] + "/" + parts[1] + "/resolve/main/" + parts[2], nil
}

func isHuggingFaceHost(host string) bool {
	host = strings.ToLower(host)
	return host == "huggingface.co" || host == "www.huggingface.co" || host == "hf.co" || strings.HasSuffix(host, ".huggingface.co")
}
