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
