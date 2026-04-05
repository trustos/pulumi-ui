package api

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"

	"github.com/gorilla/websocket"
)

// fwdHostRe extracts (forwardID, stackName) from a subdomain like
// "fwd-1--mystack.pulumi.tenevi.zero". The ".pulumi." anchor ensures
// we only match forward subdomains under the pulumi service domain.
var fwdHostRe = regexp.MustCompile(`^(fwd-\d+)--(.+?)\.pulumi\.`)

// ForwardSubdomainProxy is a middleware that checks if the Host header matches
// fwd-{id}--{stack}.tenevi.zero and proxies the entire request to the
// corresponding local port forward. The upstream service sees the request at
// root / — no path rewriting, HTML injection, or JS patching needed.
func (h *NetworkHandler) ForwardSubdomainProxy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Strip port from Host header if present.
		host := r.Host
		if idx := strings.LastIndex(host, ":"); idx != -1 {
			host = host[:idx]
		}

		m := fwdHostRe.FindStringSubmatch(host)
		if m == nil {
			next.ServeHTTP(w, r)
			return
		}

		fwdID := m[1]     // "fwd-1"
		stackName := m[2] // "mystack"

		if h.ForwardManager == nil {
			http.Error(w, "port forwarding not available", http.StatusServiceUnavailable)
			return
		}

		pf, ok := h.ForwardManager.Get(fwdID)
		if !ok || pf.StackName != stackName {
			http.Error(w, "port forward not found", http.StatusNotFound)
			return
		}

		// WebSocket upgrade.
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			proxyForwardWebSocket(w, r, pf.LocalPort)
			return
		}

		// HTTP reverse proxy — no path rewriting needed.
		target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", pf.LocalPort))
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.ServeHTTP(w, r)
	})
}

// proxyForwardWebSocket upgrades the browser connection and dials the local
// forwarded port, then copies messages bidirectionally.
func proxyForwardWebSocket(w http.ResponseWriter, r *http.Request, localPort int) {
	browserConn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[forward-proxy] WebSocket upgrade failed: %v", err)
		return
	}
	defer browserConn.Close()

	upstreamURL := fmt.Sprintf("ws://127.0.0.1:%d%s", localPort, r.URL.RequestURI())

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
