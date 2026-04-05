package api

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/trustos/pulumi-ui/internal/auth"
	"github.com/trustos/pulumi-ui/internal/oci/configparser"
)

// importProfilePreview is a single profile shown in the preview step.
type importProfilePreview struct {
	ProfileName  string `json:"profileName"`
	TenancyOCID  string `json:"tenancyOcid"`
	UserOCID     string `json:"userOcid"`
	Fingerprint  string `json:"fingerprint"`
	Region       string `json:"region"`
	KeyFilePath  string `json:"keyFilePath"`
	KeyFileError string `json:"keyFileError,omitempty"`
	KeyFileOK    bool   `json:"keyFileOk"`
}

// importConfirmEntry is a single entry submitted for the confirm step.
type importConfirmEntry struct {
	ProfileName  string `json:"profileName"`
	AccountName  string `json:"accountName"`
	TenancyOCID  string `json:"tenancyOcid"`
	UserOCID     string `json:"userOcid"`
	Fingerprint  string `json:"fingerprint"`
	Region       string `json:"region"`
	PrivateKey   string `json:"privateKey"`
	SSHPublicKey string `json:"sshPublicKey"`
}

// importConfirmResult is per-entry result after import.
type importConfirmResult struct {
	ProfileName string `json:"profileName"`
	AccountName string `json:"accountName"`
	AccountID   string `json:"accountId,omitempty"`
	Error       string `json:"error,omitempty"`
}

// ImportPreviewUpload handles POST /api/accounts/import/preview/upload.
// Body: { "content": "<raw config file text>", "keys": { "<key_file_path>": "<pem content>" } }
// The client is expected to pre-match key files by basename (path keys match the raw key_file
// values from the config, resolved client-side).
func (h *AdminHandler) ImportPreviewUpload(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content string            `json:"content"`
		Keys    map[string]string `json:"keys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Content == "" {
		http.Error(w, "content is required", http.StatusBadRequest)
		return
	}

	profiles, err := configparser.ParseContent(body.Content)
	if err != nil {
		http.Error(w, "failed to parse config: "+err.Error(), http.StatusUnprocessableEntity)
		return
	}

	entries := configparser.ToEntries(profiles, false)
	result := make([]importProfilePreview, 0, len(entries))
	for _, e := range entries {
		keyErr := ""
		keyOK := keysContain(body.Keys, e.KeyFilePath)
		if e.KeyFilePath != "" && !keyOK {
			keyErr = "key file not provided for: " + filepath.Base(e.KeyFilePath)
		}
		result = append(result, importProfilePreview{
			ProfileName:  e.ProfileName,
			TenancyOCID:  e.TenancyOCID,
			UserOCID:     e.UserOCID,
			Fingerprint:  e.Fingerprint,
			Region:       e.Region,
			KeyFilePath:  e.KeyFilePath,
			KeyFileError: keyErr,
			KeyFileOK:    keyOK,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// ImportConfirmUpload handles POST /api/accounts/import/confirm/upload.
// Body: { "entries": [{ profileName, accountName, tenancyOcid, userOcid, fingerprint, region, privateKey, sshPublicKey }] }
func (h *AdminHandler) ImportConfirmUpload(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())

	var body struct {
		Entries []importConfirmEntry `json:"entries"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	results := make([]importConfirmResult, 0, len(body.Entries))
	for _, e := range body.Entries {
		name := e.AccountName
		if name == "" {
			name = e.ProfileName
		}
		account, err := h.Accounts.Create(user.ID, name, "", e.TenancyOCID, e.Region, e.UserOCID, e.Fingerprint, e.PrivateKey, e.SSHPublicKey)
		if err != nil {
			results = append(results, importConfirmResult{ProfileName: e.ProfileName, AccountName: name, Error: err.Error()})
			continue
		}
		results = append(results, importConfirmResult{ProfileName: e.ProfileName, AccountName: name, AccountID: account.ID})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

// ImportPreviewZip handles POST /api/accounts/import/preview/zip.
// Body: { "zip": "<base64-encoded zip bytes>" }
// Accepts either the pulumi-ui export ZIP or any ZIP containing a "config" file + .pem files.
func (h *AdminHandler) ImportPreviewZip(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Zip string `json:"zip"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Zip == "" {
		http.Error(w, "zip is required", http.StatusBadRequest)
		return
	}

	profiles, pemFiles, err := parseZipContent(body.Zip)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}

	entries := configparser.ToEntries(profiles, false)
	result := make([]importProfilePreview, 0, len(entries))
	for _, e := range entries {
		base := filepath.Base(e.KeyFilePath)
		pem := pemFiles[base]
		keyErr := ""
		if pem == "" && e.KeyFilePath != "" {
			keyErr = "key file '" + base + "' not found in ZIP"
		}
		result = append(result, importProfilePreview{
			ProfileName:  e.ProfileName,
			TenancyOCID:  e.TenancyOCID,
			UserOCID:     e.UserOCID,
			Fingerprint:  e.Fingerprint,
			Region:       e.Region,
			KeyFilePath:  e.KeyFilePath,
			KeyFileError: keyErr,
			KeyFileOK:    pem != "",
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// ImportConfirmZip handles POST /api/accounts/import/confirm/zip.
// Body: { "zip": "<base64>", "entries": [{ profileName, accountName, sshPublicKey }] }
// Re-parses the ZIP to extract private keys, then creates accounts.
func (h *AdminHandler) ImportConfirmZip(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())

	var body struct {
		Zip     string `json:"zip"`
		Entries []struct {
			ProfileName  string `json:"profileName"`
			AccountName  string `json:"accountName"`
			SSHPublicKey string `json:"sshPublicKey"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Zip == "" {
		http.Error(w, "zip and entries are required", http.StatusBadRequest)
		return
	}

	profiles, pemFiles, err := parseZipContent(body.Zip)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}

	entries := configparser.ToEntries(profiles, false)
	byProfile := map[string]configparser.ProfileEntry{}
	for _, e := range entries {
		byProfile[e.ProfileName] = e
	}

	results := make([]importConfirmResult, 0, len(body.Entries))
	for _, req := range body.Entries {
		e, ok := byProfile[req.ProfileName]
		if !ok {
			results = append(results, importConfirmResult{ProfileName: req.ProfileName, AccountName: req.AccountName, Error: "profile not found"})
			continue
		}
		pem := pemFiles[filepath.Base(e.KeyFilePath)]
		if pem == "" {
			results = append(results, importConfirmResult{ProfileName: req.ProfileName, AccountName: req.AccountName, Error: "key file not found in ZIP"})
			continue
		}
		name := req.AccountName
		if name == "" {
			name = req.ProfileName
		}
		account, err := h.Accounts.Create(user.ID, name, "", e.TenancyOCID, e.Region, e.UserOCID, e.Fingerprint, pem, req.SSHPublicKey)
		if err != nil {
			results = append(results, importConfirmResult{ProfileName: req.ProfileName, AccountName: name, Error: err.Error()})
			continue
		}
		results = append(results, importConfirmResult{ProfileName: req.ProfileName, AccountName: name, AccountID: account.ID})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

// parseZipContent decodes a base64 ZIP, extracts the "config" file and all .pem files.
// Returns parsed profiles and a map of basename → PEM content.
func parseZipContent(b64 string) ([]configparser.Profile, map[string]string, error) {
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, nil, err
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, nil, err
	}

	var configContent string
	pemFiles := map[string]string{} // basename → content

	for _, f := range zr.File {
		name := filepath.Base(f.Name)
		rc, err := f.Open()
		if err != nil {
			continue
		}
		buf := new(bytes.Buffer)
		buf.ReadFrom(rc)
		rc.Close()

		if name == "config" {
			configContent = buf.String()
		} else if strings.HasSuffix(strings.ToLower(name), ".pem") {
			pemFiles[name] = buf.String()
		}
	}

	if configContent == "" {
		return nil, nil, nil
	}

	profiles, err := configparser.ParseContent(configContent)
	return profiles, pemFiles, err
}

// keysContain checks if the keys map has a non-empty value for keyPath,
// falling back to basename matching.
func keysContain(keys map[string]string, keyPath string) bool {
	if pem, ok := keys[keyPath]; ok && pem != "" {
		return true
	}
	base := filepath.Base(keyPath)
	for k, v := range keys {
		if filepath.Base(k) == base && v != "" {
			return true
		}
	}
	return false
}
