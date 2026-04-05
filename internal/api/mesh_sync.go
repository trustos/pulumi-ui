package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/trustos/pulumi-ui/internal/crypto"
	"github.com/trustos/pulumi-ui/internal/db"
)

// --- JSON payload types for S3 mesh sync ---

type meshSyncPayload struct {
	Version    int                `json:"version"`
	KeySalt    string             `json:"keySalt"` // base64-encoded PBKDF2 salt
	Connection meshSyncConnection `json:"connection"`
	Nodes      []meshSyncNode     `json:"nodes"`
}

type meshSyncConnection struct {
	CACert     string `json:"caCert"`     // PEM plaintext
	CAKey      string `json:"caKey"`      // base64(nonce || ciphertext)
	UICert     string `json:"uiCert"`     // PEM plaintext
	UIKey      string `json:"uiKey"`      // base64(nonce || ciphertext)
	Subnet     string `json:"subnet"`
	AgentCert  string `json:"agentCert"`  // PEM plaintext
	AgentKey   string `json:"agentKey"`   // base64(nonce || ciphertext)
	AgentToken string `json:"agentToken"` // plaintext hex — useless without PKI
}

type meshSyncNode struct {
	Index int    `json:"index"`
	Cert  string `json:"cert"` // PEM plaintext
	Key   string `json:"key"`  // base64(nonce || ciphertext)
	IP    string `json:"ip"`
}

// s3MeshKey returns the S3 object key for a stack's mesh JSON.
func s3MeshKey(project, stackName string) string {
	return fmt.Sprintf(".pulumi/pulumi-ui/%s/%s.mesh.json", project, stackName)
}

// encryptKey encrypts a private key with the passphrase-derived key.
func encryptKey(key []byte, passphrase string, salt []byte) (string, error) {
	ct, err := crypto.EncryptWithPassphrase(key, passphrase, salt)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(ct), nil
}

// decryptKey decrypts a base64-encoded ciphertext with the passphrase-derived key.
func decryptKey(encoded string, passphrase string, salt []byte) ([]byte, error) {
	ct, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	return crypto.DecryptWithPassphrase(ct, passphrase, salt)
}

// syncMeshToS3 uploads the stack's Nebula mesh PKI data to S3 so other
// pulumi-ui instances sharing the same backend can import it during claim.
// Path: .pulumi/pulumi-ui/<project>/<stack>.mesh.json
// Private keys are encrypted with PBKDF2(passphrase, salt) + AES-256-GCM.
// Non-critical — errors are logged but don't fail the operation.
func syncMeshToS3(ctx context.Context, creds *db.CredentialStore, passphrase, project, stackName string, conn *db.StackConnection, nodeCerts []*db.NodeCert) {
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

	// Generate a single salt for all key encryptions in this payload.
	salt := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		log.Printf("[mesh-sync] failed to generate salt for %s: %v", stackName, err)
		return
	}

	// Encrypt private keys.
	encCAKey, err := encryptKey(conn.NebulaCAKey, passphrase, salt)
	if err != nil {
		log.Printf("[mesh-sync] encrypt CA key for %s: %v", stackName, err)
		return
	}
	encUIKey, err := encryptKey(conn.NebulaUIKey, passphrase, salt)
	if err != nil {
		log.Printf("[mesh-sync] encrypt UI key for %s: %v", stackName, err)
		return
	}
	encAgentKey, err := encryptKey(conn.NebulaAgentKey, passphrase, salt)
	if err != nil {
		log.Printf("[mesh-sync] encrypt agent key for %s: %v", stackName, err)
		return
	}

	payload := meshSyncPayload{
		Version: 1,
		KeySalt: base64.StdEncoding.EncodeToString(salt),
		Connection: meshSyncConnection{
			CACert:     string(conn.NebulaCACert),
			CAKey:      encCAKey,
			UICert:     string(conn.NebulaUICert),
			UIKey:      encUIKey,
			Subnet:     conn.NebulaSubnet,
			AgentCert:  string(conn.NebulaAgentCert),
			AgentKey:   encAgentKey,
			AgentToken: conn.AgentToken,
		},
	}

	for _, nc := range nodeCerts {
		encNodeKey, err := encryptKey(nc.NebulaKey, passphrase, salt)
		if err != nil {
			log.Printf("[mesh-sync] encrypt node %d key for %s: %v", nc.NodeIndex, stackName, err)
			return
		}
		payload.Nodes = append(payload.Nodes, meshSyncNode{
			Index: nc.NodeIndex,
			Cert:  string(nc.NebulaCert),
			Key:   encNodeKey,
			IP:    nc.NebulaIP,
		})
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[mesh-sync] marshal JSON for %s: %v", stackName, err)
		return
	}

	endpoint := fmt.Sprintf("https://%s.compat.objectstorage.%s.oraclecloud.com", ns, region)
	putURL := fmt.Sprintf("%s/%s/%s", endpoint, bucket, s3MeshKey(project, stackName))

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, putURL, bytes.NewReader(body))
	if err != nil {
		log.Printf("[mesh-sync] create request for %s: %v", stackName, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	payloadHash := fmt.Sprintf("%x", sha256.Sum256(body))
	req.Header.Set("x-amz-content-sha256", payloadHash)
	signS3Request(req, accessKey, secretKey, region)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[mesh-sync] upload mesh for %s: %v", stackName, err)
		return
	}
	resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Printf("[mesh-sync] synced mesh for %s/%s to S3 (%d bytes)", project, stackName, len(body))
	} else {
		log.Printf("[mesh-sync] S3 PUT returned %d for %s/%s", resp.StatusCode, project, stackName)
	}
}

// fetchMeshFromS3 downloads a stack's mesh PKI data from S3 and decrypts
// private keys using the provided passphrase.
// Returns nil, nil, nil if the mesh JSON does not exist (stack predates this feature).
func fetchMeshFromS3(ctx context.Context, creds *db.CredentialStore, passphrase, project, stackName string) (*db.StackConnection, []*db.NodeCert, error) {
	backendType, _, _ := creds.Get(db.KeyBackendType)
	if backendType != "s3" {
		return nil, nil, nil
	}

	bucket, _, _ := creds.Get(db.KeyS3Bucket)
	ns, _, _ := creds.Get(db.KeyS3Namespace)
	region, _, _ := creds.Get(db.KeyS3Region)
	accessKey, _, _ := creds.Get(db.KeyS3AccessKeyID)
	secretKey, _, _ := creds.Get(db.KeyS3SecretAccessKey)

	if bucket == "" || ns == "" || region == "" || accessKey == "" || secretKey == "" {
		return nil, nil, nil
	}

	endpoint := fmt.Sprintf("https://%s.compat.objectstorage.%s.oraclecloud.com", ns, region)
	getURL := fmt.Sprintf("%s/%s/%s", endpoint, bucket, s3MeshKey(project, stackName))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("create request: %w", err)
	}
	signS3Request(req, accessKey, secretKey, region)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("download mesh: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("S3 GET returned %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("read body: %w", err)
	}

	var payload meshSyncPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, nil, fmt.Errorf("unmarshal mesh JSON: %w", err)
	}

	salt, err := base64.StdEncoding.DecodeString(payload.KeySalt)
	if err != nil {
		return nil, nil, fmt.Errorf("decode salt: %w", err)
	}

	// Decrypt private keys.
	caKey, err := decryptKey(payload.Connection.CAKey, passphrase, salt)
	if err != nil {
		return nil, nil, fmt.Errorf("decrypt CA key: %w", err)
	}
	uiKey, err := decryptKey(payload.Connection.UIKey, passphrase, salt)
	if err != nil {
		return nil, nil, fmt.Errorf("decrypt UI key: %w", err)
	}
	agentKey, err := decryptKey(payload.Connection.AgentKey, passphrase, salt)
	if err != nil {
		return nil, nil, fmt.Errorf("decrypt agent key: %w", err)
	}

	conn := &db.StackConnection{
		StackName:       stackName,
		NebulaCACert:    []byte(payload.Connection.CACert),
		NebulaCAKey:     caKey,
		NebulaUICert:    []byte(payload.Connection.UICert),
		NebulaUIKey:     uiKey,
		NebulaSubnet:    payload.Connection.Subnet,
		NebulaAgentCert: []byte(payload.Connection.AgentCert),
		NebulaAgentKey:  agentKey,
		AgentToken:      payload.Connection.AgentToken,
		// Runtime fields left nil — discovered post-deploy.
	}

	var nodeCerts []*db.NodeCert
	for _, n := range payload.Nodes {
		nodeKey, err := decryptKey(n.Key, passphrase, salt)
		if err != nil {
			return nil, nil, fmt.Errorf("decrypt node %d key: %w", n.Index, err)
		}
		nodeCerts = append(nodeCerts, &db.NodeCert{
			StackName:  stackName,
			NodeIndex:  n.Index,
			NebulaCert: []byte(n.Cert),
			NebulaKey:  nodeKey,
			NebulaIP:   n.IP,
		})
	}

	return conn, nodeCerts, nil
}

// meshExistsInS3 checks whether a mesh JSON file exists in S3 for the given stack
// using a lightweight HEAD request (no download or decryption).
func meshExistsInS3(ctx context.Context, creds *db.CredentialStore, project, stackName string) bool {
	backendType, _, _ := creds.Get(db.KeyBackendType)
	if backendType != "s3" {
		return false
	}

	bucket, _, _ := creds.Get(db.KeyS3Bucket)
	ns, _, _ := creds.Get(db.KeyS3Namespace)
	region, _, _ := creds.Get(db.KeyS3Region)
	accessKey, _, _ := creds.Get(db.KeyS3AccessKeyID)
	secretKey, _, _ := creds.Get(db.KeyS3SecretAccessKey)

	if bucket == "" || ns == "" || region == "" || accessKey == "" || secretKey == "" {
		return false
	}

	endpoint := fmt.Sprintf("https://%s.compat.objectstorage.%s.oraclecloud.com", ns, region)
	headURL := fmt.Sprintf("%s/%s/%s", endpoint, bucket, s3MeshKey(project, stackName))

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, headURL, nil)
	if err != nil {
		return false
	}
	signS3Request(req, accessKey, secretKey, region)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}
