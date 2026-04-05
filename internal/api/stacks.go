package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/trustos/pulumi-ui/internal/db"
	"github.com/trustos/pulumi-ui/internal/engine"
	nebulaPKI "github.com/trustos/pulumi-ui/internal/nebula"
	"github.com/trustos/pulumi-ui/internal/blueprints"
	"github.com/trustos/pulumi-ui/internal/stacks"
)

// computeDeployedState returns two booleans from the operation history:
//   - deployed:     most recent successful "up" is more recent than most recent successful "destroy"
//   - wasDeployed:  at least one successful "up" has ever run
//
// ops must be sorted descending by started_at (as returned by ListForStack).
func computeDeployedState(ops []db.Operation) (deployed bool, wasDeployed bool) {
	var lastUpAt, lastDestroyAt int64
	for _, op := range ops {
		if op.Status != "succeeded" {
			continue
		}
		if op.Operation == "up" && op.StartedAt > lastUpAt {
			lastUpAt = op.StartedAt
		}
		if op.Operation == "destroy" && op.StartedAt > lastDestroyAt {
			lastDestroyAt = op.StartedAt
		}
	}
	deployed = lastUpAt > 0 && (lastDestroyAt == 0 || lastUpAt > lastDestroyAt)
	wasDeployed = lastUpAt > 0
	return
}

// computeDeployed is a convenience wrapper used by tests.
func computeDeployed(ops []db.Operation) bool {
	d, _ := computeDeployedState(ops)
	return d
}

// resolveCredentials builds engine credentials for a stack.
// OCI credentials come from the account (or global fallback).
// The passphrase is looked up from the named passphrases table.
// If sshKeyID is set, the SSH public key from that key overrides the account's SSH key.
func (h *StackHandler) resolveCredentials(ociAccountID, passphraseID, sshKeyID *string) (engine.Credentials, error) {
	var oci db.OCICredentials
	var err error

	if ociAccountID != nil && *ociAccountID != "" {
		account, err := h.Accounts.Get(*ociAccountID)
		if err != nil {
			return engine.Credentials{}, fmt.Errorf("load OCI account: %w", err)
		}
		if account == nil {
			return engine.Credentials{}, fmt.Errorf("OCI account not found")
		}
		oci = account.ToOCICredentials()
	} else {
		oci, err = h.Creds.GetOCICredentials()
		if err != nil {
			return engine.Credentials{}, fmt.Errorf("load global OCI credentials: %w", err)
		}
	}

	// If a dedicated SSH key is linked, override the account's SSH public key.
	if sshKeyID != nil && *sshKeyID != "" {
		sshPub, err := h.SSHKeys.GetPublicKey(*sshKeyID)
		if err != nil {
			return engine.Credentials{}, fmt.Errorf("load SSH key: %w", err)
		}
		oci.SSHPublicKey = sshPub
	}

	if passphraseID == nil || *passphraseID == "" {
		return engine.Credentials{}, fmt.Errorf("no passphrase assigned to this stack — assign one in Settings")
	}
	passphrase, err := h.Passphrases.GetValue(*passphraseID)
	if err != nil {
		return engine.Credentials{}, fmt.Errorf("load passphrase: %w", err)
	}

	return engine.Credentials{OCI: oci, Passphrase: passphrase}, nil
}

// ListStacks returns all stacks from SQLite merged with last-operation status.
func (h *StackHandler) ListStacks(w http.ResponseWriter, r *http.Request) {
	rows, err := h.Stacks.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type StackSummary struct {
		Name               string  `json:"name"`
		Blueprint          string  `json:"blueprint"`
		OciAccountID       *string `json:"ociAccountId"`
		PassphraseID       *string `json:"passphraseId"`
		SshKeyID           *string `json:"sshKeyId"`
		CreatedByAccountID *string `json:"createdByAccountId"`
		LastOperation      *string `json:"lastOperation"`
		Status             string  `json:"status"`
		ResourceCount      int     `json:"resourceCount"`
	}

	result := make([]StackSummary, 0, len(rows))
	for _, row := range rows {
		ops, _ := h.Ops.ListForStack(row.Name, 1, row.CreatedAt)
		summary := StackSummary{
			Name:               row.Name,
			Blueprint:          row.Blueprint,
			OciAccountID:       row.OciAccountID,
			PassphraseID:       row.PassphraseID,
			SshKeyID:           row.SshKeyID,
			CreatedByAccountID: row.CreatedByAccountID,
			ResourceCount: 0,
			Status:        "not deployed",
		}
		if len(ops) > 0 {
			ts := time.Unix(ops[0].StartedAt, 0).Format(time.RFC3339)
			summary.LastOperation = &ts
			summary.Status = ops[0].Status
		}
		result = append(result, summary)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// PutStack saves or updates a stack config in SQLite.
func (h *StackHandler) PutStack(w http.ResponseWriter, r *http.Request) {
	stackName := chi.URLParam(r, "name")

	var body struct {
		Blueprint    string            `json:"blueprint"`
		Description  string            `json:"description"`
		Config       map[string]string `json:"config"`
		OciAccountID *string           `json:"ociAccountId"`
		PassphraseID *string           `json:"passphraseId"`
		SshKeyID     *string           `json:"sshKeyId"`
		Applications map[string]bool   `json:"applications,omitempty"`
		AppConfig    map[string]string `json:"appConfig,omitempty"`
		Claim        bool              `json:"claim,omitempty"`
		ConfigYAML   string            `json:"configYaml,omitempty"` // imported from S3 during claim
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body.Blueprint == "" {
		http.Error(w, "blueprint is required", http.StatusBadRequest)
		return
	}

	// Claim mode: register a remote stack in the local DB without blueprint
	// validation or config rendering. The blueprint may not exist locally.
	if body.Claim {
		var yamlStr string
		if body.ConfigYAML != "" {
			// Use the config imported from S3 (synced by the creating instance).
			yamlStr = body.ConfigYAML
		} else {
			// No config available — create minimal placeholder.
			cfg := &stacks.StackConfig{
				APIVersion: "pulumi.io/v1",
				Kind:       "Stack",
				Metadata: stacks.StackMetadata{
					Name:      stackName,
					Blueprint: body.Blueprint,
				},
			}
			yamlStr, _ = cfg.ToYAML()
		}
		if err := h.Stacks.Upsert(stackName, body.Blueprint, yamlStr, body.OciAccountID, body.PassphraseID, body.SshKeyID, body.OciAccountID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	prog, ok := h.Registry.Get(body.Blueprint)
	if !ok {
		http.Error(w, "unknown blueprint: "+body.Blueprint, http.StatusBadRequest)
		return
	}

	cfg := &stacks.StackConfig{
		APIVersion: "pulumi.io/v1",
		Kind:       "Stack",
		Metadata: stacks.StackMetadata{
			Name:        stackName,
			Blueprint:   body.Blueprint,
			Description: body.Description,
		},
		Config:       body.Config,
		Applications: body.Applications,
		AppConfig:    body.AppConfig,
	}
	if err := cfg.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	yamlStr, err := cfg.ToYAML()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.Stacks.Upsert(stackName, body.Blueprint, yamlStr, body.OciAccountID, body.PassphraseID, body.SshKeyID, body.OciAccountID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Sync config to S3 for cross-instance claiming.
	go syncConfigToS3(context.Background(), h.Creds, body.Blueprint, stackName, yamlStr)

	// Generate Nebula PKI for programs with agent connectivity (first time only).
	// Applies to both ApplicationProvider (built-in Go programs with catalog)
	// and AgentAccessProvider (YAML programs with meta.agentAccess: true).
	shouldGeneratePKI := false
	if _, ok := prog.(blueprints.ApplicationProvider); ok {
		shouldGeneratePKI = true
	}
	if aap, ok := prog.(blueprints.AgentAccessProvider); ok && aap.AgentAccess() {
		shouldGeneratePKI = true
	}
	if shouldGeneratePKI && h.ConnStore != nil {
		if existing, _ := h.ConnStore.Get(stackName); existing == nil {
			if err := h.generateNebulaPKI(stackName); err != nil {
				http.Error(w, "failed to generate agent PKI: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}

	w.WriteHeader(http.StatusOK)
}

// GetStackInfo returns full stack details including Pulumi outputs and last operation.
func (h *StackHandler) GetStackInfo(w http.ResponseWriter, r *http.Request) {
	stackName := chi.URLParam(r, "name")

	row, err := h.Stacks.Get(stackName)
	if err != nil || row == nil {
		http.Error(w, "stack not found", http.StatusNotFound)
		return
	}

	cfg, err := stacks.ParseYAML(row.ConfigYAML)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type MeshStatus struct {
		Connected      bool    `json:"connected"`
		LighthouseAddr *string `json:"lighthouseAddr,omitempty"`
		AgentNebulaIP  *string `json:"agentNebulaIp,omitempty"`
		AgentRealIP    *string `json:"agentRealIp,omitempty"`
		NebulaSubnet   string  `json:"nebulaSubnet,omitempty"`
		LastSeenAt     *int64  `json:"lastSeenAt,omitempty"`
	}

	type NodeInfo struct {
		NodeIndex   int     `json:"nodeIndex"`
		NebulaIP    string  `json:"nebulaIp"`
		AgentRealIP *string `json:"agentRealIp,omitempty"`
	}

	type StackInfo struct {
		Name               string                 `json:"name"`
		Blueprint          string                 `json:"blueprint"`
		OciAccountID       *string                `json:"ociAccountId"`
		PassphraseID       *string                `json:"passphraseId"`
		SshKeyID           *string                `json:"sshKeyId"`
		CreatedByAccountID *string                `json:"createdByAccountId"`
		Config       map[string]string      `json:"config"`
		Applications map[string]bool        `json:"applications,omitempty"`
		AppConfig    map[string]string      `json:"appConfig,omitempty"`
		Outputs      map[string]interface{} `json:"outputs"`
		Resources    int                    `json:"resources"`
		LastUpdated  *string                `json:"lastUpdated"`
		Status       string                 `json:"status"`
		Running      bool                   `json:"running"`
		Mesh              *MeshStatus            `json:"mesh,omitempty"`
		AgentAccess       bool                   `json:"agentAccess"`
		Nodes             []NodeInfo             `json:"nodes,omitempty"`
		Deployed          bool                   `json:"deployed"`
		WasDeployed       bool                   `json:"wasDeployed"`
		LastOperationType string                 `json:"lastOperationType,omitempty"`
	}

	info := StackInfo{
		Name:               stackName,
		Blueprint:          row.Blueprint,
		OciAccountID:       row.OciAccountID,
		PassphraseID:       row.PassphraseID,
		SshKeyID:           row.SshKeyID,
		CreatedByAccountID: row.CreatedByAccountID,
		Config:       cfg.Config,
		Applications: cfg.Applications,
		AppConfig:    cfg.AppConfig,
		Outputs:      map[string]interface{}{},
		Status:       "not deployed",
		Running:      h.Engine.IsRunning(stackName),
	}

	ops, _ := h.Ops.ListForStack(stackName, 20, row.CreatedAt)
	if len(ops) > 0 {
		ts := time.Unix(ops[0].StartedAt, 0).Format(time.RFC3339)
		info.LastUpdated = &ts
		info.Status = ops[0].Status
		info.LastOperationType = ops[0].Operation
	}

	info.Deployed, info.WasDeployed = computeDeployedState(ops)

	// Fetch stack state (resource count + outputs) when the stack may have resources.
	// For claimed stacks with no local "up" history, this also detects deployment state
	// from the actual Pulumi state file.
	if info.Status == "succeeded" || info.Status == "failed" {
		creds, err := h.resolveCredentials(row.OciAccountID, row.PassphraseID, row.SshKeyID)
		if err == nil {
			count, outputs, err := h.Engine.GetStackState(r.Context(), stackName, row.Blueprint, cfg.Config, creds)
			if err == nil {
				info.Resources = count
				for k, v := range outputs {
					info.Outputs[k] = v.Value
				}
				// If ops history says "not deployed" but state has resources,
				// the stack was deployed elsewhere (claimed stack scenario).
				if !info.Deployed && count > 0 {
					info.Deployed = true
					info.WasDeployed = true
				}
			}
		}
	}

	// Determine if program has agent access
	if prog, ok := h.Registry.Get(row.Blueprint); ok {
		if _, isApp := prog.(blueprints.ApplicationProvider); isApp {
			info.AgentAccess = true
		}
		if aap, isAgent := prog.(blueprints.AgentAccessProvider); isAgent && aap.AgentAccess() {
			info.AgentAccess = true
		}
	}

	// Mesh status from stack_connections
	if h.ConnStore != nil {
		if conn, err := h.ConnStore.Get(stackName); err == nil && conn != nil {
			// If infrastructure is not deployed, any stored agent runtime fields
			// (IPs, lighthouse) are stale. Clear them lazily — this handles stacks
			// destroyed before ClearAgentConnection was in the destroy path, and
			// the destroy→refresh case where the last op is "refresh" not "destroy".
			if !info.Deployed {
				if conn.AgentNebulaIP != nil || conn.AgentRealIP != nil {
					_ = h.ConnStore.ClearAgentConnection(stackName)
				}
				conn.AgentNebulaIP = nil
				conn.AgentRealIP = nil
				conn.LighthouseAddr = nil
				conn.LastSeenAt = nil
				conn.ClusterInfo = nil
			}
			mesh := &MeshStatus{
				Connected:      conn.AgentNebulaIP != nil,
				LighthouseAddr: conn.LighthouseAddr,
				AgentNebulaIP:  conn.AgentNebulaIP,
				AgentRealIP:    conn.AgentRealIP,
				NebulaSubnet:   conn.NebulaSubnet,
				LastSeenAt:     conn.LastSeenAt,
			}
			info.Mesh = mesh
		}
	}

	// Per-node cert data — only include nodes that have been deployed (have a real IP).
	// Node certs are pre-generated in batches of 10; undeployed slots have no real IP.
	if h.NodeCertStore != nil {
		if nodeCerts, err := h.NodeCertStore.ListForStack(stackName); err == nil {
			var nodes []NodeInfo
			for _, nc := range nodeCerts {
				if nc.AgentRealIP != nil && *nc.AgentRealIP != "" {
					nodes = append(nodes, NodeInfo{
						NodeIndex:   nc.NodeIndex,
						NebulaIP:    nc.NebulaIP,
						AgentRealIP: nc.AgentRealIP,
					})
				}
			}
			if len(nodes) > 0 {
				info.Nodes = nodes
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

// DeleteStack removes the stack config, operation history, and Pulumi backend
// state so that re-creating a stack with the same name starts completely fresh.
func (h *StackHandler) DeleteStack(w http.ResponseWriter, r *http.Request) {
	stackName := chi.URLParam(r, "name")

	// Prevent deletion while an operation is running.
	if h.Engine.IsRunning(stackName) {
		http.Error(w, "cannot remove stack while an operation is running", http.StatusConflict)
		return
	}

	// Look up the program name before deleting the SQLite row — we need it
	// to locate the correct Pulumi state directory on disk.
	row, err := h.Stacks.Get(stackName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := h.Stacks.Delete(stackName); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.Ops.DeleteForStack(stackName)
	if h.Hooks != nil {
		h.Hooks.DeleteForStack(stackName)
	}

	if row != nil {
		if err := h.Engine.RemoveStackState(stackName, row.Blueprint); err != nil {
			log.Printf("[warn] failed to remove Pulumi state for stack %s (blueprint %s): %v", stackName, row.Blueprint, err)
		}
	}

	if h.ConnStore != nil {
		if err := h.ConnStore.Delete(stackName); err != nil {
			log.Printf("[warn] failed to remove stack connection for %s: %v", stackName, err)
		}
	}

	if h.NodeCertStore != nil {
		if err := h.NodeCertStore.Delete(stackName); err != nil {
			log.Printf("[warn] failed to remove node certs for %s: %v", stackName, err)
		}
	}

	w.WriteHeader(http.StatusOK)
}

// ExportStackYAML returns the stack config as a downloadable YAML file.
func (h *StackHandler) ExportStackYAML(w http.ResponseWriter, r *http.Request) {
	stackName := chi.URLParam(r, "name")

	stackDir := os.Getenv("PULUMI_UI_STACK_DIR")
	var cfg *stacks.StackConfig
	var err error

	if stackDir != "" {
		cfg, err = stacks.LoadFromFile(filepath.Join(stackDir, stackName+".yaml"))
	} else {
		row, rowErr := h.Stacks.Get(stackName)
		if rowErr != nil || row == nil {
			http.Error(w, "stack not found", http.StatusNotFound)
			return
		}
		cfg, err = stacks.ParseYAML(row.ConfigYAML)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	data, _ := cfg.ToYAML()
	w.Header().Set("Content-Type", "application/yaml")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.yaml"`, stackName))
	w.Write([]byte(data))
}

// loadStackConfig loads a stack config and returns the stack row alongside it.
func (h *StackHandler) loadStackConfig(stackName string) (*stacks.StackConfig, *db.StackRow, error) {
	return loadStackConfig(h.Stacks, stackName)
}

// loadStackConfig is a package-level helper used by both StackHandler and BlueprintHandler.
func loadStackConfig(stackStore *db.StackStore, stackName string) (*stacks.StackConfig, *db.StackRow, error) {
	stackDir := os.Getenv("PULUMI_UI_STACK_DIR")
	if stackDir != "" {
		cfg, err := stacks.LoadFromFile(filepath.Join(stackDir, stackName+".yaml"))
		return cfg, nil, err
	}
	row, err := stackStore.Get(stackName)
	if err != nil {
		return nil, nil, err
	}
	if row == nil {
		return nil, nil, fmt.Errorf("stack %q not found — create it with PUT /api/stacks/%s first", stackName, stackName)
	}
	cfg, err := stacks.ParseYAML(row.ConfigYAML)
	return cfg, row, err
}

// runOperation is the shared SSE operation runner.
func (h *StackHandler) runOperation(w http.ResponseWriter, r *http.Request, operation string) {
	stackName := chi.URLParam(r, "name")

	cfg, row, err := h.loadStackConfig(stackName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var ociAccountID, passphraseID, sshKeyID *string
	if row != nil {
		ociAccountID = row.OciAccountID
		passphraseID = row.PassphraseID
		sshKeyID = row.SshKeyID
	}

	creds, err := h.resolveCredentials(ociAccountID, passphraseID, sshKeyID)
	if err != nil {
		http.Error(w, "credentials not configured: "+err.Error(), http.StatusBadRequest)
		return
	}

	send, ok := engine.SSEResponseWriter(w)
	if !ok {
		return
	}

	opID := uuid.New().String()
	h.Ops.Create(opID, stackName, operation)

	logSend := func(event engine.SSEEvent) {
		send(event)
		if event.Type == "output" || event.Type == "error" {
			h.Ops.AppendLog(opID, event.Data)
		}
	}

	// Use a background context so the Pulumi operation survives browser close or
	// navigation away. The only way to cancel is via the explicit /cancel endpoint.
	opCtx := context.Background()

	// Pre-operation hooks
	h.ExecuteHooks(stackName, "pre-"+operation, "", logSend)

	var status string
	switch operation {
	case "up":
		status = h.Engine.Up(opCtx, stackName, cfg.Metadata.Blueprint, cfg.Config, creds, logSend)
	case "destroy":
		status = h.Engine.Destroy(opCtx, stackName, cfg.Metadata.Blueprint, cfg.Config, creds, logSend)
		if status == "succeeded" {
			// Infrastructure is gone — clear the agent connection fields so the UI
			// no longer shows "Connected" or offers the terminal for a dead instance.
			if h.ConnStore != nil {
				if err := h.ConnStore.ClearAgentConnection(stackName); err != nil {
					log.Printf("[destroy] clear agent connection for %s: %v", stackName, err)
				}
			}
			if h.MeshManager != nil {
				h.MeshManager.CloseTunnel(stackName)
			}
		}
	case "refresh":
		status = h.Engine.Refresh(opCtx, stackName, cfg.Metadata.Blueprint, cfg.Config, creds, logSend)
	case "preview":
		status = h.Engine.Preview(opCtx, stackName, cfg.Metadata.Blueprint, cfg.Config, creds, logSend)
	}

	// Post-operation hooks
	h.ExecuteHooks(stackName, "post-"+operation, status, logSend)

	// Sync config to S3 so other pulumi-ui instances can claim this stack.
	if status == "succeeded" {
		yamlStr, yamlErr := cfg.ToYAML()
		if yamlErr != nil {
			log.Printf("[config-sync] cfg.ToYAML() failed for %s: %v", stackName, yamlErr)
		} else {
			log.Printf("[config-sync] triggering sync for %s/%s (%d bytes)", cfg.Metadata.Blueprint, stackName, len(yamlStr))
			go syncConfigToS3(context.Background(), h.Creds, cfg.Metadata.Blueprint, stackName, yamlStr)
		}
	} else {
		log.Printf("[config-sync] skipping sync — operation status: %s", status)
	}

	h.Ops.Finish(opID, status)
	send(engine.SSEEvent{Type: "done", Data: status})
}

func (h *StackHandler) StackUp(w http.ResponseWriter, r *http.Request) {
	h.runOperation(w, r, "up")
}

func (h *StackHandler) StackDestroy(w http.ResponseWriter, r *http.Request) {
	h.runOperation(w, r, "destroy")
}

func (h *StackHandler) StackRefresh(w http.ResponseWriter, r *http.Request) {
	h.runOperation(w, r, "refresh")
}

func (h *StackHandler) StackPreview(w http.ResponseWriter, r *http.Request) {
	h.runOperation(w, r, "preview")
}

func (h *StackHandler) StackCancel(w http.ResponseWriter, r *http.Request) {
	stackName := chi.URLParam(r, "name")
	h.Engine.Cancel(stackName)
	w.WriteHeader(http.StatusOK)
}

func (h *StackHandler) StackUnlock(w http.ResponseWriter, r *http.Request) {
	stackName := chi.URLParam(r, "name")
	if err := h.Engine.Unlock(stackName); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// StackDeployApps runs Phase 2+3: mesh connectivity + application deployment.
func (h *StackHandler) StackDeployApps(w http.ResponseWriter, r *http.Request) {
	stackName := chi.URLParam(r, "name")

	cfg, _, err := h.loadStackConfig(stackName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	send, ok := engine.SSEResponseWriter(w)
	if !ok {
		return
	}

	opID := uuid.New().String()
	h.Ops.Create(opID, stackName, "deploy-apps")

	logSend := func(event engine.SSEEvent) {
		send(event)
		if event.Type == "output" || event.Type == "error" {
			h.Ops.AppendLog(opID, event.Data)
		}
	}

	opCtx := context.Background()
	status := h.Engine.DeployApps(opCtx, stackName, cfg.Metadata.Blueprint, cfg.Applications, cfg.AppConfig, logSend)

	// Post-deploy-apps hooks
	h.ExecuteHooks(stackName, "post-deploy-apps", status, logSend)

	// Persist any auto-generated secrets back to the stack config so they
	// survive re-deploys. The deployer mutates appConfig in place.
	if yamlStr, marshalErr := cfg.ToYAML(); marshalErr == nil {
		if row, _ := h.Stacks.Get(stackName); row != nil {
			if err := h.Stacks.Upsert(stackName, row.Blueprint, yamlStr, row.OciAccountID, row.PassphraseID, row.SshKeyID, row.CreatedByAccountID); err != nil {
				log.Printf("[deploy-apps] WARNING: failed to persist appConfig for %s: %v (auto-generated secrets may be lost on re-deploy)", stackName, err)
			}
		}
	} else {
		log.Printf("[deploy-apps] WARNING: failed to marshal config for %s: %v", stackName, marshalErr)
	}

	h.Ops.Finish(opID, status)
	send(engine.SSEEvent{Type: "done", Data: status})
}

// GetStackLogs returns the log history for a stack (last 20 operations, oldest first).
func (h *StackHandler) GetStackLogs(w http.ResponseWriter, r *http.Request) {
	stackName := chi.URLParam(r, "name")

	row, err := h.Stacks.Get(stackName)
	if err != nil || row == nil {
		http.Error(w, "stack not found", http.StatusNotFound)
		return
	}

	ops, err := h.Ops.ListLogsForStack(stackName, 20, row.CreatedAt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type LogEntry struct {
		Operation string `json:"operation"`
		Status    string `json:"status"`
		Log       string `json:"log"`
		StartedAt int64  `json:"startedAt"`
	}

	result := make([]LogEntry, 0, len(ops))
	for _, op := range ops {
		result = append(result, LogEntry{
			Operation: op.Operation,
			Status:    op.Status,
			Log:       op.Log,
			StartedAt: op.StartedAt,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// generateNebulaPKI creates the Nebula CA, pulumi-ui node cert, per-node
// agent certs (10 nodes, .2–.11), and a per-stack auth token for a new stack.
// Node 0's cert is also stored as the legacy agent_cert in stack_connections
// for backwards compatibility with the mesh manager and Go programs.
func (h *StackHandler) generateNebulaPKI(stackName string) error {
	subnet, err := h.ConnStore.AllocateSubnet()
	if err != nil {
		return fmt.Errorf("allocate subnet: %w", err)
	}

	ca, err := nebulaPKI.GenerateCA(stackName+"-ca", 2*365*24*time.Hour)
	if err != nil {
		return fmt.Errorf("generate CA: %w", err)
	}

	uiIP, err := nebulaPKI.UIAddress(subnet)
	if err != nil {
		return fmt.Errorf("compute UI address: %w", err)
	}

	uiCert, err := nebulaPKI.IssueCert(ca.CertPEM, ca.KeyPEM, "pulumi-ui", uiIP, []string{"server"}, 365*24*time.Hour)
	if err != nil {
		return fmt.Errorf("issue UI cert: %w", err)
	}

	// Generate 10 per-node certs (.2–.11). Node 0 doubles as the legacy agent cert.
	nodeCerts, nodeIPs, err := nebulaPKI.GenerateNodeCerts(ca.CertPEM, ca.KeyPEM, subnet, 10, 365*24*time.Hour)
	if err != nil {
		return fmt.Errorf("generate node certs: %w", err)
	}

	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return fmt.Errorf("generate agent token: %w", err)
	}
	agentToken := hex.EncodeToString(tokenBytes)

	conn := &db.StackConnection{
		StackName:       stackName,
		NebulaCACert:    ca.CertPEM,
		NebulaCAKey:     ca.KeyPEM,
		NebulaUICert:    uiCert.CertPEM,
		NebulaUIKey:     uiCert.KeyPEM,
		NebulaSubnet:    subnet,
		NebulaAgentCert: nodeCerts[0].CertPEM, // node 0 = legacy single-agent identity
		NebulaAgentKey:  nodeCerts[0].KeyPEM,
		AgentToken:      agentToken,
	}
	if err := h.ConnStore.Create(conn); err != nil {
		return err
	}

	// Store per-node certs when the NodeCertStore is wired (always true in production).
	if h.NodeCertStore != nil {
		dbCerts := make([]*db.NodeCert, len(nodeCerts))
		for i, nc := range nodeCerts {
			dbCerts[i] = &db.NodeCert{
				StackName:  stackName,
				NodeIndex:  i,
				NebulaCert: nc.CertPEM,
				NebulaKey:  nc.KeyPEM,
				NebulaIP:   nodeIPs[i],
			}
		}
		if err := h.NodeCertStore.CreateAll(dbCerts); err != nil {
			return fmt.Errorf("store node certs: %w", err)
		}
	}

	return nil
}
