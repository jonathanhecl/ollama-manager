package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gense/ollama-manager/internal/comfyui"
)

// comfyToolName is the single tool exposed to models for image, video and audio
// generation. One name with a merged parameter schema beats one tool per
// workflow: the model reads the schema and picks the workflow, and adding a
// workflow in Settings never changes the tool list mid-conversation.
const comfyToolName = "run_comfy_workflow"

// comfyQuickMediaDir holds media produced by the request-bound quick chat, which
// has no session id to file it under.
const comfyQuickMediaDir = "quick"

// comfyQuickMediaMaxFiles caps that folder. Quick chat runs are throwaway, but
// a user iterating on a look can produce a lot of images in one afternoon.
const comfyQuickMediaMaxFiles = 60

// comfyToolResult separates what the model gets from what the chat renders. The
// images are the same bytes either way; the difference is the data URL prefix
// and that video and audio never reach the model at all.
type comfyToolResult struct {
	// Text is the tool message content for the model.
	Text string
	// Images are base64 payloads without a data URL prefix, ready for
	// ollama.ChatMessage.Images.
	Images []string
	// Media is everything stored for the chat, images included.
	Media []ChatMedia
	// PromptID identifies the ComfyUI run, for the "open in ComfyUI" link.
	PromptID string
	// WorkflowName is the display name of the workflow that ran.
	WorkflowName string
}

// comfyToolDefinitions builds the tool schema from the enabled workflows. Every
// writable input the user declared as a binding becomes a flat parameter, with
// the current value as its default, so the model can see what it is changing.
func (s *Server) comfyToolDefinitions(preferred string) []any {
	if s.comfyWorkflows == nil {
		return nil
	}
	enabled := s.comfyWorkflows.ListEnabled()
	if len(enabled) == 0 {
		return nil
	}

	// Prefer the selected workflow's bindings when two workflows bind the same
	// name to different nodes, so the defaults the model reads match the workflow
	// a bare call actually runs.
	var sel *ComfyWorkflow
	if found, ok := s.comfyWorkflows.Resolve(preferred); ok && found.Enabled {
		sel = found
	}
	var ordered []*ComfyWorkflow
	if sel != nil {
		ordered = append(ordered, sel)
	}
	for _, wf := range enabled {
		if sel != nil && wf.ID == sel.ID {
			continue
		}
		ordered = append(ordered, wf)
	}

	// One schema for the model regardless of how many workflows exist: a bare
	// call uses the selected workflow, an explicit one switches.
	schemaWorkflow := ordered[0]
	properties := map[string]any{}
	for _, b := range schemaWorkflow.Bindings {
		properties[b.Param] = comfyBindingSchema(b, schemaWorkflow.Workflow)
	}
	var lines []string
	for _, wf := range ordered {
		desc := strings.TrimSpace(wf.Description)
		if desc == "" {
			desc = "no description"
		}
		kind := wf.OutputKind
		if kind == "" {
			kind = "unknown output"
		}
		lines = append(lines, fmt.Sprintf("- %q: %s [produces %s]", wf.Name, desc, kind))
	}
	if len(ordered) > 1 {
		names := make([]string, 0, len(ordered))
		for _, wf := range ordered {
			names = append(names, wf.Name)
		}
		properties["workflow"] = map[string]any{
			"type":        "string",
			"enum":        names,
			"description": "Which workflow to run. Omit to use the workflow selected in the chat panel.",
		}
	}

	params := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if hasComfyParam(schemaWorkflow.Bindings, "prompt") {
		params["required"] = []string{"prompt"}
	}

	desc := "Generate images (and sometimes video or audio) by running a registered ComfyUI workflow on the user's machine. " +
		"Use it when the user asks you to draw, paint, illustrate, render, or make a picture; " +
		"do not use it for code, text or explanations.\n\n" +
		"Registered workflows:\n" + strings.Join(lines, "\n") + "\n\n" +
		"The generated image is attached to this tool result, so LOOK at it before you answer. " +
		"Use what you see to decide the next step: reword the prompt, change the style, adjust seed, steps or cfg, " +
		"or switch to another workflow. Do not describe a result you have not actually seen.\n" +
		"Video and audio outputs are shown to the user in the chat but are NOT attached to you. " +
		"Never claim to have watched or listened to them; ask the user what they think."

	return []any{
		map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        comfyToolName,
				"description": desc,
				"parameters":  params,
			},
		},
	}
}

func hasComfyParam(bindings []ComfyBinding, param string) bool {
	for _, b := range bindings {
		if b.Param == param {
			return true
		}
	}
	return false
}

// comfyBindingSchema renders one binding as a JSON schema property, including
// the value the workflow currently holds so a bare call is a rerun rather than
// a blank slate.
func comfyBindingSchema(b ComfyBinding, wf comfyui.Workflow) map[string]any {
	label := b.Label
	if label == "" {
		label = b.Param
	}
	schema := map[string]any{"type": comfyBindingSchemaType(b.Kind), "description": label}
	if len(b.Enum) > 0 {
		schema["enum"] = b.Enum
	}
	if node, ok := wf[b.NodeID]; ok {
		if v, ok := node.Inputs[b.Input]; ok {
			if d, ok := comfySchemaDefault(v); ok {
				schema["default"] = d
			}
		}
	}
	if b.Title != "" {
		schema["description"] = fmt.Sprintf("%s (node %s %q)", label, b.ClassType, b.Title)
	}
	return schema
}

// comfySchemaDefault exposes the stored value as a schema default. Long text
// prompts are truncated, since a 400-character default bloats every request.
func comfySchemaDefault(v any) (any, bool) {
	switch x := v.(type) {
	case string:
		if x == "" {
			return nil, false
		}
		if utf8.RuneCountInString(x) > 160 {
			return strings.TrimSpace(string([]rune(x)[:160])) + "…", true
		}
		return x, true
	case float64, bool:
		return x, true
	case []any:
		// A list input: show the first entry, which is the active choice.
		if len(x) > 0 {
			if s, ok := x[0].(string); ok && s != "" {
				return s, true
			}
		}
		return nil, false
	default:
		return nil, false
	}
}

// runComfyTool resolves the workflow, patches the parameters, queues the run and
// collects the outputs. onStatus, when set, receives short progress lines for
// the chat entry so a 90-second render does not look like a hang.
func (s *Server) runComfyTool(ctx context.Context, sessionID, preferred string, args json.RawMessage, onStatus func(string)) (comfyToolResult, error) {
	var res comfyToolResult
	if s.comfyWorkflows == nil {
		return res, fmt.Errorf("ComfyUI workflows are not configured")
	}
	client, err := s.comfyClient()
	if err != nil {
		return res, err
	}

	m := parseToolArgs(args)
	ref, _ := m["workflow"].(string)
	ref = strings.TrimSpace(ref)

	wf, err := s.comfyResolveWorkflow(ref, preferred)
	if err != nil {
		return res, err
	}
	res.WorkflowName = wf.Name

	params := map[string]any{}
	for k, v := range m {
		if k == "workflow" {
			continue
		}
		params[k] = v
	}
	patched, err := patchComfyWorkflow(wf.Workflow, wf.Bindings, params)
	if err != nil {
		return res, err
	}

	if onStatus != nil {
		onStatus("sending to ComfyUI")
	}
	queued, err := client.QueuePrompt(ctx, patched, s.comfyClientID())
	if err != nil {
		return res, err
	}
	res.PromptID = queued.PromptID
	if queued.Number > 0 && onStatus != nil {
		onStatus(fmt.Sprintf("queued as #%d", queued.Number))
	}

	timeout := s.comfyTimeout()
	entry, err := client.WaitResult(ctx, queued.PromptID, timeout, nil)
	if err != nil {
		// A cancelled turn should not leave the GPU grinding on a picture
		// nobody will see. Only interrupt when the job that is running is ours.
		if ctx.Err() != nil {
			interruptOwnComfyJob(client, queued.PromptID)
		}
		return res, err
	}
	if onStatus != nil {
		onStatus("downloading results")
	}

	media, err := s.collectComfyOutputs(ctx, client, sessionID, queued.PromptID, entry)
	if err != nil {
		return res, err
	}
	res.Media = media
	res.Images = comfyModelImages(sessionID, media)
	res.Text = describeComfyRun(wf, params, media, len(res.Images))
	return res, nil
}

// comfyToolEnabled reports whether this request should expose the ComfyUI tool.
// It stays off until the user turns it on in the chat panel, and it is refused
// outright when no workflow is enabled, since offering a tool that can only fail
// wastes a model turn and teaches the model to retry blindly.
func (s *Server) comfyToolEnabled(body chatRequestBody) bool {
	if body.Comfy == nil || !*body.Comfy || s.comfyWorkflows == nil {
		return false
	}
	return len(s.comfyWorkflows.ListEnabled()) > 0
}

// comfySessionID is the storage bucket for media produced during a run. Detached
// session runs pass their id so the files die with the transcript; the quick chat
// passes nothing and falls into the shared quick folder.
func comfySessionID(body chatRequestBody) string {
	if body.SessionID != "" {
		return body.SessionID
	}
	return ""
}

// executeComfyTool is the single entry point both agent loops use, so the
// session id, the selected workflow and the progress callback are resolved in
// one place.
func (s *Server) executeComfyTool(ctx context.Context, body chatRequestBody, args json.RawMessage, onStatus func(string)) (comfyToolResult, error) {
	preferred := s.comfyConfiguredWorkflowID(body.ComfyWorkflow)
	return s.runComfyTool(ctx, comfySessionID(body), preferred, args, onStatus)
}

// hasVisionModel reports whether the model can be shown images. It is the single
// gate for "the model gets to see what ComfyUI produced", so the system prompt
// and the media path can never disagree about it.
func hasVisionModel(ctx context.Context, s *Server, model string) bool {
	if s == nil || s.ollama == nil || strings.TrimSpace(model) == "" {
		return false
	}
	show, err := s.ollama.Show(ctx, model)
	if err != nil || show == nil {
		return false
	}
	caps := withoutProjectorCaps(show.Capabilities, len(show.ProjectorInfo) > 0, show.Details.Format)
	for _, c := range caps {
		if strings.EqualFold(c, "vision") {
			return true
		}
	}
	return false
}

// applyComfyMediaToToolEvent attaches the results to a "tool" done event. The
// first image also goes in the legacy "image" field, which is what the chat
// renderer already knows how to inline; "media" carries the full list, including
// the video and audio the renderer has to handle differently.
func applyComfyMediaToToolEvent(done map[string]any, images []string, media []ChatMedia) {
	if len(media) == 0 && len(images) == 0 {
		return
	}
	if len(images) > 0 {
		done["image"] = "data:image/jpeg;base64," + images[0]
	}
	if len(media) > 0 {
		done["media"] = media
	}
}

// comfyResolveWorkflow picks the workflow a call runs on: the one the model
// named, then the one the chat panel selected, then the only enabled one. The
// name it settles on is reported back to the chat, so it has to be decided the
// same way whether it is resolved for the progress header or for the run itself.
func (s *Server) comfyResolveWorkflow(ref, preferred string) (*ComfyWorkflow, error) {
	if s.comfyWorkflows == nil {
		return nil, fmt.Errorf("ComfyUI workflows are not configured")
	}
	enabled := s.comfyWorkflows.ListEnabled()
	if ref != "" {
		wf, ok := s.comfyWorkflows.Resolve(ref)
		if !ok {
			return nil, fmt.Errorf("unknown workflow %q. Available: %s", ref, strings.Join(comfyWorkflowNames(enabled), ", "))
		}
		if !wf.Enabled {
			return nil, fmt.Errorf("workflow %q is disabled. Available: %s", wf.Name, strings.Join(comfyWorkflowNames(enabled), ", "))
		}
		return wf, nil
	}
	if preferred != "" {
		if wf, ok := s.comfyWorkflows.Resolve(preferred); ok && wf.Enabled {
			return wf, nil
		}
	}
	switch len(enabled) {
	case 0:
		return nil, fmt.Errorf("no ComfyUI workflows are enabled. Ask the user to register one in Settings > ComfyUI")
	case 1:
		return enabled[0], nil
	default:
		return nil, fmt.Errorf("several workflows are available (%s) and the chat panel does not select one. "+
			"Pass workflow=%q", strings.Join(comfyWorkflowNames(enabled), ", "), enabled[0].Name)
	}
}

// comfyWorkflowLabelFor reports the workflow a call will run on, without running
// it, so the chat can name the workflow in the tool header from the start. It
// falls back to the panel selection when the model named nothing.
func (s *Server) comfyWorkflowLabelFor(preferred string, args json.RawMessage) string {
	m := parseToolArgs(args)
	ref, _ := m["workflow"].(string)
	wf, err := s.comfyResolveWorkflow(strings.TrimSpace(ref), preferred)
	if err != nil {
		return ""
	}
	return wf.Name
}

func comfyWorkflowNames(list []*ComfyWorkflow) []string {
	out := make([]string, 0, len(list))
	for _, wf := range list {
		out = append(out, wf.Name)
	}
	sort.Strings(out)
	return out
}

// interruptOwnComfyJob asks ComfyUI to stop, but only if the prompt we queued is
// the one currently executing. Interrupting unconditionally would kill a run
// started from the ComfyUI UI.
func interruptOwnComfyJob(client *comfyui.Client, promptID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	state, err := client.Queue(ctx)
	if err != nil {
		return
	}
	for _, entry := range state.QueueRunning {
		if len(entry) > 1 {
			if id, ok := entry[1].(string); ok && id == promptID {
				_ = client.Interrupt(ctx)
				return
			}
		}
	}
}

// collectComfyOutputs downloads every non-temporary file the run produced and
// stores it under the session. Files are downloaded concurrently because a video
// workflow can emit several large files and serial transfers dominate the wait.
func (s *Server) collectComfyOutputs(ctx context.Context, client *comfyui.Client, sessionID, promptID string, entry *comfyui.HistoryEntry) ([]ChatMedia, error) {
	type fetched struct {
		file comfyui.OutputFile
		ct   string
		data []byte
		err  error
	}
	var jobs []fetched
	for _, out := range entry.Outputs {
		for _, f := range out.Files() {
			if strings.EqualFold(f.Type, "temp") {
				// Intermediates (previews, latents) are noise in the chat.
				continue
			}
			jobs = append(jobs, fetched{file: f})
		}
	}
	if len(jobs) == 0 {
		return nil, nil
	}

	results := make([]fetched, len(jobs))
	work := make(chan int)
	workers := 4
	if len(jobs) < workers {
		workers = len(jobs)
	}
	done := make(chan struct{})
	for i := 0; i < workers; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for idx := range work {
				ct, data, err := client.Download(ctx, jobs[idx].file)
				results[idx] = fetched{file: jobs[idx].file, ct: ct, data: data, err: err}
			}
		}()
	}
	for i := range jobs {
		select {
		case work <- i:
		case <-ctx.Done():
			close(work)
			<-done
			return nil, ctx.Err()
		}
	}
	close(work)
	for i := 0; i < workers; i++ {
		<-done
	}

	var media []ChatMedia
	var firstErr error
	for i, r := range results {
		if r.err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("could not download %s: %w", r.file.Filename, r.err)
			}
			continue
		}
		m, err := saveComfyMedia(comfyMediaSession(sessionID), i, r.file.Filename, r.ct, r.data)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		m.PromptID = promptID
		media = append(media, m)
	}
	if len(media) == 0 && firstErr != nil {
		return nil, firstErr
	}
	if sessionID == "" {
		pruneQuickComfyMedia()
	}
	return media, nil
}

// comfyMediaSession maps a session id onto the folder name used on disk. Empty
// means the quick chat, which is not tied to any session.
func comfyMediaSession(sessionID string) string {
	if strings.TrimSpace(sessionID) == "" {
		return comfyQuickMediaDir
	}
	return sessionID
}

// pruneQuickComfyMedia keeps the quick-chat folder bounded by deleting the oldest
// files. File names start with a millisecond timestamp, so a name sort is a
// time sort.
func pruneQuickComfyMedia() {
	dir := filepath.Join(comfyMediaRoot(), comfyQuickMediaDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) <= comfyQuickMediaMaxFiles {
		return
	}
	sort.Strings(names)
	for _, name := range names[:len(names)-comfyQuickMediaMaxFiles] {
		_ = os.Remove(filepath.Join(dir, name))
	}
}

// comfyModelImages loads the stored images back as base64 for the model. It
// re-reads from disk rather than keeping the encoded copies around, because a
// 1024px JPEG is ~150 KB of base64 and holding four of those through a long
// agent loop is wasteful.
func comfyModelImages(sessionID string, media []ChatMedia) []string {
	var out []string
	for _, m := range media {
		if m.Kind != comfyMediaImage {
			continue
		}
		b64, _, _, ok := comfyModelImageFromMedia(m, comfyModelImageMaxDim)
		if !ok {
			continue
		}
		out = append(out, b64)
	}
	return out
}

// describeComfyRun writes the tool message. It has to be explicit about what the
// model can and cannot see, otherwise it will describe a video it never received.
func describeComfyRun(wf *ComfyWorkflow, params map[string]any, media []ChatMedia, imageCount int) string {
	var b strings.Builder
	if len(media) == 0 {
		fmt.Fprintf(&b, "Workflow %q finished but produced no output files. The graph probably has no save node, "+
			"or every node it produced was a temporary preview. Check that the workflow ends in a SaveImage, "+
			"VHS_VideoCombine or similar node.", wf.Name)
		return b.String()
	}

	counts := map[string]int{}
	for _, m := range media {
		counts[m.Kind]++
	}
	var kinds []string
	for _, k := range []string{comfyMediaImage, comfyMediaVideo, comfyMediaAudio, comfyMediaFile} {
		if counts[k] > 0 {
			kinds = append(kinds, fmt.Sprintf("%d %s%s", counts[k], k, plural(counts[k])))
		}
	}
	fmt.Fprintf(&b, "Workflow %q produced %s.", wf.Name, strings.Join(kinds, " and "))
	for _, m := range media {
		fmt.Fprintf(&b, "\n- %s: %s", m.Name, describeComfyMedia(m))
	}

	if imageCount > 0 {
		if imageCount == 1 {
			b.WriteString("\nThe image is attached to this message. Look at it before you reply.")
		} else {
			fmt.Fprintf(&b, "\nThe %d images are attached to this message. Look at them before you reply.", imageCount)
		}
	}
	if counts[comfyMediaVideo] > 0 || counts[comfyMediaAudio] > 0 {
		var hidden []string
		if counts[comfyMediaVideo] > 0 {
			hidden = append(hidden, fmt.Sprintf("%d video(s)", counts[comfyMediaVideo]))
		}
		if counts[comfyMediaAudio] > 0 {
			hidden = append(hidden, fmt.Sprintf("%d audio file(s)", counts[comfyMediaAudio]))
		}
		fmt.Fprintf(&b, "\nThe %s %s saved in ComfyUI and shown to the user in the chat, but nothing was attached to this "+
			"message, so you cannot see or hear %s. Say what you generated and ask the user what they think of it.",
			strings.Join(hidden, " and "), pluralVerb(counts[comfyMediaVideo]+counts[comfyMediaAudio]),
			pluralIsAre(counts[comfyMediaVideo]+counts[comfyMediaAudio]))
	}
	if len(params) > 0 {
		var parts []string
		for _, name := range sortedComfyParamNames(params) {
			parts = append(parts, fmt.Sprintf("%s=%s", name, fmtComfyParam(params[name])))
		}
		fmt.Fprintf(&b, "\nParameters you set: %s.", strings.Join(parts, ", "))
	}
	return b.String()
}

func fmtComfyParam(v any) string {
	switch x := v.(type) {
	case string:
		if utf8.RuneCountInString(x) > 80 {
			return "\"" + strings.TrimSpace(string([]rune(x)[:80])) + "…\""
		}
		return "\"" + x + "\""
	case nil:
		return "null"
	default:
		return fmt.Sprint(x)
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func pluralVerb(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

func pluralIsAre(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

// comfyToolStartPayload extends the shared tool-start event with the workflow and
// prompt, which is what the chat entry shows while the render runs.
func comfyToolStartPayload(base map[string]any, args json.RawMessage, workflowName string) map[string]any {
	if base == nil {
		base = map[string]any{}
	}
	m := parseToolArgs(args)
	// The resolved name goes in, not whatever the model typed: most calls let the
	// panel decide, and the chat entry has to name the workflow that actually ran.
	if workflowName != "" {
		base["workflow"] = workflowName
	} else if wf, _ := m["workflow"].(string); strings.TrimSpace(wf) != "" {
		base["workflow"] = wf
	}
	if p, _ := m["prompt"].(string); strings.TrimSpace(p) != "" {
		if utf8.RuneCountInString(p) > 120 {
			base["prompt"] = strings.TrimSpace(string([]rune(p)[:120])) + "…"
		} else {
			base["prompt"] = p
		}
	}
	if v, ok := m["seed"]; ok {
		base["seed"] = int64(numFromAny(v))
	}
	return base
}

// comfySystemPromptSection documents the integration to the model. The tool
// description alone does not stop a model from answering "here is your prompt for
// Midjourney" when it should be running the workflow itself.
func comfySystemPromptSection(workflows []*ComfyWorkflow, hasVision bool, selected string) string {
	if len(workflows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Image generation (ComfyUI)\n\n")
	if selected != "" {
		fmt.Fprintf(&b, "The user has a ComfyUI server connected and the chat panel has the workflow %q selected. "+
			"When they ask for a picture, run it with the `%s` tool instead of writing a prompt for them to paste elsewhere.\n",
			selected, comfyToolName)
	} else {
		fmt.Fprintf(&b, "The user has ComfyUI workflows available. When they ask for a picture, run one with the `%s` tool "+
			"instead of writing a prompt for them to paste elsewhere.\n", comfyToolName)
	}
	if hasVision {
		b.WriteString("Each run returns the generated image attached to the tool result, so you can see it and react to it. " +
			"Iterate: look at what came out, then adjust the prompt, seed, steps or cfg, or switch workflow, to correct it " +
			"or explore a variant. Say plainly what you changed and why.\n")
	} else {
		b.WriteString("You cannot see the results, so do not pretend to. Run the workflow, then tell the user what you " +
			"generated and invite them to describe how it turned out so you can adjust the next run.\n")
	}
	if selected == "" && len(workflows) > 1 {
		fmt.Fprintf(&b, "Available workflows: %s. Pass the one that best matches the request, and prefer the workflow "+
			"whose description fits instead of forcing every request through the same one.\n",
			strings.Join(comfyWorkflowNames(workflows), ", "))
	}
	return b.String()
}

// comfyClient builds a client from the current config. It is rebuilt per call
// instead of cached so a URL edited in Settings takes effect without a restart.
func (s *Server) comfyClient() (*comfyui.Client, error) {
	s.cfgMu.RLock()
	url := s.cfg.ComfyUI.URL
	s.cfgMu.RUnlock()
	if strings.TrimSpace(url) == "" {
		return nil, comfyui.ErrNotConfigured
	}
	return comfyui.New(url, 0), nil
}

func (s *Server) comfyClientID() string {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg.ComfyUI.ClientID
}

// comfyTimeout is how long one render may take before the manager gives up. The
// HTTP client itself has no timeout, because the run outlives a single request.
func (s *Server) comfyTimeout() time.Duration {
	s.cfgMu.RLock()
	secs := s.cfg.ComfyUI.TimeoutSeconds
	s.cfgMu.RUnlock()
	if secs <= 0 {
		secs = 600
	}
	return time.Duration(secs) * time.Second
}

// comfyConfiguredURL is the address the settings screen shows, which is also what
// a connectivity check reports on.
func (s *Server) comfyConfiguredURL() string {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg.ComfyUI.URL
}

// comfyConfiguredWorkflowID is the workflow the chat panel selected, falling back
// to the config default. An empty result means "let the model decide".
func (s *Server) comfyConfiguredWorkflowID(sessionSelection string) string {
	if strings.TrimSpace(sessionSelection) != "" {
		return sessionSelection
	}
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg.ComfyUI.Workflow
}

// maxComfyWorkflowBytes bounds an uploaded graph. A Flux or Hunyuan graph runs to a
// few hundred kilobytes; anything past this is a paste mistake or a hostile body.
const maxComfyWorkflowBytes = 4 << 20
