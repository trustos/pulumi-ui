package applications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/trustos/pulumi-ui/internal/db"
	"github.com/trustos/pulumi-ui/internal/programs"
)

// LogFunc is a callback for streaming deploy output. The engine adapter
// converts between this and engine.SSESender to avoid an import cycle.
type LogFunc func(eventType, message string)

// Deployer manages post-infrastructure application deployment over the Nebula mesh.
type Deployer struct {
	connStore *db.StackConnectionStore
}

func NewDeployer(connStore *db.StackConnectionStore) *Deployer {
	return &Deployer{connStore: connStore}
}

// DeployApps executes the Phase 2 (mesh) and Phase 3 (application deploy) pipeline.
// It streams progress through the log callback.
func (d *Deployer) DeployApps(
	ctx context.Context,
	stackName string,
	lighthouseAddr string,
	selectedApps map[string]bool,
	catalog []programs.ApplicationDef,
	send LogFunc,
) error {
	send("output", "=== Phase 2: Establishing mesh connectivity ===")

	conn, err := d.connStore.Get(stackName)
	if err != nil {
		return fmt.Errorf("load stack connection: %w", err)
	}
	if conn == nil {
		return fmt.Errorf("no Nebula PKI found for stack %q — create the stack first", stackName)
	}

	if lighthouseAddr != "" && (conn.LighthouseAddr == nil || *conn.LighthouseAddr != lighthouseAddr) {
		if err := d.connStore.UpdateLighthouse(stackName, lighthouseAddr); err != nil {
			send("error", fmt.Sprintf("Failed to update lighthouse address: %v", err))
		}
	}

	agentAddr := "http://10.42.0.2:41820"
	if conn.AgentNebulaIP != nil {
		agentAddr = fmt.Sprintf("http://%s:41820", *conn.AgentNebulaIP)
	}

	send("output", fmt.Sprintf("Waiting for agent at %s...", agentAddr))

	if err := d.waitForAgent(ctx, agentAddr, conn, stackName, send); err != nil {
		return err
	}

	send("output", "=== Phase 3: Deploying applications ===")

	workloadApps := filterWorkloads(catalog, selectedApps)
	if len(workloadApps) == 0 {
		send("output", "No workload applications selected.")
		return nil
	}

	for _, app := range workloadApps {
		send("output", fmt.Sprintf("Deploying %s...", app.Name))
		if err := d.deployWorkload(ctx, agentAddr, conn, app, send); err != nil {
			send("error", fmt.Sprintf("Failed to deploy %s: %v", app.Name, err))
			continue
		}
		send("output", fmt.Sprintf("%s deployed successfully.", app.Name))
	}

	return nil
}

// waitForAgent polls the agent's /health endpoint until it responds or the
// context deadline is exceeded (10 minutes by default).
func (d *Deployer) waitForAgent(
	ctx context.Context,
	agentAddr string,
	conn *db.StackConnection,
	stackName string,
	send LogFunc,
) error {
	timeout := 10 * time.Minute
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	client := &http.Client{Timeout: 5 * time.Second}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("agent at %s did not become healthy within %v", agentAddr, timeout)
			}

			req, _ := http.NewRequestWithContext(ctx, "GET", agentAddr+"/health", nil)
			req.Header.Set("Authorization", "Bearer "+conn.AgentToken)
			resp, err := client.Do(req)
			if err != nil {
				send("output", fmt.Sprintf("Agent not ready yet: %v", err))
				continue
			}
			resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				send("output", "Agent is healthy.")
				d.connStore.UpdateLastSeen(stackName)
				return nil
			}
			send("output", fmt.Sprintf("Agent returned status %d, retrying...", resp.StatusCode))
		}
	}
}

// deployWorkload sends a command to the agent to deploy a Nomad job.
func (d *Deployer) deployWorkload(
	ctx context.Context,
	agentAddr string,
	conn *db.StackConnection,
	app programs.ApplicationDef,
	send LogFunc,
) error {
	execReq := struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}{
		Command: "nomad",
		Args:    []string{"job", "run", fmt.Sprintf("/opt/nomad-jobs/%s.nomad.hcl", app.Key)},
	}

	body, _ := json.Marshal(execReq)
	req, err := http.NewRequestWithContext(ctx, "POST", agentAddr+"/exec", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+conn.AgentToken)

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("exec request: %w", err)
	}
	defer resp.Body.Close()

	// Stream the output
	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			send("output", string(buf[:n]))
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read exec output: %w", readErr)
		}
	}

	return nil
}

// filterWorkloads returns only workload-tier apps that the user selected.
func filterWorkloads(catalog []programs.ApplicationDef, selected map[string]bool) []programs.ApplicationDef {
	var result []programs.ApplicationDef
	for _, app := range catalog {
		if app.Tier != programs.TierWorkload {
			continue
		}
		if selected[app.Key] {
			result = append(result, app)
		}
	}
	return result
}
