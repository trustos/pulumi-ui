package mesh

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestForwardManager_Get_Found(t *testing.T) {
	fm := NewForwardManager(nil)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	_, cancel := context.WithCancel(context.Background())
	pf := &PortForward{
		ID:        "fwd-1",
		StackName: "test-stack",
		LocalPort: ln.Addr().(*net.TCPAddr).Port,
		listener:  ln,
		cancel:    cancel,
	}

	fm.mu.Lock()
	fm.forwards["fwd-1"] = pf
	fm.mu.Unlock()

	got, ok := fm.Get("fwd-1")
	assert.True(t, ok)
	assert.Equal(t, "fwd-1", got.ID)
	assert.Equal(t, "test-stack", got.StackName)
}

func TestForwardManager_Get_NotFound(t *testing.T) {
	fm := NewForwardManager(nil)
	got, ok := fm.Get("fwd-999")
	assert.False(t, ok)
	assert.Nil(t, got)
}

func TestForwardManager_Get_WrongID(t *testing.T) {
	fm := NewForwardManager(nil)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	_, cancel := context.WithCancel(context.Background())
	pf := &PortForward{
		ID:        "fwd-1",
		StackName: "test-stack",
		listener:  ln,
		cancel:    cancel,
	}

	fm.mu.Lock()
	fm.forwards["fwd-1"] = pf
	fm.mu.Unlock()

	got, ok := fm.Get("fwd-2")
	assert.False(t, ok)
	assert.Nil(t, got)
}
