package api

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"github.com/trustos/pulumi-ui/internal/mesh"
)

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// AgentHealth proxies a health check to the agent through the Nebula mesh.
func (h *Handler) AgentHealth(w http.ResponseWriter, r *http.Request) {
	stackName := chi.URLParam(r, "name")

	tunnel, err := h.MeshManager.GetTunnel(stackName)
	if err != nil {
		http.Error(w, "mesh tunnel: "+err.Error(), http.StatusBadGateway)
		return
	}

	resp, err := agentRequest(r.Context(), tunnel, "GET", "/health", nil)
	if err != nil {
		http.Error(w, "agent unreachable: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if h.ConnStore != nil {
		h.ConnStore.UpdateLastSeen(stackName)
		// On first successful health check, record the agent's Nebula VPN IP so
		// the UI shows "connected" and the mesh IP. AgentNebulaIP is nil until set.
		if conn, err := h.ConnStore.Get(stackName); err == nil && conn != nil && conn.AgentNebulaIP == nil {
			realIP := ""
			if conn.AgentRealIP != nil {
				realIP = *conn.AgentRealIP
			}
			h.ConnStore.UpdateAgentConnected(stackName, tunnel.AgentNebulaIP(), realIP, "")
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// AgentServices proxies a services check to the agent.
func (h *Handler) AgentServices(w http.ResponseWriter, r *http.Request) {
	stackName := chi.URLParam(r, "name")

	tunnel, err := h.MeshManager.GetTunnel(stackName)
	if err != nil {
		http.Error(w, "mesh tunnel: "+err.Error(), http.StatusBadGateway)
		return
	}

	resp, err := agentRequest(r.Context(), tunnel, "GET", "/services", nil)
	if err != nil {
		http.Error(w, "agent unreachable: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// AgentExec proxies a command execution to the agent, streaming output via SSE.
func (h *Handler) AgentExec(w http.ResponseWriter, r *http.Request) {
	stackName := chi.URLParam(r, "name")

	tunnel, err := h.MeshManager.GetTunnel(stackName)
	if err != nil {
		http.Error(w, "mesh tunnel: "+err.Error(), http.StatusBadGateway)
		return
	}

	resp, err := agentRequest(r.Context(), tunnel, "POST", "/exec", r.Body)
	if err != nil {
		http.Error(w, "agent unreachable: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			chunk := strings.ReplaceAll(string(buf[:n]), "\n", "\\n")
			fmt.Fprintf(w, "data: {\"type\":\"output\",\"data\":\"%s\"}\n\n", chunk)
			flusher.Flush()
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			log.Printf("[agent-exec] stream error for %s: %v", stackName, readErr)
			break
		}
	}

	fmt.Fprint(w, "data: {\"type\":\"done\"}\n\n")
	flusher.Flush()
}

// AgentUpload proxies a file upload to the agent.
func (h *Handler) AgentUpload(w http.ResponseWriter, r *http.Request) {
	stackName := chi.URLParam(r, "name")

	tunnel, err := h.MeshManager.GetTunnel(stackName)
	if err != nil {
		http.Error(w, "mesh tunnel: "+err.Error(), http.StatusBadGateway)
		return
	}

	client := tunnel.HTTPClient()
	agentURL := tunnel.AgentURL() + "/upload"

	req, err := http.NewRequestWithContext(r.Context(), "POST", agentURL, r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("Authorization", "Bearer "+tunnel.Token())
	if dest := r.Header.Get("X-Dest-Path"); dest != "" {
		req.Header.Set("X-Dest-Path", dest)
	}
	if mode := r.Header.Get("X-File-Mode"); mode != "" {
		req.Header.Set("X-File-Mode", mode)
	}

	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "agent unreachable: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// AgentShell proxies a WebSocket terminal session to the agent through Nebula.
func (h *Handler) AgentShell(w http.ResponseWriter, r *http.Request) {
	stackName := chi.URLParam(r, "name")

	tunnel, err := h.MeshManager.GetTunnel(stackName)
	if err != nil {
		http.Error(w, "mesh tunnel: "+err.Error(), http.StatusBadGateway)
		return
	}

	browserConn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[agent-shell] WebSocket upgrade failed: %v", err)
		return
	}
	defer browserConn.Close()

	agentURL, _ := url.Parse(tunnel.AgentURL())
	agentURL.Scheme = "ws"
	agentURL.Path = "/shell"

	dialCtx, dialCancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer dialCancel()

	dialer := websocket.Dialer{
		NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return tunnel.Dial(ctx)
		},
	}
	headers := http.Header{}
	headers.Set("Authorization", "Bearer "+tunnel.Token())

	agentConn, _, err := dialer.DialContext(dialCtx, agentURL.String(), headers)
	if err != nil {
		log.Printf("[agent-shell] agent WebSocket dial failed: %v", err)
		browserConn.WriteMessage(websocket.TextMessage, []byte("Agent connection failed: "+err.Error()))
		return
	}
	defer agentConn.Close()

	done := make(chan struct{})

	// Agent -> Browser
	go func() {
		defer close(done)
		for {
			msgType, msg, err := agentConn.ReadMessage()
			if err != nil {
				return
			}
			if err := browserConn.WriteMessage(msgType, msg); err != nil {
				return
			}
		}
	}()

	// Browser -> Agent
	go func() {
		for {
			msgType, msg, err := browserConn.ReadMessage()
			if err != nil {
				agentConn.Close()
				return
			}
			if err := agentConn.WriteMessage(msgType, msg); err != nil {
				return
			}
		}
	}()

	<-done
}

func agentRequest(ctx context.Context, tunnel *mesh.Tunnel, method, path string, body io.Reader) (*http.Response, error) {
	client := tunnel.HTTPClient()
	agentURL := tunnel.AgentURL() + path

	req, err := http.NewRequestWithContext(ctx, method, agentURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tunnel.Token())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return client.Do(req)
}
