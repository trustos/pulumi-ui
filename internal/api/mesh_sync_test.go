package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/trustos/pulumi-ui/internal/db"
)

// --- Helper: build test mesh data ---

func sampleConnection() *db.StackConnection {
	return &db.StackConnection{
		StackName:       "test-stack",
		NebulaCACert:    []byte("-----BEGIN NEBULA CERTIFICATE-----\nca-cert\n-----END NEBULA CERTIFICATE-----"),
		NebulaCAKey:     []byte("-----BEGIN NEBULA ED25519 PRIVATE KEY-----\nca-key-secret\n-----END NEBULA ED25519 PRIVATE KEY-----"),
		NebulaUICert:    []byte("-----BEGIN NEBULA CERTIFICATE-----\nui-cert\n-----END NEBULA CERTIFICATE-----"),
		NebulaUIKey:     []byte("-----BEGIN NEBULA ED25519 PRIVATE KEY-----\nui-key-secret\n-----END NEBULA ED25519 PRIVATE KEY-----"),
		NebulaSubnet:    "10.42.1.0/24",
		NebulaAgentCert: []byte("-----BEGIN NEBULA CERTIFICATE-----\nagent-cert\n-----END NEBULA CERTIFICATE-----"),
		NebulaAgentKey:  []byte("-----BEGIN NEBULA ED25519 PRIVATE KEY-----\nagent-key-secret\n-----END NEBULA ED25519 PRIVATE KEY-----"),
		AgentToken:      "deadbeef1234567890",
	}
}

func sampleNodeCerts() []*db.NodeCert {
	return []*db.NodeCert{
		{
			StackName:  "test-stack",
			NodeIndex:  0,
			NebulaCert: []byte("node-0-cert"),
			NebulaKey:  []byte("node-0-key-secret"),
			NebulaIP:   "10.42.1.2/24",
		},
		{
			StackName:  "test-stack",
			NodeIndex:  1,
			NebulaCert: []byte("node-1-cert"),
			NebulaKey:  []byte("node-1-key-secret"),
			NebulaIP:   "10.42.1.3/24",
		},
	}
}

// buildMeshPayload builds a meshSyncPayload from connection + node certs,
// encrypting private keys with the given passphrase. This mirrors syncMeshToS3's
// core logic without the S3 upload.
func buildMeshPayload(t *testing.T, passphrase string, conn *db.StackConnection, nodes []*db.NodeCert) []byte {
	t.Helper()

	salt := make([]byte, 16)
	_, err := rand.Read(salt)
	require.NoError(t, err)

	encCAKey, err := encryptKey(conn.NebulaCAKey, passphrase, salt)
	require.NoError(t, err)
	encUIKey, err := encryptKey(conn.NebulaUIKey, passphrase, salt)
	require.NoError(t, err)
	encAgentKey, err := encryptKey(conn.NebulaAgentKey, passphrase, salt)
	require.NoError(t, err)

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

	for _, nc := range nodes {
		encNodeKey, err := encryptKey(nc.NebulaKey, passphrase, salt)
		require.NoError(t, err)
		payload.Nodes = append(payload.Nodes, meshSyncNode{
			Index: nc.NodeIndex,
			Cert:  string(nc.NebulaCert),
			Key:   encNodeKey,
			IP:    nc.NebulaIP,
		})
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)
	return data
}

// parseMeshPayload decrypts a mesh JSON payload, mirroring fetchMeshFromS3's
// core logic without the S3 download.
func parseMeshPayload(t *testing.T, data []byte, passphrase, stackName string) (*db.StackConnection, []*db.NodeCert) {
	t.Helper()

	var payload meshSyncPayload
	require.NoError(t, json.Unmarshal(data, &payload))

	salt, err := base64.StdEncoding.DecodeString(payload.KeySalt)
	require.NoError(t, err)

	caKey, err := decryptKey(payload.Connection.CAKey, passphrase, salt)
	require.NoError(t, err)
	uiKey, err := decryptKey(payload.Connection.UIKey, passphrase, salt)
	require.NoError(t, err)
	agentKey, err := decryptKey(payload.Connection.AgentKey, passphrase, salt)
	require.NoError(t, err)

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
	}

	var nodes []*db.NodeCert
	for _, n := range payload.Nodes {
		nodeKey, err := decryptKey(n.Key, passphrase, salt)
		require.NoError(t, err)
		nodes = append(nodes, &db.NodeCert{
			StackName:  stackName,
			NodeIndex:  n.Index,
			NebulaCert: []byte(n.Cert),
			NebulaKey:  nodeKey,
			NebulaIP:   n.IP,
		})
	}

	return conn, nodes
}

// --- Tests ---

func TestMeshPayload_RoundTrip(t *testing.T) {
	conn := sampleConnection()
	nodes := sampleNodeCerts()
	passphrase := "test-passphrase-42"

	data := buildMeshPayload(t, passphrase, conn, nodes)

	gotConn, gotNodes := parseMeshPayload(t, data, passphrase, "test-stack")

	// Connection fields
	assert.Equal(t, conn.NebulaCACert, gotConn.NebulaCACert)
	assert.Equal(t, conn.NebulaCAKey, gotConn.NebulaCAKey)
	assert.Equal(t, conn.NebulaUICert, gotConn.NebulaUICert)
	assert.Equal(t, conn.NebulaUIKey, gotConn.NebulaUIKey)
	assert.Equal(t, conn.NebulaSubnet, gotConn.NebulaSubnet)
	assert.Equal(t, conn.NebulaAgentCert, gotConn.NebulaAgentCert)
	assert.Equal(t, conn.NebulaAgentKey, gotConn.NebulaAgentKey)
	assert.Equal(t, conn.AgentToken, gotConn.AgentToken)
	assert.Equal(t, "test-stack", gotConn.StackName)

	// Runtime fields should be nil (not synced).
	assert.Nil(t, gotConn.AgentRealIP)
	assert.Nil(t, gotConn.LighthouseAddr)
	assert.Nil(t, gotConn.AgentNebulaIP)

	// Node certs
	require.Len(t, gotNodes, 2)
	assert.Equal(t, 0, gotNodes[0].NodeIndex)
	assert.Equal(t, []byte("node-0-cert"), gotNodes[0].NebulaCert)
	assert.Equal(t, []byte("node-0-key-secret"), gotNodes[0].NebulaKey)
	assert.Equal(t, "10.42.1.2/24", gotNodes[0].NebulaIP)
	assert.Equal(t, 1, gotNodes[1].NodeIndex)
	assert.Equal(t, []byte("node-1-cert"), gotNodes[1].NebulaCert)
	assert.Equal(t, []byte("node-1-key-secret"), gotNodes[1].NebulaKey)
	assert.Equal(t, "10.42.1.3/24", gotNodes[1].NebulaIP)
}

func TestMeshPayload_WrongPassphrase(t *testing.T) {
	conn := sampleConnection()
	data := buildMeshPayload(t, "correct-pass", conn, nil)

	var payload meshSyncPayload
	require.NoError(t, json.Unmarshal(data, &payload))

	salt, err := base64.StdEncoding.DecodeString(payload.KeySalt)
	require.NoError(t, err)

	_, err = decryptKey(payload.Connection.CAKey, "wrong-pass", salt)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "decryption failed")
}

func TestMeshPayload_NoNodes(t *testing.T) {
	conn := sampleConnection()
	passphrase := "no-nodes-pass"

	data := buildMeshPayload(t, passphrase, conn, nil)

	gotConn, gotNodes := parseMeshPayload(t, data, passphrase, "test-stack")

	assert.Equal(t, conn.NebulaCAKey, gotConn.NebulaCAKey)
	assert.Nil(t, gotNodes)
}

func TestMeshPayload_VersionField(t *testing.T) {
	conn := sampleConnection()
	data := buildMeshPayload(t, "pass", conn, nil)

	var payload meshSyncPayload
	require.NoError(t, json.Unmarshal(data, &payload))

	assert.Equal(t, 1, payload.Version)
}

func TestMeshPayload_PrivateKeysEncrypted(t *testing.T) {
	conn := sampleConnection()
	data := buildMeshPayload(t, "pass", conn, sampleNodeCerts())

	var payload meshSyncPayload
	require.NoError(t, json.Unmarshal(data, &payload))

	// Certs should be plaintext PEM in the JSON.
	assert.Contains(t, payload.Connection.CACert, "BEGIN NEBULA CERTIFICATE")
	assert.Contains(t, payload.Connection.UICert, "BEGIN NEBULA CERTIFICATE")
	assert.Contains(t, payload.Connection.AgentCert, "BEGIN NEBULA CERTIFICATE")

	// Keys should NOT contain plaintext PEM markers — they're base64(encrypted).
	assert.NotContains(t, payload.Connection.CAKey, "BEGIN")
	assert.NotContains(t, payload.Connection.UIKey, "BEGIN")
	assert.NotContains(t, payload.Connection.AgentKey, "BEGIN")

	// Should be valid base64.
	_, err := base64.StdEncoding.DecodeString(payload.Connection.CAKey)
	assert.NoError(t, err)

	// Node keys should also be encrypted.
	for _, n := range payload.Nodes {
		assert.NotContains(t, n.Key, "node-")
		_, err := base64.StdEncoding.DecodeString(n.Key)
		assert.NoError(t, err)
	}

	// Agent token should be plaintext (not encrypted).
	assert.Equal(t, "deadbeef1234567890", payload.Connection.AgentToken)
}

func TestMeshPayload_StackNameOverride(t *testing.T) {
	conn := sampleConnection() // StackName is "test-stack"
	data := buildMeshPayload(t, "pass", conn, sampleNodeCerts())

	// Parse with a different stack name — simulates claiming instance.
	gotConn, gotNodes := parseMeshPayload(t, data, "pass", "claimed-stack")

	assert.Equal(t, "claimed-stack", gotConn.StackName)
	for _, nc := range gotNodes {
		assert.Equal(t, "claimed-stack", nc.StackName)
	}
}

func TestEncryptKey_DecryptKey_RoundTrip(t *testing.T) {
	salt := make([]byte, 16)
	rand.Read(salt)

	original := []byte("this is a private key")
	encoded, err := encryptKey(original, "passphrase", salt)
	require.NoError(t, err)

	decoded, err := decryptKey(encoded, "passphrase", salt)
	require.NoError(t, err)
	assert.Equal(t, original, decoded)
}

func TestDecryptKey_InvalidBase64(t *testing.T) {
	salt := make([]byte, 16)
	_, err := decryptKey("not-valid-base64!!!", "pass", salt)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "base64")
}

func TestS3MeshKey(t *testing.T) {
	assert.Equal(t, ".pulumi/pulumi-ui/myproject/mystack.mesh.json", s3MeshKey("myproject", "mystack"))
}
