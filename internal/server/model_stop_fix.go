package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/gense/ollama-manager/internal/ollama"
)

// Imported GGUF models often ship Modelfiles whose stop sequences do not match
// the tokens the model really emits. Ollama then cuts the reply before the
// first character and the chat shows a blank message whose done_reason is
// "stop". This file detects the real terminator empirically by probing the
// model, and rebuilds it in place with a stop list that never cuts the answer.
//
// Ollama merges request options over the Modelfile parameters, so a probe with
// `stop: []` reveals what the model emits without the broken stops, and a probe
// with a single candidate stop tells whether that candidate is the culprit.

const (
	stopFixNumPredict     = 64
	stopFixMaxCandidates  = 10
	stopFixMaxPromptRunes = 400
	stopFixProbeTimeout   = 180 * time.Second
	stopFixMaxProbeChars  = 4000
)

const (
	stopFixVerdictSafe     = "safe"
	stopFixVerdictCuts     = "cuts"
	stopFixVerdictPartial  = "partial"
	stopFixVerdictInactive = "inactive"
)

const (
	stopFixDiagnosisOK          = "ok"
	stopFixDiagnosisStops       = "stops"
	stopFixDiagnosisTemplate    = "template"
	stopFixDiagnosisUnsupported = "unsupported"
)

// stopTokenPattern matches the special-token shapes chat templates use around
// turns: <|im_end|>, <|eot_id|>, </s>, <turn|>, <|start_header_id|>, [INST].
var stopTokenPattern = regexp.MustCompile(`<\|[^|<>]{1,40}\|>|<[a-zA-Z/][^<>\s]{0,40}>|\[/?INST\]`)

// stopTerminatorMarkers are the markers that end a chat turn. They are used to
// cut the "lede" (the answer without the trailing turn markers) so a candidate
// stop that only removes `answer<|im_start|>user...` still counts as safe.
var stopTerminatorMarkers = []string{
	"<|im_end|>", "<|im_start|>", "<|eot_id|>", "<|eot|>", "<|end|>",
	"<end_of_turn>", "<|turn|>", "<turn|>", "</s>", "<|endoftext|>",
	"<|startoftext|>", "<|message_user|>", "<|message_model|>", "<|message_system|>",
	"<|content_text|>", "<|end_message|>", "[/INST]",
}

type stopFixRequest struct {
	Model   string    `json:"model"`
	Prompt  string    `json:"prompt,omitempty"`
	Stops   *[]string `json:"stops,omitempty"`
	Confirm bool      `json:"confirm,omitempty"`
}

type stopFixProbeResult struct {
	Content    string `json:"content"`
	DoneReason string `json:"done_reason,omitempty"`
	EvalCount  int    `json:"eval_count,omitempty"`
	Empty      bool   `json:"empty"`
}

type stopFixCandidate struct {
	Stop     string `json:"stop"`
	Source   string `json:"source"`
	Verdict  string `json:"verdict"`
	Evidence string `json:"evidence"`
}

type stopFixPreview struct {
	Model         string             `json:"model"`
	Prompt        string             `json:"prompt"`
	Supported     bool               `json:"supported"`
	Diagnosis     string             `json:"diagnosis"`
	NeedsFix      bool               `json:"needs_fix"`
	CurrentStops  []string           `json:"current_stops"`
	ProposedStops []string           `json:"proposed_stops"`
	AddedStops    []string           `json:"added_stops"`
	RemovedStops  []string           `json:"removed_stops"`
	Candidates    []stopFixCandidate `json:"candidates,omitempty"`
	CurrentProbe  stopFixProbeResult `json:"current_probe"`
	NoStopProbe   stopFixProbeResult `json:"no_stop_probe"`
	Notes         []string           `json:"notes,omitempty"`
	Warnings      []string           `json:"warnings,omitempty"`
	Modelfile     string             `json:"modelfile"`
}

type stopFixApplyResult struct {
	Model      string             `json:"model"`
	Stops      []string           `json:"stops"`
	BackupPath string             `json:"backup_path,omitempty"`
	Verify     stopFixProbeResult `json:"verify"`
	Replaced   bool               `json:"replaced"`
}

// ---------------------------------------------------------------------------
// Parameter parsing
// ---------------------------------------------------------------------------

// parseShowParameters reads the textual parameter block returned by /api/show
// ("top_p  0.95", `stop  "<|im_end|>"`) and returns the parsed parameters plus
// the stop sequences in declaration order. Values are unquoted; numbers and
// booleans are converted back to their JSON types.
func parseShowParameters(text string) (map[string]any, []string) {
	params := make(map[string]any)
	var stops []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, value, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		name = strings.ToLower(strings.TrimSpace(name))
		value = strings.TrimSpace(value)
		if name == "" || value == "" {
			continue
		}
		parsed := parseRepairParameterValue(value)
		if name == "stop" {
			stops = append(stops, fmt.Sprint(parsed))
			continue
		}
		// Repeated scalar parameters keep the last value, matching Ollama.
		params[name] = parsed
	}
	return params, stops
}

// ---------------------------------------------------------------------------
// Probing
// ---------------------------------------------------------------------------

func normalizeStopFixPrompt(prompt string) string {
	prompt = strings.Join(strings.Fields(prompt), " ")
	if prompt == "" {
		return "hi"
	}
	runes := []rune(prompt)
	if len(runes) > stopFixMaxPromptRunes {
		prompt = string(runes[:stopFixMaxPromptRunes])
	}
	return prompt
}

// probeStops runs one non-streaming chat turn. With clear=true the request
// option stop is an empty list, which makes Ollama ignore every stop parameter
// baked into the model. With stops!=nil only those stop sequences apply.
func (s *Server) probeStops(ctx context.Context, model, prompt string, stops []string, clear bool) (stopFixProbeResult, error) {
	opts := map[string]any{
		"num_predict": stopFixNumPredict,
		"temperature": 0,
	}
	switch {
	case clear:
		opts["stop"] = []string{}
	case stops != nil:
		opts["stop"] = stops
	}
	think := ollama.ThinkLevel("off")
	probeCtx, cancel := context.WithTimeout(ctx, stopFixProbeTimeout)
	defer cancel()
	resp, err := s.ollama.ChatOnce(probeCtx, ollama.ChatRequest{
		Model:    model,
		Messages: []ollama.ChatMessage{{Role: "user", Content: prompt}},
		Think:    &think,
		Options:  opts,
	})
	if err != nil {
		return stopFixProbeResult{}, fmt.Errorf("probe %s: %w", model, err)
	}
	res := stopFixProbeResult{
		Content:    resp.Message.Content,
		DoneReason: resp.DoneReason,
		EvalCount:  resp.EvalCount,
		Empty:      strings.TrimSpace(resp.Message.Content) == "" && strings.TrimSpace(resp.Message.Thinking) == "",
	}
	res.Content = truncateRunes(res.Content, stopFixMaxProbeChars)
	return res, nil
}

// ---------------------------------------------------------------------------
// Candidate analysis
// ---------------------------------------------------------------------------

func extractStopCandidates(text string) []string {
	if text == "" {
		return nil
	}
	var out []string
	seen := make(map[string]bool)
	for _, match := range stopTokenPattern.FindAllString(text, -1) {
		match = strings.TrimSpace(match)
		if match == "" || seen[match] {
			continue
		}
		seen[match] = true
		out = append(out, match)
	}
	return out
}

// ledeOf returns the answer with any trailing turn marker removed.
func ledeOf(text string) string {
	s := strings.TrimSpace(text)
	cut := -1
	for _, marker := range stopTerminatorMarkers {
		if idx := strings.Index(s, marker); idx > 0 && (cut == -1 || idx < cut) {
			cut = idx
		}
	}
	if cut > 0 {
		s = strings.TrimSpace(s[:cut])
	}
	return s
}

// classifyStopCandidate judges one candidate stop against the no-stop reply.
// A candidate is only safe when the reply it produces is a meaningful prefix of
// what the model says when no stop interferes.
func classifyStopCandidate(base, probe stopFixProbeResult) (string, string) {
	lede := ledeOf(base.Content)
	if lede == "" {
		return stopFixVerdictInactive, "the model produced no answer to compare against"
	}
	got := ledeOf(probe.Content)
	switch {
	case strings.TrimSpace(probe.Content) == "":
		return stopFixVerdictCuts, "blank reply: the stop matched before the first character"
	case strings.TrimSpace(probe.Content) == lede:
		return stopFixVerdictSafe, "keeps the full answer"
	case got == "" || !strings.HasPrefix(lede, got):
		return stopFixVerdictCuts, "changes the answer instead of ending it"
	case len([]rune(got))*10 < len([]rune(lede))*6:
		return stopFixVerdictPartial, "cuts the answer before it is complete"
	default:
		return stopFixVerdictSafe, "keeps the answer"
	}
}

// ---------------------------------------------------------------------------
// Preview
// ---------------------------------------------------------------------------

func (s *Server) buildStopFixPreview(ctx context.Context, model, prompt string) (*stopFixPreview, *ollama.ShowResponse, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil, nil, errors.New("missing model name")
	}
	show, err := s.ollama.Show(ctx, model)
	if err != nil {
		return nil, nil, fmt.Errorf("read model: %w", err)
	}
	if show == nil {
		show = &ollama.ShowResponse{}
	}
	if caps := show.Capabilities; len(caps) > 0 &&
		slices.Contains(caps, "embedding") && !slices.Contains(caps, "completion") {
		return nil, nil, fmt.Errorf("%s is an embedding model and cannot chat", model)
	}

	_, currentStops := parseShowParameters(show.Parameters)
	prompt = normalizeStopFixPrompt(prompt)
	_, blobErr := stopFixBlobPlan(show)
	preview := &stopFixPreview{
		Model:         model,
		Prompt:        prompt,
		Supported:     blobErr == nil,
		Diagnosis:     stopFixDiagnosisOK,
		CurrentStops:  append([]string{}, currentStops...),
		ProposedStops: append([]string{}, currentStops...),
		AddedStops:    []string{},
		RemovedStops:  []string{},
	}
	if !preview.Supported {
		preview.Diagnosis = stopFixDiagnosisUnsupported
		if blobErr != nil {
			preview.Notes = append(preview.Notes, blobErr.Error()+"; the stop fix only works on single-file local models.")
		}
		preview.Modelfile = displayModelfile(show.Modelfile, currentStops)
		return preview, show, nil
	}

	current, err := s.probeStops(ctx, model, prompt, nil, false)
	if err != nil {
		return nil, nil, err
	}
	preview.CurrentProbe = current
	if !current.Empty {
		preview.Notes = append(preview.Notes, "The model answered with its current stop tokens, so there is nothing to fix.")
		preview.Modelfile = displayModelfile(show.Modelfile, currentStops)
		return preview, show, nil
	}

	plain, err := s.probeStops(ctx, model, prompt, nil, true)
	if err != nil {
		return nil, nil, err
	}
	preview.NoStopProbe = plain
	if plain.Empty {
		preview.Diagnosis = stopFixDiagnosisTemplate
		preview.Notes = append(preview.Notes, "The model also stays blank with every stop removed, so the stop parameters are not the cause.")
		preview.Warnings = append(preview.Warnings, "This looks like a template, renderer or prompt-format problem. Fix the template from Repair options instead of the stop tokens.")
		preview.Modelfile = displayModelfile(show.Modelfile, currentStops)
		return preview, show, nil
	}

	preview.Diagnosis = stopFixDiagnosisStops
	preview.NeedsFix = true
	preview.Notes = append(preview.Notes,
		fmt.Sprintf("The reply is blank with the current %d stop token(s) but complete when no stop applies (%d tokens).", len(currentStops), plain.EvalCount))

	// Candidate order: tokens the model really emitted, then the current stops,
	// then tokens the template mentions.
	var candidates []string
	seen := make(map[string]bool)
	sources := make(map[string]string)
	addCandidate := func(stop, source string) {
		stop = strings.TrimSpace(stop)
		if stop == "" || seen[stop] || len(candidates) >= stopFixMaxCandidates {
			return
		}
		seen[stop] = true
		sources[stop] = source
		candidates = append(candidates, stop)
	}
	for _, stop := range extractStopCandidates(plain.Content) {
		addCandidate(stop, "output")
	}
	for _, stop := range currentStops {
		addCandidate(stop, "current")
	}
	for _, stop := range extractStopCandidates(show.Template) {
		addCandidate(stop, "template")
	}

	verdicts := make(map[string]string, len(candidates))
	for _, stop := range candidates {
		probe, err := s.probeStops(ctx, model, prompt, []string{stop}, false)
		if err != nil {
			return nil, nil, err
		}
		verdict, evidence := classifyStopCandidate(plain, probe)
		verdicts[stop] = verdict
		preview.Candidates = append(preview.Candidates, stopFixCandidate{
			Stop:     stop,
			Source:   sources[stop],
			Verdict:  verdict,
			Evidence: evidence,
		})
	}

	// Keep the current stops that never cut the answer, plus terminators the
	// model really emits. A candidate the model never printed is not added: it
	// would be dead weight in the Modelfile.
	var proposed []string
	emitted := make(map[string]bool)
	for _, stop := range extractStopCandidates(plain.Content) {
		emitted[stop] = true
	}
	for _, stop := range currentStops {
		if verdicts[stop] == stopFixVerdictSafe {
			proposed = append(proposed, stop)
		}
	}
	for _, stop := range candidates {
		if verdicts[stop] != stopFixVerdictSafe || !emitted[stop] {
			continue
		}
		if !slices.Contains(proposed, stop) {
			proposed = append(proposed, stop)
		}
	}
	if len(proposed) == 0 {
		preview.Notes = append(preview.Notes, "No stop survives the probe: the model ends on its own end-of-sequence token, so every stop is removed.")
	}
	if proposed == nil {
		proposed = []string{}
	}
	preview.ProposedStops = proposed
	preview.AddedStops = missingStops(proposed, currentStops)
	preview.RemovedStops = missingStops(currentStops, proposed)
	preview.Modelfile = displayModelfile(show.Modelfile, proposed)
	if plain.DoneReason == "length" && len(proposed) == 0 {
		preview.Warnings = append(preview.Warnings, "Without any stop the model runs to the token limit: add the right terminator by hand if the answer is cut at the end.")
	}
	return preview, show, nil
}

// missingStops returns the entries of a that are not in b.
func missingStops(a, b []string) []string {
	var out []string
	for _, s := range a {
		if !slices.Contains(b, s) {
			out = append(out, s)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Modelfile rendering
// ---------------------------------------------------------------------------

// rewriteModelfileStops removes every PARAMETER stop line and appends the new
// list right after the FROM directives, leaving TEMPLATE, SYSTEM, RENDERER,
// PARSER and every other parameter untouched.
func rewriteModelfileStops(modelfile string, stops []string) string {
	var lines []string
	insertAt := -1
	for _, line := range strings.Split(modelfile, "\n") {
		trimmed := strings.TrimSpace(line)
		upper := strings.ToUpper(trimmed)
		if strings.HasPrefix(upper, "PARAMETER STOP ") || strings.EqualFold(trimmed, "PARAMETER STOP") {
			continue
		}
		lines = append(lines, line)
		if strings.HasPrefix(upper, "FROM ") {
			insertAt = len(lines)
		}
	}
	if insertAt < 0 {
		insertAt = 0
	}
	var stopLines []string
	for _, stop := range stops {
		stopLines = append(stopLines, fmt.Sprintf("PARAMETER stop %q", stop))
	}
	out := make([]string, 0, len(lines)+len(stopLines)+1)
	out = append(out, lines[:insertAt]...)
	if len(stopLines) > 0 {
		out = append(out, "")
		out = append(out, stopLines...)
	}
	out = append(out, lines[insertAt:]...)
	return strings.TrimSpace(strings.Join(out, "\n")) + "\n"
}

// displayModelfile is the Modelfile shown in the modal: the rewritten file
// without the license body, which is long and irrelevant to the fix.
func displayModelfile(modelfile string, stops []string) string {
	rewritten := rewriteModelfileStops(modelfile, stops)
	upper := strings.ToUpper(rewritten)
	if idx := strings.Index(upper, "LICENSE \"\"\""); idx >= 0 {
		rest := rewritten[idx+len("LICENSE \"\"\""):]
		if end := strings.Index(rest, "\"\"\""); end >= 0 {
			rewritten = rewritten[:idx] + "LICENSE \"\"\"(preserved, not shown)\"\"\"" + rest[end+3:]
		}
	}
	rewritten = truncateRunes(rewritten, 16*1024)
	return rewritten
}

// ---------------------------------------------------------------------------
// Apply
// ---------------------------------------------------------------------------

// stopFixBlobPlan maps the model's blobs to create files. A second blob is only
// accepted when Ollama reports a projector: a two-shard split GGUF would
// otherwise be mislabelled as a vision projector and the rebuild would produce
// a broken model.
func stopFixBlobPlan(show *ollama.ShowResponse) (map[string]string, error) {
	blobs := extractBlobs(show.Modelfile)
	if len(blobs) == 0 {
		return nil, errors.New("model has no GGUF blob to rebuild from (remote or cloud model)")
	}
	if len(blobs) > 2 {
		return nil, fmt.Errorf("the model is stored as %d GGUF shards; split GGUFs cannot be rebuilt automatically", len(blobs))
	}
	if len(blobs) == 2 && len(show.ProjectorInfo) == 0 {
		return nil, errors.New("the model has two GGUF blobs but no vision projector; this looks like a split GGUF, which cannot be rebuilt automatically")
	}
	files := map[string]string{"model.gguf": blobDigest(blobs[0])}
	if len(blobs) == 2 {
		files["mmproj.gguf"] = blobDigest(blobs[1])
	}
	for name, digest := range files {
		if digest == "" {
			return nil, fmt.Errorf("unreadable blob reference for %s", name)
		}
	}
	return files, nil
}

// buildStopFixCreateRequest rebuilds the model from its own GGUF blob(s) with
// the same template, system, renderer, parser and parameters, only replacing
// the stop list. Building from the blob avoids inheriting the broken stops of
// the previous manifest.
func buildStopFixCreateRequest(model string, show *ollama.ShowResponse, stops []string) (ollama.CreateRequest, error) {
	files, err := stopFixBlobPlan(show)
	if err != nil {
		return ollama.CreateRequest{}, err
	}

	params, _ := parseShowParameters(show.Parameters)
	delete(params, "stop")
	if len(stops) > 0 {
		params["stop"] = append([]string(nil), stops...)
	} else {
		// An explicit empty list keeps the rebuilt model stop-free instead of
		// letting Ollama re-apply architecture defaults.
		params["stop"] = []string{}
	}

	system := strings.TrimSpace(show.System)
	if system == "" {
		system = strings.TrimSpace(extractModelfileSystem(show.Modelfile))
	}

	req := ollama.CreateRequest{
		Model:      model,
		Files:      files,
		Template:   show.Template,
		System:     system,
		Parameters: params,
		Renderer:   extractLineDirective(show.Modelfile, "RENDERER"),
		Parser:     extractLineDirective(show.Modelfile, "PARSER"),
		Stream:     false,
	}
	if strings.TrimSpace(show.License) != "" {
		req.License = show.License
	}
	return req, nil
}

func (s *Server) stopFixBackupDir() string {
	if s != nil && strings.TrimSpace(s.stopFixBackupRoot) != "" {
		return s.stopFixBackupRoot
	}
	dir := ""
	if s != nil && s.cfg != nil {
		if path := strings.TrimSpace(s.cfg.Path()); path != "" {
			dir = filepath.Dir(path)
		}
	}
	if dir == "" {
		if base, err := os.UserConfigDir(); err == nil {
			dir = filepath.Join(base, "ollama-manager")
		}
	}
	if dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "stop-fix-backups")
}

var nonFileNameChars = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// writeStopFixBackup stores the current Modelfile so the change can be undone
// by hand with `ollama create <model> -f <file>`.
func (s *Server) writeStopFixBackup(model, modelfile string) (string, error) {
	dir := s.stopFixBackupDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create backup directory: %w", err)
	}
	safe := nonFileNameChars.ReplaceAllString(model, "_")
	name := fmt.Sprintf("%s_%s.Modelfile", safe, time.Now().Format("20060102-150405"))
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(modelfile), 0o600); err != nil {
		return "", fmt.Errorf("write backup: %w", err)
	}
	return path, nil
}

func (s *Server) applyStopFix(ctx context.Context, req stopFixRequest) (*stopFixApplyResult, error) {
	model := strings.TrimSpace(req.Model)
	if model == "" {
		return nil, errors.New("missing model name")
	}
	show, err := s.ollama.Show(ctx, model)
	if err != nil {
		return nil, fmt.Errorf("read model: %w", err)
	}
	if show == nil {
		show = &ollama.ShowResponse{}
	}

	var stops []string
	if req.Stops != nil {
		for _, stop := range *req.Stops {
			stop = strings.TrimSpace(stop)
			if stop != "" {
				stops = append(stops, stop)
			}
		}
	} else {
		preview, _, err := s.buildStopFixPreview(ctx, model, req.Prompt)
		if err != nil {
			return nil, err
		}
		if !preview.NeedsFix {
			return nil, errors.New("the diagnosis found nothing to fix; run the preview again before applying")
		}
		stops = preview.ProposedStops
	}

	createReq, err := buildStopFixCreateRequest(model, show, stops)
	if err != nil {
		return nil, err
	}
	backup, err := s.writeStopFixBackup(model, show.Modelfile)
	if err != nil {
		return nil, err
	}

	// The old runner keeps the previous manifest in memory; unload it so the
	// verification probe runs against the rebuilt model.
	_ = s.ollama.Unload(ctx, model)
	if err := s.ollama.Create(ctx, createReq); err != nil {
		return nil, fmt.Errorf("rebuild %s: %w (previous Modelfile saved to %s)", model, err, backup)
	}

	// The daemon may answer /api/show from a cache for a moment; wait until the
	// rebuilt parameters are visible before probing, but never fail on mismatch:
	// the probe below is the real proof.
	for i := 0; i < 10; i++ {
		if refreshed, err := s.ollama.Show(ctx, model); err == nil && refreshed != nil {
			_, gotStops := parseShowParameters(refreshed.Parameters)
			if sameStops(gotStops, stops) {
				break
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}

	verify, err := s.probeStops(ctx, model, normalizeStopFixPrompt(req.Prompt), nil, false)
	if err != nil || verify.Empty {
		// The rebuilt model does not answer: put the previous one back instead
		// of leaving the user with a model that is worse than before.
		restoreErr := s.restoreStopFix(ctx, model, show)
		reason := "the rebuilt model did not answer"
		if err != nil {
			reason = fmt.Sprintf("verification probe failed: %v", err)
		}
		if restoreErr != nil {
			return nil, fmt.Errorf("%s; restoring the previous model also failed: %v (previous Modelfile saved to %s)", reason, restoreErr, backup)
		}
		return nil, fmt.Errorf("%s; the previous model was restored (Modelfile copy saved to %s)", reason, backup)
	}
	return &stopFixApplyResult{
		Model:      model,
		Stops:      append([]string{}, stops...),
		BackupPath: backup,
		Verify:     verify,
		Replaced:   true,
	}, nil
}

// restoreStopFix rebuilds the model exactly as it was before the fix, using the
// snapshot taken before the first create.
func (s *Server) restoreStopFix(ctx context.Context, model string, original *ollama.ShowResponse) error {
	if original == nil {
		return errors.New("no snapshot of the previous model")
	}
	_, originalStops := parseShowParameters(original.Parameters)
	req, err := buildStopFixCreateRequest(model, original, originalStops)
	if err != nil {
		return err
	}
	_ = s.ollama.Unload(ctx, model)
	return s.ollama.Create(ctx, req)
}

// sameStops compares stop lists ignoring order and duplicates.
func sameStops(a, b []string) bool {
	norm := func(in []string) []string {
		out := make([]string, 0, len(in))
		for _, s := range in {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		slices.Sort(out)
		return slices.Compact(out)
	}
	x, y := norm(a), norm(b)
	return slices.Equal(x, y)
}

// ---------------------------------------------------------------------------
// HTTP handlers
// ---------------------------------------------------------------------------

func (s *Server) handleStopFixPreview(w http.ResponseWriter, r *http.Request) {
	var body stopFixRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
		return
	}
	preview, _, err := s.buildStopFixPreview(r.Context(), body.Model, body.Prompt)
	if err != nil {
		writeStopFixError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) handleStopFixApply(w http.ResponseWriter, r *http.Request) {
	var body stopFixRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
		return
	}
	if !body.Confirm {
		writeError(w, http.StatusBadRequest, errors.New("confirmation is required before rebuilding the model"))
		return
	}
	result, err := s.applyStopFix(r.Context(), body)
	if err != nil {
		writeStopFixError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeStopFixError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
	case strings.Contains(err.Error(), "read model") || strings.Contains(err.Error(), "probe "):
		status = http.StatusBadGateway
	}
	writeError(w, status, err)
}
