// Package comfyui is a minimal HTTP client for the ComfyUI server API.
//
// It covers the subset the chat agent needs: queueing an API-format workflow,
// waiting for it to finish, and downloading whatever files it produced.
// Everything is stdlib so the manager stays a single dependency-free binary.
package comfyui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Node is one node of an API-format workflow: the "Save (API Format)" shape
// ComfyUI exports, keyed by a numeric id in the parent object.
type Node struct {
	ClassType string         `json:"class_type"`
	Inputs    map[string]any `json:"inputs"`
	Title     string         `json:"_meta_title,omitempty"`
}

// Workflow is an API-format graph: node id -> node.
type Workflow map[string]Node

// OutputFile is one file produced by a node. Type is usually "output" or
// "temp"; temporary files are intermediates and must never be shown.
type OutputFile struct {
	Filename  string `json:"filename"`
	Subfolder string `json:"subfolder"`
	Type      string `json:"type"`
}

// NodeOutput holds the files one node produced. ComfyUI uses "gifs" for every
// non-still-image output (mp4, webm, animated webp) as well as real gifs.
type NodeOutput struct {
	Images []OutputFile `json:"images"`
	Gifs   []OutputFile `json:"gifs"`
}

// Files returns every file the node produced, stills first.
func (o NodeOutput) Files() []OutputFile {
	out := make([]OutputFile, 0, len(o.Images)+len(o.Gifs))
	out = append(out, o.Images...)
	out = append(out, o.Gifs...)
	return out
}

// Status is the execution status ComfyUI reports for a prompt.
type Status struct {
	StatusStr string  `json:"status_str"`
	Completed bool    `json:"completed"`
	Messages  [][]any `json:"messages,omitempty"`
}

// HistoryEntry is one finished prompt as returned by /history/{prompt_id}.
type HistoryEntry struct {
	Prompt  []any                 `json:"prompt"`
	Outputs map[string]NodeOutput `json:"outputs"`
	Status  Status                `json:"status"`
}

// QueueResult is the /prompt response.
type QueueResult struct {
	PromptID   string         `json:"prompt_id"`
	Number     int            `json:"number"`
	NodeErrors map[string]any `json:"node_errors"`
	Error      string         `json:"error,omitempty"`
}

// Client talks to one ComfyUI server.
type Client struct {
	baseURL string
	hc      *http.Client
}

// ErrNotConfigured is returned when no base URL is configured.
var ErrNotConfigured = errors.New("comfyui: no server URL configured")

// New builds a client for baseURL. A timeout of 0 means no client-side
// timeout, which is what long-running requests (queueing a prompt) want; the
// caller bounds them with a context instead.
func New(baseURL string, timeout time.Duration) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	c := &Client{baseURL: baseURL}
	if timeout > 0 {
		c.hc = &http.Client{Timeout: timeout}
	} else {
		c.hc = &http.Client{}
	}
	return c
}

// BaseURL returns the configured server URL.
func (c *Client) BaseURL() string { return c.baseURL }

func (c *Client) ready() error {
	if c == nil || c.baseURL == "" {
		return ErrNotConfigured
	}
	return nil
}

// asSlice normalizes a JSON array, tolerating a null or a wrong type.
func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

// SystemStatsResult is the useful half of /system_stats, flattened out of its
// {"system": {...}, "devices": [...]} envelope so the settings screen does not
// have to know the nesting.
type SystemStatsResult struct {
	OS             string `json:"os"`
	PythonVersion  string `json:"python_version"`
	ComfyUIVersion string `json:"comfyui_version"`
	Devices        []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"devices"`
}

// Stats reports the server version and devices. It is also the cheapest
// reachability check.
//
// Decoding goes through map[string]any rather than a typed struct: /system_stats
// grows fields between ComfyUI releases and changes the type of others
// (embedded_python is a bool on some builds and a path on others). A struct
// would turn any such drift into "unreachable", which is the one thing a
// reachability probe must not report wrongly.
func (c *Client) Stats(ctx context.Context) (*SystemStatsResult, error) {
	if err := c.ready(); err != nil {
		return nil, err
	}
	var env map[string]any
	if err := c.getJSON(ctx, "/system_stats", &env); err != nil {
		return nil, err
	}
	out := &SystemStatsResult{}
	if sys, ok := env["system"].(map[string]any); ok {
		out.OS, _ = sys["os"].(string)
		out.PythonVersion, _ = sys["python_version"].(string)
		out.ComfyUIVersion, _ = sys["comfyui_version"].(string)
	}
	// Devices sit beside the system object on current builds and inside it on
	// older ones.
	raw := env["devices"]
	if sys, ok := env["system"].(map[string]any); ok && raw == nil {
		raw = sys["devices"]
	}
	for _, d := range asSlice(raw) {
		m, ok := d.(map[string]any)
		if !ok {
			continue
		}
		var dev struct {
			Name string `json:"name"`
			Type string `json:"type"`
		}
		dev.Name, _ = m["name"].(string)
		dev.Type, _ = m["type"].(string)
		out.Devices = append(out.Devices, dev)
	}
	if out.ComfyUIVersion == "" && out.PythonVersion == "" && out.OS == "" && len(out.Devices) == 0 {
		// A 200 with a body we recognise as nothing means something is answering
		// on that port, but it is not ComfyUI.
		return nil, fmt.Errorf("comfyui: /system_stats returned no recognizable fields")
	}
	return out, nil
}

// ObjectInfo returns the input schema of one node class. It is how the settings
// screen tells whether a workflow can run on the connected server (missing
// custom nodes show up here as a 404 for the class).
func (c *Client) ObjectInfo(ctx context.Context, classType string) (map[string]any, error) {
	if err := c.ready(); err != nil {
		return nil, err
	}
	var out map[string]any
	if err := c.getJSON(ctx, "/object_info/"+url.PathEscape(classType), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// AllObjectInfo returns every node class the server knows about.
func (c *Client) AllObjectInfo(ctx context.Context) (map[string]any, error) {
	if err := c.ready(); err != nil {
		return nil, err
	}
	var out map[string]any
	if err := c.getJSON(ctx, "/object_info", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// QueuePrompt validates a workflow and adds it to the execution queue. It does
// not wait: use WaitResult with the returned prompt id.
func (c *Client) QueuePrompt(ctx context.Context, wf Workflow, clientID string) (*QueueResult, error) {
	if err := c.ready(); err != nil {
		return nil, err
	}
	if len(wf) == 0 {
		return nil, errors.New("comfyui: workflow is empty")
	}
	if strings.TrimSpace(clientID) == "" {
		clientID = "ollama-manager"
	}
	body, err := json.Marshal(map[string]any{
		"prompt":     wf,
		"client_id":  clientID,
		"extra_data": map[string]any{"extra_pnginfo": map[string]any{"client": "ollama-manager"}},
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/prompt", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	var res QueueResult
	if err := c.do(req, &res); err != nil {
		return nil, err
	}
	if res.PromptID == "" {
		// ComfyUI answers 200 with an error body when validation fails.
		if msg := FormatNodeErrors(res.NodeErrors); msg != "" {
			return nil, fmt.Errorf("comfyui rejected the workflow: %s", msg)
		}
		if res.Error != "" {
			return nil, fmt.Errorf("comfyui rejected the workflow: %s", res.Error)
		}
		return nil, errors.New("comfyui: no prompt_id in response")
	}
	return &res, nil
}

// WaitResult polls /history/{prompt_id} until ComfyUI reports the prompt
// finished, the context is cancelled, or the timeout expires. A failed
// execution returns the node error text so the model sees something useful.
func (c *Client) WaitResult(ctx context.Context, promptID string, timeout time.Duration, onTick func()) (*HistoryEntry, error) {
	if err := c.ready(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(promptID) == "" {
		return nil, errors.New("comfyui: empty prompt id")
	}

	var ctxWithTimeout = ctx
	var cancel context.CancelFunc = func() {}
	if timeout > 0 {
		ctxWithTimeout, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	path := "/history/" + url.PathEscape(promptID)
	for {
		var raw map[string]HistoryEntry
		err := c.getJSON(ctxWithTimeout, path, &raw)
		if err != nil {
			// A cancelled parent context must surface as-is, otherwise the
			// caller cannot tell "user cancelled" from "server is down".
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if ctxWithTimeout.Err() != nil {
				return nil, fmt.Errorf("comfyui: timed out waiting for %s", promptID)
			}
			return nil, err
		}
		if entry, ok := raw[promptID]; ok {
			if msg := executionErrorText(entry.Status); msg != "" {
				return &entry, fmt.Errorf("comfyui: execution failed: %s", msg)
			}
			if entry.Status.Completed {
				return &entry, nil
			}
		}
		select {
		case <-ctxWithTimeout.Done():
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("comfyui: timed out after %s waiting for %s", timeout, promptID)
		case <-time.After(comfyPollInterval):
			if onTick != nil {
				onTick()
			}
		}
	}
}

// comfyPollInterval is how often WaitResult asks ComfyUI for an update.
const comfyPollInterval = 900 * time.Millisecond

// executionErrorText pulls a readable message out of a failed prompt's status.
// ComfyUI reports failures as
// [["execution_error", {"node_id": "9", "node_type": "KSampler",
//
//	"exception_message": "...", "exception_type": "..."}]]
//
// so the payload is walked defensively instead of unmarshalled into a struct.
func executionErrorText(st Status) string {
	for _, group := range st.Messages {
		if len(group) < 2 {
			continue
		}
		parts := group
		kind, _ := parts[0].(string)
		if kind != "execution_error" && kind != "execution_interrupted" {
			continue
		}
		detail, _ := parts[1].(map[string]any)
		msg, _ := detail["exception_message"].(string)
		nodeID := fmt.Sprint(detail["node_id"])
		nodeType, _ := detail["node_type"].(string)
		if msg == "" {
			msg = kind
		}
		switch {
		case nodeType != "" && nodeID != "" && nodeID != "<nil>":
			return fmt.Sprintf("%s at node %s (%s)", msg, nodeID, nodeType)
		case nodeType != "":
			return fmt.Sprintf("%s in %s", msg, nodeType)
		default:
			return msg
		}
	}
	if st.StatusStr == "error" {
		return "the server reported an execution error"
	}
	return ""
}

// Download fetches a produced file through /view. The returned content type is
// ComfyUI's when it sends one, otherwise it is sniffed from the bytes.
func (c *Client) Download(ctx context.Context, f OutputFile) (contentType string, data []byte, err error) {
	if err = c.ready(); err != nil {
		return "", nil, err
	}
	q := url.Values{}
	q.Set("filename", f.Filename)
	if f.Subfolder != "" {
		q.Set("subfolder", f.Subfolder)
	}
	typ := f.Type
	if typ == "" {
		typ = "output"
	}
	q.Set("type", typ)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/view?"+q.Encode(), nil)
	if err != nil {
		return "", nil, err
	}
	res, err := c.hc.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("comfyui: /view returned %s", res.Status)
	}
	// Videos can be large; 512 MB is a generous ceiling that still refuses to
	// buffer an unbounded response.
	data, err = io.ReadAll(io.LimitReader(res.Body, 512<<20))
	if err != nil {
		return "", nil, err
	}
	if ct := res.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "application/octet-stream") {
		contentType = ct
	}
	return contentType, data, nil
}

// Interrupt asks the server to stop whatever it is running. It is best-effort:
// the caller uses it on cancellation, where the error no longer matters.
func (c *Client) Interrupt(ctx context.Context) error {
	if err := c.ready(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/interrupt", strings.NewReader("{}"))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4<<10))
	return nil
}

// QueueState reports the pending and running queues, which is what the chat
// shows while a job is being submitted.
type QueueState struct {
	QueueRunning [][]any `json:"queue_running"`
	QueuePending [][]any `json:"queue_pending"`
}

// Queue returns the current queue depth so the UI can tell the user their job
// is waiting behind others.
func (c *Client) Queue(ctx context.Context) (*QueueState, error) {
	if err := c.ready(); err != nil {
		return nil, err
	}
	var out QueueState
	if err := c.getJSON(ctx, "/queue", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UploadImage uploads bytes to ComfyUI's input folder and returns the name the
// workflow should reference from a LoadImage node. It is the entry point for
// image-to-image workflows.
func (c *Client) UploadImage(ctx context.Context, filename string, contentType string, data []byte) (string, error) {
	if err := c.ready(); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	boundary := "----ollama-manager-comfyui"
	writeField := func(name, value string) {
		fmt.Fprintf(&buf, "--%s\r\nContent-Disposition: form-data; name=\"%s\"\r\n\r\n%s\r\n", boundary, name, value)
	}
	if filename == "" {
		filename = "input.png"
	}
	writeField("image", filename)
	if contentType != "" {
		writeField("type", contentType)
	}
	fmt.Fprintf(&buf, "--%s\r\nContent-Disposition: form-data; name=\"image\"; filename=\"%s\"\r\nContent-Type: %s\r\n\r\n",
		boundary, filename, orDefault(contentType, "application/octet-stream"))
	buf.Write(data)
	fmt.Fprintf(&buf, "\r\n--%s--\r\n", boundary)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/upload/image", &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	var out struct {
		Name      string `json:"name"`
		Subfolder string `json:"subfolder"`
		Type      string `json:"type"`
	}
	if err := c.do(req, &out); err != nil {
		return "", err
	}
	if out.Name == "" {
		out.Name = filename
	}
	return out.Name, nil
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	return c.do(req, out)
}

func (c *Client) do(req *http.Request, out any) error {
	res, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("comfyui: %w", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return err
	}
	if res.StatusCode == http.StatusNotFound {
		return fmt.Errorf("comfyui: %s not found on the server", req.URL.Path)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 400 {
			msg = msg[:400] + "…"
		}
		return fmt.Errorf("comfyui: %s returned %s: %s", req.URL.Path, res.Status, msg)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("comfyui: invalid JSON from %s: %w", req.URL.Path, err)
	}
	return nil
}

// FormatNodeErrors renders ComfyUI's node_errors object as a short readable
// message. The shape is
// {"12": {"errors": [{"type": "value_not_in_list", "message": "...", "details": "..."}], "class_type": "KSampler"}}
// so it is walked defensively rather than unmarshalled into a fixed struct.
func FormatNodeErrors(nodeErrors map[string]any) string {
	if len(nodeErrors) == 0 {
		return ""
	}
	var lines []string
	for _, id := range sortedKeys(nodeErrors) {
		entry, _ := nodeErrors[id].(map[string]any)
		class, _ := entry["class_type"].(string)
		parts := []string{fmt.Sprintf("node %s", id)}
		if class != "" {
			parts = append(parts, class)
		}
		line := strings.Join(parts, " (")
		if class != "" {
			line += ")"
		}
		if errs, ok := entry["errors"].([]any); ok {
			var msgs []string
			for _, e := range errs {
				m, _ := e.(map[string]any)
				msg, _ := m["message"].(string)
				kind, _ := m["type"].(string)
				switch {
				case msg != "" && kind != "":
					msgs = append(msgs, kind+": "+msg)
				case msg != "":
					msgs = append(msgs, msg)
				case kind != "":
					msgs = append(msgs, kind)
				}
			}
			if len(msgs) > 0 {
				line += ": " + strings.Join(msgs, "; ")
			}
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, " | ")
}

// sortedKeys returns the map keys in ascending order so error text is stable.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Small maps: insertion sort keeps this dependency-free and stable.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
