package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

// TestLiveOmlxEndpoint is a live integration test for testing OpenAI/oMLX endpoints.
// It skips automatically if the remote host is unreachable.
func TestLiveOmlxEndpoint(t *testing.T) {
	client := &http.Client{Timeout: 10 * time.Second}
	url := "http://mac-mini.local:8000/v1/chat/completions"
	apiKey := "omlx"
	model := "qwen38-27b-ablit"

	// Quick check if host is reachable
	checkReq, _ := http.NewRequestWithContext(context.Background(), "GET", "http://mac-mini.local:8000/v1/models", nil)
	checkReq.Header.Set("Authorization", "Bearer "+apiKey)
	if resp, err := client.Do(checkReq); err != nil || resp.StatusCode >= 400 {
		t.Skip("Live oMLX endpoint not reachable on mac-mini.local:8000, skipping live test")
		return
	}

	t.Log("--- Test 1: Basic Chat ---")
	reqBody1 := map[string]any{
		"model": model,
		"messages": []map[string]any{
			{"role": "user", "content": "How are you? Think step by step."},
		},
		"max_tokens": 100,
	}
	doPostLiveTest(t, client, url, apiKey, reqBody1)

	t.Log("--- Test 2: Vision with 1x1 image ---")
	reqBody2 := map[string]any{
		"model": model,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "text", "text": "What is in this image?"},
					{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAA="}},
				},
			},
		},
		"max_tokens": 50,
	}
	doPostLiveTest(t, client, url, apiKey, reqBody2)

	t.Log("--- Test 3: Streaming Basic Chat ---")
	reqBody3 := map[string]any{
		"model": model,
		"messages": []map[string]any{
			{"role": "user", "content": "Hi"},
		},
		"stream": true,
	}
	doPostLiveTest(t, client, url, apiKey, reqBody3)
}

func doPostLiveTest(t *testing.T, client *http.Client, url, apiKey string, payload any) {
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		t.Logf("Error: %v", err)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	t.Logf("Status: %d (%v)\nResponse: %s", resp.StatusCode, time.Since(start), string(body))
}
