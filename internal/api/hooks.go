package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/trustos/pulumi-ui/internal/applications"
	"github.com/trustos/pulumi-ui/internal/db"
	"github.com/trustos/pulumi-ui/internal/engine"
)

// ListHooks returns all hooks for a stack.
func (h *Handler) ListHooks(w http.ResponseWriter, r *http.Request) {
	stackName := chi.URLParam(r, "name")
	hooks, err := h.Hooks.ListForStack(stackName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if hooks == nil {
		hooks = []db.Hook{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(hooks)
}

// CreateHook creates a new lifecycle hook for a stack.
func (h *Handler) CreateHook(w http.ResponseWriter, r *http.Request) {
	stackName := chi.URLParam(r, "name")

	var body struct {
		Trigger         string  `json:"trigger"`
		Type            string  `json:"type"`
		Priority        int     `json:"priority"`
		ContinueOnError bool    `json:"continueOnError"`
		Command         *string `json:"command,omitempty"`
		NodeIndex       *int    `json:"nodeIndex,omitempty"`
		URL             *string `json:"url,omitempty"`
		Description     string  `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if body.Trigger == "" || body.Type == "" {
		http.Error(w, "trigger and type are required", http.StatusBadRequest)
		return
	}

	hook := &db.Hook{
		StackName:       stackName,
		Trigger:         body.Trigger,
		Type:            body.Type,
		Priority:        body.Priority,
		ContinueOnError: body.ContinueOnError,
		Command:         body.Command,
		NodeIndex:       body.NodeIndex,
		URL:             body.URL,
		Source:          "user",
		Description:     body.Description,
	}
	if err := h.Hooks.Create(hook); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(hook)
}

// DeleteHook removes a lifecycle hook.
func (h *Handler) DeleteHook(w http.ResponseWriter, r *http.Request) {
	hookID := chi.URLParam(r, "hookId")
	if err := h.Hooks.Delete(hookID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// executeHooks runs all hooks for a given stack and trigger in priority order.
// It streams output via logSend. Returns nil even if individual hooks fail
// (respecting continueOnError).
func (h *Handler) executeHooks(ctx context.Context, stackName, trigger, opStatus string, logSend func(engine.SSEEvent)) {
	if h.Hooks == nil {
		return
	}

	hooks, err := h.Hooks.ListByTrigger(stackName, trigger)
	if err != nil {
		log.Printf("[hooks] failed to list hooks for %s/%s: %v", stackName, trigger, err)
		return
	}
	if len(hooks) == 0 {
		return
	}

	logSend(engine.SSEEvent{Type: "output", Data: fmt.Sprintf("=== Running %s hooks ===", trigger)})

	for _, hook := range hooks {
		desc := hook.Description
		if desc == "" {
			desc = hook.Type + " hook"
		}
		logSend(engine.SSEEvent{Type: "output", Data: fmt.Sprintf("Hook: %s (priority %d)", desc, hook.Priority)})

		var hookErr error
		switch hook.Type {
		case "agent-exec":
			hookErr = h.executeAgentExecHook(ctx, stackName, hook, logSend)
		case "webhook":
			hookErr = h.executeWebhook(ctx, hook, stackName, trigger, opStatus)
		default:
			logSend(engine.SSEEvent{Type: "error", Data: fmt.Sprintf("Unknown hook type: %s", hook.Type)})
			continue
		}

		if hookErr != nil {
			logSend(engine.SSEEvent{Type: "error", Data: fmt.Sprintf("Hook failed: %v", hookErr)})
			if !hook.ContinueOnError {
				logSend(engine.SSEEvent{Type: "error", Data: "Stopping hook execution (continueOnError=false)"})
				return
			}
		}
	}

	logSend(engine.SSEEvent{Type: "output", Data: fmt.Sprintf("=== %s hooks complete ===", trigger)})
}

// executeAgentExecHook runs a command on the agent via the mesh tunnel.
func (h *Handler) executeAgentExecHook(ctx context.Context, stackName string, hook db.Hook, logSend func(engine.SSEEvent)) error {
	if h.MeshManager == nil {
		return fmt.Errorf("mesh manager not available")
	}
	if hook.Command == nil || *hook.Command == "" {
		return fmt.Errorf("no command specified for agent-exec hook")
	}

	nodeIndex := 0
	if hook.NodeIndex != nil {
		nodeIndex = *hook.NodeIndex
	}

	tunnel, err := h.MeshManager.GetTunnelForNode(stackName, nodeIndex)
	if err != nil {
		return fmt.Errorf("get tunnel: %w", err)
	}

	// Use the deployer's ExecOnAgent to execute the command.
	deployer := applications.NewDeployer(h.ConnStore, h.MeshManager, h.Hooks)
	output, exitCode, err := deployer.ExecOnAgent(ctx, tunnel, *hook.Command)
	if err != nil {
		return fmt.Errorf("exec: %w", err)
	}

	if output != "" {
		logSend(engine.SSEEvent{Type: "output", Data: output})
	}
	if exitCode != 0 {
		return fmt.Errorf("command exited with code %d", exitCode)
	}
	return nil
}

// executeWebhook sends an HTTP POST to the hook's URL with a JSON payload.
func (h *Handler) executeWebhook(ctx context.Context, hook db.Hook, stackName, trigger, opStatus string) error {
	if hook.URL == nil || *hook.URL == "" {
		return fmt.Errorf("no URL specified for webhook hook")
	}

	payload := map[string]string{
		"stackName": stackName,
		"trigger":   trigger,
		"status":    opStatus,
		"hookId":    hook.ID,
	}
	body, _ := json.Marshal(payload)

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "POST", *hook.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}
