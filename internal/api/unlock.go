package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/config"
	"github.com/trustos/pulumi-ui/internal/auth"
	"github.com/trustos/pulumi-ui/internal/db"
)

type unlockRequest struct {
	PassphraseID    string `json:"passphraseId"`    // select mode: ID of saved passphrase
	PassphraseValue string `json:"passphraseValue"` // manual mode: raw passphrase string
	Blueprint       string `json:"blueprint"`        // project name (from RemoteStackSummary)
}

// UnlockResult contains the metadata extracted from a successfully decrypted
// remote stack. Returned by UnlockRemoteStack.
type UnlockResult struct {
	Valid              bool    `json:"valid"`
	ResourceCount      int     `json:"resourceCount"`
	TenancyOCID        string  `json:"tenancyOcid,omitempty"`
	LastUpdated        string  `json:"lastUpdated,omitempty"`
	SuggestedAccountID *string `json:"suggestedAccountId,omitempty"`
	PassphraseID       *string `json:"passphraseId,omitempty"`
	ConfigYAML         string  `json:"configYaml,omitempty"` // pulumi-ui stack config from S3
	HasMeshData        bool    `json:"hasMeshData"`          // true if Nebula mesh PKI is available in S3
}

// UnlockRemoteStack downloads a remote stack's state file from S3, decrypts it
// with the provided passphrase, and returns metadata including the tenancy OCID
// for account auto-matching.
func (h *PlatformHandler) UnlockRemoteStack(w http.ResponseWriter, r *http.Request) {
	stackName := chi.URLParam(r, "name")

	var req unlockRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.PassphraseID == "" && req.PassphraseValue == "" {
		http.Error(w, "provide either passphraseId or passphraseValue", http.StatusBadRequest)
		return
	}
	if req.Blueprint == "" {
		http.Error(w, "blueprint is required", http.StatusBadRequest)
		return
	}

	// Resolve the passphrase value.
	passphrase := req.PassphraseValue
	var passphraseID *string
	if req.PassphraseID != "" {
		val, err := h.Passphrases.GetValue(req.PassphraseID)
		if err != nil {
			http.Error(w, "passphrase not found", http.StatusBadRequest)
			return
		}
		passphrase = val
		passphraseID = &req.PassphraseID
	}

	// Download the state file from S3.
	stateJSON, err := h.downloadStateFile(r.Context(), req.Blueprint, stackName)
	if err != nil {
		http.Error(w, "failed to download state: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Parse the checkpoint to extract the salt and resources.
	salt, resources, err := parseCheckpoint(stateJSON)
	if err != nil {
		http.Error(w, "failed to parse state: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Create a decrypter from the passphrase + salt.
	decrypter, err := newDecrypterFromPassphrase(passphrase, salt)
	if err != nil {
		http.Error(w, "invalid passphrase or salt", http.StatusBadRequest)
		return
	}

	// Try to decrypt a secret to validate the passphrase.
	if !validatePassphrase(r.Context(), decrypter, resources) {
		http.Error(w, "wrong passphrase — could not decrypt stack secrets", http.StatusUnauthorized)
		return
	}

	// Extract tenancy OCID from the OCI provider resource.
	tenancyOCID := extractTenancyOCID(r.Context(), decrypter, resources)

	// Match tenancy against local accounts.
	var suggestedAccountID *string
	if tenancyOCID != "" {
		user := auth.UserFromContext(r.Context())
		if user != nil && h.Accounts != nil {
			if accounts, err := h.Accounts.ListForUser(user.ID); err == nil {
				for _, a := range accounts {
					if a.TenancyOCID == tenancyOCID {
						suggestedAccountID = &a.ID
						break
					}
				}
			}
		}
	}

	// Fetch pulumi-ui config YAML from S3 (synced during operations).
	configYAML := fetchConfigFromS3(r.Context(), h.Creds, req.Blueprint, stackName)

	// Check if Nebula mesh PKI data exists in S3 (lightweight HEAD request).
	hasMesh := meshExistsInS3(r.Context(), h.Creds, req.Blueprint, stackName)

	result := UnlockResult{
		Valid:              true,
		ResourceCount:      len(resources),
		TenancyOCID:        tenancyOCID,
		SuggestedAccountID: suggestedAccountID,
		PassphraseID:       passphraseID,
		ConfigYAML:         configYAML,
		HasMeshData:        hasMesh,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// downloadStateFile fetches the Pulumi state file from S3.
func (h *PlatformHandler) downloadStateFile(ctx context.Context, project, stack string) ([]byte, error) {
	backendType, _, _ := h.Creds.Get(db.KeyBackendType)
	if backendType != "s3" {
		return nil, fmt.Errorf("backend is not S3")
	}

	bucket, _, _ := h.Creds.Get(db.KeyS3Bucket)
	ns, _, _ := h.Creds.Get(db.KeyS3Namespace)
	region, _, _ := h.Creds.Get(db.KeyS3Region)
	accessKey, _, _ := h.Creds.Get(db.KeyS3AccessKeyID)
	secretKey, _, _ := h.Creds.Get(db.KeyS3SecretAccessKey)

	if bucket == "" || ns == "" || region == "" || accessKey == "" || secretKey == "" {
		return nil, fmt.Errorf("S3 credentials not fully configured")
	}

	endpoint := fmt.Sprintf("https://%s.compat.objectstorage.%s.oraclecloud.com", ns, region)
	key := fmt.Sprintf(".pulumi/stacks/%s/%s.json", project, stack)
	getURL := fmt.Sprintf("%s/%s/%s", endpoint, bucket, key)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
	if err != nil {
		return nil, err
	}
	signS3Request(req, accessKey, secretKey, region)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("S3 request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("S3 returned %d: %s", resp.StatusCode, string(body))
	}

	return io.ReadAll(resp.Body)
}

// checkpointResources holds the parsed salt and resource list from a Pulumi state file.
type checkpointResource struct {
	Type    string         `json:"type"`
	URN     string         `json:"urn"`
	Inputs  map[string]any `json:"inputs,omitempty"`
	Outputs map[string]any `json:"outputs,omitempty"`
}

// parseCheckpoint extracts the encryption salt and resource list from a Pulumi state JSON.
func parseCheckpoint(data []byte) (salt string, resources []checkpointResource, err error) {
	// Pulumi state format: { "version": N, "checkpoint": { ... } }
	var outer struct {
		Version    int             `json:"version"`
		Checkpoint json.RawMessage `json:"checkpoint"`
	}
	if err := json.Unmarshal(data, &outer); err != nil {
		return "", nil, fmt.Errorf("unmarshal outer: %w", err)
	}

	// Checkpoint v3: { "latest": { "secrets_providers": ..., "resources": [...] } }
	var cp struct {
		Latest *struct {
			SecretsProviders *struct {
				Type  string          `json:"type"`
				State json.RawMessage `json:"state,omitempty"`
			} `json:"secrets_providers,omitempty"`
			Resources []checkpointResource `json:"resources,omitempty"`
		} `json:"latest,omitempty"`
	}
	if err := json.Unmarshal(outer.Checkpoint, &cp); err != nil {
		return "", nil, fmt.Errorf("unmarshal checkpoint: %w", err)
	}

	if cp.Latest == nil {
		return "", nil, fmt.Errorf("no deployment found in state")
	}

	resources = cp.Latest.Resources

	// Extract salt from secrets_providers.
	if cp.Latest.SecretsProviders != nil && cp.Latest.SecretsProviders.Type == "passphrase" {
		var state struct {
			Salt string `json:"salt"`
		}
		if err := json.Unmarshal(cp.Latest.SecretsProviders.State, &state); err == nil {
			salt = state.Salt
		}
	}

	return salt, resources, nil
}

// newDecrypterFromPassphrase creates a Pulumi secret decrypter from a passphrase and salt string.
// Salt format: "v1:base64encodedSalt:checksum"
func newDecrypterFromPassphrase(passphrase, salt string) (config.Decrypter, error) {
	if salt == "" {
		return nil, fmt.Errorf("no encryption salt found in state")
	}

	// Salt format: "v1:<base64-salt>:<base64-checksum>"
	parts := strings.SplitN(salt, ":", 3)
	if len(parts) < 2 || parts[0] != "v1" {
		return nil, fmt.Errorf("unsupported salt format: %s", salt)
	}

	saltBytes, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode salt: %w", err)
	}

	return config.NewSymmetricCrypterFromPassphrase(passphrase, saltBytes), nil
}

// secretSig is the JSON key used by Pulumi to mark secret values.
const secretSig = "4dabf18193072939515e22adb298388d"

// validatePassphrase tries to decrypt any secret found in the resources.
// Returns true if decryption succeeds with the provided passphrase.
func validatePassphrase(ctx context.Context, dec config.Decrypter, resources []checkpointResource) bool {
	for _, r := range resources {
		if tryDecryptAny(ctx, dec, r.Inputs) {
			return true
		}
		if tryDecryptAny(ctx, dec, r.Outputs) {
			return true
		}
	}
	return false
}

// tryDecryptAny walks a property map looking for encrypted secrets and tries to decrypt one.
func tryDecryptAny(ctx context.Context, dec config.Decrypter, props map[string]any) bool {
	for _, v := range props {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		// Check for Pulumi secret signature.
		if _, hasSig := m[secretSig]; hasSig {
			if ct, ok := m["ciphertext"].(string); ok {
				if _, err := dec.DecryptValue(ctx, ct); err == nil {
					return true
				}
			}
		}
		// Recurse into nested objects.
		if tryDecryptAny(ctx, dec, m) {
			return true
		}
	}
	return false
}

// extractTenancyOCID finds the OCI provider resource and decrypts the tenancyOcid.
func extractTenancyOCID(ctx context.Context, dec config.Decrypter, resources []checkpointResource) string {
	for _, r := range resources {
		// OCI provider type: "pulumi:providers:oci"
		if r.Type != "pulumi:providers:oci" {
			continue
		}
		if tenancy := decryptField(ctx, dec, r.Inputs, "tenancyOcid"); tenancy != "" {
			return tenancy
		}
		if tenancy := decryptField(ctx, dec, r.Outputs, "tenancyOcid"); tenancy != "" {
			return tenancy
		}
	}
	return ""
}

// decryptField reads a field from a property map, decrypting it if it's a secret.
func decryptField(ctx context.Context, dec config.Decrypter, props map[string]any, key string) string {
	v, ok := props[key]
	if !ok {
		return ""
	}
	// Plain string value.
	if s, ok := v.(string); ok {
		return s
	}
	// Encrypted secret value.
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	if _, hasSig := m[secretSig]; hasSig {
		if ct, ok := m["ciphertext"].(string); ok {
			if plain, err := dec.DecryptValue(ctx, ct); err == nil {
				return plain
			}
		}
	}
	return ""
}

// signS3Request is defined in discover.go — reused here for S3 state file download.
// (No duplicate needed since both files are in the same package.)
