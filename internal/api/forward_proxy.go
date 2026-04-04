package api

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
)

// ForwardProxy reverse-proxies HTTP (and WebSocket) requests through an active
// port forward. This allows browsers on remote machines to access forwarded
// services without needing direct access to the server's loopback interface.
//
// Route: /api/stacks/{name}/forward/{id}/proxy/*
func (h *Handler) ForwardProxy(w http.ResponseWriter, r *http.Request) {
	stackName := chi.URLParam(r, "name")
	fwdID := chi.URLParam(r, "id")

	if h.ForwardManager == nil {
		http.Error(w, "port forwarding not available", http.StatusServiceUnavailable)
		return
	}

	pf, ok := h.ForwardManager.Get(fwdID)
	if !ok || pf.StackName != stackName {
		http.Error(w, "port forward not found", http.StatusNotFound)
		return
	}

	// WebSocket upgrade path.
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		h.proxyForwardWebSocket(w, r, pf.LocalPort)
		return
	}

	// HTTP reverse proxy.
	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", pf.LocalPort))
	proxy := httputil.NewSingleHostReverseProxy(target)

	// Strip the proxy prefix so the upstream service sees the original path.
	prefix := fmt.Sprintf("/api/stacks/%s/forward/%s/proxy", stackName, fwdID)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.URL.Path = strings.TrimPrefix(req.URL.Path, prefix)
		if req.URL.Path == "" {
			req.URL.Path = "/"
		}
		req.URL.RawPath = ""
		req.Host = target.Host
	}

	// Rewrite Location headers on redirects so they stay within the proxy.
	proxy.ModifyResponse = func(resp *http.Response) error {
		if loc := resp.Header.Get("Location"); loc != "" {
			if strings.HasPrefix(loc, "/") {
				resp.Header.Set("Location", prefix+loc)
			}
		}
		return nil
	}

	proxy.ServeHTTP(w, r)
}

// proxyForwardWebSocket upgrades the browser connection and dials the local
// forwarded port, then copies messages bidirectionally. Follows the same
// pattern as AgentShell in agent_proxy.go.
func (h *Handler) proxyForwardWebSocket(w http.ResponseWriter, r *http.Request, localPort int) {
	browserConn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[forward-proxy] WebSocket upgrade failed: %v", err)
		return
	}
	defer browserConn.Close()

	// Build upstream WebSocket URL from the remaining path.
	remaining := chi.URLParam(r, "*")
	upstreamURL := fmt.Sprintf("ws://127.0.0.1:%d/%s", localPort, remaining)

	// Forward query string if present.
	if r.URL.RawQuery != "" {
		upstreamURL += "?" + r.URL.RawQuery
	}

	upstreamConn, _, err := websocket.DefaultDialer.Dial(upstreamURL, nil)
	if err != nil {
		log.Printf("[forward-proxy] upstream WebSocket dial failed: %v", err)
		browserConn.WriteMessage(websocket.TextMessage, []byte("upstream connection failed: "+err.Error()))
		return
	}
	defer upstreamConn.Close()

	done := make(chan struct{})

	// Upstream → Browser
	go func() {
		defer close(done)
		for {
			msgType, msg, err := upstreamConn.ReadMessage()
			if err != nil {
				return
			}
			if err := browserConn.WriteMessage(msgType, msg); err != nil {
				return
			}
		}
	}()

	// Browser → Upstream
	go func() {
		for {
			msgType, msg, err := browserConn.ReadMessage()
			if err != nil {
				upstreamConn.Close()
				return
			}
			if err := upstreamConn.WriteMessage(msgType, msg); err != nil {
				return
			}
		}
	}()

	<-done
}
