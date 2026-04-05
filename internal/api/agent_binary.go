package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// ServeAgentBinary serves a pre-compiled agent binary for the given OS/arch.
// Binaries are embedded in the server at compile time from cmd/server/dist/.
func (h *PlatformHandler) ServeAgentBinary(w http.ResponseWriter, r *http.Request) {
	osName := chi.URLParam(r, "os")
	arch := chi.URLParam(r, "arch")

	if osName == "" {
		osName = "linux"
	}
	if arch == "" {
		arch = "arm64"
	}

	filename := "agent_" + osName + "_" + arch
	embedPath := "dist/" + filename

	data, err := h.AgentBinaries.ReadFile(embedPath)
	if err != nil {
		http.Error(w, "agent binary not found: "+filename, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)
	w.Write(data)
}
