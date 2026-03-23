package agentinject

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGzipBase64_RoundTrip(t *testing.T) {
	original := []byte("#!/bin/bash\necho hello world\n")
	encoded := GzipBase64(original)

	assert.NotEmpty(t, encoded)
	assert.NotContains(t, encoded, "\n")

	decoded, ok := DecodeUserData(encoded)
	require.True(t, ok)
	assert.Equal(t, original, decoded)
}

func TestComposeMultipart_BothScripts(t *testing.T) {
	program := []byte("#!/bin/bash\necho program\n")
	agent := []byte("#!/bin/bash\n# PULUMI_UI_AGENT_BOOTSTRAP\necho agent\n")

	composed := ComposeMultipart(program, agent)
	body := string(composed)

	assert.Contains(t, body, "multipart/mixed")
	assert.Contains(t, body, mimeBoundary)
	assert.Contains(t, body, "echo program")
	assert.Contains(t, body, "echo agent")
	assert.Equal(t, 2, strings.Count(body, "text/x-shellscript"))
}

func TestComposeMultipart_AgentOnly(t *testing.T) {
	agent := []byte("#!/bin/bash\necho agent\n")
	composed := ComposeMultipart(nil, agent)
	body := string(composed)

	assert.Contains(t, body, "echo agent")
	assert.Equal(t, 1, strings.Count(body, "text/x-shellscript"))
}

func TestComposeAndEncode_Decodable(t *testing.T) {
	program := []byte("#!/bin/bash\necho test\n")
	agent := []byte("#!/bin/bash\n# PULUMI_UI_AGENT_BOOTSTRAP\n")

	encoded := ComposeAndEncode(program, agent)
	decoded, ok := DecodeUserData(encoded)
	require.True(t, ok)

	assert.Contains(t, string(decoded), "echo test")
	assert.Contains(t, string(decoded), AgentBootstrapMarker)
}

func TestHasAgentBootstrap_Positive(t *testing.T) {
	script := []byte("#!/bin/bash\n# PULUMI_UI_AGENT_BOOTSTRAP\necho setup\n")
	assert.True(t, HasAgentBootstrap(script))
}

func TestHasAgentBootstrap_Negative(t *testing.T) {
	script := []byte("#!/bin/bash\necho regular script\n")
	assert.False(t, HasAgentBootstrap(script))
}

func TestDecodeUserData_InvalidBase64(t *testing.T) {
	_, ok := DecodeUserData("not-valid-base64!!!")
	assert.False(t, ok)
}

func TestDecodeUserData_PlainBase64(t *testing.T) {
	// Non-gzipped base64 content
	decoded, ok := DecodeUserData("aGVsbG8=") // "hello"
	require.True(t, ok)
	assert.Equal(t, []byte("hello"), decoded)
}
