package api

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"
)

// ServeAgentBinary serves a pre-compiled agent binary for the given OS/arch.
// Looks for binaries in dist/agent_{os}_{arch} relative to the working directory.
func (h *Handler) ServeAgentBinary(w http.ResponseWriter, r *http.Request) {
	osName := chi.URLParam(r, "os")
	arch := chi.URLParam(r, "arch")

	if osName == "" {
		osName = "linux"
	}
	if arch == "" {
		arch = "arm64"
	}

	filename := "agent_" + osName + "_" + arch
	path := filepath.Join("dist", filename)

	if _, err := os.Stat(path); os.IsNotExist(err) {
		http.Error(w, "agent binary not found: "+filename, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)
	http.ServeFile(w, r, path)
}
