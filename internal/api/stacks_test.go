package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/trustos/pulumi-ui/internal/crypto"
	"github.com/trustos/pulumi-ui/internal/db"
)

func setupTestHandler(t *testing.T) *Handler {
	t.Helper()
	database, err := db.Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	require.NoError(t, db.Migrate(database))

	enc, err := crypto.NewEncryptor("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	require.NoError(t, err)

	connStore := db.NewStackConnectionStore(database, enc)
	return &Handler{ConnStore: connStore}
}

func TestGenerateNebulaPKI_ProducesValidCertAndToken(t *testing.T) {
	h := setupTestHandler(t)

	err := h.generateNebulaPKI("test-stack")
	require.NoError(t, err)

	conn, err := h.ConnStore.Get("test-stack")
	require.NoError(t, err)
	require.NotNil(t, conn)

	assert.Equal(t, "test-stack", conn.StackName)
	assert.Contains(t, string(conn.NebulaCACert), "NEBULA CERTIFICATE")
	assert.Contains(t, string(conn.NebulaCAKey), "NEBULA")
	assert.Contains(t, string(conn.NebulaUICert), "NEBULA CERTIFICATE")
	assert.Contains(t, string(conn.NebulaUIKey), "NEBULA")
	assert.Contains(t, string(conn.NebulaAgentCert), "NEBULA CERTIFICATE")
	assert.Contains(t, string(conn.NebulaAgentKey), "NEBULA")
	assert.NotEmpty(t, conn.NebulaSubnet)
	assert.Len(t, conn.AgentToken, 64, "token should be 32 bytes = 64 hex chars")
}

func TestGenerateNebulaPKI_AllocatesDistinctSubnets(t *testing.T) {
	h := setupTestHandler(t)

	require.NoError(t, h.generateNebulaPKI("stack-1"))
	require.NoError(t, h.generateNebulaPKI("stack-2"))

	c1, err := h.ConnStore.Get("stack-1")
	require.NoError(t, err)
	c2, err := h.ConnStore.Get("stack-2")
	require.NoError(t, err)

	assert.NotEqual(t, c1.NebulaSubnet, c2.NebulaSubnet, "each stack must get a distinct subnet")
}

func TestGenerateNebulaPKI_SkipsWhenExisting(t *testing.T) {
	h := setupTestHandler(t)

	require.NoError(t, h.generateNebulaPKI("existing"))

	conn1, err := h.ConnStore.Get("existing")
	require.NoError(t, err)
	origToken := conn1.AgentToken

	err = h.generateNebulaPKI("existing")
	assert.Error(t, err, "second insert for the same stack_name should fail (UNIQUE constraint)")

	conn2, err := h.ConnStore.Get("existing")
	require.NoError(t, err)
	assert.Equal(t, origToken, conn2.AgentToken, "original connection should remain unchanged")
}
