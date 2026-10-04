package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
)

// chatSink is the destination for chat stream events. The agent loops in
// chat_artifact_agent.go and chat_web_agent.go write through a sink instead of
// straight to an http.ResponseWriter, which lets the very same generation code
// either stream to a live browser (quick chat) or feed a detached, persisted
// chat session that keeps running after the tab is closed.
type chatSink interface {
	// Send delivers one stream event. Implementations must be safe for
	// concurrent use and must never block for long.
	Send(event string, payload any)
}

// writeSSEHeaders applies the headers every chat stream response shares.
func writeSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
}

// sseSink forwards events to an in-flight HTTP response as Server-Sent Events.
// It reproduces the exact wire format the browser has always received.
type sseSink struct {
	mu      sync.Mutex
	w       http.ResponseWriter
	flusher http.Flusher
}

func newSSESink(w http.ResponseWriter, flusher http.Flusher) *sseSink {
	return &sseSink{w: w, flusher: flusher}
}

func (s *sseSink) Send(event string, payload any) {
	buf, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[chat] failed to encode %q event payload: %v", event, err)
		buf = []byte(`{"error":"failed to encode event payload"}`)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if event != "" {
		fmt.Fprintf(s.w, "event: %s\n", event)
	}
	fmt.Fprintf(s.w, "data: %s\n\n", buf)
	s.flusher.Flush()
}

// nopSink swallows every event. Useful for tests and for callers that only
// want the side effects of a run.
type nopSink struct{}

func (nopSink) Send(string, any) {}
