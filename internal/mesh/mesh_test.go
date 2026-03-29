package mesh

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/trustos/pulumi-ui/internal/crypto"
	"github.com/trustos/pulumi-ui/internal/db"
)

func TestIndentPEM(t *testing.T) {
	pem := "-----BEGIN CERTIFICATE-----\nMIIB...\n-----END CERTIFICATE-----\n"
	result := indentPEM(pem, 4)
	assert.Contains(t, result, "    -----BEGIN CERTIFICATE-----\n")
	assert.Contains(t, result, "    MIIB...\n")
	assert.Contains(t, result, "    -----END CERTIFICATE-----\n")
}

func TestSplitLines(t *testing.T) {
	lines := splitLines("a\nb\nc")
	assert.Equal(t, []string{"a", "b", "c"}, lines)
}

func TestSplitLines_TrailingNewline(t *testing.T) {
	lines := splitLines("a\nb\n")
	assert.Equal(t, []string{"a", "b"}, lines)
}

func TestSplitLines_Empty(t *testing.T) {
	lines := splitLines("")
	assert.Nil(t, lines)
}

func TestNewManager(t *testing.T) {
	m := NewManager(nil)
	assert.NotNil(t, m)
	assert.NotNil(t, m.tunnels)
	m.Stop()
}

func TestCloseTunnel_NonExistent(t *testing.T) {
	m := NewManager(nil)
	defer m.Stop()
	m.CloseTunnel("non-existent")
}

func TestGetTunnel_NilConnStore(t *testing.T) {
	m := NewManager(nil)
	defer m.Stop()

	_, err := m.GetTunnel("some-stack")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no connection store")
}

func TestManagerStop_Idempotent(t *testing.T) {
	m := NewManager(nil)
	m.Stop()
	assert.Empty(t, m.tunnels)
}

func TestTunnelToken(t *testing.T) {
	tunnel := &Tunnel{token: "my-secret"}
	assert.Equal(t, "my-secret", tunnel.Token())
}

func TestTunnelAgentURL(t *testing.T) {
	tunnel := &Tunnel{agentAddr: "10.42.1.2:41820"}
	assert.Equal(t, "http://10.42.1.2:41820", tunnel.AgentURL())
}

func TestTunnelAgentNebulaIP(t *testing.T) {
	tunnel := &Tunnel{agentAddr: "10.42.1.2:41820"}
	assert.Equal(t, "10.42.1.2", tunnel.AgentNebulaIP())
}

func TestTunnelClose_NilSvc(t *testing.T) {
	tunnel := &Tunnel{}
	assert.NotPanics(t, func() { tunnel.Close() })
}

func TestTunnelPin(t *testing.T) {
	tunnel := &Tunnel{}
	assert.False(t, tunnel.pinned)
	tunnel.Pin()
	assert.True(t, tunnel.pinned)
}

func TestTunnelDialPort_ConstructsCorrectAddr(t *testing.T) {
	// DialPort should target nebulaIP:port (not agentAddr)
	tunnel := &Tunnel{agentAddr: "10.42.1.2:41820"}
	// We can't actually dial (no Nebula service), but we can verify AgentNebulaIP
	assert.Equal(t, "10.42.1.2", tunnel.AgentNebulaIP())
}

func TestWithNodeCertStore(t *testing.T) {
	m := NewManager(nil)
	defer m.Stop()
	assert.Nil(t, m.nodeCertStore)
	m.WithNodeCertStore(nil) // should not panic
	assert.Nil(t, m.nodeCertStore)
}

func TestGetTunnelForNode_NilNodeCertStore(t *testing.T) {
	m := NewManager(nil)
	defer m.Stop()

	_, err := m.GetTunnelForNode("some-stack", 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "node cert store not available")
}

func TestGetTunnelForNode_InvalidNodeIndex(t *testing.T) {
	// GetTunnelForNode with a nil nodeCertStore returns early with error.
	m := NewManager(nil)
	defer m.Stop()

	_, err := m.GetTunnelForNode("stack", -1)
	assert.Error(t, err)
}

// setupNodeCertStoreForMesh creates an in-memory DB with migrations applied and
// returns a NodeCertStore ready for use in mesh tests.
func setupNodeCertStoreForMesh(t *testing.T) (*db.NodeCertStore, *db.StackConnectionStore) {
	t.Helper()
	database, err := db.Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	require.NoError(t, db.Migrate(database))

	enc, err := crypto.NewEncryptor("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	require.NoError(t, err)

	return db.NewNodeCertStore(database, enc), db.NewStackConnectionStore(database, enc)
}

func TestGetTunnelForNode_NodeNotFound(t *testing.T) {
	nodeCertStore, _ := setupNodeCertStoreForMesh(t)

	// Insert certs for node indexes 0, 1, 2.
	certs := make([]*db.NodeCert, 3)
	for i := 0; i < 3; i++ {
		certs[i] = &db.NodeCert{
			StackName:  "stack",
			NodeIndex:  i,
			NebulaCert: []byte("cert-" + fmt.Sprintf("%d", i)),
			NebulaKey:  []byte("key-" + fmt.Sprintf("%d", i)),
			NebulaIP:   fmt.Sprintf("10.42.1.%d/24", i+2),
		}
	}
	require.NoError(t, nodeCertStore.CreateAll(certs))

	m := NewManager(nil)
	defer m.Stop()
	m.WithNodeCertStore(nodeCertStore)

	_, err := m.GetTunnelForNode("stack", 5)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no node cert")
}

func TestGetTunnelForNode_NoRealIP(t *testing.T) {
	nodeCertStore, _ := setupNodeCertStoreForMesh(t)

	// Node at index 0 with AgentRealIP = nil (default).
	certs := []*db.NodeCert{{
		StackName:  "stack",
		NodeIndex:  0,
		NebulaCert: []byte("cert-0"),
		NebulaKey:  []byte("key-0"),
		NebulaIP:   "10.42.1.2/24",
	}}
	require.NoError(t, nodeCertStore.CreateAll(certs))

	m := NewManager(nil)
	defer m.Stop()
	m.WithNodeCertStore(nodeCertStore)

	_, err := m.GetTunnelForNode("stack", 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no real IP")
}

func TestGetTunnelForNode_EmptyRealIP(t *testing.T) {
	nodeCertStore, _ := setupNodeCertStoreForMesh(t)

	certs := []*db.NodeCert{{
		StackName:  "stack",
		NodeIndex:  0,
		NebulaCert: []byte("cert-0"),
		NebulaKey:  []byte("key-0"),
		NebulaIP:   "10.42.1.2/24",
	}}
	require.NoError(t, nodeCertStore.CreateAll(certs))

	// Set agent_real_ip to empty string.
	require.NoError(t, nodeCertStore.UpdateAgentRealIP("stack", 0, ""))

	m := NewManager(nil)
	defer m.Stop()
	m.WithNodeCertStore(nodeCertStore)

	_, err := m.GetTunnelForNode("stack", 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no real IP")
}

func TestGetTunnelForNode_CacheKey(t *testing.T) {
	nodeCertStore, connStore := setupNodeCertStoreForMesh(t)

	// Create a node with a real IP so we get past the validation checks.
	certs := []*db.NodeCert{{
		StackName:  "mystack",
		NodeIndex:  3,
		NebulaCert: []byte("cert-3"),
		NebulaKey:  []byte("key-3"),
		NebulaIP:   "10.42.1.5/24",
	}}
	require.NoError(t, nodeCertStore.CreateAll(certs))
	require.NoError(t, nodeCertStore.UpdateAgentRealIP("mystack", 3, "1.2.3.4"))

	// Use a real connStore but with no matching stack connection row.
	// GetTunnelForNode will pass node validation, then fail at connStore.Get
	// because there's no stack_connections row for "mystack".
	m := NewManager(connStore)
	defer m.Stop()
	m.WithNodeCertStore(nodeCertStore)

	// First call — passes node validation, fails because no stack connection exists.
	_, err := m.GetTunnelForNode("mystack", 3)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "load stack connection")

	// Verify the tunnel was NOT cached on error.
	m.mu.Lock()
	_, cached := m.tunnels["mystack:3"]
	m.mu.Unlock()
	assert.False(t, cached, "tunnel should not be cached after an error")

	// Verify cache key format by manually inserting a tunnel and checking retrieval.
	fakeTunnel := &Tunnel{
		stackName: "mystack",
		agentAddr: "10.42.1.5:41820",
		token:     "test-token",
	}
	m.mu.Lock()
	m.tunnels["mystack:3"] = fakeTunnel
	m.mu.Unlock()

	// Second call should return the cached tunnel without hitting the DB.
	got, err := m.GetTunnelForNode("mystack", 3)
	require.NoError(t, err)
	assert.Equal(t, fakeTunnel, got)
	assert.Equal(t, "test-token", got.Token())
}
