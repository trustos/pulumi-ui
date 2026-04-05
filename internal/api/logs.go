package api

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// GetLogs returns the buffered application log entries as JSON.
func (h *PlatformHandler) GetLogs(w http.ResponseWriter, r *http.Request) {
	if h.LogBuffer == nil {
		http.Error(w, "logging not configured", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(h.LogBuffer.Entries())
}

// StreamLogs opens an SSE connection that streams new log entries in real time.
func (h *PlatformHandler) StreamLogs(w http.ResponseWriter, r *http.Request) {
	if h.LogBuffer == nil {
		http.Error(w, "logging not configured", http.StatusServiceUnavailable)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	id, ch := h.LogBuffer.Subscribe()
	defer h.LogBuffer.Unsubscribe(id)

	// Send existing entries as the initial batch.
	for _, entry := range h.LogBuffer.Entries() {
		data, _ := json.Marshal(entry)
		fmt.Fprintf(w, "data: %s\n\n", data)
	}
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case entry, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(entry)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
