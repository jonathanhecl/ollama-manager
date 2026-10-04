package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gense/ollama-manager/internal/comfyui"
)

// comfyWorkflowDir is the default registry location. New() overrides it with a
// path next to config.json.
const comfyWorkflowDir = "comfyui/workflows"

// comfyParamKind is the JSON type a bound input is exposed with. It is derived
// from the value already in the workflow, so a KSampler seed becomes an
// integer while its cfg becomes a number.
const (
	comfyKindString = "string"
	comfyKindInt    = "integer"
	comfyKindNumber = "number"
	comfyKindBool   = "boolean"
	comfyKindEnum   = "enum"
	comfyKindImage  = "image"
)

// ComfyBinding maps one public parameter name onto a node input. The model only
// ever sees Param; NodeID/Input are what the server patches. Bindings are the
// only writable surface, so a workflow can never be steered outside them.
type ComfyBinding struct {
	Param     string   `json:"param"`
	Label     string   `json:"label,omitempty"`
	NodeID    string   `json:"node_id"`
	ClassType string   `json:"class_type,omitempty"`
	Input     string   `json:"input"`
	Kind      string   `json:"kind"`
	Enum      []string `json:"enum,omitempty"`
	Title     string   `json:"title,omitempty"`
}

// ComfyWorkflow is one registered workflow: the API-format graph plus the
// metadata the model needs to decide when to use it.
type ComfyWorkflow struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Enabled     bool             `json:"enabled"`
	Workflow    comfyui.Workflow `json:"workflow"`
	Bindings    []ComfyBinding   `json:"bindings"`
	Nodes       []ComfyNodeRef   `json:"nodes,omitempty"`
	CreatedAt   time.Time        `json:"created_at,omitempty"`
	UpdatedAt   time.Time        `json:"updated_at,omitempty"`
	// Outputs documents what the workflow is expected to produce, so the model
	// can tell an image workflow from a video or audio one.
	OutputKind string `json:"output_kind,omitempty"` // image | video | audio | any
}

// ComfyNodeRef describes one node for the settings UI.
type ComfyNodeRef struct {
	ID        string `json:"id"`
	ClassType string `json:"class_type"`
	Title     string `json:"title,omitempty"`
}

// comfyWorkflowStore keeps every registered workflow in one file each. Workflows
// are far too big for config.json, which is rewritten in full on every PATCH.
type comfyWorkflowStore struct {
	mu    sync.RWMutex
	dir   string
	items map[string]*ComfyWorkflow
	order []string
}

func newComfyWorkflowStore(dir string) *comfyWorkflowStore {
	if dir == "" {
		dir = comfyWorkflowDir
	}
	return &comfyWorkflowStore{dir: dir, items: map[string]*ComfyWorkflow{}}
}

func (st *comfyWorkflowStore) Load() {
	st.mu.Lock()
	defer st.mu.Unlock()
	entries, err := os.ReadDir(st.dir)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[comfyui] read %s: %v", st.dir, err)
		}
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(st.dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			log.Printf("[comfyui] read %s: %v", path, err)
			continue
		}
		var wf ComfyWorkflow
		if err := json.Unmarshal(raw, &wf); err != nil || wf.ID == "" {
			log.Printf("[comfyui] skipping invalid workflow %s", path)
			continue
		}
		wf.Nodes = describeComfyNodes(wf.Workflow)
		st.items[wf.ID] = &wf
		st.order = append(st.order, wf.ID)
	}
	log.Printf("[comfyui] loaded %d workflow(s) from %s", len(st.order), st.dir)
}

func (st *comfyWorkflowStore) List() []*ComfyWorkflow {
	st.mu.RLock()
	defer st.mu.RUnlock()
	out := make([]*ComfyWorkflow, 0, len(st.order))
	for _, id := range st.order {
		if wf := st.items[id]; wf != nil {
			out = append(out, wf)
		}
	}
	return out
}

// ListEnabled returns only the workflows the chat agent may use.
func (st *comfyWorkflowStore) ListEnabled() []*ComfyWorkflow {
	out := []*ComfyWorkflow{}
	for _, wf := range st.List() {
		if wf.Enabled {
			out = append(out, wf)
		}
	}
	return out
}

func (st *comfyWorkflowStore) Get(id string) (*ComfyWorkflow, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	wf, ok := st.items[id]
	return wf, ok
}

// Clone returns a deep copy of a workflow. Handlers that edit metadata must work
// on a copy: Get hands out the live pointer, so mutating it in place would race
// with a concurrent List serialization and leave the store changed even if the
// write to disk then failed.
func (st *comfyWorkflowStore) Clone(id string) (*ComfyWorkflow, bool) {
	st.mu.RLock()
	wf, ok := st.items[id]
	st.mu.RUnlock()
	if !ok || wf == nil {
		return nil, false
	}
	buf, err := json.Marshal(wf)
	if err != nil {
		return nil, false
	}
	var out ComfyWorkflow
	if err := json.Unmarshal(buf, &out); err != nil {
		return nil, false
	}
	return &out, true
}

// Resolve finds a workflow by id first, then by exact name, then by a
// case-insensitive match on either. The model is allowed to pass a name, since
// that reads better in a tool call than an opaque id.
func (st *comfyWorkflowStore) Resolve(ref string) (*ComfyWorkflow, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, false
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	if wf, ok := st.items[ref]; ok {
		return wf, true
	}
	lower := strings.ToLower(ref)
	for _, id := range st.order {
		if wf := st.items[id]; wf != nil && strings.ToLower(wf.Name) == lower {
			return wf, true
		}
	}
	for _, id := range st.order {
		if wf := st.items[id]; wf != nil && strings.ToLower(id) == lower {
			return wf, true
		}
	}
	return nil, false
}

// Put creates or replaces a workflow and persists it.
func (st *comfyWorkflowStore) Put(wf *ComfyWorkflow) error {
	if strings.TrimSpace(wf.ID) == "" {
		wf.ID = newComfyWorkflowID()
	}
	wf.UpdatedAt = time.Now()
	if wf.CreatedAt.IsZero() {
		wf.CreatedAt = wf.UpdatedAt
	}
	wf.Nodes = describeComfyNodes(wf.Workflow)

	if err := os.MkdirAll(st.dir, 0o700); err != nil {
		return err
	}
	if err := writeJSONFileAtomic(filepath.Join(st.dir, wf.ID+".json"), wf, 0o600); err != nil {
		return err
	}

	st.mu.Lock()
	if _, existed := st.items[wf.ID]; !existed {
		st.order = append(st.order, wf.ID)
	}
	st.items[wf.ID] = wf
	st.mu.Unlock()
	return nil
}

func (st *comfyWorkflowStore) Delete(id string) bool {
	st.mu.Lock()
	wf, ok := st.items[id]
	if ok {
		delete(st.items, id)
		for i, existing := range st.order {
			if existing == id {
				st.order = append(st.order[:i], st.order[i+1:]...)
				break
			}
		}
	}
	st.mu.Unlock()
	if !ok {
		return false
	}
	_ = os.Remove(filepath.Join(st.dir, wf.ID+".json"))
	return true
}

// sanitizeComfyWorkflowID keeps a browser-supplied id from escaping the registry
// directory. It accepts the ids this server generates and nothing else, so a
// crafted id cannot make Put write outside comfyui/workflows.
func sanitizeComfyWorkflowID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	// Allow the timestamp form plus an optional short suffix, so a rename in the
	// UI can keep a stable id without letting the client choose its shape.
	if len(raw) > 64 {
		return ""
	}
	for _, r := range raw {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_'
		if !ok {
			return ""
		}
	}
	return raw
}

// ---------- output kind ----------

// normalizeOutputKind accepts only the kinds the rest of the code branches on.
// Anything else becomes empty, which means "let the detector decide".
func normalizeOutputKind(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case comfyMediaImage, comfyMediaVideo, comfyMediaAudio:
		return strings.ToLower(strings.TrimSpace(raw))
	case "any", "unknown", "":
		if strings.TrimSpace(raw) == "" {
			return ""
		}
		return "any"
	}
	return ""
}

// guessOutputKind reads the output nodes of a graph so a workflow pasted without
// metadata still tells the model whether it produces a picture or a clip. SaveImage
// and its video siblings differ only in class name, so the node classes are the
// signal; anything unrecognised stays "image", the overwhelmingly common case.
func guessOutputKind(wf comfyui.Workflow) string {
	kinds := map[string]bool{}
	for _, node := range wf {
		switch {
		case strings.Contains(node.ClassType, "Video"):
			kinds[comfyMediaVideo] = true
		case strings.Contains(node.ClassType, "Audio"):
			kinds[comfyMediaAudio] = true
		case strings.Contains(node.ClassType, "Gif"), strings.Contains(node.ClassType, "WebP"), strings.Contains(node.ClassType, "Animated"):
			kinds[comfyMediaVideo] = true
		// The video saver nodes are named after the container they write, so
		// SaveWEBM and SaveMP4 say nothing about being videos in their own name.
		case strings.Contains(node.ClassType, "WEBM"), strings.Contains(node.ClassType, "MP4"),
			strings.Contains(node.ClassType, "AVI"), strings.Contains(node.ClassType, "MKV"):
			kinds[comfyMediaVideo] = true
		case node.ClassType == "SaveImage", node.ClassType == "PreviewImage",
			strings.Contains(node.ClassType, "SaveImage"), strings.Contains(node.ClassType, "ImageSave"):
			kinds[comfyMediaImage] = true
		}
	}
	// A graph that mixes savers is a frame-plus-clip pipeline; report the exotic
	// one so the model does not promise a video the chat cannot play.
	if kinds[comfyMediaVideo] {
		return comfyMediaVideo
	}
	if kinds[comfyMediaAudio] {
		return comfyMediaAudio
	}
	if kinds[comfyMediaImage] {
		return comfyMediaImage
	}
	return comfyMediaImage
}

// applyComfyBindingOverrides replaces the detected bindings with the ones the
// user pinned by hand, which is the escape hatch for a workflow whose node layout
// the detection rules cannot guess. Every override has to point at a real input of
// a real node, otherwise a typo would silently do nothing at render time.
func applyComfyBindingOverrides(wf *ComfyWorkflow, overrides []ComfyBinding) error {
	if len(overrides) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]ComfyBinding, 0, len(overrides))
	for _, b := range overrides {
		param := strings.TrimSpace(b.Param)
		if param == "" {
			return fmt.Errorf("a binding is missing its parameter name")
		}
		if !comfyParamNameOK(param) {
			return fmt.Errorf("invalid parameter name %q: use letters, digits and underscores", param)
		}
		if seen[param] {
			return fmt.Errorf("duplicate binding for parameter %q", param)
		}
		seen[param] = true
		node, ok := wf.Workflow[b.NodeID]
		if !ok {
			return fmt.Errorf("binding %q points at node %q, which is not in this workflow", param, b.NodeID)
		}
		if _, ok := node.Inputs[b.Input]; !ok {
			return fmt.Errorf("node %q (%s) has no input %q", b.NodeID, node.ClassType, b.Input)
		}
		// Fill in whatever the caller left blank from the graph itself, so a
		// partial override still yields a complete binding.
		if b.ClassType == "" {
			b.ClassType = node.ClassType
		}
		if b.Title == "" {
			b.Title = node.Title
		}
		if b.Kind == "" {
			b.Kind = comfyParamKind(node.ClassType, b.Input, node.Inputs[b.Input])
		}
		if b.Label == "" {
			b.Label = defaultComfyBindingLabel(b.Param)
		}
		out = append(out, b)
	}
	sortComfyBindings(out)
	wf.Bindings = out
	return nil
}

// comfyParamNameOK keeps the public parameter names to something a model can be
// relied on to type correctly and that can be a JSON schema property.
func comfyParamNameOK(param string) bool {
	if param == "" || len(param) > 48 {
		return false
	}
	for i, r := range param {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// defaultComfyWorkflowName invents a label for a graph the user pasted without
// one. The model reads this name to decide when to reach for the workflow, so it
// is better than "wf-1758…" but it is clearly a fallback.
func defaultComfyWorkflowName(wf comfyui.Workflow) string {
	best := ""
	bestClass := 0
	for _, node := range wf {
		if n := len(node.ClassType); n > bestClass {
			bestClass, best = n, node.ClassType
		}
	}
	if best == "" {
		return "Workflow"
	}
	words := strings.FieldsFunc(best, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	if len(words) == 0 {
		return "Workflow"
	}
	// "KSamplerAdvanced" reads better split, so "KSampler Advanced".
	return strings.Join(words, " ")
}

// defaultComfyBindingLabel is the human half of a parameter name in the settings
// list, for the overrides the user did not label themselves.
func defaultComfyBindingLabel(param string) string {
	words := strings.FieldsFunc(param, func(r rune) bool { return r == '_' || r == '-' })
	if len(words) == 0 {
		return param
	}
	out := make([]string, 0, len(words))
	for _, w := range words {
		out = append(out, strings.ToUpper(w[:1])+w[1:])
	}
	return strings.Join(out, " ")
}

func newComfyWorkflowID() string {
	return "wf-" + strconv.FormatInt(time.Now().UnixMilli(), 10)
}

// ---------- workflow parsing ----------

// comfyUIMarkerKeys are top-level keys only ever present in the visual editor
// export. Their presence means the user pasted the wrong format.
var comfyUIMarkerKeys = []string{"nodes", "links", "last_node_id", "extra", "groups", "version"}

// parseComfyWorkflow turns raw uploaded JSON into a workflow. It rejects the
// visual editor format with a message that says exactly what to do instead.
func parseComfyWorkflow(raw []byte) (comfyui.Workflow, error) {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return nil, errors.New("the workflow is empty")
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	// The API format is sometimes exported wrapped in an envelope.
	for _, wrapper := range []string{"prompt", "workflow", "graph"} {
		if inner, ok := probe[wrapper]; ok && isJSONObject(inner) {
			return parseComfyWorkflow(inner)
		}
	}
	for _, key := range comfyUIMarkerKeys {
		if _, ok := probe[key]; ok {
			return nil, fmt.Errorf("that is the visual editor format. In ComfyUI choose \"Workflow > Export (API)\" " +
				"(enable Developer mode under Settings > Comfy first) and import the file it produces")
		}
	}

	var wf comfyui.Workflow
	if err := json.Unmarshal(raw, &wf); err != nil {
		return nil, fmt.Errorf("invalid API workflow: %w", err)
	}
	if len(wf) == 0 {
		return nil, errors.New("the workflow has no nodes")
	}
	for id, node := range wf {
		if node.ClassType == "" {
			return nil, fmt.Errorf("node %q has no class_type, so this is not an API-format workflow", id)
		}
		if node.Inputs == nil {
			node.Inputs = map[string]any{}
			wf[id] = node
		}
	}
	return wf, nil
}

func isJSONObject(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return strings.HasPrefix(trimmed, "{")
}

// describeComfyNodes builds the node list the settings UI shows, sorted by id.
func describeComfyNodes(wf comfyui.Workflow) []ComfyNodeRef {
	out := make([]ComfyNodeRef, 0, len(wf))
	for id, node := range wf {
		out = append(out, ComfyNodeRef{ID: id, ClassType: node.ClassType, Title: node.Title})
	}
	sort.Slice(out, func(i, j int) bool {
		ni, erri := strconv.Atoi(out[i].ID)
		nj, errj := strconv.Atoi(out[j].ID)
		if erri == nil && errj == nil {
			return ni < nj
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// ---------- parameter binding ----------

// comfyAutodetectRules maps (class_type, input) onto the public parameter name.
// Order matters: the first rule that matches a node wins for that node, except
// for CLIPTextEncode, which is handled separately so its two text inputs do not
// collide.
type comfyAutodetectRule struct {
	ClassTypes []string
	Inputs     []string
	Param      string
	Label      string
}

var comfyAutodetectRules = []comfyAutodetectRule{
	{[]string{"KSampler", "KSamplerAdvanced", "SamplerCustom", "SamplerCustomAdvanced", "FaceDetailer", "UltimateSDUpscale"},
		[]string{"seed", "noise_seed"}, "seed", "Seed"},
	{[]string{"KSampler", "KSamplerAdvanced", "SamplerCustom", "SamplerCustomAdvanced", "FaceDetailer", "UltimateSDUpscale"},
		[]string{"steps"}, "steps", "Steps"},
	{[]string{"KSampler", "KSamplerAdvanced", "SamplerCustom", "SamplerCustomAdvanced", "FaceDetailer"},
		[]string{"cfg", "guidance"}, "cfg", "CFG scale"},
	{[]string{"KSampler", "KSamplerAdvanced", "SamplerCustom", "SamplerCustomAdvanced", "UltimateSDUpscale"},
		[]string{"denoise"}, "denoise", "Denoise"},
	{[]string{"EmptyLatentImage", "EmptySD3LatentImage", "EmptyLatentImagePresets", "EmptyMochiLatentVideo",
		"EmptyHunyuanLatentVideo", "EmptyCosmosLatentVideo"},
		[]string{"width"}, "width", "Width"},
	{[]string{"EmptyLatentImage", "EmptySD3LatentImage", "EmptyLatentImagePresets", "EmptyMochiLatentVideo",
		"EmptyHunyuanLatentVideo", "EmptyCosmosLatentVideo"},
		[]string{"height"}, "height", "Height"},
	{[]string{"LoadImage", "LoadImageMask", "LoadImageOutput", "VHS_LoadVideo", "VHS_LoadVideoPath"},
		[]string{"image", "video"}, "image", "Source image"},
	{[]string{"LoraLoader", "LoraLoaderModelOnly"},
		[]string{"lora_name"}, "lora", "LoRA"},
	{[]string{"CLIPTextEncodeSDXLRefiner"}, []string{"text"}, "prompt", "Prompt"},
}

// autoDetectComfyBindings proposes the editable inputs of a workflow. The input
// kind comes from the value already sitting in the graph, so a KSampler seed is
// exposed as an integer and its cfg as a number without a hardcoded table.
func autoDetectComfyBindings(wf comfyui.Workflow) []ComfyBinding {
	var out []ComfyBinding
	used := map[string]string{} // param -> node id already taken

	claim := func(b ComfyBinding) {
		if _, taken := used[b.Param]; taken {
			return
		}
		used[b.Param] = b.NodeID
		out = append(out, b)
	}

	// CLIPTextEncode is first: telling the positive from the negative text input
	// cannot be expressed as a plain (class, input) rule.
	for _, b := range detectComfyPromptBindings(wf) {
		claim(b)
	}

	for _, ref := range describeComfyNodes(wf) {
		node := wf[ref.ID]
		for _, rule := range comfyAutodetectRules {
			if !containsFold(rule.ClassTypes, ref.ClassType) {
				continue
			}
			for _, input := range rule.Inputs {
				val, ok := node.Inputs[input]
				if !ok {
					continue
				}
				claim(ComfyBinding{Param: rule.Param, Label: rule.Label, NodeID: ref.ID,
					ClassType: ref.ClassType, Input: input, Kind: comfyParamKind(ref.ClassType, input, val),
					Title: ref.Title})
			}
		}
	}

	sortComfyBindings(out)
	return out
}

// sortComfyBindings puts prompt first, negative prompt second and everything else
// alphabetically. The order decides the layout of the generated tool schema, so it
// has to match what the model reads most often.
func sortComfyBindings(bindings []ComfyBinding) {
	sort.SliceStable(bindings, func(i, j int) bool {
		rank := func(p string) int {
			switch p {
			case "prompt":
				return 0
			case "negative_prompt":
				return 1
			default:
				return 2
			}
		}
		if rank(bindings[i].Param) != rank(bindings[j].Param) {
			return rank(bindings[i].Param) < rank(bindings[j].Param)
		}
		return bindings[i].Param < bindings[j].Param
	})
}

// comfyTextEncoder is one CLIPTextEncode candidate with whatever role signals
// its text or title carried.
type comfyTextEncoder struct {
	ref ComfyNodeRef
	neg bool
	pos bool
}

// detectComfyPromptBindings maps the text encoders of a workflow onto `prompt`
// and `negative_prompt`.
//
// The wording of the text is a weak signal: "blurry, low quality, watermark" is
// unmistakably a negative prompt to a human and contains neither the word
// "negative" nor any node title that does. Node order is the strong one —
// every stock SD/SDXL graph encodes the positive first and the negative second,
// because the KSampler takes them as `positive` then `negative`. So explicit
// labels win when present, and otherwise the first encoder is the prompt.
func detectComfyPromptBindings(wf comfyui.Workflow) []ComfyBinding {
	var encs []comfyTextEncoder
	for _, ref := range describeComfyNodes(wf) {
		if ref.ClassType != "CLIPTextEncode" && ref.ClassType != "CLIPTextEncodeRuntime" {
			continue
		}
		text, _ := wf[ref.ID].Inputs["text"].(string)
		if strings.TrimSpace(text) == "" {
			continue
		}
		e := comfyTextEncoder{ref: ref}
		e.neg = looksNegative(text) || looksNegative(ref.Title)
		e.pos = looksPositive(text) || looksPositive(ref.Title)
		encs = append(encs, e)
	}
	if len(encs) == 0 {
		return nil
	}

	// An explicit label is decisive on its own; it is only a tie-breaker between
	// two unlabelled nodes that order gets consulted.
	anyLabelled := false
	for i := range encs {
		if encs[i].neg || encs[i].pos {
			anyLabelled = true
		}
	}

	var promptAt, negativeAt = -1, -1
	if anyLabelled {
		for i := range encs {
			if promptAt < 0 && encs[i].pos {
				promptAt = i
			}
			if negativeAt < 0 && encs[i].neg {
				negativeAt = i
			}
		}
		// Only one side got a label, so the other is whatever it has to be.
		if promptAt < 0 {
			promptAt = firstUnlabelled(encs, negativeAt)
		}
		if negativeAt < 0 {
			negativeAt = firstUnlabelled(encs, promptAt)
		}
	} else {
		promptAt = 0
		// A third encoder is probably a refiner caption, and guessing at it would
		// expose the wrong input, so only the stock pair is paired up.
		if len(encs) == 2 {
			negativeAt = 1
		}
	}
	var out []ComfyBinding
	if promptAt >= 0 {
		out = append(out, ComfyBinding{Param: "prompt", Label: "Prompt", NodeID: encs[promptAt].ref.ID,
			ClassType: encs[promptAt].ref.ClassType, Input: "text", Kind: comfyKindString, Title: encs[promptAt].ref.Title})
	}
	if negativeAt >= 0 && negativeAt != promptAt {
		out = append(out, ComfyBinding{Param: "negative_prompt", Label: "Negative prompt", NodeID: encs[negativeAt].ref.ID,
			ClassType: encs[negativeAt].ref.ClassType, Input: "text", Kind: comfyKindString, Title: encs[negativeAt].ref.Title})
	}
	return out
}

// firstUnlabelled returns the first encoder carrying no explicit label, skipping
// the one at skip.
func firstUnlabelled(encs []comfyTextEncoder, skip int) int {
	for i := range encs {
		if i == skip || encs[i].neg || encs[i].pos {
			continue
		}
		return i
	}
	return -1
}

func looksNegative(s string) bool {
	l := strings.ToLower(strings.TrimSpace(s))
	return strings.Contains(l, "negative") || strings.Contains(l, "negativo")
}

func looksPositive(s string) bool {
	l := strings.ToLower(strings.TrimSpace(s))
	return strings.Contains(l, "positive") || strings.Contains(l, "positivo")
}

func containsFold(list []string, want string) bool {
	for _, item := range list {
		if strings.EqualFold(item, want) {
			return true
		}
	}
	return false
}

// comfyParamKind derives the exposed JSON type of a bound input from the value
// currently in the graph.
func comfyParamKind(classType, input string, val any) string {
	switch v := val.(type) {
	case bool:
		return comfyKindBool
	case float64:
		if v == float64(int64(v)) && !looksFractional(input) {
			return comfyKindInt
		}
		return comfyKindNumber
	case int, int64:
		return comfyKindInt
	case []any:
		// A list input whose current value is a list of strings behaves as a
		// fixed choice (checkpoints, LoRAs, samplers).
		if len(v) > 0 {
			if _, ok := v[0].(string); ok {
				return comfyKindEnum
			}
		}
		return comfyKindString
	case string:
		if classType == "LoadImage" || input == "image" {
			return comfyKindImage
		}
		return comfyKindString
	default:
		return comfyKindString
	}
}

func looksFractional(input string) bool {
	switch input {
	case "cfg", "denoise", "guidance", "strength", "noise", "eta", "scheduler", "start_at_step", "end_at_step":
		return true
	}
	return false
}

// comfyBindingSchemaType maps a binding kind onto its JSON schema type.
func comfyBindingSchemaType(kind string) string {
	switch kind {
	case comfyKindInt:
		return "integer"
	case comfyKindNumber:
		return "number"
	case comfyKindBool:
		return "boolean"
	default:
		return "string"
	}
}

// ---------- patching ----------

// patchComfyWorkflow applies params to a copy of the workflow. Only declared
// bindings are writable; an unknown parameter is an error rather than something
// silently dropped, because the model would otherwise think it took effect.
func patchComfyWorkflow(wf comfyui.Workflow, bindings []ComfyBinding, params map[string]any) (comfyui.Workflow, error) {
	byParam := make(map[string]ComfyBinding, len(bindings))
	for _, b := range bindings {
		byParam[b.Param] = b
	}
	unknown := []string{}
	for name := range params {
		if _, ok := byParam[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("unknown parameter(s): %s. Allowed: %s",
			strings.Join(unknown, ", "), strings.Join(comfyBindingNames(bindings), ", "))
	}

	out := make(comfyui.Workflow, len(wf))
	for id, node := range wf {
		clone := comfyui.Node{ClassType: node.ClassType, Title: node.Title, Inputs: make(map[string]any, len(node.Inputs))}
		for k, v := range node.Inputs {
			clone.Inputs[k] = v
		}
		out[id] = clone
	}

	for _, name := range sortedComfyParamNames(params) {
		b := byParam[name]
		node, ok := out[b.NodeID]
		if !ok {
			return nil, fmt.Errorf("parameter %q points at node %s, which is not in the workflow", name, b.NodeID)
		}
		coerced, err := coerceComfyParam(params[name], b)
		if err != nil {
			return nil, fmt.Errorf("parameter %q: %w", name, err)
		}
		node.Inputs[b.Input] = coerced
		out[b.NodeID] = node
	}
	return out, nil
}

func sortedComfyParamNames(params map[string]any) []string {
	names := make([]string, 0, len(params))
	for n := range params {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func comfyBindingNames(bindings []ComfyBinding) []string {
	out := make([]string, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, b.Param)
	}
	sort.Strings(out)
	return out
}

// coerceComfyParam converts a model-supplied JSON value into the type ComfyUI
// expects for that input. Models routinely send "512" for a number and 768.0
// for a seed, and rejecting those would just cause a retry loop.
func coerceComfyParam(val any, b ComfyBinding) (any, error) {
	switch b.Kind {
	case comfyKindInt:
		switch v := val.(type) {
		case int:
			return int64(v), nil
		case int64:
			return v, nil
		case float64:
			return int64(v), nil
		case json.Number:
			n, err := v.Int64()
			if err != nil {
				return nil, fmt.Errorf("expected a whole number, got %s", v)
			}
			return n, nil
		case string:
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				return nil, fmt.Errorf("expected a whole number, got %q", v)
			}
			return n, nil
		case bool:
			return nil, errors.New("expected a whole number, got a boolean")
		default:
			return nil, fmt.Errorf("expected a whole number, got %T", val)
		}
	case comfyKindNumber:
		switch v := val.(type) {
		case float64:
			return v, nil
		case float32:
			return float64(v), nil
		case int:
			return float64(v), nil
		case int64:
			return float64(v), nil
		case json.Number:
			f, err := v.Float64()
			if err != nil {
				return nil, fmt.Errorf("expected a number, got %s", v)
			}
			return f, nil
		case string:
			f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil {
				return nil, fmt.Errorf("expected a number, got %q", v)
			}
			return f, nil
		case bool:
			return nil, errors.New("expected a number, got a boolean")
		default:
			return nil, fmt.Errorf("expected a number, got %T", val)
		}
	case comfyKindBool:
		switch v := val.(type) {
		case bool:
			return v, nil
		case string:
			b, err := strconv.ParseBool(strings.TrimSpace(v))
			if err != nil {
				return nil, fmt.Errorf("expected true or false, got %q", v)
			}
			return b, nil
		default:
			return nil, fmt.Errorf("expected true or false, got %T", val)
		}
	case comfyKindEnum:
		s, _ := val.(string)
		if len(b.Enum) > 0 && !containsFold(b.Enum, s) {
			return nil, fmt.Errorf("%q is not one of: %s", s, strings.Join(b.Enum, ", "))
		}
		return s, nil
	case comfyKindImage:
		s, _ := val.(string)
		if strings.TrimSpace(s) == "" {
			return "", fmt.Errorf("expected an uploaded image name, got an empty string")
		}
		return s, nil
	default:
		s, _ := val.(string)
		return s, nil
	}
}
