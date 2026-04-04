package api

import (
	"archive/zip"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// ExportSetup streams a zip archive containing the database and encryption key.
// This allows migrating the entire instance to a new server.
func (h *Handler) ExportSetup(w http.ResponseWriter, r *http.Request) {
	dbPath := filepath.Join(h.DataDir, "pulumi-ui.db")
	keyPath := h.KeyFilePath

	// Verify both files exist.
	for _, p := range []string{dbPath, keyPath} {
		if _, err := os.Stat(p); err != nil {
			http.Error(w, fmt.Sprintf("file not found: %s", filepath.Base(p)), http.StatusInternalServerError)
			return
		}
	}

	timestamp := time.Now().Format("2006-01-02")
	filename := fmt.Sprintf("pulumi-ui-export-%s.zip", timestamp)

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))

	zw := zip.NewWriter(w)
	defer zw.Close()

	for _, p := range []string{dbPath, keyPath} {
		data, err := os.ReadFile(p)
		if err != nil {
			// Headers already sent; can't return a clean error.
			return
		}
		f, err := zw.Create(filepath.Base(p))
		if err != nil {
			return
		}
		f.Write(data)
	}
}
