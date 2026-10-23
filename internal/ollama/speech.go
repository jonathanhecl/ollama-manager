package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// SpeechRequest is the payload of the OpenAI-compatible speech endpoint.
// Format is "wav" (default) or "pcm".
type SpeechRequest struct {
	Model  string
	Input  string
	Voice  string
	Format string
}

// Speech synthesizes Input into audio with a "speech" capability model and
// returns the audio bytes plus the response Content-Type. Unlike the native
// API this endpoint lives under /v1/audio/speech and answers with raw audio
// instead of JSON.
func (c *Client) Speech(ctx context.Context, req SpeechRequest) ([]byte, string, error) {
	voice := strings.TrimSpace(req.Voice)
	if voice == "" {
		voice = "default"
	}
	format := strings.TrimSpace(req.Format)
	if format == "" {
		format = "wav"
	}
	body, _ := json.Marshal(map[string]any{
		"model":           req.Model,
		"input":           req.Input,
		"voice":           voice,
		"response_format": format,
	})
	resp, err := c.do(ctx, http.MethodPost, "/v1/audio/speech", bytes.NewReader(body), "application/json")
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return nil, "", err
	}
	audio, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read speech audio: %w", err)
	}
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "audio/wav"
	}
	return audio, contentType, nil
}
