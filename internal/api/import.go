package api

import (
	"encoding/json"
	"net/http"

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
	ProfileName string `json:"profileName"`
	AccountName string `json:"accountName"`
	TenancyOCID string `json:"tenancyOcid"`
	UserOCID    string `json:"userOcid"`
	Fingerprint string `json:"fingerprint"`
	Region      string `json:"region"`
	PrivateKey  string `json:"privateKey"`
	SSHPublicKey string `json:"sshPublicKey"`
}

// importConfirmResult is per-entry result after import.
type importConfirmResult struct {
	ProfileName string `json:"profileName"`
	AccountName string `json:"accountName"`
	AccountID   string `json:"accountId,omitempty"`
	Error       string `json:"error,omitempty"`
}

// ImportPreviewPath handles POST /api/accounts/import/preview/path.
// Body: { "path": "/absolute/or/relative/path/to/config" }
// Returns a preview of all profiles in the config file.
func (h *Handler) ImportPreviewPath(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	profiles, err := configparser.ParseFile(body.Path)
	if err != nil {
		http.Error(w, "failed to parse config file: "+err.Error(), http.StatusUnprocessableEntity)
		return
	}

	entries := configparser.ToEntries(profiles, true)
	result := make([]importProfilePreview, 0, len(entries))
	for _, e := range entries {
		result = append(result, importProfilePreview{
			ProfileName:  e.ProfileName,
			TenancyOCID:  e.TenancyOCID,
			UserOCID:     e.UserOCID,
			Fingerprint:  e.Fingerprint,
			Region:       e.Region,
			KeyFilePath:  e.KeyFilePath,
			KeyFileError: e.KeyFileError,
			KeyFileOK:    e.KeyFileError == "" && e.PrivateKey != "",
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// ImportPreviewUpload handles POST /api/accounts/import/preview/upload.
// Body: { "content": "<raw config file text>", "keys": { "<key_file_path>": "<pem content>" } }
// Returns a preview of all profiles in the uploaded config content.
func (h *Handler) ImportPreviewUpload(w http.ResponseWriter, r *http.Request) {
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
		keyOK := false
		if e.KeyFilePath != "" {
			if pem, ok := body.Keys[e.KeyFilePath]; ok && pem != "" {
				keyOK = true
			} else {
				keyErr = "key file not provided: " + e.KeyFilePath
			}
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

// ImportConfirmPath handles POST /api/accounts/import/confirm/path.
// Reads the config file again, creates accounts for the selected profiles.
// Body: { "path": "...", "entries": [{ "profileName", "accountName", "sshPublicKey" }] }
func (h *Handler) ImportConfirmPath(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())

	var body struct {
		Path    string `json:"path"`
		Entries []struct {
			ProfileName  string `json:"profileName"`
			AccountName  string `json:"accountName"`
			SSHPublicKey string `json:"sshPublicKey"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" {
		http.Error(w, "path and entries are required", http.StatusBadRequest)
		return
	}

	profiles, err := configparser.ParseFile(body.Path)
	if err != nil {
		http.Error(w, "failed to parse config file: "+err.Error(), http.StatusUnprocessableEntity)
		return
	}
	entries := configparser.ToEntries(profiles, true)
	byProfile := map[string]configparser.ProfileEntry{}
	for _, e := range entries {
		byProfile[e.ProfileName] = e
	}

	results := make([]importConfirmResult, 0, len(body.Entries))
	for _, req := range body.Entries {
		e, ok := byProfile[req.ProfileName]
		if !ok {
			results = append(results, importConfirmResult{ProfileName: req.ProfileName, AccountName: req.AccountName, Error: "profile not found in config"})
			continue
		}
		if e.KeyFileError != "" {
			results = append(results, importConfirmResult{ProfileName: req.ProfileName, AccountName: req.AccountName, Error: e.KeyFileError})
			continue
		}
		name := req.AccountName
		if name == "" {
			name = req.ProfileName
		}
		account, err := h.Accounts.Create(user.ID, name, "", e.TenancyOCID, e.Region, e.UserOCID, e.Fingerprint, e.PrivateKey, req.SSHPublicKey)
		if err != nil {
			results = append(results, importConfirmResult{ProfileName: req.ProfileName, AccountName: name, Error: err.Error()})
			continue
		}
		results = append(results, importConfirmResult{ProfileName: req.ProfileName, AccountName: name, AccountID: account.ID})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

// ImportConfirmUpload handles POST /api/accounts/import/confirm/upload.
// Body: { "entries": [{ profileName, accountName, tenancyOcid, userOcid, fingerprint, region, privateKey, sshPublicKey }] }
func (h *Handler) ImportConfirmUpload(w http.ResponseWriter, r *http.Request) {
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
