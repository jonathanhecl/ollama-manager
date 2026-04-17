// Package ollama is a thin client for the local Ollama HTTP API.
//
// Only the endpoints needed by ollama-manager are implemented:
//   - GET  /api/tags
//   - GET  /api/ps
//   - POST /api/show
//   - POST /api/pull   (NDJSON stream)
//   - DELETE /api/delete
package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is a minimal Ollama HTTP client.
type Client struct {
	baseURL string
	http    *http.Client
}

// New returns a Client targeting baseURL (e.g. "http://localhost:11434").
func New(baseURL string) *Client {
	baseURL = strings.TrimRight(baseURL, "/")
	return &Client{
		baseURL: baseURL,
		http: &http.Client{
			Timeout: 0, // streaming endpoints need no global timeout
		},
	}
}

// ModelDetails is the nested detail block returned by /api/tags and /api/show.
type ModelDetails struct {
	ParentModel       string   `json:"parent_model"`
	Format            string   `json:"format"`
	Family            string   `json:"family"`
	Families          []string `json:"families"`
	ParameterSize     string   `json:"parameter_size"`
	QuantizationLevel string   `json:"quantization_level"`
}

// Model is one entry from /api/tags.
type Model struct {
	Name       string       `json:"name"`
	Model      string       `json:"model"`
	ModifiedAt time.Time    `json:"modified_at"`
	Size       int64        `json:"size"`
	Digest     string       `json:"digest"`
	Details    ModelDetails `json:"details"`
}

// RunningModel is one entry from /api/ps.
type RunningModel struct {
	Name      string       `json:"name"`
	Model     string       `json:"model"`
	Size      int64        `json:"size"`
	SizeVRAM  int64        `json:"size_vram"`
	Digest    string       `json:"digest"`
	Details   ModelDetails `json:"details"`
	ExpiresAt time.Time    `json:"expires_at"`
}

// ShowResponse is the trimmed result of POST /api/show. The model_info map is
// kept as raw JSON values because keys are namespaced by family
// (e.g. qwen3.context_length, gemma3.context_length).
type ShowResponse struct {
	License      string                     `json:"license"`
	Modelfile    string                     `json:"modelfile"`
	Parameters   string                     `json:"parameters"`
	Template     string                     `json:"template"`
	Details      ModelDetails               `json:"details"`
	ModelInfo    map[string]json.RawMessage `json:"model_info"`
	Capabilities []string                   `json:"capabilities"`
	ModifiedAt   time.Time                  `json:"modified_at"`
}

// PullProgress is one streamed event from POST /api/pull.
type PullProgress struct {
	Status    string `json:"status"`
	Digest    string `json:"digest,omitempty"`
	Total     int64  `json:"total,omitempty"`
	Completed int64  `json:"completed,omitempty"`
	Error     string `json:"error,omitempty"`
}

// List calls GET /api/tags.
func (c *Client) List(ctx context.Context) ([]Model, error) {
	var out struct {
		Models []Model `json:"models"`
	}
	if err := c.getJSON(ctx, "/api/tags", &out); err != nil {
		return nil, err
	}
	return out.Models, nil
}

// PS calls GET /api/ps.
func (c *Client) PS(ctx context.Context) ([]RunningModel, error) {
	var out struct {
		Models []RunningModel `json:"models"`
	}
	if err := c.getJSON(ctx, "/api/ps", &out); err != nil {
		return nil, err
	}
	return out.Models, nil
}

// Show calls POST /api/show.
func (c *Client) Show(ctx context.Context, name string) (*ShowResponse, error) {
	body, _ := json.Marshal(map[string]any{"name": name})
	resp, err := c.do(ctx, http.MethodPost, "/api/show", bytes.NewReader(body), "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return nil, err
	}
	var out ShowResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode show response: %w", err)
	}
	return &out, nil
}

// Delete calls DELETE /api/delete.
func (c *Client) Delete(ctx context.Context, name string) error {
	body, _ := json.Marshal(map[string]any{"name": name})
	resp, err := c.do(ctx, http.MethodDelete, "/api/delete", bytes.NewReader(body), "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkStatus(resp)
}

// Pull starts POST /api/pull and invokes onEvent for every NDJSON progress
// event until the stream completes, the context is cancelled, or onEvent
// returns an error. The final event from Ollama is typically {status:"success"}.
func (c *Client) Pull(ctx context.Context, name string, onEvent func(PullProgress) error) error {
	body, _ := json.Marshal(map[string]any{
		"name":   name,
		"stream": true,
	})
	resp, err := c.do(ctx, http.MethodPost, "/api/pull", bytes.NewReader(body), "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return err
	}

	// Use a Scanner with a generous buffer because some events can be wide.
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev PullProgress
		if err := json.Unmarshal(line, &ev); err != nil {
			return fmt.Errorf("decode pull event: %w (line=%q)", err, string(line))
		}
		if ev.Error != "" {
			return fmt.Errorf("ollama: %s", ev.Error)
		}
		if err := onEvent(ev); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		// Context cancellation surfaces as a wrapped error from the body reader.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("read pull stream: %w", err)
	}
	return nil
}

// Ping checks whether the Ollama server responds. Useful at startup.
func (c *Client) Ping(ctx context.Context) error {
	resp, err := c.do(ctx, http.MethodGet, "/api/tags", nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkStatus(resp)
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	resp, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return err
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return c.http.Do(req)
}

func checkStatus(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	msg := strings.TrimSpace(string(data))
	if msg == "" {
		msg = resp.Status
	}
	return fmt.Errorf("ollama %s: %s", resp.Status, msg)
}
