package db

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/trustos/pulumi-ui/internal/crypto"
)

func setupTestConnStore(t *testing.T) *StackConnectionStore {
	t.Helper()
	database, err := Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	require.NoError(t, Migrate(database))

	enc, err := crypto.NewEncryptor("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	return NewStackConnectionStore(database, enc)
}

func TestCreateAndGet_Roundtrip(t *testing.T) {
	store := setupTestConnStore(t)

	conn := &StackConnection{
		StackName:       "test-stack",
		NebulaCACert:    []byte("ca-cert"),
		NebulaCAKey:     []byte("ca-key-secret"),
		NebulaUICert:    []byte("ui-cert"),
		NebulaUIKey:     []byte("ui-key-secret"),
		NebulaSubnet:    "10.42.1.0/24",
		NebulaAgentCert: []byte("agent-cert"),
		NebulaAgentKey:  []byte("agent-key-secret"),
		AgentToken:      "hex-token-abc123",
	}
	require.NoError(t, store.Create(conn))

	got, err := store.Get("test-stack")
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Equal(t, "test-stack", got.StackName)
	assert.Equal(t, []byte("ca-cert"), got.NebulaCACert)
	assert.Equal(t, []byte("ca-key-secret"), got.NebulaCAKey)
	assert.Equal(t, []byte("ui-cert"), got.NebulaUICert)
	assert.Equal(t, []byte("ui-key-secret"), got.NebulaUIKey)
	assert.Equal(t, "10.42.1.0/24", got.NebulaSubnet)
	assert.Equal(t, []byte("agent-cert"), got.NebulaAgentCert)
	assert.Equal(t, []byte("agent-key-secret"), got.NebulaAgentKey)
	assert.Equal(t, "hex-token-abc123", got.AgentToken)
}

func TestGet_NotFound(t *testing.T) {
	store := setupTestConnStore(t)
	got, err := store.Get("nonexistent")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestCreate_NoAgentKey(t *testing.T) {
	store := setupTestConnStore(t)

	conn := &StackConnection{
		StackName:    "no-agent",
		NebulaCACert: []byte("ca"),
		NebulaCAKey:  []byte("cakey"),
		NebulaUICert: []byte("ui"),
		NebulaUIKey:  []byte("uikey"),
		NebulaSubnet: "10.42.2.0/24",
		AgentToken:   "tok",
	}
	require.NoError(t, store.Create(conn))

	got, err := store.Get("no-agent")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Empty(t, got.NebulaAgentCert)
	assert.Empty(t, got.NebulaAgentKey)
}

func TestUpdateAgentRealIP(t *testing.T) {
	store := setupTestConnStore(t)
	conn := &StackConnection{
		StackName:    "ip-test",
		NebulaCACert: []byte("c"),
		NebulaCAKey:  []byte("k"),
		NebulaUICert: []byte("c"),
		NebulaUIKey:  []byte("k"),
		NebulaSubnet: "10.42.3.0/24",
		AgentToken:   "t",
	}
	require.NoError(t, store.Create(conn))

	require.NoError(t, store.UpdateAgentRealIP("ip-test", "130.61.1.1"))

	got, err := store.Get("ip-test")
	require.NoError(t, err)
	require.NotNil(t, got.AgentRealIP)
	assert.Equal(t, "130.61.1.1", *got.AgentRealIP)
}

func TestUpdateAgentConnected(t *testing.T) {
	store := setupTestConnStore(t)
	conn := &StackConnection{
		StackName:    "conn-test",
		NebulaCACert: []byte("c"),
		NebulaCAKey:  []byte("k"),
		NebulaUICert: []byte("c"),
		NebulaUIKey:  []byte("k"),
		NebulaSubnet: "10.42.4.0/24",
		AgentToken:   "t",
	}
	require.NoError(t, store.Create(conn))

	require.NoError(t, store.UpdateAgentConnected("conn-test", "10.42.4.2", "89.1.2.3", `{"nodes":1}`))

	got, err := store.Get("conn-test")
	require.NoError(t, err)
	require.NotNil(t, got.AgentNebulaIP)
	assert.Equal(t, "10.42.4.2", *got.AgentNebulaIP)
	require.NotNil(t, got.AgentRealIP)
	assert.Equal(t, "89.1.2.3", *got.AgentRealIP)
	require.NotNil(t, got.ClusterInfo)
	assert.Equal(t, `{"nodes":1}`, *got.ClusterInfo)
	require.NotNil(t, got.LastSeenAt)
}

func TestUpdateLastSeen(t *testing.T) {
	store := setupTestConnStore(t)
	conn := &StackConnection{
		StackName:    "seen-test",
		NebulaCACert: []byte("c"),
		NebulaCAKey:  []byte("k"),
		NebulaUICert: []byte("c"),
		NebulaUIKey:  []byte("k"),
		NebulaSubnet: "10.42.5.0/24",
		AgentToken:   "t",
	}
	require.NoError(t, store.Create(conn))

	require.NoError(t, store.UpdateLastSeen("seen-test"))
	got, err := store.Get("seen-test")
	require.NoError(t, err)
	require.NotNil(t, got.LastSeenAt)
}

func TestDelete(t *testing.T) {
	store := setupTestConnStore(t)
	conn := &StackConnection{
		StackName:    "del-test",
		NebulaCACert: []byte("c"),
		NebulaCAKey:  []byte("k"),
		NebulaUICert: []byte("c"),
		NebulaUIKey:  []byte("k"),
		NebulaSubnet: "10.42.6.0/24",
		AgentToken:   "t",
	}
	require.NoError(t, store.Create(conn))

	require.NoError(t, store.Delete("del-test"))
	got, err := store.Get("del-test")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestAllocateSubnet_Sequential(t *testing.T) {
	store := setupTestConnStore(t)

	s1, err := store.AllocateSubnet()
	require.NoError(t, err)
	assert.Equal(t, "10.42.1.0/24", s1)

	s2, err := store.AllocateSubnet()
	require.NoError(t, err)
	assert.Equal(t, "10.42.2.0/24", s2)

	s3, err := store.AllocateSubnet()
	require.NoError(t, err)
	assert.Equal(t, "10.42.3.0/24", s3)
}

func TestUpdateLighthouse(t *testing.T) {
	store := setupTestConnStore(t)
	conn := &StackConnection{
		StackName:    "lh-test",
		NebulaCACert: []byte("c"),
		NebulaCAKey:  []byte("k"),
		NebulaUICert: []byte("c"),
		NebulaUIKey:  []byte("k"),
		NebulaSubnet: "10.42.9.0/24",
		AgentToken:   "t",
	}
	require.NoError(t, store.Create(conn))

	require.NoError(t, store.UpdateLighthouse("lh-test", "1.2.3.4:41820"))
	got, err := store.Get("lh-test")
	require.NoError(t, err)
	require.NotNil(t, got.LighthouseAddr)
	assert.Equal(t, "1.2.3.4:41820", *got.LighthouseAddr)
}
