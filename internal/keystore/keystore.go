// Package keystore manages loading and persisting the application encryption key.
//
// On startup, main.go calls Load(). If no key is found, a fresh one is generated
// and immediately persisted via Save(). The backend is selected by PULUMI_UI_KEY_STORE:
//
//	file   (default) — reads/writes $DATA_DIR/encryption.key
//	consul           — reads/writes a Consul KV path
//
// If PULUMI_UI_ENCRYPTION_KEY is set in the environment it always takes precedence
// and no store is consulted (useful for Nomad Variables injection without changing
// the key-store backend).
package keystore

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
)

// KeyStore is a simple load/save interface for the encryption key.
type KeyStore interface {
	// Load returns the stored hex key, or ("", nil) if nothing is stored yet.
	Load() (string, error)
	// Save persists a newly-generated hex key.
	Save(hexKey string) error
	// Description returns a human-readable description of where the key lives.
	Description() string
}

// Resolve returns a 64-hex-char encryption key using the following priority:
//
//  1. PULUMI_UI_ENCRYPTION_KEY env var (explicit override — used by Nomad)
//  2. Load from the configured KeyStore
//  3. Generate a new key, save it to the KeyStore, return it
func Resolve(store KeyStore) (string, error) {
	// Priority 1: explicit env var
	if v := os.Getenv("PULUMI_UI_ENCRYPTION_KEY"); v != "" {
		if err := validateHexKey(v); err != nil {
			return "", fmt.Errorf("PULUMI_UI_ENCRYPTION_KEY is invalid: %w", err)
		}
		return v, nil
	}

	// Priority 2: load from store
	key, err := store.Load()
	if err != nil {
		return "", fmt.Errorf("load encryption key from %s: %w", store.Description(), err)
	}
	if key != "" {
		return key, nil
	}

	// Priority 3: generate and persist
	key, err = generateKey()
	if err != nil {
		return "", fmt.Errorf("generate encryption key: %w", err)
	}
	if err := store.Save(key); err != nil {
		return "", fmt.Errorf("save encryption key to %s: %w", store.Description(), err)
	}
	return key, nil
}

func generateKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func validateHexKey(s string) error {
	b, err := hex.DecodeString(s)
	if err != nil {
		return fmt.Errorf("not valid hex: %w", err)
	}
	if len(b) != 32 {
		return fmt.Errorf("must be 64 hex chars (32 bytes), got %d bytes", len(b))
	}
	return nil
}
