package api

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
)

// proxyPathRe extracts (stackName, forwardID) from a forward proxy URL.
var proxyPathRe = regexp.MustCompile(`/api/stacks/([^/]+)/forward/([^/]+)/proxy`)

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

	// Rewrite Location headers and HTML bodies so absolute paths stay within
	// the proxy, regardless of what upstream service is being proxied.
	proxy.ModifyResponse = func(resp *http.Response) error {
		// Rewrite Location header on redirects.
		if loc := resp.Header.Get("Location"); loc != "" {
			if strings.HasPrefix(loc, "/") {
				resp.Header.Set("Location", prefix+loc)
			}
		}

		// Rewrite HTML responses: convert absolute paths to proxy-relative.
		ct := resp.Header.Get("Content-Type")
		if !strings.Contains(ct, "text/html") {
			return nil
		}

		var reader io.ReadCloser
		var isGzip bool
		if resp.Header.Get("Content-Encoding") == "gzip" {
			gr, err := gzip.NewReader(resp.Body)
			if err != nil {
				return nil // can't decompress; pass through unchanged
			}
			reader = gr
			isGzip = true
		} else {
			reader = resp.Body
		}

		body, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			return nil
		}

		// Inject <base> tag so relative URLs resolve through the proxy.
		baseTag := []byte(fmt.Sprintf(`<base href="%s/">`, prefix))
		body = bytes.Replace(body, []byte("<head>"), append([]byte("<head>"), baseTag...), 1)

		// Rewrite absolute paths in src/href/action attributes to be relative
		// (the <base> tag then resolves them through the proxy).
		// Matches: src="/...", href="/...", action="/..." (not "//" protocol-relative)
		for _, attr := range []string{"src", "href", "action"} {
			body = bytes.ReplaceAll(body, []byte(attr+`="/`), []byte(attr+`="`+prefix+`/`))
			body = bytes.ReplaceAll(body, []byte(attr+`='/`), []byte(attr+`='`+prefix+`/`))
		}

		if isGzip {
			var buf bytes.Buffer
			gw := gzip.NewWriter(&buf)
			gw.Write(body)
			gw.Close()
			body = buf.Bytes()
		} else {
			resp.Header.Del("Content-Length")
		}

		resp.Body = io.NopCloser(bytes.NewReader(body))
		resp.ContentLength = int64(len(body))
		resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(body)))
		return nil
	}

	proxy.ServeHTTP(w, r)
}

// ForwardProxyRefererMiddleware intercepts requests whose Referer header points
// to a forward proxy URL and reroutes them through the same proxy. This handles
// the case where a proxied page's JavaScript makes fetch/XHR calls with absolute
// paths (e.g., fetch('/v1/jobs')) that would otherwise hit the SPA catch-all.
func (h *Handler) ForwardProxyRefererMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only intercept non-API, non-asset paths that would hit the SPA catch-all.
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/assets/") {
			next.ServeHTTP(w, r)
			return
		}

		ref := r.Header.Get("Referer")
		if ref == "" {
			next.ServeHTTP(w, r)
			return
		}

		// Parse the Referer to extract any forward proxy path.
		refURL, err := url.Parse(ref)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}

		m := proxyPathRe.FindStringSubmatch(refURL.Path)
		if m == nil {
			next.ServeHTTP(w, r)
			return
		}

		stackName := m[1]
		fwdID := m[2]

		// Verify the forward still exists.
		if h.ForwardManager == nil {
			next.ServeHTTP(w, r)
			return
		}
		pf, ok := h.ForwardManager.Get(fwdID)
		if !ok || pf.StackName != stackName {
			next.ServeHTTP(w, r)
			return
		}

		// Proxy this request to the forwarded service.
		target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", pf.LocalPort))
		proxy := httputil.NewSingleHostReverseProxy(target)

		originalDirector := proxy.Director
		proxy.Director = func(req *http.Request) {
			originalDirector(req)
			req.Host = target.Host
		}

		proxy.ServeHTTP(w, r)
	})
}

// proxyForwardWebSocket upgrades the browser connection and dials the local
// forwarded port, then copies messages bidirectionally.
func (h *Handler) proxyForwardWebSocket(w http.ResponseWriter, r *http.Request, localPort int) {
	browserConn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[forward-proxy] WebSocket upgrade failed: %v", err)
		return
	}
	defer browserConn.Close()

	remaining := chi.URLParam(r, "*")
	upstreamURL := fmt.Sprintf("ws://127.0.0.1:%d/%s", localPort, remaining)

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
