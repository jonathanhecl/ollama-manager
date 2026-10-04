package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// comfyStatusResponse is what the Settings page polls to tell the user whether the
// configured ComfyUI is actually reachable. A wrong URL is the single most common
// setup mistake, and without this the user only finds out when the model silently
// refuses to call the tool.
type comfyStatusResponse struct {
	URL            string   `json:"url"`
	Reachable      bool     `json:"reachable"`
	Error          string   `json:"error,omitempty"`
	QueueRunning   int      `json:"queue_running"`
	QueuePending   int      `json:"queue_pending"`
	ComfyUIVersion string   `json:"comfyui_version,omitempty"`
	PythonVersion  string   `json:"python_version,omitempty"`
	Devices        []string `json:"devices,omitempty"`
	Workflows      int      `json:"workflows"`
	Enabled        int      `json:"enabled"`
	MediaWritable  bool     `json:"media_writable"`
}

// handleComfyStatus reports reachability and queue depth.
// Path: GET /api/comfyui/status
func (s *Server) handleComfyStatus(w http.ResponseWriter, r *http.Request) {
	client, err := s.comfyClient()
	if err != nil {
		writeJSON(w, http.StatusOK, comfyStatusResponse{URL: s.comfyConfiguredURL(), Error: err.Error()})
		return
	}
	resp := comfyStatusResponse{
		URL:           s.comfyConfiguredURL(),
		Reachable:     true,
		MediaWritable: comfyMediaDirIsWritable(comfyMediaBase),
	}
	ctx := r.Context()
	if stats, err := client.Stats(ctx); err == nil {
		resp.ComfyUIVersion = stats.ComfyUIVersion
		resp.PythonVersion = stats.PythonVersion
		for _, d := range stats.Devices {
			resp.Devices = append(resp.Devices, d.Name)
		}
	} else {
		resp.Reachable = false
		resp.Error = err.Error()
	}
	if resp.Reachable {
		if q, err := client.Queue(ctx); err == nil {
			resp.QueueRunning = len(q.QueueRunning)
			resp.QueuePending = len(q.QueuePending)
		}
	}
	list := s.comfyWorkflows.List()
	resp.Workflows = len(list)
	for _, wf := range list {
		if wf.Enabled {
			resp.Enabled++
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleComfyWorkflows lists every registered workflow.
// Path: GET /api/comfyui/workflows
func (s *Server) handleComfyWorkflows(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"workflows": s.comfyWorkflows.List()})
}

// comfyWorkflowInput is the Settings page's create payload. The graph itself is
// accepted raw because that is what the user pasted from ComfyUI; the other
// fields are optional conveniences that keep the label the model sees useful.
type comfyWorkflowInput struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Enabled     *bool           `json:"enabled"`
	OutputKind  string          `json:"output_kind"`
	Bindings    []ComfyBinding  `json:"bindings"`
	Workflow    json.RawMessage `json:"workflow"`
	// Prompt is only used to prefill the stored default so a bare call is a
	// re-run rather than an empty render.
	Prompt string `json:"prompt"`
	// WorkflowID lets the browser upsert: editing an existing workflow posts the
	// same id instead of piling up duplicates.
	WorkflowID string `json:"workflow_id"`
}

// handleComfyWorkflowCreate registers a workflow from a pasted API-format graph.
// Path: POST /api/comfyui/workflows
func (s *Server) handleComfyWorkflowCreate(w http.ResponseWriter, r *http.Request) {
	var in comfyWorkflowInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxComfyWorkflowBytes)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}
	if len(in.Workflow) == 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("missing workflow graph"))
		return
	}
	graph, err := parseComfyWorkflow(in.Workflow)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = defaultComfyWorkflowName(graph)
	}

	id := sanitizeComfyWorkflowID(in.WorkflowID)
	if id == "" {
		id = newComfyWorkflowID()
	}
	// Re-registering an id overwrites it, but only when the caller owns the slot:
	// a paste for "new" must never silently replace something the user saved.
	existing, hadExisting := s.comfyWorkflows.Clone(id)
	wf := &ComfyWorkflow{
		ID:          id,
		Name:        name,
		Description: strings.TrimSpace(in.Description),
		Workflow:    graph,
		Bindings:    autoDetectComfyBindings(graph),
		Nodes:       describeComfyNodes(graph),
		OutputKind:  normalizeOutputKind(in.OutputKind),
		Enabled:     true,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	if hadExisting {
		wf.CreatedAt = existing.CreatedAt
		if in.Enabled != nil {
			wf.Enabled = *in.Enabled
		}
		if strings.TrimSpace(in.Description) == "" {
			wf.Description = existing.Description
		}
		if in.OutputKind == "" {
			wf.OutputKind = existing.OutputKind
		}
	}
	if err := applyComfyBindingOverrides(wf, in.Bindings); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if wf.OutputKind == "" {
		wf.OutputKind = guessOutputKind(graph)
	}
	if err := s.comfyWorkflows.Put(wf); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "workflow": wf})
}

// handleComfyWorkflowGet returns one workflow, graph included so the Settings page
// can show the nodes it found.
// Path: GET /api/comfyui/workflows/{id}
func (s *Server) handleComfyWorkflowGet(w http.ResponseWriter, r *http.Request) {
	wf, ok := s.comfyWorkflows.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("workflow not found"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflow": wf})
}

// comfyWorkflowPatch is the metadata-only edit. Replacing the graph goes through
// the create handler so the bindings are re-detected from scratch, which is the
// whole point of re-pasting.
type comfyWorkflowPatch struct {
	Name        *string         `json:"name"`
	Description *string         `json:"description"`
	Enabled     *bool           `json:"enabled"`
	OutputKind  *string         `json:"output_kind"`
	Bindings    *[]ComfyBinding `json:"bindings"`
	Detected    *bool           `json:"autodetect"`
}

// handleComfyWorkflowUpdate edits a workflow's metadata, bindings or enabled flag.
// Path: PATCH /api/comfyui/workflows/{id}
func (s *Server) handleComfyWorkflowUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	wf, ok := s.comfyWorkflows.Clone(id)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("workflow not found"))
		return
	}
	var in comfyWorkflowPatch
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxComfyWorkflowBytes)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}
	if in.Name != nil {
		if name := strings.TrimSpace(*in.Name); name != "" {
			wf.Name = name
		}
	}
	if in.Description != nil {
		wf.Description = strings.TrimSpace(*in.Description)
	}
	if in.Enabled != nil {
		wf.Enabled = *in.Enabled
	}
	if in.OutputKind != nil {
		if kind := normalizeOutputKind(*in.OutputKind); kind != "" {
			wf.OutputKind = kind
		}
	}
	switch {
	case in.Detected != nil && *in.Detected:
		wf.Bindings = autoDetectComfyBindings(wf.Workflow)
	case in.Bindings != nil:
		if err := applyComfyBindingOverrides(wf, *in.Bindings); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	wf.UpdatedAt = time.Now()
	if err := s.comfyWorkflows.Put(wf); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "workflow": wf})
}

// handleComfyWorkflowDelete removes a registered workflow. The images it produced
// stay: they belong to whichever chat asked for them.
// Path: DELETE /api/comfyui/workflows/{id}
func (s *Server) handleComfyWorkflowDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.comfyWorkflows.Delete(id) {
		writeError(w, http.StatusNotFound, fmt.Errorf("workflow not found"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

// comfyRunRequest is the Settings page's "try it" button. It goes through the
// same runner as the chat tool so the user sees exactly what the model would see.
type comfyRunRequest struct {
	Workflow string          `json:"workflow"`
	Params   json.RawMessage `json:"params"`
}

// handleComfyRun renders a workflow on demand, outside any chat turn.
// Path: POST /api/comfyui/run
func (s *Server) handleComfyRun(w http.ResponseWriter, r *http.Request) {
	var in comfyRunRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}
	wf, ok := s.comfyWorkflows.Resolve(in.Workflow)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("workflow not found: %s", in.Workflow))
		return
	}
	params := map[string]any{}
	if len(in.Params) > 0 {
		if err := json.Unmarshal(in.Params, &params); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Errorf("invalid params: %w", err))
			return
		}
	}
	res, err := s.runComfyTool(r.Context(), "", wf.ID, mustMarshalComfyArgs(params), nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"media":     res.Media,
		"prompt_id": res.PromptID,
		"workflow":  res.WorkflowName,
		"text":      res.Text,
	})
}

// handleComfyMedia serves a stored ComfyUI output. http.ServeFile handles Range
// requests, which is what lets <video> seek without downloading the whole file.
// Path: GET /api/comfyui/media/{file...}
func (s *Server) handleComfyMedia(w http.ResponseWriter, r *http.Request) {
	rel := r.PathValue("file")
	if rel == "" {
		http.NotFound(w, r)
		return
	}
	abs, ok := resolveComfyMediaFile(rel)
	if !ok {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	// Generated files are immutable once named, so a re-render never invalidates a
	// URL the chat already stored.
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeFile(w, r, abs)
}

// handleComfyDeleteMedia removes a single stored output, for the "remove this
// image" affordance in the chat.
// Path: DELETE /api/comfyui/media/{file...}
func (s *Server) handleComfyDeleteMedia(w http.ResponseWriter, r *http.Request) {
	rel := r.PathValue("file")
	abs, ok := resolveComfyMediaFile(rel)
	if !ok {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid media path"))
		return
	}
	if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "file": rel})
}

// mustMarshalComfyArgs turns a params map into the raw JSON the runner expects.
// The values came out of json.Unmarshal so they are always marshallable, and the
// fallback keeps the caller from having to handle an impossible error.
func mustMarshalComfyArgs(params map[string]any) json.RawMessage {
	buf, err := json.Marshal(params)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return buf
}

// comfyWorkflowSummary is the compact shape the Settings list renders. The graph
// is deliberately excluded: a workflow can be a few hundred nodes and the list
// only needs the label and the knobs.
type comfyWorkflowSummary struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Enabled     bool     `json:"enabled"`
	OutputKind  string   `json:"output_kind"`
	NodeCount   int      `json:"node_count"`
	Bindings    []string `json:"bindings"`
	UpdatedAt   string   `json:"updated_at"`
}

// comfyWorkflowsSummary backs the settings list endpoint.
// Path: GET /api/comfyui/workflows/summary
func (s *Server) handleComfyWorkflowsSummary(w http.ResponseWriter, r *http.Request) {
	list := s.comfyWorkflows.List()
	out := make([]comfyWorkflowSummary, 0, len(list))
	for _, wf := range list {
		sum := comfyWorkflowSummary{
			ID:          wf.ID,
			Name:        wf.Name,
			Description: wf.Description,
			Enabled:     wf.Enabled,
			OutputKind:  wf.OutputKind,
			NodeCount:   len(wf.Nodes),
			UpdatedAt:   wf.UpdatedAt.UTC().Format(time.RFC3339),
		}
		for _, b := range wf.Bindings {
			sum.Bindings = append(sum.Bindings, b.Param)
		}
		out = append(out, sum)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"workflows": out,
		// The chat panel needs to know whether the integration is usable at all
		// before it offers the toggle, and that needs the URL, not just the list.
		"configured": strings.TrimSpace(s.comfyConfiguredURL()) != "",
		"url":        s.comfyConfiguredURL(),
	})
}

// comfyDetectRequest asks the server what it would bind for a pasted graph,
// without registering anything. The detection rules live server-side, so the
// Settings page has to ask rather than duplicate them in JavaScript.
type comfyDetectRequest struct {
	Workflow json.RawMessage `json:"workflow"`
}

// handleComfyDetect previews the bindings and node list for a pasted graph.
// Path: POST /api/comfyui/workflows/detect
func (s *Server) handleComfyDetect(w http.ResponseWriter, r *http.Request) {
	var in comfyDetectRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxComfyWorkflowBytes)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}
	graph, err := parseComfyWorkflow(in.Workflow)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"bindings":    autoDetectComfyBindings(graph),
		"nodes":       describeComfyNodes(graph),
		"output_kind": guessOutputKind(graph),
	})
}

// handleComfyInterrupt stops whatever ComfyUI is rendering right now. It exists so
// a runaway render can be killed from the Settings page without hunting for the
// ComfyUI window.
// Path: POST /api/comfyui/interrupt
func (s *Server) handleComfyInterrupt(w http.ResponseWriter, r *http.Request) {
	client, err := s.comfyClient()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := client.Interrupt(r.Context()); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleComfyObjectInfo exposes ComfyUI's node catalogue so the Settings page can
// offer a node picker when a binding has to be pointed at a node by hand.
// Path: GET /api/comfyui/object-info
func (s *Server) handleComfyObjectInfo(w http.ResponseWriter, r *http.Request) {
	client, err := s.comfyClient()
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	info, err := client.AllObjectInfo(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	// Only the class names are needed to label a dropdown; the full spec per node
	// is tens of kilobytes and would dominate the response.
	names := make([]string, 0, len(info))
	for name := range info {
		names = append(names, name)
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodes": names, "count": len(names)})
}

// comfyMediaDirIsWritable is a startup sanity check surfaced through /status: a
// ComfyUI that renders fine but whose outputs cannot be stored is worse than a
// broken one, because the failure shows up after the wait.
func comfyMediaDirIsWritable(dir string) bool {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	probe := filepath.Join(dir, ".probe")
	return os.WriteFile(probe, []byte("ok"), 0o600) == nil && os.Remove(probe) == nil
}
