package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthMiddleware_ValidToken(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := authMiddleware("secret-token", inner)

	req := httptest.NewRequest("GET", "/health", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := authMiddleware("secret-token", inner)

	req := httptest.NewRequest("GET", "/health", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestAuthMiddleware_MissingHeader(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := authMiddleware("secret-token", inner)

	req := httptest.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestHandleHealth(t *testing.T) {
	req := httptest.NewRequest("GET", "/health", nil)
	rr := httptest.NewRecorder()

	handleHealth(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)

	var resp healthResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	assert.Equal(t, "ok", resp.Status)
	assert.NotEmpty(t, resp.Hostname)
	assert.NotEmpty(t, resp.OS)
	assert.NotEmpty(t, resp.Arch)
}

func TestHandleUpload_MissingDestPath(t *testing.T) {
	req := httptest.NewRequest("POST", "/upload", strings.NewReader("file content"))
	rr := httptest.NewRecorder()

	handleUpload(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Contains(t, rr.Body.String(), "X-Dest-Path")
}

func TestHandleUpload_Success(t *testing.T) {
	dir := t.TempDir()
	destPath := filepath.Join(dir, "subdir", "test.txt")

	req := httptest.NewRequest("POST", "/upload", strings.NewReader("hello world"))
	req.Header.Set("X-Dest-Path", destPath)
	req.Header.Set("X-File-Mode", "0755")
	rr := httptest.NewRecorder()

	handleUpload(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)

	data, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Equal(t, "hello world", string(data))
}

func TestHandleExec_MissingCommand(t *testing.T) {
	req := httptest.NewRequest("POST", "/exec", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()

	handleExec(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandleExec_InvalidJSON(t *testing.T) {
	req := httptest.NewRequest("POST", "/exec", strings.NewReader(`not json`))
	rr := httptest.NewRecorder()

	handleExec(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// ── Nomad structs JSON serialization ─────────────────────────────────────

func TestNomadPortJSON(t *testing.T) {
	p := nomadPort{Label: "http", Value: 28000, To: 80}
	data, err := json.Marshal(p)
	require.NoError(t, err)

	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, "http", decoded["label"])
	assert.Equal(t, float64(28000), decoded["value"])
	assert.Equal(t, float64(80), decoded["to"])
}

func TestNomadJobSummaryJSON_WithPorts(t *testing.T) {
	s := nomadJobSummary{
		Name:   "traefik",
		Status: "running",
		Type:   "service",
		Ports: []nomadPort{
			{Label: "http", Value: 80, To: 80},
			{Label: "https", Value: 443, To: 443},
			{Label: "api", Value: 8080, To: 8080},
		},
	}
	data, err := json.Marshal(s)
	require.NoError(t, err)

	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, "traefik", decoded["name"])
	assert.Equal(t, "running", decoded["status"])
	assert.Equal(t, "service", decoded["type"])

	ports := decoded["ports"].([]interface{})
	assert.Len(t, ports, 3)
	assert.Equal(t, "http", ports[0].(map[string]interface{})["label"])
	assert.Equal(t, "api", ports[2].(map[string]interface{})["label"])
	assert.Equal(t, float64(8080), ports[2].(map[string]interface{})["value"])
}

func TestNomadJobSummaryJSON_EmptyPorts(t *testing.T) {
	s := nomadJobSummary{
		Name:   "app",
		Status: "running",
		Type:   "service",
		Ports:  []nomadPort{},
	}
	data, err := json.Marshal(s)
	require.NoError(t, err)

	// Empty slice is omitted by omitempty — same behavior as nil
	assert.NotContains(t, string(data), "ports")

	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Nil(t, decoded["ports"])
}

func TestNomadJobSummaryJSON_NoPorts(t *testing.T) {
	s := nomadJobSummary{
		Name:   "batch-job",
		Status: "dead",
		Type:   "batch",
	}
	data, err := json.Marshal(s)
	require.NoError(t, err)

	// Ports should be omitted from JSON when nil (omitempty)
	assert.NotContains(t, string(data), "ports")

	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Nil(t, decoded["ports"])
}

// ── Two-step alloc lookup struct tests ───────────────────────────────────
// getAllocPorts uses a two-step approach: (1) list alloc IDs via
// /v1/job/{id}/allocations (minimal fields), then (2) fetch full details
// via /v1/allocation/{id} to get ports. These tests verify the JSON
// structs used in each step deserialize correctly from real Nomad payloads.

func TestAllocListDeserialization(t *testing.T) {
	// Step 1 response: list endpoint returns ID + ClientStatus (among others)
	payload := `[
		{"ID": "abc-123", "ClientStatus": "running", "TaskGroup": "web"},
		{"ID": "def-456", "ClientStatus": "complete", "TaskGroup": "web"}
	]`

	var allocList []struct {
		ID           string `json:"ID"`
		ClientStatus string `json:"ClientStatus"`
	}
	require.NoError(t, json.Unmarshal([]byte(payload), &allocList))
	assert.Len(t, allocList, 2)
	assert.Equal(t, "abc-123", allocList[0].ID)
	assert.Equal(t, "running", allocList[0].ClientStatus)
	assert.Equal(t, "def-456", allocList[1].ID)
	assert.Equal(t, "complete", allocList[1].ClientStatus)
}

func TestAllocDetailDeserialization(t *testing.T) {
	// Step 2 response: full allocation detail includes AllocatedResources.Shared.Ports
	payload := `{
		"ID": "abc-123",
		"AllocatedResources": {
			"Shared": {
				"Ports": [
					{"Label": "http", "Value": 28000, "To": 80},
					{"Label": "https", "Value": 28001, "To": 443}
				]
			}
		}
	}`

	var detail struct {
		AllocatedResources struct {
			Shared struct {
				Ports []struct {
					Label string `json:"Label"`
					Value int    `json:"Value"`
					To    int    `json:"To"`
				} `json:"Ports"`
			} `json:"Shared"`
		} `json:"AllocatedResources"`
	}
	require.NoError(t, json.Unmarshal([]byte(payload), &detail))

	ports := detail.AllocatedResources.Shared.Ports
	assert.Len(t, ports, 2)
	assert.Equal(t, "http", ports[0].Label)
	assert.Equal(t, 28000, ports[0].Value)
	assert.Equal(t, 80, ports[0].To)
	assert.Equal(t, "https", ports[1].Label)
	assert.Equal(t, 28001, ports[1].Value)
	assert.Equal(t, 443, ports[1].To)
}

func TestAllocDetailDeserialization_NoPorts(t *testing.T) {
	// Allocation that has no port mappings (e.g., batch job)
	payload := `{
		"ID": "xyz-789",
		"AllocatedResources": {
			"Shared": {
				"Ports": []
			}
		}
	}`

	var detail struct {
		AllocatedResources struct {
			Shared struct {
				Ports []struct {
					Label string `json:"Label"`
					Value int    `json:"Value"`
					To    int    `json:"To"`
				} `json:"Ports"`
			} `json:"Shared"`
		} `json:"AllocatedResources"`
	}
	require.NoError(t, json.Unmarshal([]byte(payload), &detail))
	assert.Empty(t, detail.AllocatedResources.Shared.Ports)
}

func TestAllocDetailDeserialization_MissingAllocatedResources(t *testing.T) {
	// Some older Nomad versions or pending allocations may omit AllocatedResources
	payload := `{"ID": "xyz-789"}`

	var detail struct {
		AllocatedResources struct {
			Shared struct {
				Ports []struct {
					Label string `json:"Label"`
					Value int    `json:"Value"`
					To    int    `json:"To"`
				} `json:"Ports"`
			} `json:"Shared"`
		} `json:"AllocatedResources"`
	}
	require.NoError(t, json.Unmarshal([]byte(payload), &detail))
	assert.Nil(t, detail.AllocatedResources.Shared.Ports)
}
