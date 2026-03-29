package api

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateTraefikConfig_Basic(t *testing.T) {
	result := generateTraefikConfig("nocobase", "nocobase.example.com", 13000)

	assert.Contains(t, result, "nocobase-domain")
	assert.Contains(t, result, "nocobase.example.com")
	assert.Contains(t, result, "websecure")
	assert.Contains(t, result, "letsencrypt")
	assert.Contains(t, result, "nocobase.service.consul:13000")
}

func TestGenerateTraefikConfig_DifferentApps(t *testing.T) {
	tests := []struct {
		appKey string
		domain string
		port   int
	}{
		{"traefik", "traefik.example.com", 80},
		{"pgadmin", "pgadmin.example.com", 80},
		{"nocobase", "app.mysite.com", 13000},
		{"postgres", "db.mysite.com", 5432},
	}

	for _, tt := range tests {
		t.Run(tt.appKey, func(t *testing.T) {
			result := generateTraefikConfig(tt.appKey, tt.domain, tt.port)
			assert.Contains(t, result, tt.domain)
			assert.Contains(t, result, tt.appKey+".service.consul")
			assert.Contains(t, result, "certResolver: letsencrypt")
		})
	}
}

func TestGenerateTraefikConfig_ValidYAML(t *testing.T) {
	result := generateTraefikConfig("nocobase", "nocobase.example.com", 13000)

	// Should be valid YAML structure (basic checks)
	assert.True(t, strings.HasPrefix(result, "http:"))
	assert.Contains(t, result, "routers:")
	assert.Contains(t, result, "services:")
	assert.Contains(t, result, "loadBalancer:")
	assert.Contains(t, result, "servers:")
}
