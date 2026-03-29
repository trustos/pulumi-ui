package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/trustos/pulumi-ui/internal/programs"
)

type appDomainRequest struct {
	Domain string `json:"domain"`
}

type appDomainEntry struct {
	AppKey string `json:"appKey"`
	Domain string `json:"domain"`
	Port   int    `json:"port"`
}

// ListAppDomains returns all domain mappings for a stack's applications.
func (h *Handler) ListAppDomains(w http.ResponseWriter, r *http.Request) {
	stackName := chi.URLParam(r, "name")

	cfg, _, err := h.loadStackConfig(stackName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	prog, ok := h.Registry.Get(cfg.Metadata.Program)
	if !ok {
		http.Error(w, "unknown program", http.StatusBadRequest)
		return
	}

	provider, ok := prog.(programs.ApplicationProvider)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
		return
	}

	var entries []appDomainEntry
	for _, app := range provider.Applications() {
		domain := cfg.AppConfig[app.Key+".domain"]
		if app.Port > 0 {
			entries = append(entries, appDomainEntry{
				AppKey: app.Key,
				Domain: domain,
				Port:   app.Port,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(entries)
}

// SetAppDomain assigns a domain to an application and uploads the Traefik
// dynamic config to the agent. The config is picked up by Traefik's file
// provider instantly (watch: true).
func (h *Handler) SetAppDomain(w http.ResponseWriter, r *http.Request) {
	stackName := chi.URLParam(r, "name")
	appKey := chi.URLParam(r, "appKey")

	var req appDomainRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	domain := strings.TrimSpace(req.Domain)
	if domain == "" {
		http.Error(w, "domain is required", http.StatusBadRequest)
		return
	}

	// Find the app's port from the program catalog.
	cfg, _, err := h.loadStackConfig(stackName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	prog, ok := h.Registry.Get(cfg.Metadata.Program)
	if !ok {
		http.Error(w, "unknown program", http.StatusBadRequest)
		return
	}

	provider, ok := prog.(programs.ApplicationProvider)
	if !ok {
		http.Error(w, "program has no applications", http.StatusBadRequest)
		return
	}

	var appPort int
	for _, app := range provider.Applications() {
		if app.Key == appKey {
			appPort = app.Port
			break
		}
	}
	if appPort == 0 {
		http.Error(w, fmt.Sprintf("app %q has no port defined", appKey), http.StatusBadRequest)
		return
	}

	// Save domain to appConfig.
	if cfg.AppConfig == nil {
		cfg.AppConfig = make(map[string]string)
	}
	cfg.AppConfig[appKey+".domain"] = domain

	yamlStr, err := cfg.ToYAML()
	if err != nil {
		http.Error(w, "marshal config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if row, _ := h.Stacks.Get(stackName); row != nil {
		if err := h.Stacks.Upsert(stackName, row.Program, yamlStr, row.OciAccountID, row.PassphraseID, row.SshKeyID); err != nil {
			log.Printf("[app-domains] WARNING: failed to persist domain for %s/%s: %v", stackName, appKey, err)
		}
	}

	// Generate and upload Traefik dynamic config.
	traefikYAML := generateTraefikConfig(appKey, domain, appPort)
	if err := h.uploadToAgent(r.Context(), stackName, fmt.Sprintf("/opt/traefik/dynamic/%s.yaml", appKey), traefikYAML); err != nil {
		http.Error(w, "upload to agent: "+err.Error(), http.StatusBadGateway)
		return
	}

	log.Printf("[app-domains] %s/%s → %s (port %d)", stackName, appKey, domain, appPort)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "domain": domain})
}

// RemoveAppDomain removes a domain mapping and deletes the Traefik dynamic config.
func (h *Handler) RemoveAppDomain(w http.ResponseWriter, r *http.Request) {
	stackName := chi.URLParam(r, "name")
	appKey := chi.URLParam(r, "appKey")

	// Remove from appConfig.
	cfg, _, err := h.loadStackConfig(stackName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	delete(cfg.AppConfig, appKey+".domain")

	yamlStr, err := cfg.ToYAML()
	if err == nil {
		if row, _ := h.Stacks.Get(stackName); row != nil {
			h.Stacks.Upsert(stackName, row.Program, yamlStr, row.OciAccountID, row.PassphraseID, row.SshKeyID)
		}
	}

	// Delete the Traefik config file via agent exec.
	filePath := fmt.Sprintf("/opt/traefik/dynamic/%s.yaml", appKey)
	// Upload an empty file (Traefik ignores empty files, effectively removing the route).
	if err := h.uploadToAgent(r.Context(), stackName, filePath, ""); err != nil {
		log.Printf("[app-domains] WARNING: failed to clear %s on agent: %v", filePath, err)
	}

	log.Printf("[app-domains] %s/%s domain removed", stackName, appKey)
	w.WriteHeader(http.StatusNoContent)
}

// uploadToAgent sends a file to the agent via the mesh tunnel.
func (h *Handler) uploadToAgent(ctx context.Context, stackName, destPath, content string) error {
	tunnel, err := h.MeshManager.GetTunnelForNode(stackName, 0)
	if err != nil {
		return fmt.Errorf("mesh tunnel: %w", err)
	}

	client := tunnel.HTTPClient()
	req, err := http.NewRequestWithContext(ctx, "POST", tunnel.AgentURL()+"/upload", bytes.NewBufferString(content))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tunnel.Token())
	req.Header.Set("X-Dest-Path", destPath)
	req.Header.Set("X-File-Mode", "0644")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("agent unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("agent error (%d): %s", resp.StatusCode, string(body))
	}
	return nil
}

// generateTraefikConfig produces a Traefik dynamic YAML config for a domain → app routing.
func generateTraefikConfig(appKey, domain string, port int) string {
	return fmt.Sprintf(`http:
  routers:
    %s-domain:
      rule: "Host(\x60%s\x60)"
      entryPoints:
        - websecure
      service: %s-domain
      tls:
        certResolver: letsencrypt
  services:
    %s-domain:
      loadBalancer:
        servers:
          - url: "http://%s.service.consul:%d"
`, appKey, domain, appKey, appKey, appKey, port)
}
