package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRemoveStackState_CleansAllFiles(t *testing.T) {
	stateDir := t.TempDir()
	e := &Engine{stateDir: stateDir}

	project := "test-vcn"
	stack := "my-stack"

	// Seed the directory structure that Pulumi's file backend creates.
	stackDir := filepath.Join(stateDir, ".pulumi", "stacks", project)
	histDir := filepath.Join(stateDir, ".pulumi", "history", project, stack)
	backupDir := filepath.Join(stateDir, ".pulumi", "backups", project, stack)

	require.NoError(t, os.MkdirAll(stackDir, 0755))
	require.NoError(t, os.MkdirAll(histDir, 0755))
	require.NoError(t, os.MkdirAll(backupDir, 0755))

	require.NoError(t, os.WriteFile(filepath.Join(stackDir, stack+".json"), []byte(`{}`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, stack+".json.bak"), []byte(`{}`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(histDir, "1.json"), []byte(`{}`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(backupDir, "1.json"), []byte(`{}`), 0644))

	err := e.RemoveStackState(stack, project)
	require.NoError(t, err)

	assert.NoFileExists(t, filepath.Join(stackDir, stack+".json"))
	assert.NoFileExists(t, filepath.Join(stackDir, stack+".json.bak"))
	assert.NoDirExists(t, histDir)
	assert.NoDirExists(t, backupDir)
}

func TestRemoveStackState_NoopWhenMissing(t *testing.T) {
	stateDir := t.TempDir()
	e := &Engine{stateDir: stateDir}

	err := e.RemoveStackState("nonexistent", "nonexistent-program")
	assert.NoError(t, err)
}

func TestRemoveStackState_LeavesOtherStacks(t *testing.T) {
	stateDir := t.TempDir()
	e := &Engine{stateDir: stateDir}

	project := "test-vcn"

	stackDir := filepath.Join(stateDir, ".pulumi", "stacks", project)
	require.NoError(t, os.MkdirAll(stackDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, "keep-me.json"), []byte(`{}`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(stackDir, "delete-me.json"), []byte(`{}`), 0644))

	err := e.RemoveStackState("delete-me", project)
	require.NoError(t, err)

	assert.NoFileExists(t, filepath.Join(stackDir, "delete-me.json"))
	assert.FileExists(t, filepath.Join(stackDir, "keep-me.json"))
}

func TestRemoveStackState_LeavesOtherProjects(t *testing.T) {
	stateDir := t.TempDir()
	e := &Engine{stateDir: stateDir}

	// Two different projects, same stack name.
	for _, proj := range []string{"test-vcn", "nomad-cluster"} {
		dir := filepath.Join(stateDir, ".pulumi", "stacks", proj)
		require.NoError(t, os.MkdirAll(dir, 0755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "prod.json"), []byte(`{}`), 0644))
	}

	err := e.RemoveStackState("prod", "test-vcn")
	require.NoError(t, err)

	assert.NoFileExists(t, filepath.Join(stateDir, ".pulumi", "stacks", "test-vcn", "prod.json"))
	assert.FileExists(t, filepath.Join(stateDir, ".pulumi", "stacks", "nomad-cluster", "prod.json"))
}

func TestProjectIsolation(t *testing.T) {
	stateDir := t.TempDir()

	// Simulate what Pulumi's file backend does: state files are stored under
	// .pulumi/stacks/{projectName}/{stackName}.json.
	// With per-program project names, "test-vcn" and "nomad-cluster" using
	// the same stack name "test" should produce separate state files.

	for _, proj := range []string{"test-vcn", "nomad-cluster"} {
		dir := filepath.Join(stateDir, ".pulumi", "stacks", proj)
		require.NoError(t, os.MkdirAll(dir, 0755))
		require.NoError(t, os.WriteFile(
			filepath.Join(dir, "test.json"),
			[]byte(`{"project":"`+proj+`"}`),
			0644,
		))
	}

	// Verify each project has its own state.
	vcnState, err := os.ReadFile(filepath.Join(stateDir, ".pulumi", "stacks", "test-vcn", "test.json"))
	require.NoError(t, err)
	assert.Contains(t, string(vcnState), `"test-vcn"`)

	nomadState, err := os.ReadFile(filepath.Join(stateDir, ".pulumi", "stacks", "nomad-cluster", "test.json"))
	require.NoError(t, err)
	assert.Contains(t, string(nomadState), `"nomad-cluster"`)

	// Deleting one project's stack doesn't affect the other.
	e := &Engine{stateDir: stateDir}
	require.NoError(t, e.RemoveStackState("test", "test-vcn"))

	assert.NoFileExists(t, filepath.Join(stateDir, ".pulumi", "stacks", "test-vcn", "test.json"))
	assert.FileExists(t, filepath.Join(stateDir, ".pulumi", "stacks", "nomad-cluster", "test.json"))
}
