package applications

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
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

// deployTimeout is the maximum time to wait for a single app deployment to become healthy.
const deployTimeout = 10 * time.Minute

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

	if _, err := d.waitForAgent(ctx, stackName, send); err != nil {
		return err
	}

	send("output", "=== Deploying applications ===")

	workloadApps := filterWorkloads(catalog, selectedApps)
	if len(workloadApps) == 0 {
		send("output", "No workload applications selected.")
		return nil
	}

	var failed []string
	for _, app := range workloadApps {
		send("output", fmt.Sprintf("Deploying %s...", app.Name))

		// Resolve tunnel fresh for each app — cached tunnels may be replaced
		// by health check retries or the idle reaper between deploys.
		tunnel, err := d.meshManager.GetTunnelForNode(stackName, 0)
		if err != nil {
			send("error", fmt.Sprintf("Failed to get tunnel for %s: %v", app.Name, err))
			failed = append(failed, app.Key)
			continue
		}

		// Upload the rendered job template to the agent.
		if err := d.uploadJobFile(ctx, tunnel, app, appConfig, send); err != nil {
			send("error", fmt.Sprintf("Failed to upload job file for %s: %v", app.Name, err))
			failed = append(failed, app.Key)
			continue
		}

		// Register and monitor the Nomad job via the agent.
		if err := d.deployWorkload(ctx, stackName, tunnel, app, send); err != nil {
			send("error", fmt.Sprintf("Failed to deploy %s: %v", app.Name, err))
			failed = append(failed, app.Key)
			continue
		}
		send("output", fmt.Sprintf("%s deployed successfully.", app.Name))
	}

	if len(failed) > 0 {
		return fmt.Errorf("%d application(s) failed to deploy: %s", len(failed), strings.Join(failed, ", "))
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
	// Internal metadata keys (prefixed with "_") are skipped.
	data := make(map[string]string)
	prefix := app.Key + "."
	for k, v := range appConfig {
		if strings.HasPrefix(k, prefix) {
			fieldKey := strings.TrimPrefix(k, prefix)
			if strings.HasPrefix(fieldKey, "_") {
				continue // skip internal metadata keys like _autoCredentials
			}
			data[fieldKey] = v
		}
	}
	// Ensure all declared config fields exist in data (with defaults or empty).
	for _, cf := range app.ConfigFields {
		if _, ok := data[cf.Key]; !ok {
			data[cf.Key] = cf.Default // empty string if no default
		}
	}

	// Build set of Consul KV-managed secret field keys.
	secretFields := make(map[string]bool)
	for _, cf := range app.ConfigFields {
		if cf.Secret {
			secretFields[cf.Key] = true
		}
	}

	// Auto-generate secrets for empty fields. Fields marked secret: true
	// are managed by the job's init-secrets task in Consul KV — leave them
	// empty so init-secrets generates the value. Non-catalog secret fields
	// (matching isSecretField heuristic) still get Go-side generation.
	for key, val := range data {
		if val != "" {
			continue
		}
		if secretFields[key] {
			// Consul KV init-secrets will handle generation — leave empty.
			continue
		}
		if isSecretField(key) {
			generated := generateSecret()
			data[key] = generated
			appConfig[prefix+key] = generated // persist back
			send("output", fmt.Sprintf("  Auto-generated %s.%s", app.Key, key))
		}
	}

	// Validate all required config fields have non-empty values.
	for _, cf := range app.ConfigFields {
		if cf.Required && data[cf.Key] == "" {
			return fmt.Errorf("required config field %q is empty", cf.Key)
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

	client := &http.Client{
		Timeout: 2 * time.Minute,
		Transport: &http.Transport{
			DialContext: func(dialCtx context.Context, network, addr string) (net.Conn, error) {
				return tunnel.Dial(dialCtx)
			},
		},
	}
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

// deployWorkload registers a Nomad job and monitors its deployment status.
// Uses -detach to avoid blocking on a single long-lived exec stream, then
// polls deployment status with separate short-lived exec calls. Each poll
// gets a fresh tunnel, making this resilient to tunnel deaths.
func (d *Deployer) deployWorkload(
	ctx context.Context,
	stackName string,
	tunnel *mesh.Tunnel,
	app programs.ApplicationDef,
	send LogFunc,
) error {
	jobFile := fmt.Sprintf("/opt/nomad-jobs/%s.nomad.hcl", app.Key)

	envExports := d.buildEnvExports(app.ConsulEnv)
	if envExports == "" && len(app.ConsulEnv) > 0 {
		return fmt.Errorf("invalid consulEnv configuration for %s", app.Key)
	}

	// Step 1: Register the job (detached — returns immediately).
	regOutput, regExit, err := d.execOnAgent(ctx, tunnel, envExports+"nomad job run -detach "+jobFile)
	if err != nil {
		return fmt.Errorf("job registration: %w", err)
	}
	send("output", regOutput)
	if regExit != 0 {
		return fmt.Errorf("job registration exited with code %d", regExit)
	}

	// Periodic/batch jobs (like postgres-backup) print "Job registration successful"
	// and have no deployment to monitor.
	if !strings.Contains(regOutput, "Evaluation ID:") {
		return nil
	}

	// Step 2: Poll deployment status with short-lived exec calls.
	send("output", "Monitoring deployment...")
	deadline := time.Now().Add(deployTimeout)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("deployment of %s timed out after %v", app.Key, deployTimeout)
			}

			// Fresh tunnel per poll — survives tunnel deaths between checks.
			t, err := d.meshManager.GetTunnelForNode(stackName, 0)
			if err != nil {
				send("output", fmt.Sprintf("Tunnel reconnecting: %v", err))
				continue
			}

			status, err := d.checkDeploymentStatus(ctx, t, envExports, app.Key)
			if err != nil {
				send("output", fmt.Sprintf("Status check: %v (retrying...)", err))
				continue
			}

			switch status {
			case "successful":
				return nil
			case "failed", "cancelled":
				// Fetch detailed status for error context.
				detail, _, _ := d.execOnAgent(ctx, t, envExports+"nomad job deployments -latest "+app.Key)
				if detail != "" {
					send("output", detail)
				}
				return fmt.Errorf("deployment %s: %s", app.Key, status)
			default:
				send("output", fmt.Sprintf("Deployment status: %s", status))
			}
		}
	}
}

// checkDeploymentStatus queries the latest deployment status for a Nomad job.
// Returns the status string: "running", "successful", "failed", "cancelled", or "pending".
func (d *Deployer) checkDeploymentStatus(
	ctx context.Context,
	tunnel *mesh.Tunnel,
	envExports string,
	jobKey string,
) (string, error) {
	// Use Nomad CLI Go template to extract just the status string.
	// nomad job deployments -latest -t outputs a single deployment.
	cmd := fmt.Sprintf(
		`%snomad job deployments -latest -json %s 2>/dev/null | grep -o '"Status": *"[^"]*"' | head -1 | cut -d'"' -f4`,
		envExports, jobKey,
	)
	output, exitCode, err := d.execOnAgent(ctx, tunnel, cmd)
	if err != nil {
		return "", err
	}
	if exitCode != 0 {
		return "", fmt.Errorf("status query exited with code %d", exitCode)
	}

	status := strings.TrimSpace(output)
	if status == "" {
		return "pending", nil
	}
	return status, nil
}

// execOnAgent sends a short-lived command to the agent and returns the output.
func (d *Deployer) execOnAgent(
	ctx context.Context,
	tunnel *mesh.Tunnel,
	cmdStr string,
) (string, int, error) {
	execReq := struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}{
		Command: "bash",
		Args:    []string{"-c", cmdStr},
	}

	body, _ := json.Marshal(execReq)
	client := &http.Client{
		Timeout: 2 * time.Minute,
		Transport: &http.Transport{
			DialContext: func(dialCtx context.Context, network, addr string) (net.Conn, error) {
				return tunnel.Dial(dialCtx)
			},
		},
	}
	req, err := http.NewRequestWithContext(ctx, "POST", tunnel.AgentURL()+"/exec", bytes.NewReader(body))
	if err != nil {
		return "", -1, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tunnel.Token())

	resp, err := client.Do(req)
	if err != nil {
		return "", -1, fmt.Errorf("exec request: %w", err)
	}
	defer resp.Body.Close()

	// Read full output.
	var output strings.Builder
	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			output.WriteString(string(buf[:n]))
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return output.String(), -1, fmt.Errorf("read output: %w", readErr)
		}
	}

	// Parse exit code from ---EXIT:N--- marker.
	full := output.String()
	exitCode := 0
	if idx := strings.Index(full, "---EXIT:"); idx >= 0 {
		marker := full[idx:]
		if end := strings.Index(marker[8:], "---"); end >= 0 {
			code := marker[8 : 8+end]
			var c int
			if n, _ := fmt.Sscanf(code, "%d", &c); n == 1 {
				exitCode = c
			}
		}
		// Strip the exit marker from the output.
		full = strings.TrimSpace(full[:idx])
	}

	return full, exitCode, nil
}

// buildEnvExports creates shell export statements from consulEnv mappings.
// Returns empty string if consulEnv is empty. Returns empty string with
// non-empty consulEnv if validation fails.
func (d *Deployer) buildEnvExports(consulEnv map[string]string) string {
	if len(consulEnv) == 0 {
		return ""
	}
	var exports string
	for envVar, kvPath := range consulEnv {
		if !isValidShellIdentifier(envVar) || !isValidKVPath(kvPath) {
			return ""
		}
		exports += fmt.Sprintf("export %s=$(consul kv get '%s' 2>/dev/null || true) && ", envVar, kvPath)
	}
	return exports
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

// isSecretField returns true if the config field key looks like a secret
// that should be auto-generated when empty.
func isSecretField(key string) bool {
	lower := strings.ToLower(key)
	return strings.HasSuffix(lower, "password") ||
		strings.HasSuffix(lower, "key") ||
		strings.HasSuffix(lower, "secret") ||
		strings.HasSuffix(lower, "token")
}

// generateSecret returns a 32-character hex string from crypto/rand.
func generateSecret() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "change-me-" + fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// isValidShellIdentifier checks that a string is safe for use as a shell
// variable name (alphanumeric + underscore, starts with letter or underscore).
func isValidShellIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '_' {
			continue
		}
		if i > 0 && c >= '0' && c <= '9' {
			continue
		}
		return false
	}
	return true
}

// isValidKVPath checks that a Consul KV path contains only safe characters
// (alphanumeric, /, -, _, .).
func isValidKVPath(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' ||
			c == '/' || c == '-' || c == '_' || c == '.' {
			continue
		}
		return false
	}
	return true
}
