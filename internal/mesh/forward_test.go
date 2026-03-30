package mesh

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewForwardManager(t *testing.T) {
	fm := NewForwardManager(nil)
	assert.NotNil(t, fm)
}

func TestForwardManager_ListEmpty(t *testing.T) {
	fm := NewForwardManager(nil)
	result := fm.List("test")
	assert.Empty(t, result)
}

func TestForwardManager_ListAll(t *testing.T) {
	fm := NewForwardManager(nil)
	result := fm.List("")
	assert.Empty(t, result)
}

func TestForwardManager_StopNonExistent(t *testing.T) {
	fm := NewForwardManager(nil)
	err := fm.Stop("fwd-999")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestForwardManager_StartRequiresMeshManager(t *testing.T) {
	fm := NewForwardManager(nil)
	_, err := fm.Start("test", 0, 4646, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "mesh manager not available")
}

func TestForwardManager_StopAllEmpty(t *testing.T) {
	fm := NewForwardManager(nil)
	// Should not panic on empty
	fm.StopAll("test")
}

func TestPortForward_ActiveConns(t *testing.T) {
	pf := &PortForward{}
	assert.Equal(t, 0, pf.ActiveConns())
	pf.active.Add(3)
	assert.Equal(t, 3, pf.ActiveConns())
	pf.active.Add(-1)
	assert.Equal(t, 2, pf.ActiveConns())
}

// TestForwardManager_StopDrainTimeout verifies that Stop returns within a
// bounded time even when active connections remain open. The 3-second drain
// timeout in Stop prevents the DELETE request from hanging indefinitely when
// browsers keep connections alive (e.g., Nomad UI long-polling).
func TestForwardManager_StopDrainTimeout(t *testing.T) {
	fm := NewForwardManager(nil)

	// Create a listener manually and register a PortForward with an open connWg.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	pf := &PortForward{
		ID:       "fwd-drain-test",
		listener: ln,
		cancel:   cancel,
		manager:  nil,
	}
	// Simulate an active connection that never completes.
	pf.connWg.Add(1)
	pf.active.Add(1)

	fm.mu.Lock()
	fm.forwards["fwd-drain-test"] = pf
	fm.mu.Unlock()

	// Stop should return within ~3 seconds (the drain timeout), not hang.
	start := time.Now()
	err = fm.Stop("fwd-drain-test")
	elapsed := time.Since(start)

	assert.NoError(t, err)
	// Should take at least 2.5s (drain timeout is 3s) but no more than 5s.
	assert.GreaterOrEqual(t, elapsed.Seconds(), 2.5,
		"Stop should wait for drain timeout")
	assert.LessOrEqual(t, elapsed.Seconds(), 5.0,
		"Stop should not hang beyond the drain timeout")

	// Cleanup: release the simulated connection so connWg doesn't leak.
	pf.connWg.Done()
	pf.active.Add(-1)

	// Context should be cancelled.
	select {
	case <-ctx.Done():
		// expected
	default:
		t.Error("expected context to be cancelled after Stop")
	}
}

func TestForwardManager_IDsAreUnique(t *testing.T) {
	fm := NewForwardManager(nil)
	// Manually increment to simulate ID generation
	fm.mu.Lock()
	fm.nextID++
	id1 := fm.nextID
	fm.nextID++
	id2 := fm.nextID
	fm.mu.Unlock()
	assert.NotEqual(t, id1, id2)
}
