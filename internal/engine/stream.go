package engine

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type SSEEvent struct {
	Type      string `json:"type"`      // "output" | "error" | "done"
	Data      string `json:"data"`
	Timestamp string `json:"timestamp"` // RFC3339
}

type SSESender func(event SSEEvent)

// SSEResponseWriter sets SSE headers and returns a sender function.
// Returns false if the ResponseWriter doesn't support streaming.
func SSEResponseWriter(w http.ResponseWriter) (SSESender, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return nil, false
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable Nginx/Traefik buffering

	send := func(event SSEEvent) {
		if event.Timestamp == "" {
			event.Timestamp = time.Now().Format(time.RFC3339)
		}
		data, _ := json.Marshal(event)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	return send, true
}
