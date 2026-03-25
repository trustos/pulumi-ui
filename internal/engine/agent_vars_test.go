package engine

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/trustos/pulumi-ui/internal/crypto"
	"github.com/trustos/pulumi-ui/internal/db"
)

func setupConnStore(t *testing.T) *db.StackConnectionStore {
	t.Helper()
	database, err := db.Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	require.NoError(t, db.Migrate(database))

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
	assert.Equal(t, "v0.1.0", vars.AgentVersion)
	assert.Equal(t, "hex-token-abc123", vars.AgentToken)
	assert.Empty(t, vars.AgentDownloadURL, "should be empty when PULUMI_UI_EXTERNAL_URL is unset")
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
	assert.Equal(t, "https://pulumi.example.com/api/agent/binary/linux", vars.AgentDownloadURL)
}

func TestAgentVarsForStack_ExternalURL_TrailingSlash(t *testing.T) {
	store := setupConnStore(t)
	seedConnection(t, store, "trailing", []byte("ac"), []byte("ak"))

	t.Setenv("PULUMI_UI_EXTERNAL_URL", "https://pulumi.example.com/")

	e := &Engine{connStore: store}
	vars := e.agentVarsForStack("trailing")
	require.NotNil(t, vars)
	assert.Equal(t, "https://pulumi.example.com/api/agent/binary/linux", vars.AgentDownloadURL)
}

func TestAgentVarsForStack_ExternalURL_Unset(t *testing.T) {
	store := setupConnStore(t)
	seedConnection(t, store, "no-ext", []byte("ac"), []byte("ak"))

	os.Unsetenv("PULUMI_UI_EXTERNAL_URL")

	e := &Engine{connStore: store}
	vars := e.agentVarsForStack("no-ext")
	require.NotNil(t, vars)
	assert.Empty(t, vars.AgentDownloadURL, "should be empty when PULUMI_UI_EXTERNAL_URL is unset")
}
