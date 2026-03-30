package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

func main() {
	listenAddr := flag.String("listen", ":41820", "Address to listen on (host:port)")
	tokenFile := flag.String("token-file", "/etc/pulumi-ui-agent/token", "Path to the bearer token file")
	token := flag.String("token", "", "Bearer token (overrides -token-file)")
	flag.Parse()

	authToken := *token
	if authToken == "" {
		data, err := os.ReadFile(*tokenFile)
		if err != nil {
			log.Fatalf("Cannot read token file %s: %v", *tokenFile, err)
		}
		authToken = strings.TrimSpace(string(data))
	}
	if authToken == "" {
		log.Fatal("No authentication token configured")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("GET /services", handleServices)
	mux.HandleFunc("GET /nomad-jobs", handleNomadJobs)
	mux.HandleFunc("POST /exec", handleExec)
	mux.HandleFunc("POST /upload", handleUpload)
	mux.HandleFunc("GET /shell", handleShell)

	authed := authMiddleware(authToken, mux)

	ln, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		log.Fatalf("Listen %s: %v", *listenAddr, err)
	}
	log.Printf("pulumi-ui-agent listening on %s", ln.Addr())

	srv := &http.Server{
		Handler:      authed,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // streaming responses for /exec
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Serve: %v", err)
		}
	}()

	<-stop
	log.Println("Shutting down agent...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}

func authMiddleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// --- Health endpoint ---

type healthResponse struct {
	Status   string `json:"status"`
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Uptime   string `json:"uptime,omitempty"`
}

var startTime = time.Now()

func handleHealth(w http.ResponseWriter, r *http.Request) {
	hostname, _ := os.Hostname()
	resp := healthResponse{
		Status:   "ok",
		Hostname: hostname,
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		Uptime:   time.Since(startTime).Truncate(time.Second).String(),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// --- Services endpoint ---

type serviceStatus struct {
	Name   string `json:"name"`
	Active string `json:"active"`
}

func handleServices(w http.ResponseWriter, r *http.Request) {
	units := []string{"docker", "consul", "nomad", "nebula", "pulumi-ui-agent"}
	results := make([]serviceStatus, 0, len(units))

	for _, u := range units {
		out, _ := exec.Command("systemctl", "is-active", u).Output()
		results = append(results, serviceStatus{
			Name:   u,
			Active: strings.TrimSpace(string(out)),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

// --- Nomad Jobs endpoint ---

type nomadJobSummary struct {
	Name   string      `json:"name"`
	Status string      `json:"status"`
	Type   string      `json:"type"`
	Ports  []nomadPort `json:"ports,omitempty"`
}

type nomadPort struct {
	Label string `json:"label"`
	Value int    `json:"value"` // host port
	To    int    `json:"to"`    // container port
}

func getNomadToken() string {
	if b, err := os.ReadFile("/etc/nomad.d/nomad-bootstrap-token"); err == nil {
		var parsed struct {
			SecretID string `json:"SecretID"`
		}
		if json.Unmarshal(b, &parsed) == nil && parsed.SecretID != "" {
			return parsed.SecretID
		}
	}
	if out, err := exec.Command("consul", "kv", "get", "nomad/bootstrap-token").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}

func nomadAPIGet(ctx context.Context, client *http.Client, token, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "http://localhost:4646"+path, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("X-Nomad-Token", token)
	}
	return client.Do(req)
}

// getAllocPorts queries the allocations for a job and returns ports from the
// first running allocation. The list endpoint (/v1/job/{id}/allocations) does
// NOT include AllocatedResources, so we must fetch the full allocation detail
// via /v1/allocation/{id} for port data.
func getAllocPorts(ctx context.Context, client *http.Client, token, jobID string) []nomadPort {
	// Step 1: Get allocation IDs from list endpoint (minimal fields).
	resp, err := nomadAPIGet(ctx, client, token, "/v1/job/"+jobID+"/allocations")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}

	var allocList []struct {
		ID           string `json:"ID"`
		ClientStatus string `json:"ClientStatus"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&allocList); err != nil {
		return nil
	}

	// Step 2: For the first running allocation, fetch full details with ports.
	for _, a := range allocList {
		if a.ClientStatus != "running" {
			continue
		}

		detailResp, err := nomadAPIGet(ctx, client, token, "/v1/allocation/"+a.ID)
		if err != nil {
			continue
		}

		var detail struct {
			AllocatedResources struct {
				Shared struct {
					Ports []struct {
						Label string `json:"Label"`
						Value int    `json:"Value"`
						To    int    `json:"To"`
					} `json:"Ports"`
				} `json:"Shared"`
			} `json:"AllocatedResources"`
		}
		err = json.NewDecoder(detailResp.Body).Decode(&detail)
		detailResp.Body.Close()
		if err != nil {
			continue
		}

		ports := detail.AllocatedResources.Shared.Ports
		if len(ports) == 0 {
			continue
		}
		result := make([]nomadPort, 0, len(ports))
		for _, p := range ports {
			result = append(result, nomadPort{
				Label: p.Label,
				Value: p.Value,
				To:    p.To,
			})
		}
		return result
	}
	return nil
}

func handleNomadJobs(w http.ResponseWriter, r *http.Request) {
	token := getNomadToken()
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := nomadAPIGet(r.Context(), client, token, "/v1/jobs?meta=false")
	if err != nil {
		http.Error(w, "nomad API unreachable: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		http.Error(w, fmt.Sprintf("nomad API error (%d): %s", resp.StatusCode, string(body)), resp.StatusCode)
		return
	}

	var nomadJobs []struct {
		ID     string `json:"ID"`
		Status string `json:"Status"`
		Type   string `json:"Type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&nomadJobs); err != nil {
		http.Error(w, "parse nomad response: "+err.Error(), http.StatusInternalServerError)
		return
	}

	results := make([]nomadJobSummary, 0, len(nomadJobs))
	for _, j := range nomadJobs {
		summary := nomadJobSummary{
			Name:   j.ID,
			Status: j.Status,
			Type:   j.Type,
		}
		// For running jobs, fetch allocated ports.
		if j.Status == "running" {
			summary.Ports = getAllocPorts(r.Context(), client, token, j.ID)
		}
		results = append(results, summary)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

// --- Exec endpoint ---

type execRequest struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Dir     string   `json:"dir,omitempty"`
	Env     []string `json:"env,omitempty"`
}

func handleExec(w http.ResponseWriter, r *http.Request) {
	var req execRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Command == "" {
		http.Error(w, "command is required", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, req.Command, req.Args...)
	if req.Dir != "" {
		cmd.Dir = req.Dir
	}
	if len(req.Env) > 0 {
		cmd.Env = append(os.Environ(), req.Env...)
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(w, "ERROR: %v\n", err)
		flusher.Flush()
		return
	}

	go func() {
		cmd.Wait()
		pw.Close()
	}()

	buf := make([]byte, 4096)
	for {
		n, err := pr.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			flusher.Flush()
		}
		if err != nil {
			break
		}
	}

	exitCode := 0
	if cmd.ProcessState != nil && !cmd.ProcessState.Success() {
		exitCode = cmd.ProcessState.ExitCode()
	}
	fmt.Fprintf(w, "\n---EXIT:%d---\n", exitCode)
	flusher.Flush()
}

// --- Upload endpoint ---

func handleUpload(w http.ResponseWriter, r *http.Request) {
	destPath := r.Header.Get("X-Dest-Path")
	if destPath == "" {
		http.Error(w, "X-Dest-Path header is required", http.StatusBadRequest)
		return
	}
	modeStr := r.Header.Get("X-File-Mode")
	mode := os.FileMode(0644)
	if modeStr != "" {
		var m uint32
		if _, err := fmt.Sscanf(modeStr, "%o", &m); err == nil {
			mode = os.FileMode(m)
		}
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		http.Error(w, "mkdir: "+err.Error(), http.StatusInternalServerError)
		return
	}

	f, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		http.Error(w, "create file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()

	n, err := io.Copy(f, r.Body)
	if err != nil {
		http.Error(w, "write file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"path":  destPath,
		"bytes": n,
	})
}

// --- Shell endpoint (interactive WebSocket terminal) ---

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Control message types for resize (sent as binary with a 1-byte prefix).
const shellResizePrefix = 1

func handleShell(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[shell] WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()

	shell := "/bin/bash"
	if _, err := os.Stat(shell); err != nil {
		shell = "/bin/sh"
	}

	cmd := exec.Command(shell)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.Start(cmd)
	if err != nil {
		log.Printf("[shell] PTY start failed: %v", err)
		conn.WriteMessage(websocket.TextMessage, []byte("Error: "+err.Error()))
		return
	}
	defer func() {
		ptmx.Close()
		cmd.Process.Kill()
		cmd.Wait()
	}()

	_ = pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 80})

	done := make(chan struct{})

	// PTY stdout -> WebSocket
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if err != nil {
				return
			}
			if err := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
				return
			}
		}
	}()

	// WebSocket -> PTY stdin
	go func() {
		for {
			msgType, msg, err := conn.ReadMessage()
			if err != nil {
				ptmx.Close()
				return
			}

			if msgType == websocket.BinaryMessage && len(msg) > 0 && msg[0] == shellResizePrefix {
				if len(msg) >= 5 {
					rows := uint16(msg[1])<<8 | uint16(msg[2])
					cols := uint16(msg[3])<<8 | uint16(msg[4])
					_ = pty.Setsize(ptmx, &pty.Winsize{Rows: rows, Cols: cols})
				}
				continue
			}

			ptmx.Write(msg)
		}
	}()

	<-done
}
