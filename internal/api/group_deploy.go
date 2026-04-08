package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/trustos/pulumi-ui/internal/blueprints"
	"github.com/trustos/pulumi-ui/internal/db"
	"github.com/trustos/pulumi-ui/internal/engine"
	"github.com/trustos/pulumi-ui/internal/stacks"
)

// DeployGroup runs a phased deployment of all stacks in a deployment group.
// Phase 1: Deploy primary stack, capture outputs.
// Phase 2: Update worker configs with primary outputs, deploy workers in parallel.
// Phase 3: Re-up primary with collected worker tenancy OCIDs for IAM policies.
// Streams progress as SSE events.
func (h *PlatformHandler) DeployGroup(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "id")
	group, err := h.Groups.Get(groupID)
	if err != nil || group == nil {
		http.Error(w, "group not found", http.StatusNotFound)
		return
	}

	// Get blueprint multi-account wiring.
	prog, ok := h.Registry.Get(group.Blueprint)
	if !ok {
		http.Error(w, "blueprint not found", http.StatusBadRequest)
		return
	}
	var multiAccount *blueprints.MultiAccountMeta
	if map_, ok := prog.(blueprints.MultiAccountProvider); ok {
		multiAccount = map_.MultiAccount()
	}
	if multiAccount == nil {
		http.Error(w, "blueprint has no multi-account wiring", http.StatusBadRequest)
		return
	}

	members, err := h.Groups.ListMembers(groupID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Set up SSE streaming.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	send := func(ev engine.SSEEvent) {
		data, _ := json.Marshal(ev)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	h.Groups.UpdateStatus(groupID, "deploying")

	// Separate members by role.
	var primaryMember *memberWithCreds
	var workerMembers []*memberWithCreds

	for _, m := range members {
		mc, err := h.resolveMemberCreds(m)
		if err != nil {
			send(engine.SSEEvent{Type: "error", Data: fmt.Sprintf("Failed to resolve credentials for %s: %v", m.StackName, err)})
			h.Groups.UpdateStatus(groupID, "failed")
			return
		}
		if m.Role == "primary" {
			primaryMember = mc
		} else {
			workerMembers = append(workerMembers, mc)
		}
	}

	if primaryMember == nil {
		send(engine.SSEEvent{Type: "error", Data: "No primary member found"})
		h.Groups.UpdateStatus(groupID, "failed")
		return
	}

	// ── Phase 1: Deploy primary ──────────────────────────────────────────
	send(engine.SSEEvent{Type: "output", Data: "═══ Phase 1: Deploying primary stack ═══"})
	primaryStatus := h.Engine.Up(
		r.Context(),
		primaryMember.stackName,
		group.Blueprint,
		primaryMember.config,
		primaryMember.creds,
		send,
	)
	if primaryStatus != "succeeded" {
		send(engine.SSEEvent{Type: "error", Data: "Primary deployment failed — aborting group deploy"})
		h.Groups.UpdateStatus(groupID, "failed")
		return
	}

	// Capture primary outputs.
	outputs, err := h.Engine.GetStackOutputs(
		r.Context(),
		primaryMember.stackName,
		group.Blueprint,
		primaryMember.config,
		primaryMember.creds,
	)
	if err != nil {
		send(engine.SSEEvent{Type: "error", Data: fmt.Sprintf("Failed to read primary outputs: %v", err)})
		h.Groups.UpdateStatus(groupID, "failed")
		return
	}

	send(engine.SSEEvent{Type: "output", Data: fmt.Sprintf("Primary outputs captured: %d keys", len(outputs))})

	// ── Phase 2: Wire outputs → workers, deploy in parallel ──────────────
	send(engine.SSEEvent{Type: "output", Data: "═══ Phase 2: Deploying worker stacks ═══"})

	// Apply wiring: primary outputs → worker config.
	for _, wiring := range multiAccount.Wiring {
		if wiring.FromRole != "primary" || wiring.ToRole != "worker" {
			continue
		}
		for _, worker := range workerMembers {
			// Output mappings (e.g., drgOcid → drgOcid)
			for _, m := range wiring.Mappings {
				if out, ok := outputs[m.Output]; ok {
					if val, ok := out.Value.(string); ok {
						worker.config[m.Config] = val
						log.Printf("[group-deploy] %s: %s = %s (from primary output %s)", worker.stackName, m.Config, val, m.Output)
					}
				}
			}
			// Account mappings (e.g., primary tenancyOcid → primaryTenancyOcid)
			for _, am := range wiring.AccountMappings {
				if primaryMember.accountMeta != nil {
					if val, ok := primaryMember.accountMeta[am.AccountField]; ok {
						worker.config[am.Config] = val
					}
				}
			}
			// Update the stack config in DB.
			h.updateStackConfig(worker.stackName, group.Blueprint, worker.config, worker.passphraseID, worker.accountID)
		}
	}

	// Deploy workers in parallel.
	var wg sync.WaitGroup
	var mu sync.Mutex
	failedWorkers := []string{}

	for _, worker := range workerMembers {
		wg.Add(1)
		go func(w *memberWithCreds) {
			defer wg.Done()
			send(engine.SSEEvent{Type: "output", Data: fmt.Sprintf("── Deploying %s ──", w.stackName)})
			status := h.Engine.Up(
				context.Background(), // independent context per worker
				w.stackName,
				group.Blueprint,
				w.config,
				w.creds,
				send,
			)
			if status != "succeeded" {
				mu.Lock()
				failedWorkers = append(failedWorkers, w.stackName)
				mu.Unlock()
			}
		}(worker)
	}
	wg.Wait()

	if len(failedWorkers) > 0 {
		send(engine.SSEEvent{Type: "output", Data: fmt.Sprintf("WARNING: %d worker(s) failed: %s", len(failedWorkers), strings.Join(failedWorkers, ", "))})
	}

	// ── Phase 3: Collect worker tenancy OCIDs → re-up primary for IAM ────
	send(engine.SSEEvent{Type: "output", Data: "═══ Phase 3: Updating primary IAM policies ═══"})

	for _, wiring := range multiAccount.Wiring {
		if wiring.FromRole != "worker" || wiring.ToRole != "primary" {
			continue
		}
		for _, cm := range wiring.CollectMappings {
			var collected []string
			for _, worker := range workerMembers {
				if worker.accountMeta != nil {
					if val, ok := worker.accountMeta[cm.AccountField]; ok && val != "" {
						collected = append(collected, val)
					}
				}
			}
			sep := cm.Separator
			if sep == "" {
				sep = ","
			}
			primaryMember.config[cm.Config] = strings.Join(collected, sep)
			log.Printf("[group-deploy] primary: %s = %s", cm.Config, primaryMember.config[cm.Config])
		}
	}

	// Update primary config and re-deploy (just adds IAM policies).
	h.updateStackConfig(primaryMember.stackName, group.Blueprint, primaryMember.config, primaryMember.passphraseID, primaryMember.accountID)

	reUpStatus := h.Engine.Up(
		r.Context(),
		primaryMember.stackName,
		group.Blueprint,
		primaryMember.config,
		primaryMember.creds,
		send,
	)

	// Set final status.
	if reUpStatus == "succeeded" && len(failedWorkers) == 0 {
		h.Groups.UpdateStatus(groupID, "deployed")
		send(engine.SSEEvent{Type: "output", Data: "═══ Cluster deployment complete ═══"})
	} else if len(failedWorkers) > 0 {
		h.Groups.UpdateStatus(groupID, "partial")
		send(engine.SSEEvent{Type: "output", Data: "═══ Partial deployment — some workers failed ═══"})
	} else {
		h.Groups.UpdateStatus(groupID, "failed")
		send(engine.SSEEvent{Type: "error", Data: "Primary IAM re-up failed"})
	}

	send(engine.SSEEvent{Type: "complete", Data: fmt.Sprintf(`{"status":"%s"}`, group.Status)})
}

// memberWithCreds bundles a group member with resolved credentials and config.
type memberWithCreds struct {
	stackName    string
	config       map[string]string
	creds        engine.Credentials
	accountID    *string
	passphraseID *string
	accountMeta  map[string]string // tenancyOcid, region, etc.
}

func (h *PlatformHandler) resolveMemberCreds(m db.GroupMember) (*memberWithCreds, error) {
	row, err := h.Stacks.Get(m.StackName)
	if err != nil || row == nil {
		return nil, fmt.Errorf("stack %s not found", m.StackName)
	}

	// Parse stack config.
	cfg, err := stacks.ParseYAML(row.ConfigYAML)
	if err != nil {
		return nil, fmt.Errorf("parse config for %s: %v", m.StackName, err)
	}

	// Resolve OCI credentials.
	var ociCreds engine.Credentials
	accountMeta := map[string]string{}

	if row.OciAccountID != nil && *row.OciAccountID != "" {
		account, err := h.Accounts.Get(*row.OciAccountID)
		if err != nil || account == nil {
			return nil, fmt.Errorf("account not found for stack %s", m.StackName)
		}
		ociCreds.OCI = account.ToOCICredentials()
		accountMeta["tenancyOcid"] = account.TenancyOCID
		accountMeta["region"] = account.Region
		accountMeta["tenancyName"] = account.TenancyName
	}

	if row.PassphraseID != nil && *row.PassphraseID != "" {
		passphrase, err := h.Passphrases.GetValue(*row.PassphraseID)
		if err != nil {
			return nil, fmt.Errorf("passphrase not found for stack %s", m.StackName)
		}
		ociCreds.Passphrase = passphrase
	}

	return &memberWithCreds{
		stackName:    m.StackName,
		config:       cfg.Config,
		creds:        ociCreds,
		accountID:    row.OciAccountID,
		passphraseID: row.PassphraseID,
		accountMeta:  accountMeta,
	}, nil
}

func (h *PlatformHandler) updateStackConfig(stackName, blueprint string, config map[string]string, passphraseID, accountID *string) {
	cfg := &stacks.StackConfig{
		APIVersion: "pulumi.io/v1",
		Kind:       "Stack",
		Metadata: stacks.StackMetadata{
			Name:      stackName,
			Blueprint: blueprint,
		},
		Config: config,
	}
	yamlStr, _ := cfg.ToYAML()
	h.Stacks.Upsert(stackName, blueprint, yamlStr, accountID, passphraseID, nil, accountID)
}
