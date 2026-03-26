package db

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/trustos/pulumi-ui/internal/crypto"
)

func setupNodeCertTest(t *testing.T) *NodeCertStore {
	t.Helper()
	database, err := Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	require.NoError(t, Migrate(database))

	enc, err := crypto.NewEncryptor("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	return NewNodeCertStore(database, enc)
}

func makeTestNodeCerts(stackName string, n int) []*NodeCert {
	certs := make([]*NodeCert, n)
	for i := 0; i < n; i++ {
		certs[i] = &NodeCert{
			StackName:  stackName,
			NodeIndex:  i,
			NebulaCert: []byte("cert-pem-" + stackName + "-" + string(rune('0'+i))),
			NebulaKey:  []byte("key-pem-" + stackName + "-" + string(rune('0'+i))),
			NebulaIP:   "10.42.1." + string(rune('2'+i)) + "/24",
		}
	}
	return certs
}

func TestNodeCertStore_CreateAll_And_ListForStack(t *testing.T) {
	s := setupNodeCertTest(t)

	certs := makeTestNodeCerts("my-stack", 5)
	require.NoError(t, s.CreateAll(certs))

	got, err := s.ListForStack("my-stack")
	require.NoError(t, err)
	require.Len(t, got, 5)

	for i, c := range got {
		assert.Equal(t, i, c.NodeIndex)
		assert.Equal(t, "my-stack", c.StackName)
		assert.Equal(t, certs[i].NebulaCert, c.NebulaCert)
		assert.Equal(t, certs[i].NebulaKey, c.NebulaKey)
		assert.Equal(t, certs[i].NebulaIP, c.NebulaIP)
		assert.Nil(t, c.AgentRealIP)
	}
}

func TestNodeCertStore_ListForStack_OrderedByIndex(t *testing.T) {
	s := setupNodeCertTest(t)

	// Insert in reverse order to confirm ORDER BY works.
	certs := makeTestNodeCerts("ordered-stack", 4)
	reversed := []*NodeCert{certs[3], certs[1], certs[2], certs[0]}
	require.NoError(t, s.CreateAll(reversed))

	got, err := s.ListForStack("ordered-stack")
	require.NoError(t, err)
	require.Len(t, got, 4)
	for i, c := range got {
		assert.Equal(t, i, c.NodeIndex, "node_index should be ascending")
	}
}

func TestNodeCertStore_ListForStack_UnknownStack(t *testing.T) {
	s := setupNodeCertTest(t)
	got, err := s.ListForStack("nonexistent")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestNodeCertStore_UpdateAgentRealIP(t *testing.T) {
	s := setupNodeCertTest(t)

	certs := makeTestNodeCerts("ip-stack", 3)
	require.NoError(t, s.CreateAll(certs))

	require.NoError(t, s.UpdateAgentRealIP("ip-stack", 1, "1.2.3.4"))

	got, err := s.ListForStack("ip-stack")
	require.NoError(t, err)
	require.Len(t, got, 3)

	assert.Nil(t, got[0].AgentRealIP)
	require.NotNil(t, got[1].AgentRealIP)
	assert.Equal(t, "1.2.3.4", *got[1].AgentRealIP)
	assert.Nil(t, got[2].AgentRealIP)
}

func TestNodeCertStore_Delete(t *testing.T) {
	s := setupNodeCertTest(t)

	certs := makeTestNodeCerts("del-stack", 10)
	require.NoError(t, s.CreateAll(certs))

	got, err := s.ListForStack("del-stack")
	require.NoError(t, err)
	assert.Len(t, got, 10)

	require.NoError(t, s.Delete("del-stack"))

	got, err = s.ListForStack("del-stack")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestNodeCertStore_KeyEncryption(t *testing.T) {
	s := setupNodeCertTest(t)

	plainKey := []byte("super-secret-nebula-key")
	certs := []*NodeCert{{
		StackName:  "enc-stack",
		NodeIndex:  0,
		NebulaCert: []byte("cert"),
		NebulaKey:  plainKey,
		NebulaIP:   "10.42.1.2/24",
	}}
	require.NoError(t, s.CreateAll(certs))

	// Raw DB should not contain the plain-text key.
	var raw []byte
	err := s.db.QueryRow(`SELECT nebula_key FROM stack_node_certs WHERE stack_name = ?`, "enc-stack").Scan(&raw)
	require.NoError(t, err)
	assert.NotEqual(t, plainKey, raw, "key must be encrypted at rest")

	// But reading via store should decrypt correctly.
	got, err := s.ListForStack("enc-stack")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, plainKey, got[0].NebulaKey)
}

func TestNodeCertStore_MultipleStacks_Isolated(t *testing.T) {
	s := setupNodeCertTest(t)

	require.NoError(t, s.CreateAll(makeTestNodeCerts("stack-a", 3)))
	require.NoError(t, s.CreateAll(makeTestNodeCerts("stack-b", 5)))

	a, err := s.ListForStack("stack-a")
	require.NoError(t, err)
	assert.Len(t, a, 3)

	b, err := s.ListForStack("stack-b")
	require.NoError(t, err)
	assert.Len(t, b, 5)

	// Deleting stack-a should not affect stack-b.
	require.NoError(t, s.Delete("stack-a"))
	b2, err := s.ListForStack("stack-b")
	require.NoError(t, err)
	assert.Len(t, b2, 5)
}
