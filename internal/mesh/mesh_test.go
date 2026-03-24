package mesh

import (
	"testing"

	"github.com/stretchr/testify/assert"
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

	assert.Panics(t, func() {
		_, _ = m.GetTunnel("some-stack")
	}, "nil connStore should cause a panic")
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

func TestTunnelClose_NilSvc(t *testing.T) {
	tunnel := &Tunnel{}
	assert.NotPanics(t, func() { tunnel.Close() })
}
