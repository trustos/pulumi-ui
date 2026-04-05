package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/trustos/pulumi-ui/internal/db"
)

// syncConfigToS3 uploads the stack's config YAML to the S3 backend so other
// pulumi-ui instances sharing the same backend can import it during claim.
// Path: .pulumi/pulumi-ui/<project>/<stack>.yaml
// Non-critical — errors are logged but don't fail the operation.
func syncConfigToS3(ctx context.Context, creds *db.CredentialStore, project, stackName, configYAML string) {
	backendType, _, _ := creds.Get(db.KeyBackendType)
	if backendType != "s3" {
		return
	}

	bucket, _, _ := creds.Get(db.KeyS3Bucket)
	ns, _, _ := creds.Get(db.KeyS3Namespace)
	region, _, _ := creds.Get(db.KeyS3Region)
	accessKey, _, _ := creds.Get(db.KeyS3AccessKeyID)
	secretKey, _, _ := creds.Get(db.KeyS3SecretAccessKey)

	if bucket == "" || ns == "" || region == "" || accessKey == "" || secretKey == "" {
		return
	}

	endpoint := fmt.Sprintf("https://%s.compat.objectstorage.%s.oraclecloud.com", ns, region)
	key := fmt.Sprintf(".pulumi/pulumi-ui/%s/%s.yaml", project, stackName)
	putURL := fmt.Sprintf("%s/%s/%s", endpoint, bucket, key)

	body := []byte(configYAML)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, putURL, bytes.NewReader(body))
	if err != nil {
		log.Printf("[config-sync] failed to create request for %s: %v", stackName, err)
		return
	}
	req.Header.Set("Content-Type", "application/yaml")
	// Set the payload hash BEFORE signing — signS3Request uses the x-amz-content-sha256
	// header value for signature computation. Without this, PUT requests fail with 400
	// because the signature is computed over an empty payload hash.
	payloadHash := fmt.Sprintf("%x", sha256.Sum256(body))
	req.Header.Set("x-amz-content-sha256", payloadHash)
	signS3Request(req, accessKey, secretKey, region)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[config-sync] failed to upload config for %s: %v", stackName, err)
		return
	}
	resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Printf("[config-sync] synced config for %s/%s to S3", project, stackName)
	} else {
		log.Printf("[config-sync] S3 PUT returned %d for %s/%s", resp.StatusCode, project, stackName)
	}
}

// fetchConfigFromS3 downloads a stack's config YAML from the S3 backend.
// Returns empty string if not found or any error occurs.
func fetchConfigFromS3(ctx context.Context, creds *db.CredentialStore, project, stackName string) string {
	backendType, _, _ := creds.Get(db.KeyBackendType)
	if backendType != "s3" {
		return ""
	}

	bucket, _, _ := creds.Get(db.KeyS3Bucket)
	ns, _, _ := creds.Get(db.KeyS3Namespace)
	region, _, _ := creds.Get(db.KeyS3Region)
	accessKey, _, _ := creds.Get(db.KeyS3AccessKeyID)
	secretKey, _, _ := creds.Get(db.KeyS3SecretAccessKey)

	if bucket == "" || ns == "" || region == "" || accessKey == "" || secretKey == "" {
		return ""
	}

	endpoint := fmt.Sprintf("https://%s.compat.objectstorage.%s.oraclecloud.com", ns, region)
	key := fmt.Sprintf(".pulumi/pulumi-ui/%s/%s.yaml", project, stackName)
	getURL := fmt.Sprintf("%s/%s/%s", endpoint, bucket, key)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
	if err != nil {
		return ""
	}
	signS3Request(req, accessKey, secretKey, region)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	return string(data)
}
