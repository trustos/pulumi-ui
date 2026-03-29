package applications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"text/template"
	"time"

	builtins "github.com/trustos/pulumi-ui/programs"

	"github.com/trustos/pulumi-ui/internal/db"
	"github.com/trustos/pulumi-ui/internal/mesh"
	"github.com/trustos/pulumi-ui/internal/programs"
)

// LogFunc is a callback for streaming deploy output. The engine adapter
// converts between this and engine.SSESender to avoid an import cycle.
type LogFunc func(eventType, message string)

// Deployer manages post-infrastructure application deployment over the Nebula mesh.
type Deployer struct {
	connStore   *db.StackConnectionStore
	meshManager *mesh.Manager
}

func NewDeployer(connStore *db.StackConnectionStore, meshManager *mesh.Manager) *Deployer {
	return &Deployer{
		connStore:   connStore,
		meshManager: meshManager,
	}
}

// DeployApps executes the application deployment pipeline.
// It establishes mesh connectivity, waits for the agent, uploads job templates,
// and runs each selected workload application via `nomad job run`.
func (d *Deployer) DeployApps(
	ctx context.Context,
	stackName string,
	selectedApps map[string]bool,
	appConfig map[string]string,
	catalog []programs.ApplicationDef,
	send LogFunc,
) error {
	send("output", "=== Establishing mesh connectivity ===")

	tunnel, err := d.waitForAgent(ctx, stackName, send)
	if err != nil {
		return err
	}

	send("output", "=== Deploying applications ===")

	workloadApps := filterWorkloads(catalog, selectedApps)
	if len(workloadApps) == 0 {
		send("output", "No workload applications selected.")
		return nil
	}

	for _, app := range workloadApps {
		send("output", fmt.Sprintf("Deploying %s...", app.Name))

		// Upload the rendered job template to the agent.
		if err := d.uploadJobFile(ctx, tunnel, app, appConfig, send); err != nil {
			send("error", fmt.Sprintf("Failed to upload job file for %s: %v", app.Name, err))
			continue
		}

		// Execute `nomad job run` on the agent.
		if err := d.deployWorkload(ctx, tunnel, app, send); err != nil {
			send("error", fmt.Sprintf("Failed to deploy %s: %v", app.Name, err))
			continue
		}
		send("output", fmt.Sprintf("%s deployed successfully.", app.Name))
	}

	return nil
}

// waitForAgent polls the agent's /health endpoint through a mesh tunnel until
// it responds or the context deadline is exceeded (10 minutes).
func (d *Deployer) waitForAgent(
	ctx context.Context,
	stackName string,
	send LogFunc,
) (*mesh.Tunnel, error) {
	timeout := 10 * time.Minute
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	send("output", "Waiting for agent to become healthy...")

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("agent for stack %q did not become healthy within %v", stackName, timeout)
			}

			tunnel, err := d.meshManager.GetTunnelForNode(stackName, 0)
			if err != nil {
				send("output", fmt.Sprintf("Tunnel not ready: %v", err))
				continue
			}

			client := tunnel.HTTPClient()
			req, _ := http.NewRequestWithContext(ctx, "GET", tunnel.AgentURL()+"/health", nil)
			req.Header.Set("Authorization", "Bearer "+tunnel.Token())

			resp, err := client.Do(req)
			if err != nil {
				send("output", fmt.Sprintf("Agent not ready yet: %v", err))
				continue
			}
			resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				send("output", "Agent is healthy.")
				if d.connStore != nil {
					d.connStore.UpdateLastSeen(stackName)
				}
				return tunnel, nil
			}
			send("output", fmt.Sprintf("Agent returned status %d, retrying...", resp.StatusCode))
		}
	}
}

// uploadJobFile reads the embedded job template for the application, renders
// it with app-specific config values, and uploads it to the agent.
func (d *Deployer) uploadJobFile(
	ctx context.Context,
	tunnel *mesh.Tunnel,
	app programs.ApplicationDef,
	appConfig map[string]string,
	send LogFunc,
) error {
	templateContent, err := builtins.ReadJobFile(app.Key + ".nomad.hcl")
	if err != nil {
		return fmt.Errorf("read job template: %w", err)
	}

	// Build template data from appConfig. Keys are stored as "appKey.fieldKey"
	// (e.g. "github-runner.githubToken"), extract just the field key part.
	data := make(map[string]string)
	prefix := app.Key + "."
	for k, v := range appConfig {
		if strings.HasPrefix(k, prefix) {
			data[strings.TrimPrefix(k, prefix)] = v
		}
	}
	// Also apply defaults from the app's config fields.
	for _, cf := range app.ConfigFields {
		if _, ok := data[cf.Key]; !ok && cf.Default != "" {
			data[cf.Key] = cf.Default
		}
	}

	tmpl, err := template.New(app.Key).Delims("[[", "]]").Parse(templateContent)
	if err != nil {
		return fmt.Errorf("parse job template: %w", err)
	}

	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, data); err != nil {
		return fmt.Errorf("render job template: %w", err)
	}

	destPath := fmt.Sprintf("/opt/nomad-jobs/%s.nomad.hcl", app.Key)
	send("output", fmt.Sprintf("Uploading job file to %s...", destPath))

	client := tunnel.HTTPClient()
	req, err := http.NewRequestWithContext(ctx, "POST", tunnel.AgentURL()+"/upload", &rendered)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tunnel.Token())
	req.Header.Set("X-Dest-Path", destPath)
	req.Header.Set("X-File-Mode", "0644")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("upload request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload failed (status %d): %s", resp.StatusCode, string(body))
	}

	log.Printf("[deployer] uploaded %s to agent", destPath)
	return nil
}

// deployWorkload sends a command to the agent to deploy a Nomad job.
func (d *Deployer) deployWorkload(
	ctx context.Context,
	tunnel *mesh.Tunnel,
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
	client := tunnel.HTTPClient()
	req, err := http.NewRequestWithContext(ctx, "POST", tunnel.AgentURL()+"/exec", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tunnel.Token())

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("exec request: %w", err)
	}
	defer resp.Body.Close()

	// Stream the output.
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
