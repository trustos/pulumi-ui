package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/trustos/pulumi-ui/internal/blueprints"
	"github.com/trustos/pulumi-ui/internal/db"
	"github.com/trustos/pulumi-ui/internal/engine"
	"github.com/trustos/pulumi-ui/internal/oci"
	"github.com/trustos/pulumi-ui/internal/stacks"
)

// DeployGroup runs a phased deployment of all stacks in a deployment group.
// Phase 1: Deploy primary stack, capture outputs.
// Phase 2: Re-up primary with worker tenancy OCIDs for cross-tenancy IAM.
// Phase 3: Wire primary outputs → workers, deploy workers in parallel.
// Streams progress as SSE events. The deployment survives client disconnects
// (uses a background context). Cancel via POST /api/groups/{id}/cancel.
func (h *PlatformHandler) DeployGroup(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "id")
	log.Printf("[group-deploy] starting deploy for group %s", groupID)

	group, err := h.Groups.Get(groupID)
	if err != nil || group == nil {
		http.Error(w, "group not found", http.StatusNotFound)
		return
	}
	log.Printf("[group-deploy] group=%s blueprint=%s", group.Name, group.Blueprint)

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
	log.Printf("[group-deploy] %d members in group", len(members))

	// Set up SSE streaming.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Use a background context so the deployment survives browser close or
	// navigation away. The only way to cancel is via the explicit /cancel endpoint.
	opCtx, cancel := context.WithCancel(context.Background())
	h.groupMu.Lock()
	if h.groupCancels == nil {
		h.groupCancels = make(map[string]context.CancelFunc)
	}
	h.groupCancels[groupID] = cancel
	h.groupMu.Unlock()
	defer func() {
		h.groupMu.Lock()
		delete(h.groupCancels, groupID)
		h.groupMu.Unlock()
		cancel()
		log.Printf("[group-deploy] deploy goroutine finished for group %s", groupID)
	}()

	send := func(ev engine.SSEEvent) {
		data, _ := json.Marshal(ev)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		// Persist each event for log recovery across page navigations.
		h.Groups.AppendDeployLog(groupID, string(data))
	}

	h.Groups.ClearDeployLog(groupID)
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
		log.Printf("[group-deploy] resolved member %s role=%s account=%v tenancy=%s",
			mc.stackName, m.Role, mc.accountID, mc.accountMeta["tenancyOcid"])
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

	log.Printf("[group-deploy] primary=%s workers=%d", primaryMember.stackName, len(workerMembers))

	// ── Phase 1: Deploy primary ──────────────────────────────────────────
	// Unlock stacks before deploying — a previous failed/cancelled deploy may have left locks.
	for _, m := range members {
		if err := h.Engine.Unlock(m.StackName); err != nil {
			log.Printf("[group-deploy] unlock %s: %v (non-fatal)", m.StackName, err)
		}
	}

	// Clear phase-specific config from previous deploy attempts.
	// These are only set in Phase 4/5 — a previous failed deploy may have
	// left them populated, causing premature rendering.
	primaryMember.config["workerVcnOcids"] = ""
	primaryMember.config["workerPrivateIps"] = ""
	for _, w := range workerMembers {
		w.config["drgAttached"] = ""
		w.config["nlbAgentPort"] = ""
	}

	// Default serverMode based on infrastructure role if not explicitly set.
	allMembers := append([]*memberWithCreds{primaryMember}, workerMembers...)
	for _, m := range allMembers {
		if m.config["serverMode"] == "" {
			if m == primaryMember {
				m.config["serverMode"] = "server+client"
			} else {
				m.config["serverMode"] = "client"
			}
		}
	}

	// Calculate total server node count across all members for bootstrap_expect.
	totalServerNodes := 0
	for _, m := range allMembers {
		if m.config["serverMode"] == "server" || m.config["serverMode"] == "server+client" {
			nc := 1
			if v, err := strconv.Atoi(m.config["nodeCount"]); err == nil && v > 0 {
				nc = v
			}
			totalServerNodes += nc
		}
	}
	bootstrapExpect := strconv.Itoa(totalServerNodes)
	for _, m := range allMembers {
		m.config["bootstrapExpect"] = bootstrapExpect
	}
	log.Printf("[group-deploy] serverMode defaults applied, bootstrapExpect=%s (total server nodes)", bootstrapExpect)

	send(engine.SSEEvent{Type: "output", Data: "═══ Phase 1: Deploying primary stack ═══"})
	log.Printf("[group-deploy] phase 1: deploying primary %s (blueprint=%s)", primaryMember.stackName, group.Blueprint)
	primaryStatus := h.trackedUp(
		opCtx,
		primaryMember.stackName,
		group.Blueprint,
		primaryMember.config,
		primaryMember.creds,
		send,
	)
	log.Printf("[group-deploy] phase 1: primary status=%s", primaryStatus)
	if primaryStatus != "succeeded" {
		send(engine.SSEEvent{Type: "error", Data: "Primary deployment failed — aborting group deploy"})
		h.Groups.UpdateStatus(groupID, "failed")
		return
	}

	// Capture primary outputs.
	outputs, err := h.Engine.GetStackOutputs(
		opCtx,
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

	if len(outputs) == 0 {
		send(engine.SSEEvent{Type: "error", Data: "Primary deployment produced no outputs — workers cannot be wired"})
		h.Groups.UpdateStatus(groupID, "failed")
		return
	}
	log.Printf("[group-deploy] primary outputs: %d keys", len(outputs))
	for k, v := range outputs {
		log.Printf("[group-deploy]   output %s = %v", k, v.Value)
	}
	send(engine.SSEEvent{Type: "output", Data: fmt.Sprintf("Primary outputs captured: %d keys", len(outputs))})

	// Resolve primary pool instance IPs via OCI API (data sources fail at deploy time).
	primaryIPs, err := h.resolvePoolInstanceIPs(primaryMember, outputs)
	if err != nil {
		log.Printf("[group-deploy] WARNING: could not resolve primary pool IPs: %v", err)
	} else if len(primaryIPs) > 0 {
		log.Printf("[group-deploy] primary pool IPs: %v", primaryIPs)
		// Store first IP as the primary's private IP for worker wiring
		outputs["instancePrivateIp"] = auto.OutputValue{Value: primaryIPs[0]}
	}

	// ── Phase 1.5: Re-up primary with poolReady=true ─────────────────────
	// Pool instances are now RUNNING. Re-up creates per-node agent NLB backends
	// using the getInstancePoolInstances data source (which now resolves valid IDs).
	send(engine.SSEEvent{Type: "output", Data: "── Creating per-node agent backends ──"})
	primaryMember.config["poolReady"] = "true"
	if err := h.updateStackConfig(primaryMember.stackName, group.Blueprint, primaryMember.config, primaryMember.passphraseID, primaryMember.accountID); err != nil {
		log.Printf("[group-deploy] ERROR: failed to update primary config for phase 1.5: %v", err)
	}
	phase15Status := h.trackedUp(opCtx, primaryMember.stackName, group.Blueprint, primaryMember.config, primaryMember.creds, send)
	log.Printf("[group-deploy] phase 1.5: pool-ready re-up status=%s", phase15Status)
	if phase15Status != "succeeded" {
		send(engine.SSEEvent{Type: "error", Data: "Failed to create per-node agent backends — continuing with round-robin"})
	}

	// Re-read outputs after Phase 1.5 (instancePrivateIp now available)
	outputs, err = h.Engine.GetStackOutputs(opCtx, primaryMember.stackName, group.Blueprint, primaryMember.config, primaryMember.creds)
	if err != nil {
		log.Printf("[group-deploy] WARNING: could not re-read outputs after phase 1.5: %v", err)
	} else {
		log.Printf("[group-deploy] phase 1.5 outputs: %d keys", len(outputs))
		for k, v := range outputs {
			log.Printf("[group-deploy]   output %s = %v", k, v.Value)
		}
	}

	// ── Phase 2: Create cross-tenancy IAM policies on primary ────────────
	// This must happen BEFORE deploying workers, because workers need
	// permission to reference the primary's DRG for cross-tenancy routing.
	send(engine.SSEEvent{Type: "output", Data: "═══ Phase 2: Creating cross-tenancy IAM policies ═══"})

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
						log.Printf("[group-deploy] collected %s=%s from worker %s", cm.AccountField, val, worker.stackName)
					}
				}
			}
			sep := cm.Separator
			if sep == "" {
				sep = ","
			}
			primaryMember.config[cm.Config] = strings.Join(collected, sep)
			log.Printf("[group-deploy] primary config: %s = %q", cm.Config, primaryMember.config[cm.Config])
		}
	}

	// Collect worker VCN CIDRs as peerCidrs for primary route tables.
	workerCidrs := []string{}
	for _, worker := range workerMembers {
		if cidr := worker.config["vcnCidr"]; cidr != "" {
			workerCidrs = append(workerCidrs, cidr)
		}
	}
	primaryMember.config["peerCidrs"] = strings.Join(workerCidrs, ",")
	log.Printf("[group-deploy] phase 2: peerCidrs=%s", primaryMember.config["peerCidrs"])

	// Log full primary config for debugging
	log.Printf("[group-deploy] phase 2: primary config before re-deploy (%d keys):", len(primaryMember.config))
	for k, v := range primaryMember.config {
		if strings.Contains(strings.ToLower(k), "key") || strings.Contains(strings.ToLower(k), "private") {
			log.Printf("[group-deploy]   %s = [REDACTED]", k)
		} else {
			log.Printf("[group-deploy]   %s = %s", k, v)
		}
	}

	// Update primary config and re-deploy to create the Admit policies.
	if err := h.updateStackConfig(primaryMember.stackName, group.Blueprint, primaryMember.config, primaryMember.passphraseID, primaryMember.accountID); err != nil {
		log.Printf("[group-deploy] ERROR: failed to update primary config: %v", err)
		send(engine.SSEEvent{Type: "error", Data: fmt.Sprintf("Failed to update primary config: %v", err)})
	}

	log.Printf("[group-deploy] phase 2: re-deploying primary for IAM policies")
	iamStatus := h.trackedUp(
		opCtx,
		primaryMember.stackName,
		group.Blueprint,
		primaryMember.config,
		primaryMember.creds,
		send,
	)
	log.Printf("[group-deploy] phase 2: IAM re-up status=%s", iamStatus)
	if iamStatus != "succeeded" {
		send(engine.SSEEvent{Type: "error", Data: "Failed to create cross-tenancy IAM policies — aborting"})
		h.Groups.UpdateStatus(groupID, "failed")
		send(engine.SSEEvent{Type: "complete", Data: `{"status":"failed"}`})
		return
	}

	// ── Phase 3: Wire outputs → workers, deploy in parallel ──────────────
	// Workers can now reference the primary's DRG because the Admit policies exist.
	send(engine.SSEEvent{Type: "output", Data: "═══ Phase 3: Deploying worker stacks ═══"})

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
						log.Printf("[group-deploy] wire %s: %s = %s (from primary output %s)", worker.stackName, m.Config, val, m.Output)
					}
				}
			}
			// Account mappings (e.g., primary tenancyOcid → primaryTenancyOcid)
			for _, am := range wiring.AccountMappings {
				if primaryMember.accountMeta != nil {
					if val, ok := primaryMember.accountMeta[am.AccountField]; ok {
						worker.config[am.Config] = val
						log.Printf("[group-deploy] wire %s: %s = %s (from primary account %s)", worker.stackName, am.Config, val, am.AccountField)
					}
				}
			}

			// Log full worker config before deploy
			log.Printf("[group-deploy] worker %s config (%d keys):", worker.stackName, len(worker.config))
			for k, v := range worker.config {
				if strings.Contains(strings.ToLower(k), "key") || strings.Contains(strings.ToLower(k), "private") {
					log.Printf("[group-deploy]   %s = [REDACTED]", k)
				} else {
					log.Printf("[group-deploy]   %s = %s", k, v)
				}
			}

			// Update the stack config in DB.
			if err := h.updateStackConfig(worker.stackName, group.Blueprint, worker.config, worker.passphraseID, worker.accountID); err != nil {
				log.Printf("[group-deploy] ERROR: failed to update config for %s: %v", worker.stackName, err)
				send(engine.SSEEvent{Type: "error", Data: fmt.Sprintf("Failed to update config for %s: %v", worker.stackName, err)})
			}
		}
	}

	// Deploy workers in parallel.
	var wg sync.WaitGroup
	var mu sync.Mutex
	failedWorkers := []string{}

	for _, worker := range workerMembers {
		wg.Add(1)
		go func(w *memberWithCreds) {
			defer func() {
				if rec := recover(); rec != nil {
					mu.Lock()
					failedWorkers = append(failedWorkers, w.stackName)
					mu.Unlock()
					log.Printf("[group-deploy] PANIC deploying %s: %v", w.stackName, rec)
					send(engine.SSEEvent{Type: "error", Data: fmt.Sprintf("Panic deploying %s: %v", w.stackName, rec)})
				}
				wg.Done()
			}()
			log.Printf("[group-deploy] phase 3: starting worker deploy %s (account=%v)", w.stackName, w.accountID)
			send(engine.SSEEvent{Type: "output", Data: fmt.Sprintf("── Deploying %s ──", w.stackName)})
			status := h.trackedUp(opCtx, w.stackName, group.Blueprint, w.config, w.creds, send)
			log.Printf("[group-deploy] phase 3: worker %s status=%s", w.stackName, status)
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
		h.Groups.UpdateStatus(groupID, "partial")
		send(engine.SSEEvent{Type: "output", Data: "═══ Partial deployment — some workers failed ═══"})
		send(engine.SSEEvent{Type: "complete", Data: `{"status":"partial"}`})
		return
	}

	// ── Phase 4: Create cross-tenancy DRG attachments to worker VCNs ─────
	// The primary (DRG owner) creates cross-tenancy attachments using worker
	// VCN OCIDs captured from Phase 3 outputs.
	send(engine.SSEEvent{Type: "output", Data: "═══ Phase 4: Creating cross-tenancy DRG attachments ═══"})

	workerVcnOcids := []string{}
	workerPrivateIps := []string{}
	for _, worker := range workerMembers {
		wOutputs, err := h.Engine.GetStackOutputs(opCtx, worker.stackName, group.Blueprint, worker.config, worker.creds)
		if err != nil {
			log.Printf("[group-deploy] phase 4: failed to get outputs for %s: %v", worker.stackName, err)
			send(engine.SSEEvent{Type: "error", Data: fmt.Sprintf("Failed to read outputs for %s: %v", worker.stackName, err)})
			continue
		}
		if vcn, ok := wOutputs["vcnOcid"]; ok {
			if val, ok := vcn.Value.(string); ok && val != "" {
				workerVcnOcids = append(workerVcnOcids, val)
				log.Printf("[group-deploy] phase 4: worker %s vcnOcid=%s", worker.stackName, val)
			}
		}
		// Resolve worker pool instance IPs via OCI API
		wIPs, err := h.resolvePoolInstanceIPs(worker, wOutputs)
		if err != nil {
			log.Printf("[group-deploy] phase 4: WARNING: could not resolve pool IPs for %s: %v", worker.stackName, err)
		}
		workerPrivateIps = append(workerPrivateIps, wIPs...)
		for _, ip := range wIPs {
			log.Printf("[group-deploy] phase 4: worker %s poolInstanceIp=%s", worker.stackName, ip)
		}
	}

	if len(workerVcnOcids) == 0 {
		send(engine.SSEEvent{Type: "error", Data: "No worker VCN OCIDs captured — cannot create cross-tenancy DRG attachments"})
		h.Groups.UpdateStatus(groupID, "partial")
		send(engine.SSEEvent{Type: "complete", Data: `{"status":"partial"}`})
		return
	}

	primaryMember.config["workerVcnOcids"] = strings.Join(workerVcnOcids, ",")
	primaryMember.config["workerPrivateIps"] = strings.Join(workerPrivateIps, ",")
	log.Printf("[group-deploy] phase 4: workerVcnOcids=%s", primaryMember.config["workerVcnOcids"])

	if err := h.updateStackConfig(primaryMember.stackName, group.Blueprint, primaryMember.config, primaryMember.passphraseID, primaryMember.accountID); err != nil {
		log.Printf("[group-deploy] ERROR: failed to update primary config for phase 4: %v", err)
		send(engine.SSEEvent{Type: "error", Data: fmt.Sprintf("Failed to update primary config: %v", err)})
	}

	// Retry Phase 4 with delays — cross-tenancy IAM policies from Phase 2 and
	// Phase 3 need time to propagate through OCI's distributed IAM system.
	var attachStatus string
	for attempt := 1; attempt <= 3; attempt++ {
		attachStatus = h.trackedUp(opCtx, primaryMember.stackName, group.Blueprint, primaryMember.config, primaryMember.creds, send)
		log.Printf("[group-deploy] phase 4: DRG attachment attempt %d/3 status=%s", attempt, attachStatus)
		if attachStatus == "succeeded" {
			break
		}
		if attempt < 3 {
			delay := time.Duration(attempt) * 90 * time.Second
			send(engine.SSEEvent{Type: "output", Data: fmt.Sprintf("Cross-tenancy DRG attachment failed — waiting %ds for IAM propagation (attempt %d/3)...", int(delay.Seconds()), attempt)})
			log.Printf("[group-deploy] phase 4: waiting %v for IAM propagation before retry", delay)
			select {
			case <-time.After(delay):
			case <-opCtx.Done():
				attachStatus = "cancelled"
			}
		}
	}
	if attachStatus != "succeeded" {
		send(engine.SSEEvent{Type: "error", Data: "Failed to create cross-tenancy DRG attachments after retries"})
		h.Groups.UpdateStatus(groupID, "partial")
		send(engine.SSEEvent{Type: "complete", Data: `{"status":"partial"}`})
		return
	}

	// ── Phase 5: Update worker routes + agent discovery config ───────────
	// Workers get: DRG route, NLB public IP + agent port for Nebula discovery,
	// and peer CIDRs for DRG routing.
	send(engine.SSEEvent{Type: "output", Data: "═══ Phase 5: Updating worker routes ═══"})

	// Get the primary's NLB public IP from outputs for worker agent discovery.
	nlbPublicIp := ""
	phase5Outputs, err := h.Engine.GetStackOutputs(opCtx, primaryMember.stackName, group.Blueprint, primaryMember.config, primaryMember.creds)
	if err == nil {
		if v, ok := phase5Outputs["nlbPublicIp"]; ok {
			if s, ok := v.Value.(string); ok {
				nlbPublicIp = s
			}
		}
	}
	log.Printf("[group-deploy] phase 5: nlbPublicIp=%s", nlbPublicIp)

	// Primary VCN CIDR as peer CIDR for workers
	primaryVcnCidr := primaryMember.config["vcnCidr"]

	// Worker NLB agent ports start after primary's agent-injected ports.
	primaryNodeCount := 1
	if nc, err := strconv.Atoi(primaryMember.config["nodeCount"]); err == nil && nc > 0 {
		primaryNodeCount = nc
	}
	workerNlbPortBase := 41821 + primaryNodeCount // e.g., 41822 for 1-node primary, 41823 for 2-node

	failedRoutes := []string{}
	for i, worker := range workerMembers {
		worker.config["drgAttached"] = "true"
		worker.config["peerCidrs"] = primaryVcnCidr
		if nlbPublicIp != "" {
			worker.config["nlbPublicIp"] = nlbPublicIp
			worker.config["nlbAgentPort"] = fmt.Sprintf("%d", workerNlbPortBase+i)
		}
		log.Printf("[group-deploy] phase 5: updating worker %s (drgAttached=true, drgOcid=%s, nlbPublicIp=%s, nlbAgentPort=%s, peerCidrs=%s)",
			worker.stackName, worker.config["drgOcid"], worker.config["nlbPublicIp"], worker.config["nlbAgentPort"], worker.config["peerCidrs"])

		if err := h.updateStackConfig(worker.stackName, group.Blueprint, worker.config, worker.passphraseID, worker.accountID); err != nil {
			log.Printf("[group-deploy] ERROR: failed to update config for %s: %v", worker.stackName, err)
			send(engine.SSEEvent{Type: "error", Data: fmt.Sprintf("Failed to update config for %s: %v", worker.stackName, err)})
		}

		send(engine.SSEEvent{Type: "output", Data: fmt.Sprintf("── Updating routes for %s ──", worker.stackName)})
		routeStatus := h.trackedUp(opCtx, worker.stackName, group.Blueprint, worker.config, worker.creds, send)
		log.Printf("[group-deploy] phase 5: worker %s route update status=%s", worker.stackName, routeStatus)
		if routeStatus != "succeeded" {
			failedRoutes = append(failedRoutes, worker.stackName)
		}
	}

	// Set final status.
	finalStatus := "failed"
	if len(failedRoutes) == 0 {
		finalStatus = "deployed"
		h.Groups.UpdateStatus(groupID, "deployed")
		send(engine.SSEEvent{Type: "output", Data: "═══ Cluster deployment complete ═══"})
	} else {
		finalStatus = "partial"
		h.Groups.UpdateStatus(groupID, "partial")
		send(engine.SSEEvent{Type: "output", Data: fmt.Sprintf("═══ Partial deployment — %d worker route update(s) failed: %s ═══", len(failedRoutes), strings.Join(failedRoutes, ", "))})
	}

	log.Printf("[group-deploy] deploy complete: group=%s status=%s", group.Name, finalStatus)
	send(engine.SSEEvent{Type: "complete", Data: fmt.Sprintf(`{"status":"%s"}`, finalStatus)})
}

// CancelGroupDeploy cancels a running group deployment.
func (h *PlatformHandler) CancelGroupDeploy(w http.ResponseWriter, r *http.Request) {
	groupID := chi.URLParam(r, "id")
	log.Printf("[group-deploy] cancel requested for group %s", groupID)

	h.groupMu.Lock()
	cancel, ok := h.groupCancels[groupID]
	h.groupMu.Unlock()

	if !ok {
		http.Error(w, "no running deployment for this group", http.StatusNotFound)
		return
	}

	cancel()

	// Also cancel each member stack's operation via the engine.
	members, _ := h.Groups.ListMembers(groupID)
	for _, m := range members {
		log.Printf("[group-deploy] cancelling stack %s", m.StackName)
		h.Engine.Cancel(m.StackName)
	}

	w.WriteHeader(http.StatusOK)
}

// IsGroupDeploying reports whether a group deployment is currently in flight.
func (h *PlatformHandler) IsGroupDeploying(groupID string) bool {
	h.groupMu.Lock()
	defer h.groupMu.Unlock()
	_, ok := h.groupCancels[groupID]
	return ok
}

// trackedUp runs Engine.Up and records the operation in the operations table
// so the stack shows correct status in the stack detail page.
func (h *PlatformHandler) trackedUp(
	ctx context.Context,
	stackName, blueprint string,
	config map[string]string,
	creds engine.Credentials,
	send engine.SSESender,
) string {
	opID := uuid.New().String()
	h.Ops.Create(opID, stackName, "up")
	logSend := func(ev engine.SSEEvent) {
		send(ev)
		if ev.Type == "output" || ev.Type == "error" {
			h.Ops.AppendLog(opID, ev.Data)
		}
	}
	status := h.Engine.Up(ctx, stackName, blueprint, config, creds, logSend)
	h.Ops.Finish(opID, status)
	return status
}

// trackedRefresh runs Engine.Refresh and records the operation.
func (h *PlatformHandler) trackedRefresh(
	ctx context.Context,
	stackName, blueprint string,
	config map[string]string,
	creds engine.Credentials,
	send engine.SSESender,
) string {
	opID := uuid.New().String()
	h.Ops.Create(opID, stackName, "refresh")
	logSend := func(ev engine.SSEEvent) {
		send(ev)
		if ev.Type == "output" || ev.Type == "error" {
			h.Ops.AppendLog(opID, ev.Data)
		}
	}
	status := h.Engine.Refresh(ctx, stackName, blueprint, config, creds, logSend)
	h.Ops.Finish(opID, status)
	return status
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
	log.Printf("[group-deploy] loaded config for %s: %d keys", m.StackName, len(cfg.Config))

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
		log.Printf("[group-deploy] credentials for %s: account=%s tenancy=...%s region=%s",
			m.StackName, account.Name, account.TenancyOCID[len(account.TenancyOCID)-12:], account.Region)
	} else {
		log.Printf("[group-deploy] WARNING: no OCI account for stack %s", m.StackName)
	}

	if row.PassphraseID != nil && *row.PassphraseID != "" {
		passphrase, err := h.Passphrases.GetValue(*row.PassphraseID)
		if err != nil {
			return nil, fmt.Errorf("passphrase not found for stack %s", m.StackName)
		}
		ociCreds.Passphrase = passphrase
		log.Printf("[group-deploy] passphrase loaded for %s", m.StackName)
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

func (h *PlatformHandler) updateStackConfig(stackName, blueprint string, config map[string]string, passphraseID, accountID *string) error {
	cfg := &stacks.StackConfig{
		APIVersion: "pulumi.io/v1",
		Kind:       "Stack",
		Metadata: stacks.StackMetadata{
			Name:      stackName,
			Blueprint: blueprint,
		},
		Config: config,
	}
	yamlStr, err := cfg.ToYAML()
	if err != nil {
		return fmt.Errorf("marshal config for %s: %w", stackName, err)
	}
	log.Printf("[group-deploy] saving config for %s (%d bytes)", stackName, len(yamlStr))
	return h.Stacks.Upsert(stackName, blueprint, yamlStr, accountID, passphraseID, nil, accountID)
}

// resolvePoolInstanceIPs queries OCI API to get private IPs for all instances
// in an InstancePool. Uses poolId and compartmentId from Pulumi stack outputs.
func (h *PlatformHandler) resolvePoolInstanceIPs(member *memberWithCreds, outputs auto.OutputMap) ([]string, error) {
	poolID := ""
	compartmentID := ""
	if v, ok := outputs["poolId"]; ok {
		poolID, _ = v.Value.(string)
	}
	if v, ok := outputs["compartmentId"]; ok {
		compartmentID, _ = v.Value.(string)
	}
	if poolID == "" || compartmentID == "" {
		return nil, fmt.Errorf("missing poolId or compartmentId outputs")
	}

	// Build OCI client from member credentials
	ociCred := member.creds.OCI
	if ociCred.TenancyOCID == "" {
		return nil, fmt.Errorf("no OCI credentials for %s", member.stackName)
	}
	client, err := oci.NewClient(ociCred.TenancyOCID, ociCred.UserOCID, ociCred.Fingerprint, ociCred.PrivateKey, ociCred.Region)
	if err != nil {
		return nil, fmt.Errorf("create OCI client: %w", err)
	}

	// List pool instances
	instances, err := client.ListInstancePoolInstances(compartmentID, poolID)
	if err != nil {
		return nil, fmt.Errorf("list pool instances: %w", err)
	}
	log.Printf("[group-deploy] pool %s: %d instances found", poolID[len(poolID)-12:], len(instances))
	for i, inst := range instances {
		state := inst.State
		if state == "" {
			state = inst.LifecycleState
		}
		log.Printf("[group-deploy]   instance[%d] id=%s state=%s name=%s", i, inst.ID, state, inst.DisplayName)
	}

	// Resolve private IPs for each instance
	var ips []string
	for _, inst := range instances {
		state := inst.State
		if state == "" {
			state = inst.LifecycleState
		}
		if state != "Running" && state != "RUNNING" {
			log.Printf("[group-deploy] pool instance %s state=%s, skipping", inst.ID, state)
			continue
		}
		ip, err := client.GetInstancePrivateIP(compartmentID, inst.ID)
		if err != nil {
			log.Printf("[group-deploy] failed to get IP for instance %s: %v", inst.ID, err)
			continue
		}
		ips = append(ips, ip)
		log.Printf("[group-deploy] pool instance %s privateIp=%s", inst.ID, ip)
	}
	return ips, nil
}
