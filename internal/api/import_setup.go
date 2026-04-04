package api

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/trustos/pulumi-ui/internal/crypto"
	"github.com/trustos/pulumi-ui/internal/db"
)

// ImportSetup accepts a multipart upload of a database file and encryption key,
// validates them, replaces the current (empty) database and key file, then
// triggers a graceful server restart. Only allowed on a fresh instance (no users).
func (h *Handler) ImportSetup(w http.ResponseWriter, r *http.Request) {
	// Guard: only allowed when no users exist.
	n, err := h.Users.Count()
	if err != nil {
		http.Error(w, "failed to check users: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if n > 0 {
		http.Error(w, "import is only allowed on a fresh instance with no users", http.StatusForbidden)
		return
	}

	// Parse multipart form — 100 MB limit.
	if err := r.ParseMultipartForm(100 << 20); err != nil {
		http.Error(w, "failed to parse upload: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Read key file.
	keyFile, _, err := r.FormFile("key")
	if err != nil {
		http.Error(w, "encryption key file is required", http.StatusBadRequest)
		return
	}
	defer keyFile.Close()
	keyBytes, err := io.ReadAll(keyFile)
	if err != nil {
		http.Error(w, "failed to read key file: "+err.Error(), http.StatusBadRequest)
		return
	}
	hexKey := strings.TrimSpace(string(keyBytes))

	// Validate key format: must be 64 hex chars (32 bytes).
	keyRaw, err := hex.DecodeString(hexKey)
	if err != nil {
		http.Error(w, "encryption key is not valid hex", http.StatusBadRequest)
		return
	}
	if len(keyRaw) != 32 {
		http.Error(w, fmt.Sprintf("encryption key must be 64 hex chars (32 bytes), got %d bytes", len(keyRaw)), http.StatusBadRequest)
		return
	}

	// Read database file to a temp location.
	dbFile, _, err := r.FormFile("database")
	if err != nil {
		http.Error(w, "database file is required", http.StatusBadRequest)
		return
	}
	defer dbFile.Close()

	tmpPath := filepath.Join(h.DataDir, "pulumi-ui.db.import")
	tmp, err := os.Create(tmpPath)
	if err != nil {
		http.Error(w, "failed to create temp file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := io.Copy(tmp, dbFile); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		http.Error(w, "failed to write database file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	tmp.Close()

	// Validate: open the uploaded DB, run migrations, check it has users.
	importDB, err := db.Open(tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		http.Error(w, "uploaded file is not a valid SQLite database", http.StatusBadRequest)
		return
	}

	if err := db.Migrate(importDB); err != nil {
		importDB.Close()
		os.Remove(tmpPath)
		http.Error(w, "database migration failed: "+err.Error(), http.StatusBadRequest)
		return
	}

	var userCount int
	if err := importDB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&userCount); err != nil {
		importDB.Close()
		os.Remove(tmpPath)
		http.Error(w, "failed to query users in uploaded database: "+err.Error(), http.StatusBadRequest)
		return
	}
	if userCount == 0 {
		importDB.Close()
		os.Remove(tmpPath)
		http.Error(w, "uploaded database has no users — nothing to import", http.StatusBadRequest)
		return
	}

	// Validate: encryption key can decrypt data in the uploaded DB.
	enc, err := crypto.NewEncryptor(hexKey)
	if err != nil {
		importDB.Close()
		os.Remove(tmpPath)
		http.Error(w, "invalid encryption key: "+err.Error(), http.StatusBadRequest)
		return
	}

	if ok, reason := validateKeyMatchesDB(importDB, enc); !ok {
		importDB.Close()
		os.Remove(tmpPath)
		http.Error(w, reason, http.StatusBadRequest)
		return
	}

	importDB.Close()

	// Close the current live DB.
	h.DB.Close()

	// Atomic swap: rename temp → real, clean up WAL/SHM from old DB.
	dbPath := filepath.Join(h.DataDir, "pulumi-ui.db")
	if err := os.Rename(tmpPath, dbPath); err != nil {
		http.Error(w, "failed to replace database: "+err.Error(), http.StatusInternalServerError)
		return
	}
	os.Remove(dbPath + "-wal")
	os.Remove(dbPath + "-shm")

	// Write the new encryption key.
	if err := os.WriteFile(h.KeyFilePath, []byte(hexKey+"\n"), 0600); err != nil {
		http.Error(w, "failed to write encryption key: "+err.Error(), http.StatusInternalServerError)
		return
	}

	log.Printf("[import] setup imported successfully (%d user(s)), triggering restart", userCount)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":      true,
		"message": "Setup imported successfully. Server is restarting.",
	})

	// Flush response before triggering restart.
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	// Signal graceful restart.
	select {
	case h.RestartCh <- struct{}{}:
	default:
	}
}

// validateKeyMatchesDB tries to decrypt at least one encrypted field in the DB
// to verify the key is correct. Returns (true, "") if valid or no encrypted data
// exists to test against. Returns (false, reason) if decryption fails.
func validateKeyMatchesDB(database *sql.DB, enc *crypto.Encryptor) (bool, string) {
	// Try oci_accounts first (has multiple encrypted fields).
	var ciphertext []byte
	err := database.QueryRow(`SELECT private_key FROM oci_accounts LIMIT 1`).Scan(&ciphertext)
	if err == nil && len(ciphertext) > 0 {
		if _, err := enc.Decrypt(ciphertext); err != nil {
			return false, "encryption key does not match the database (failed to decrypt account data)"
		}
		return true, ""
	}

	// Try passphrases.
	err = database.QueryRow(`SELECT value FROM passphrases LIMIT 1`).Scan(&ciphertext)
	if err == nil && len(ciphertext) > 0 {
		if _, err := enc.Decrypt(ciphertext); err != nil {
			return false, "encryption key does not match the database (failed to decrypt passphrase data)"
		}
		return true, ""
	}

	// No encrypted data to validate against — accept on faith.
	return true, ""
}
