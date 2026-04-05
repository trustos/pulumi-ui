package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/trustos/pulumi-ui/internal/crypto"
	"github.com/trustos/pulumi-ui/internal/db"
)

// ---------------------------------------------------------------------------
// computeDeployed
// ---------------------------------------------------------------------------

func TestComputeDeployed(t *testing.T) {
	type op struct {
		operation string
		status    string
		startedAt int64
	}
	makeOps := func(in []op) []db.Operation {
		out := make([]db.Operation, len(in))
		for i, o := range in {
			out[i] = db.Operation{Operation: o.operation, Status: o.status, StartedAt: o.startedAt}
		}
		return out
	}

	tests := []struct {
		name string
		ops  []op
		want bool
	}{
		{
			name: "no operations — never deployed",
			ops:  nil,
			want: false,
		},
		{
			name: "successful up only",
			ops:  []op{{"up", "succeeded", 100}},
			want: true,
		},
		{
			name: "failed up only",
			ops:  []op{{"up", "failed", 100}},
			want: false,
		},
		{
			name: "up then destroy — not deployed",
			ops: []op{
				{"destroy", "succeeded", 200},
				{"up", "succeeded", 100},
			},
			want: false,
		},
		{
			name: "up then destroy then up again — deployed",
			ops: []op{
				{"up", "succeeded", 300},
				{"destroy", "succeeded", 200},
				{"up", "succeeded", 100},
			},
			want: true,
		},
		{
			name: "destroy then refresh — not deployed",
			ops: []op{
				{"refresh", "succeeded", 300},
				{"destroy", "succeeded", 200},
				{"up", "succeeded", 100},
			},
			want: false,
		},
		{
			name: "up then failed destroy — still deployed",
			ops: []op{
				{"destroy", "failed", 200},
				{"up", "succeeded", 100},
			},
			want: true,
		},
		{
			name: "destroy only (no prior up) — not deployed",
			ops:  []op{{"destroy", "succeeded", 100}},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, computeDeployed(makeOps(tc.ops)))
		})
	}
}

// ---------------------------------------------------------------------------
// computeDeployedState — wasDeployed field
// ---------------------------------------------------------------------------

func TestComputeDeployedState_WasDeployed(t *testing.T) {
	type op struct {
		operation string
		status    string
		startedAt int64
	}
	makeOps := func(in []op) []db.Operation {
		out := make([]db.Operation, len(in))
		for i, o := range in {
			out[i] = db.Operation{Operation: o.operation, Status: o.status, StartedAt: o.startedAt}
		}
		return out
	}

	tests := []struct {
		name        string
		ops         []op
		deployed    bool
		wasDeployed bool
	}{
		{
			name:        "no ops — never deployed",
			ops:         nil,
			deployed:    false,
			wasDeployed: false,
		},
		{
			name:        "preview + refresh only — never deployed",
			ops:         []op{{"refresh", "succeeded", 200}, {"preview", "succeeded", 100}},
			deployed:    false,
			wasDeployed: false,
		},
		{
			name:        "up succeeded — deployed and wasDeployed",
			ops:         []op{{"up", "succeeded", 100}},
			deployed:    true,
			wasDeployed: true,
		},
		{
			name:        "up then destroy — not deployed but wasDeployed",
			ops:         []op{{"destroy", "succeeded", 200}, {"up", "succeeded", 100}},
			deployed:    false,
			wasDeployed: true,
		},
		{
			name:        "up then destroy then refresh — not deployed but wasDeployed",
			ops:         []op{{"refresh", "succeeded", 300}, {"destroy", "succeeded", 200}, {"up", "succeeded", 100}},
			deployed:    false,
			wasDeployed: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, wd := computeDeployedState(makeOps(tc.ops))
			assert.Equal(t, tc.deployed, d, "deployed")
			assert.Equal(t, tc.wasDeployed, wd, "wasDeployed")
		})
	}
}

// ---------------------------------------------------------------------------
// lastOperationType population
// ---------------------------------------------------------------------------

func TestLastOperationType(t *testing.T) {
	// ops sorted descending by started_at (as ListForStack returns them)
	ops := []db.Operation{
		{Operation: "refresh", Status: "succeeded", StartedAt: 300},
		{Operation: "destroy", Status: "succeeded", StartedAt: 200},
		{Operation: "up", Status: "succeeded", StartedAt: 100},
	}
	// The most recent op (index 0) should be the lastOperationType
	assert.Equal(t, "refresh", ops[0].Operation)
	_, wasDeployed := computeDeployedState(ops)
	assert.True(t, wasDeployed, "destroy→refresh: wasDeployed should be true")
	assert.False(t, computeDeployed(ops), "destroy→refresh should not be deployed")
}

func setupTestHandler(t *testing.T) *StackHandler {
	t.Helper()
	database, err := db.Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { database.Close() })
	require.NoError(t, db.Migrate(database))

	enc, err := crypto.NewEncryptor("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	require.NoError(t, err)

	connStore := db.NewStackConnectionStore(database, enc)
	return &StackHandler{ConnStore: connStore}
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
