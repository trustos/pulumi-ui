package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/trustos/pulumi-ui/internal/db"
	"github.com/trustos/pulumi-ui/internal/engine"
)

type AppSettings struct {
	BackendType string `json:"backendType"` // "local" or "s3"
	StateDir    string `json:"stateDir"`
	S3Bucket    string `json:"s3Bucket,omitempty"`
	S3Namespace string `json:"s3Namespace,omitempty"`
	S3Region    string `json:"s3Region,omitempty"`
	S3HasKeys   bool   `json:"s3HasKeys"`
}

func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	backendType, _, _ := h.Creds.Get(db.KeyBackendType)
	if backendType == "" {
		backendType = "local"
	}

	bucket, _, _ := h.Creds.Get(db.KeyS3Bucket)
	ns, _, _ := h.Creds.Get(db.KeyS3Namespace)
	region, _, _ := h.Creds.Get(db.KeyS3Region)
	ak, akSet, _ := h.Creds.Get(db.KeyS3AccessKeyID)
	sk, skSet, _ := h.Creds.Get(db.KeyS3SecretAccessKey)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(AppSettings{
		BackendType: backendType,
		StateDir:    os.Getenv("PULUMI_UI_STATE_DIR"),
		S3Bucket:    bucket,
		S3Namespace: ns,
		S3Region:    region,
		S3HasKeys:   akSet && ak != "" && skSet && sk != "",
	})
}

func (h *Handler) PutSettings(w http.ResponseWriter, r *http.Request) {
	var body AppSettings
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if body.BackendType != "local" && body.BackendType != "s3" {
		http.Error(w, "backendType must be \"local\" or \"s3\"", http.StatusBadRequest)
		return
	}

	// When switching to S3, validate that all required credentials are set.
	if body.BackendType == "s3" {
		for _, key := range []string{db.KeyS3Bucket, db.KeyS3Namespace, db.KeyS3Region, db.KeyS3AccessKeyID, db.KeyS3SecretAccessKey} {
			v, ok, _ := h.Creds.Get(key)
			if !ok || v == "" {
				http.Error(w, fmt.Sprintf("cannot switch to S3 backend: %s is not configured", key), http.StatusBadRequest)
				return
			}
		}
	}

	h.Creds.Set(db.KeyBackendType, body.BackendType)
	w.WriteHeader(http.StatusOK)
}

// TestS3Connection validates connectivity to the OCI Object Storage S3-compatible
// endpoint by sending a HEAD bucket request with AWS SigV4 authentication.
func (h *Handler) TestS3Connection(w http.ResponseWriter, r *http.Request) {
	bucket, _, _ := h.Creds.Get(db.KeyS3Bucket)
	ns, _, _ := h.Creds.Get(db.KeyS3Namespace)
	region, _, _ := h.Creds.Get(db.KeyS3Region)
	accessKey, _, _ := h.Creds.Get(db.KeyS3AccessKeyID)
	secretKey, _, _ := h.Creds.Get(db.KeyS3SecretAccessKey)

	if bucket == "" || ns == "" || region == "" || accessKey == "" || secretKey == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "S3 credentials are not fully configured"})
		return
	}

	endpoint := fmt.Sprintf("https://%s.compat.objectstorage.%s.oraclecloud.com", ns, region)
	// Path-style: endpoint/<bucket>
	url := fmt.Sprintf("%s/%s", endpoint, bucket)

	req, err := http.NewRequestWithContext(r.Context(), http.MethodHead, url, nil)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}

	// Sign the request with AWS Signature V4 for OCI S3-compatible API.
	signS3Request(req, accessKey, secretKey, region)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": fmt.Sprintf("connection failed: %v", err)})
		return
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "endpoint": endpoint})
	case resp.StatusCode == http.StatusNotFound:
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": fmt.Sprintf("bucket %q not found at %s", bucket, endpoint)})
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized:
		// Read error detail from OCI response if available.
		detail := ""
		if body, err := io.ReadAll(io.LimitReader(resp.Body, 1024)); err == nil && len(body) > 0 {
			detail = " — " + string(body)
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": fmt.Sprintf("access denied (HTTP %d)%s", resp.StatusCode, detail)})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": fmt.Sprintf("unexpected status %d from S3 endpoint", resp.StatusCode)})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// signS3Request applies AWS Signature Version 4 to an S3 request.
// Works with any S3 operation (HEAD, GET, PUT) — derives the canonical URI
// and query string from the request URL.
//
// OCI requires: x-amz-content-sha256 header present and included in signing.
// Go's net/http ignores req.Header["Host"], so we set req.Host explicitly.
func signS3Request(req *http.Request, accessKey, secretKey, region string) {
	now := time.Now().UTC()
	datestamp := now.Format("20060102")
	amzdate := now.Format("20060102T150405Z")
	host := req.URL.Host

	// Empty payload hash (SHA-256 of empty string).
	payloadHash := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	// Set required headers before signing.
	req.Host = host
	req.Header.Set("x-amz-date", amzdate)
	req.Header.Set("x-amz-content-sha256", payloadHash)

	// Canonical URI from the request path.
	canonicalURI := req.URL.Path
	if canonicalURI == "" {
		canonicalURI = "/"
	}

	// Canonical query string — params sorted by key (SigV4 requirement).
	canonicalQuerystring := sortedQueryString(req.URL.Query())

	// Canonical headers — must be sorted alphabetically.
	canonicalHeaders := fmt.Sprintf("host:%s\nx-amz-content-sha256:%s\nx-amz-date:%s\n",
		host, payloadHash, amzdate)
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"

	canonicalRequest := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s",
		req.Method, canonicalURI, canonicalQuerystring,
		canonicalHeaders, signedHeaders, payloadHash)

	// String to sign
	credentialScope := fmt.Sprintf("%s/%s/s3/aws4_request", datestamp, region)
	hasher := sha256.New()
	hasher.Write([]byte(canonicalRequest))
	stringToSign := fmt.Sprintf("AWS4-HMAC-SHA256\n%s\n%s\n%s",
		amzdate, credentialScope, hex.EncodeToString(hasher.Sum(nil)))

	// Signing key
	kDate := hmacSHA256([]byte("AWS4"+secretKey), datestamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, "s3")
	kSigning := hmacSHA256(kService, "aws4_request")

	// Signature
	sig := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))

	// Authorization header
	authHeader := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, credentialScope, signedHeaders, sig)
	req.Header.Set("Authorization", authHeader)
}

// sortedQueryString builds a canonical query string with params sorted by key.
func sortedQueryString(params url.Values) string {
	if len(params) == 0 {
		return ""
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		for _, v := range params[k] {
			parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(v))
		}
	}
	return strings.Join(parts, "&")
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

// MigrateState migrates all stack state from local backend to S3 (or vice versa).
// Streams progress as SSE events and switches the backend type on success.
func (h *Handler) MigrateState(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Direction string `json:"direction"` // "to-s3" or "to-local"
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body.Direction != "to-s3" && body.Direction != "to-local" {
		http.Error(w, `direction must be "to-s3" or "to-local"`, http.StatusBadRequest)
		return
	}

	// Validate S3 credentials are configured.
	bucket, _, _ := h.Creds.Get(db.KeyS3Bucket)
	ns, _, _ := h.Creds.Get(db.KeyS3Namespace)
	region, _, _ := h.Creds.Get(db.KeyS3Region)
	ak, _, _ := h.Creds.Get(db.KeyS3AccessKeyID)
	sk, _, _ := h.Creds.Get(db.KeyS3SecretAccessKey)
	if bucket == "" || ns == "" || region == "" || ak == "" || sk == "" {
		http.Error(w, "S3 credentials are not fully configured", http.StatusBadRequest)
		return
	}

	endpoint := fmt.Sprintf("https://%s.compat.objectstorage.%s.oraclecloud.com", ns, region)
	s3URL := fmt.Sprintf("s3://%s?endpoint=%s&s3ForcePathStyle=true&region=%s", bucket, endpoint, region)

	stateDir := os.Getenv("PULUMI_UI_STATE_DIR")
	if stateDir == "" {
		dataDir := os.Getenv("PULUMI_UI_DATA_DIR")
		if dataDir == "" {
			dataDir = "/data"
		}
		stateDir = dataDir + "/state"
	}
	localURL := "file://" + stateDir

	var sourceURL, targetURL, newBackendType string
	if body.Direction == "to-s3" {
		sourceURL = localURL
		targetURL = s3URL
		newBackendType = "s3"
	} else {
		sourceURL = s3URL
		targetURL = localURL
		newBackendType = "local"
	}

	// Collect all stacks and resolve their passphrases.
	rows, err := h.Stacks.List()
	if err != nil {
		http.Error(w, "failed to list stacks: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var inputs []engine.StackMigrationInput
	for _, row := range rows {
		if row.PassphraseID == nil || *row.PassphraseID == "" {
			continue // skip stacks without passphrases — they have no state
		}
		passphrase, err := h.Passphrases.GetValue(*row.PassphraseID)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to resolve passphrase for stack %s: %v", row.Name, err), http.StatusInternalServerError)
			return
		}
		inputs = append(inputs, engine.StackMigrationInput{
			StackName:     row.Name,
			BlueprintName: row.Blueprint,
			Passphrase:    passphrase,
		})
	}

	if len(inputs) == 0 {
		// No stacks to migrate — just switch the backend type.
		h.Creds.Set(db.KeyBackendType, newBackendType)
		writeJSON(w, http.StatusOK, map[string]any{
			"migrated": 0,
			"total":    0,
			"message":  "No stacks to migrate. Backend switched to " + newBackendType + ".",
		})
		return
	}

	// Set up SSE streaming.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	send := func(ev engine.SSEEvent) {
		data, _ := json.Marshal(ev)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	migrated, err := h.Engine.MigrateStacks(r.Context(), inputs, sourceURL, targetURL, send)
	if err != nil {
		send(engine.SSEEvent{Type: "error", Data: err.Error()})
		return
	}

	// Switch backend type on success.
	if migrated == len(inputs) {
		h.Creds.Set(db.KeyBackendType, newBackendType)
		send(engine.SSEEvent{Type: "output", Data: fmt.Sprintf("All %d stack(s) migrated. Backend switched to %s.", migrated, newBackendType)})
	} else {
		send(engine.SSEEvent{Type: "output", Data: fmt.Sprintf("%d/%d stack(s) migrated. Backend NOT switched — fix failures and retry.", migrated, len(inputs))})
	}
	send(engine.SSEEvent{Type: "complete", Data: fmt.Sprintf(`{"migrated":%d,"total":%d}`, migrated, len(inputs))})
}
