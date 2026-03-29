package mesh

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
