package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/trustos/pulumi-ui/internal/mesh"
)

func TestFwdHostRe_MatchesValidSubdomain(t *testing.T) {
	tests := []struct {
		host      string
		wantID    string
		wantStack string
	}{
		{"fwd-1--mystack.pulumi.tenevi.zero", "fwd-1", "mystack"},
		{"fwd-12--my-stack-name.pulumi.tenevi.cloud", "fwd-12", "my-stack-name"},
		{"fwd-1--stack--with--dashes.pulumi.example.com", "fwd-1", "stack--with--dashes"},
		{"fwd-99--nocobase-nomad-cluster.pulumi.tenevi.zero", "fwd-99", "nocobase-nomad-cluster"},
		{"fwd-1--s.pulumi.tenevi.zero:8080", "fwd-1", "s"}, // with port (stripped before regex in middleware)
	}
	for _, tt := range tests {
		m := fwdHostRe.FindStringSubmatch(tt.host)
		if assert.NotNil(t, m, "host=%q should match", tt.host) {
			assert.Equal(t, tt.wantID, m[1], "host=%q id", tt.host)
			assert.Equal(t, tt.wantStack, m[2], "host=%q stack", tt.host)
		}
	}
}

func TestFwdHostRe_RejectsNonForwardHosts(t *testing.T) {
	hosts := []string{
		"pulumi.tenevi.zero",
		"dashy.tenevi.zero",
		"fwd-1--stack.tenevi.zero",       // missing .pulumi. anchor
		"anything.tenevi.zero",
		"localhost",
		"192.168.23.169",
		"",
	}
	for _, host := range hosts {
		m := fwdHostRe.FindStringSubmatch(host)
		assert.Nil(t, m, "host=%q should NOT match", host)
	}
}

func TestForwardSubdomainProxy_PassesThroughNonMatchingHost(t *testing.T) {
	h := &NetworkHandler{ForwardManager: mesh.NewForwardManager(nil)}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	mw := h.ForwardSubdomainProxy(next)

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "pulumi.tenevi.zero"
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.True(t, called, "next handler should be called for non-matching host")
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestForwardSubdomainProxy_Returns404ForMissingForward(t *testing.T) {
	h := &NetworkHandler{ForwardManager: mesh.NewForwardManager(nil)}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler should NOT be called for matching host")
	})

	mw := h.ForwardSubdomainProxy(next)

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "fwd-1--mystack.pulumi.tenevi.zero"
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestForwardSubdomainProxy_Returns503WhenForwardManagerNil(t *testing.T) {
	h := &NetworkHandler{ForwardManager: nil}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler should NOT be called")
	})

	mw := h.ForwardSubdomainProxy(next)

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "fwd-1--mystack.pulumi.tenevi.zero"
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestForwardSubdomainProxy_StripsPortFromHost(t *testing.T) {
	h := &NetworkHandler{ForwardManager: mesh.NewForwardManager(nil)}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next handler should NOT be called for matching host")
	})

	mw := h.ForwardSubdomainProxy(next)

	// Host with port — should still match after stripping
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "fwd-1--mystack.pulumi.tenevi.zero:8080"
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	// Should be 404 (forward not found), not 200 (passed through)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestForwardProxyUrl_Localhost(t *testing.T) {
	// Verify the expected URL format for localhost (documented behavior):
	// On localhost, links should be http://localhost:{localPort}/
	// On production domains, links should be http://fwd-{id}--{stack}.pulumi.{domain}/
	//
	// This is tested via the frontend (api.ts forwardProxyUrl function).
	// The Go side just needs to ensure the middleware and port forward manager
	// work correctly — the URL generation is a frontend concern.
}
