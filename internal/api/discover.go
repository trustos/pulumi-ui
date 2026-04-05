package api

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/trustos/pulumi-ui/internal/auth"
	"github.com/trustos/pulumi-ui/internal/db"
)

// RemoteStackSummary represents a stack discovered in the S3 backend
// that does not yet exist in the local database.
type RemoteStackSummary struct {
	Name               string  `json:"name"`
	Blueprint          string  `json:"blueprint"`           // Pulumi project name from S3 path
	SuggestedAccountID *string `json:"suggestedAccountId"`  // hint for ClaimStackDialog
}

// listBucketResult is the minimal XML struct for S3 ListObjectsV2 responses.
type listBucketResult struct {
	Contents    []s3Object `xml:"Contents"`
	IsTruncated bool       `xml:"IsTruncated"`
	NextToken   string     `xml:"NextContinuationToken"`
}

type s3Object struct {
	Key string `xml:"Key"`
}

// DiscoverRemoteStacks lists stacks in the S3 backend that are not registered
// in the local database. Returns an empty array if the backend is not S3.
func (h *PlatformHandler) DiscoverRemoteStacks(w http.ResponseWriter, r *http.Request) {
	backendType, _, _ := h.Creds.Get(db.KeyBackendType)
	if backendType != "s3" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]RemoteStackSummary{})
		return
	}

	bucket, _, _ := h.Creds.Get(db.KeyS3Bucket)
	ns, _, _ := h.Creds.Get(db.KeyS3Namespace)
	region, _, _ := h.Creds.Get(db.KeyS3Region)
	accessKey, _, _ := h.Creds.Get(db.KeyS3AccessKeyID)
	secretKey, _, _ := h.Creds.Get(db.KeyS3SecretAccessKey)

	if bucket == "" || ns == "" || region == "" || accessKey == "" || secretKey == "" {
		http.Error(w, "S3 credentials not fully configured", http.StatusBadRequest)
		return
	}

	// Collect all stack keys from S3 with pagination.
	endpoint := fmt.Sprintf("https://%s.compat.objectstorage.%s.oraclecloud.com", ns, region)
	var allKeys []string
	continuationToken := ""

	client := &http.Client{Timeout: 15 * time.Second}

	for {
		listURL := fmt.Sprintf("%s/%s?list-type=2&prefix=.pulumi/stacks/", endpoint, bucket)
		if continuationToken != "" {
			listURL += "&continuation-token=" + continuationToken
		}

		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, listURL, nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		signS3Request(req, accessKey, secretKey, region)

		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, "S3 list failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			http.Error(w, fmt.Sprintf("S3 list returned %d: %s", resp.StatusCode, string(body)), http.StatusBadGateway)
			return
		}

		var result listBucketResult
		if err := xml.Unmarshal(body, &result); err != nil {
			http.Error(w, "failed to parse S3 response: "+err.Error(), http.StatusInternalServerError)
			return
		}

		for _, obj := range result.Contents {
			allKeys = append(allKeys, obj.Key)
		}

		if !result.IsTruncated || result.NextToken == "" {
			break
		}
		continuationToken = result.NextToken
	}

	// Parse keys: .pulumi/stacks/<project>/<stack>.json
	type stackKey struct {
		project string
		stack   string
	}
	var discovered []stackKey
	for _, key := range allKeys {
		if !strings.HasSuffix(key, ".json") || strings.HasSuffix(key, ".json.bak") {
			continue
		}
		// Strip prefix and suffix
		trimmed := strings.TrimPrefix(key, ".pulumi/stacks/")
		trimmed = strings.TrimSuffix(trimmed, ".json")
		parts := strings.SplitN(trimmed, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			continue
		}
		discovered = append(discovered, stackKey{project: parts[0], stack: parts[1]})
	}

	// Get local stacks to filter out already-claimed ones.
	localRows, err := h.Stacks.List()
	if err != nil {
		http.Error(w, "failed to list local stacks: "+err.Error(), http.StatusInternalServerError)
		return
	}
	localNames := make(map[string]bool, len(localRows))
	for _, row := range localRows {
		localNames[row.Name] = true
	}

	// Suggest an account if only one is configured (common single-user case).
	var suggestedID *string
	if h.Accounts != nil {
		user := auth.UserFromContext(r.Context())
		if user != nil {
			if accts, err := h.Accounts.ListForUser(user.ID); err == nil && len(accts) == 1 {
				suggestedID = &accts[0].ID
			}
		}
	}

	// Build result — only stacks not in local DB.
	remote := make([]RemoteStackSummary, 0)
	for _, sk := range discovered {
		if localNames[sk.stack] {
			continue
		}
		remote = append(remote, RemoteStackSummary{
			Name:               sk.stack,
			Blueprint:          sk.project,
			SuggestedAccountID: suggestedID,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(remote)
}
