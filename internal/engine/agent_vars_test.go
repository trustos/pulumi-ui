package engine

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/trustos/pulumi-ui/internal/crypto"
	"github.com/trustos/pulumi-ui/internal/db"
)

// setupStores creates an in-memory DB and returns both StackConnectionStore and
// NodeCertStore backed by the same database. Useful for PKI tests that need both.
func setupStores(t *testing.T) (*db.StackConnectionStore, *db.NodeCertStore) {
	t.Helper()
	database, err := db.Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	require.NoError(t, db.Migrate(database.WriteDB))

	enc, err := crypto.NewEncryptor("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	connStore := db.NewStackConnectionStore(database, enc)
	nodeCertStore := db.NewNodeCertStore(database, enc)
	return connStore, nodeCertStore
}

func setupConnStore(t *testing.T) *db.StackConnectionStore {
	t.Helper()
	database, err := db.Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	require.NoError(t, db.Migrate(database.WriteDB))

	enc, err := crypto.NewEncryptor("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	return db.NewStackConnectionStore(database, enc)
}

func seedConnection(t *testing.T, store *db.StackConnectionStore, name string, agentCert, agentKey []byte) {
	t.Helper()
	conn := &db.StackConnection{
		StackName:       name,
		NebulaCACert:    []byte("ca-cert-pem"),
		NebulaCAKey:     []byte("ca-key-pem"),
		NebulaUICert:    []byte("ui-cert-pem"),
		NebulaUIKey:     []byte("ui-key-pem"),
		NebulaSubnet:    "10.42.1.0/24",
		NebulaAgentCert: agentCert,
		NebulaAgentKey:  agentKey,
		AgentToken:      "hex-token-abc123",
	}
	require.NoError(t, store.Create(conn))
}

func TestAgentVarsForStack_NilConnStore(t *testing.T) {
	e := &Engine{connStore: nil}
	assert.Nil(t, e.agentVarsForStack("anything"))
}

func TestAgentVarsForStack_MissingStack(t *testing.T) {
	store := setupConnStore(t)
	e := &Engine{connStore: store}
	assert.Nil(t, e.agentVarsForStack("nonexistent"))
}

func TestAgentVarsForStack_EmptyAgentCert_ReturnsNil(t *testing.T) {
	store := setupConnStore(t)
	seedConnection(t, store, "no-agent-cert", nil, nil)

	e := &Engine{connStore: store}
	result := e.agentVarsForStack("no-agent-cert")
	assert.Nil(t, result, "should return nil when agent cert is empty")
}

func TestAgentVarsForStack_Normal(t *testing.T) {
	store := setupConnStore(t)
	seedConnection(t, store, "my-stack", []byte("agent-cert-pem"), []byte("agent-key-pem"))

	e := &Engine{connStore: store}
	vars := e.agentVarsForStack("my-stack")
	require.NotNil(t, vars)

	assert.Equal(t, "ca-cert-pem", vars.NebulaCACert)
	assert.Equal(t, "agent-cert-pem", vars.NebulaHostCert)
	assert.Equal(t, "agent-key-pem", vars.NebulaHostKey)
	assert.Equal(t, "v1.10.3", vars.NebulaVersion)
	assert.Equal(t, "v0.2.7", vars.AgentVersion)
	assert.Equal(t, "hex-token-abc123", vars.AgentToken)
	assert.Empty(t, vars.NebulaServerRealIP, "should be empty when PULUMI_UI_EXTERNAL_URL is unset")
}

func TestAgentVarsForStack_EmptyToken_FallsBackToPlaceholder(t *testing.T) {
	store := setupConnStore(t)
	conn := &db.StackConnection{
		StackName:       "empty-token",
		NebulaCACert:    []byte("ca"),
		NebulaCAKey:     []byte("k"),
		NebulaUICert:    []byte("u"),
		NebulaUIKey:     []byte("uk"),
		NebulaSubnet:    "10.42.2.0/24",
		NebulaAgentCert: []byte("ac"),
		NebulaAgentKey:  []byte("ak"),
		AgentToken:      "",
	}
	require.NoError(t, store.Create(conn))

	e := &Engine{connStore: store}
	vars := e.agentVarsForStack("empty-token")
	require.NotNil(t, vars)
	assert.Equal(t, "placeholder-token", vars.AgentToken)
}

func TestAgentVarsForStack_ExternalURL_Set(t *testing.T) {
	store := setupConnStore(t)
	seedConnection(t, store, "ext-url", []byte("ac"), []byte("ak"))

	t.Setenv("PULUMI_UI_EXTERNAL_URL", "https://pulumi.example.com")

	e := &Engine{connStore: store}
	vars := e.agentVarsForStack("ext-url")
	require.NotNil(t, vars)
	assert.Equal(t, "pulumi.example.com", vars.NebulaServerRealIP)
}

func TestAgentVarsForStack_ExternalURL_TrailingSlash(t *testing.T) {
	store := setupConnStore(t)
	seedConnection(t, store, "trailing", []byte("ac"), []byte("ak"))

	t.Setenv("PULUMI_UI_EXTERNAL_URL", "https://pulumi.example.com/")

	e := &Engine{connStore: store}
	vars := e.agentVarsForStack("trailing")
	require.NotNil(t, vars)
	assert.Equal(t, "pulumi.example.com", vars.NebulaServerRealIP)
}

func TestAgentVarsForStack_ExternalURL_Unset(t *testing.T) {
	store := setupConnStore(t)
	seedConnection(t, store, "no-ext", []byte("ac"), []byte("ak"))

	os.Unsetenv("PULUMI_UI_EXTERNAL_URL")

	e := &Engine{connStore: store}
	vars := e.agentVarsForStack("no-ext")
	require.NotNil(t, vars)
	assert.Empty(t, vars.NebulaServerRealIP, "should be empty when PULUMI_UI_EXTERNAL_URL is unset")
}

// --- ensureNebulaPKI / generateNebulaPKI tests ---

func TestEnsureNebulaPKI_NilConnStore(t *testing.T) {
	e := &Engine{connStore: nil}
	assert.NoError(t, e.ensureNebulaPKI("any-stack"))
}

func TestEnsureNebulaPKI_AlreadyExists_NoOp(t *testing.T) {
	connStore, nodeCertStore := setupStores(t)
	seedConnection(t, connStore, "existing-stack", []byte("ac"), []byte("ak"))

	e := &Engine{connStore: connStore, nodeCertStore: nodeCertStore}
	require.NoError(t, e.ensureNebulaPKI("existing-stack"))

	// Connection record should be unchanged (still the seeded one)
	conn, err := connStore.Get("existing-stack")
	require.NoError(t, err)
	require.NotNil(t, conn)
	assert.Equal(t, "hex-token-abc123", conn.AgentToken, "should not overwrite existing connection")

	// No node certs should have been inserted (seeded connection has none)
	certs, err := nodeCertStore.ListForStack("existing-stack")
	require.NoError(t, err)
	assert.Empty(t, certs, "no node certs expected for seeded stub connection")
}

func TestEnsureNebulaPKI_GeneratesWhenMissing(t *testing.T) {
	connStore, nodeCertStore := setupStores(t)

	e := &Engine{connStore: connStore, nodeCertStore: nodeCertStore}
	require.NoError(t, e.ensureNebulaPKI("new-stack"))

	// Connection record must exist with real PKI material
	conn, err := connStore.Get("new-stack")
	require.NoError(t, err)
	require.NotNil(t, conn, "connection record must be created")

	assert.Equal(t, "new-stack", conn.StackName)
	assert.NotEmpty(t, conn.NebulaCACert, "CA cert must be set")
	assert.NotEmpty(t, conn.NebulaCAKey, "CA key must be set")
	assert.NotEmpty(t, conn.NebulaAgentCert, "agent cert must be set")
	assert.NotEmpty(t, conn.NebulaAgentKey, "agent key must be set")
	assert.NotEmpty(t, conn.AgentToken, "agent token must be set")
	assert.NotEmpty(t, conn.NebulaSubnet, "subnet must be allocated")

	// generateNebulaPKI always creates 10 node certs
	certs, err := nodeCertStore.ListForStack("new-stack")
	require.NoError(t, err)
	assert.Len(t, certs, 10, "expected 10 node certs")

	for i, c := range certs {
		assert.Equal(t, i, c.NodeIndex)
		assert.NotEmpty(t, c.NebulaCert)
		assert.NotEmpty(t, c.NebulaKey)
		assert.NotEmpty(t, c.NebulaIP)
	}
}

func TestEnsureNebulaPKI_Idempotent(t *testing.T) {
	connStore, nodeCertStore := setupStores(t)

	e := &Engine{connStore: connStore, nodeCertStore: nodeCertStore}
	require.NoError(t, e.ensureNebulaPKI("idem-stack"))
	// Second call must not fail (record already exists → no-op)
	require.NoError(t, e.ensureNebulaPKI("idem-stack"))

	certs, err := nodeCertStore.ListForStack("idem-stack")
	require.NoError(t, err)
	assert.Len(t, certs, 10, "certs must not be duplicated by second call")
}

func TestEnsureNebulaPKI_WithoutNodeCertStore(t *testing.T) {
	connStore, _ := setupStores(t)

	// nodeCertStore is nil — generateNebulaPKI must still succeed, just skip cert storage
	e := &Engine{connStore: connStore, nodeCertStore: nil}
	require.NoError(t, e.ensureNebulaPKI("no-node-store-stack"))

	conn, err := connStore.Get("no-node-store-stack")
	require.NoError(t, err)
	require.NotNil(t, conn, "connection record must still be created")
}
