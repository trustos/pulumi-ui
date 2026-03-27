package mesh

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/slackhq/nebula"
	"github.com/slackhq/nebula/config"
	"github.com/slackhq/nebula/overlay"
	"github.com/slackhq/nebula/service"
	"github.com/trustos/pulumi-ui/internal/db"
	nebulaHelper "github.com/trustos/pulumi-ui/internal/nebula"
)

const (
	idleTimeout   = 5 * time.Minute
	nebulaUDPPort = 41820
	agentTCPPort  = 41820
)

// Tunnel wraps a userspace Nebula mesh connection to a single stack.
type Tunnel struct {
	svc       *service.Service
	stackName string
	agentAddr string // e.g. "10.42.1.2:41820"
	token     string
	lastUsed  time.Time
	mu        sync.Mutex
}

// Manager creates and caches on-demand Nebula tunnels per stack.
type Manager struct {
	connStore     *db.StackConnectionStore
	nodeCertStore *db.NodeCertStore

	mu      sync.Mutex
	tunnels map[string]*Tunnel
	done    chan struct{}
}

func NewManager(connStore *db.StackConnectionStore) *Manager {
	m := &Manager{
		connStore: connStore,
		tunnels:   make(map[string]*Tunnel),
		done:      make(chan struct{}),
	}
	go m.reaper()
	return m
}

// WithNodeCertStore enables per-node tunnel support.
func (m *Manager) WithNodeCertStore(s *db.NodeCertStore) {
	m.nodeCertStore = s
}

// GetOrStartPassive returns a cached tunnel for the stack, or creates a new one
// that listens for incoming Nebula connections without a static_host_map entry
// for the agent. Used before a deploy completes (agent real IP not yet known)
// so that when the agent initiates a Nebula handshake to the server's real IP,
// the server's Nebula instance accepts it. A subsequent call to CloseTunnel
// (triggered by discoverAgentAddress) will replace this passive tunnel with an
// active one that has the agent's real IP in its static_host_map.
func (m *Manager) GetOrStartPassive(stackName string, conn *db.StackConnection) (*Tunnel, error) {
	m.mu.Lock()
	if t, ok := m.tunnels[stackName]; ok {
		t.mu.Lock()
		t.lastUsed = time.Now()
		t.mu.Unlock()
		m.mu.Unlock()
		return t, nil
	}
	m.mu.Unlock()

	t, err := m.connectPassive(conn)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	if existing, ok := m.tunnels[stackName]; ok {
		m.mu.Unlock()
		t.Close()
		return existing, nil
	}
	m.tunnels[stackName] = t
	m.mu.Unlock()
	return t, nil
}

// connectPassive builds a Nebula service for the server side without a
// static_host_map entry for the agent. The server will accept any handshake
// from a peer with a cert signed by the stack CA.
func (m *Manager) connectPassive(conn *db.StackConnection) (*Tunnel, error) {
	cfgStr := fmt.Sprintf(`
tun:
  user: true
pki:
  ca: |
%s
  cert: |
%s
  key: |
%s
static_host_map: {}
listen:
  host: 0.0.0.0
  port: 0
lighthouse:
  am_lighthouse: false
  hosts: []
punchy:
  punch: true
  respond: true
firewall:
  outbound:
    - port: any
      proto: any
      host: any
  inbound:
    - port: 8080
      proto: tcp
      host: any
    - port: any
      proto: icmp
      host: any
`,
		indentPEM(string(conn.NebulaCACert), 4),
		indentPEM(string(conn.NebulaUICert), 4),
		indentPEM(string(conn.NebulaUIKey), 4),
	)

	var cfg config.C
	if err := cfg.LoadString(cfgStr); err != nil {
		return nil, fmt.Errorf("load passive nebula config: %w", err)
	}

	logger := logrus.New()
	logger.SetLevel(logrus.WarnLevel)

	ctrl, err := nebula.Main(&cfg, false, "pulumi-ui-passive-"+conn.StackName, logger, overlay.NewUserDeviceFromConfig)
	if err != nil {
		return nil, fmt.Errorf("start passive nebula: %w", err)
	}

	svc, err := service.New(ctrl)
	if err != nil {
		log.Printf("[mesh] service.New failed for passive stack %s: %v (nebula goroutines may leak)", conn.StackName, err)
		return nil, fmt.Errorf("create passive nebula service: %w", err)
	}

	// Passive tunnels have no agent addr; they accept incoming connections only.
	return &Tunnel{
		svc:       svc,
		stackName: conn.StackName,
		agentAddr: "",
		token:     conn.AgentToken,
		lastUsed:  time.Now(),
	}, nil
}

// GetTunnel returns a cached or freshly created tunnel for the given stack.
func (m *Manager) GetTunnel(stackName string) (*Tunnel, error) {
	if m.connStore == nil {
		return nil, fmt.Errorf("mesh manager has no connection store")
	}

	m.mu.Lock()
	if t, ok := m.tunnels[stackName]; ok {
		t.mu.Lock()
		t.lastUsed = time.Now()
		t.mu.Unlock()
		m.mu.Unlock()
		return t, nil
	}
	m.mu.Unlock()

	conn, err := m.connStore.Get(stackName)
	if err != nil {
		return nil, fmt.Errorf("load stack connection: %w", err)
	}
	if conn == nil {
		return nil, fmt.Errorf("no Nebula PKI for stack %q", stackName)
	}
	if conn.AgentRealIP == nil || *conn.AgentRealIP == "" {
		return nil, fmt.Errorf("no agent real IP for stack %q — deploy infrastructure first", stackName)
	}

	t, err := m.connect(conn)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	if existing, ok := m.tunnels[stackName]; ok {
		m.mu.Unlock()
		t.Close()
		return existing, nil
	}
	m.tunnels[stackName] = t
	m.mu.Unlock()

	return t, nil
}

// GetTunnelForNode returns a cached or freshly created Nebula tunnel to a
// specific node identified by its index. Each node gets its own tunnel cache
// entry (key = "stackName:nodeIndex") and connects to that node's Nebula IP
// and real IP. The server still authenticates with the shared UI cert.
func (m *Manager) GetTunnelForNode(stackName string, nodeIndex int) (*Tunnel, error) {
	if m.nodeCertStore == nil {
		return nil, fmt.Errorf("node cert store not available")
	}
	key := fmt.Sprintf("%s:%d", stackName, nodeIndex)

	m.mu.Lock()
	if t, ok := m.tunnels[key]; ok {
		t.mu.Lock()
		t.lastUsed = time.Now()
		t.mu.Unlock()
		m.mu.Unlock()
		return t, nil
	}
	m.mu.Unlock()

	nodes, err := m.nodeCertStore.ListForStack(stackName)
	if err != nil {
		return nil, fmt.Errorf("load node certs for %q: %w", stackName, err)
	}
	var nc *db.NodeCert
	for _, n := range nodes {
		if n.NodeIndex == nodeIndex {
			nc = n
			break
		}
	}
	if nc == nil {
		return nil, fmt.Errorf("no node cert for stack %q node %d", stackName, nodeIndex)
	}
	if nc.AgentRealIP == nil || *nc.AgentRealIP == "" {
		return nil, fmt.Errorf("no real IP for stack %q node %d — deploy infrastructure first", stackName, nodeIndex)
	}

	conn, err := m.connStore.Get(stackName)
	if err != nil || conn == nil {
		return nil, fmt.Errorf("load stack connection for %q: %w", stackName, err)
	}

	t, err := m.connectNode(conn, nc)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	if existing, ok := m.tunnels[key]; ok {
		m.mu.Unlock()
		t.Close()
		return existing, nil
	}
	m.tunnels[key] = t
	m.mu.Unlock()
	return t, nil
}

func (m *Manager) connect(conn *db.StackConnection) (*Tunnel, error) {
	agentNebulaIP, err := nebulaHelper.AgentAddress(conn.NebulaSubnet)
	if err != nil {
		return nil, fmt.Errorf("compute agent nebula IP: %w", err)
	}
	agentIP := agentNebulaIP.Addr().String()

	realIPRaw := *conn.AgentRealIP
	udpHost, portStr, splitErr := net.SplitHostPort(realIPRaw)
	udpPort := nebulaUDPPort
	if splitErr == nil {
		if p, _ := strconv.Atoi(portStr); p > 0 {
			udpPort = p
		}
	} else {
		udpHost = realIPRaw
	}
	staticMap := fmt.Sprintf("'%s': ['%s:%d']", agentIP, udpHost, udpPort)

	cfgStr := fmt.Sprintf(`
tun:
  user: true
pki:
  ca: |
%s
  cert: |
%s
  key: |
%s
static_host_map:
  %s
listen:
  host: 0.0.0.0
  port: 0
lighthouse:
  am_lighthouse: false
  hosts: []
punchy:
  punch: true
  respond: true
firewall:
  outbound:
    - port: any
      proto: any
      host: any
  inbound:
    - port: 8080
      proto: tcp
      host: any
    - port: any
      proto: icmp
      host: any
`,
		indentPEM(string(conn.NebulaCACert), 4),
		indentPEM(string(conn.NebulaUICert), 4),
		indentPEM(string(conn.NebulaUIKey), 4),
		staticMap,
	)

	var cfg config.C
	if err := cfg.LoadString(cfgStr); err != nil {
		return nil, fmt.Errorf("load nebula config: %w", err)
	}

	logger := logrus.New()
	logger.SetLevel(logrus.WarnLevel)

	ctrl, err := nebula.Main(&cfg, false, "pulumi-ui-"+conn.StackName, logger, overlay.NewUserDeviceFromConfig)
	if err != nil {
		return nil, fmt.Errorf("start nebula: %w", err)
	}

	svc, err := service.New(ctrl)
	if err != nil {
		// Do not call ctrl.Stop() — Nebula's interface.go calls os.Exit(2) on
		// EOF if f.closed is not set, crashing the server. Accept the goroutine
		// leak on this rare error path; goroutines exit when the server exits.
		log.Printf("[mesh] service.New failed for stack %s: %v (nebula goroutines may leak)", conn.StackName, err)
		return nil, fmt.Errorf("create nebula service: %w", err)
	}

	return &Tunnel{
		svc:       svc,
		stackName: conn.StackName,
		agentAddr: fmt.Sprintf("%s:%d", agentIP, agentTCPPort),
		token:     conn.AgentToken,
		lastUsed:  time.Now(),
	}, nil
}

// connectNode creates a Nebula tunnel to a specific node. The server uses its
// shared UI cert (conn.NebulaUICert/UIKey); only the static_host_map target
// changes — it points to the node's individual Nebula IP and real IP.
func (m *Manager) connectNode(conn *db.StackConnection, nc *db.NodeCert) (*Tunnel, error) {
	// nc.NebulaIP is in CIDR form e.g. "10.42.1.3/24"; strip the prefix.
	agentIP, _, err := net.ParseCIDR(nc.NebulaIP)
	if err != nil {
		// Fallback: try it as a plain IP.
		agentIP = net.ParseIP(nc.NebulaIP)
		if agentIP == nil {
			return nil, fmt.Errorf("parse node nebula IP %q: %w", nc.NebulaIP, err)
		}
	}

	realIPRaw := *nc.AgentRealIP
	udpHost, portStr, splitErr := net.SplitHostPort(realIPRaw)
	udpPort := nebulaUDPPort
	if splitErr == nil {
		if p, _ := strconv.Atoi(portStr); p > 0 {
			udpPort = p
		}
	} else {
		udpHost = realIPRaw
	}
	staticMap := fmt.Sprintf("'%s': ['%s:%d']", agentIP.String(), udpHost, udpPort)

	cfgStr := fmt.Sprintf(`
tun:
  user: true
pki:
  ca: |
%s
  cert: |
%s
  key: |
%s
static_host_map:
  %s
listen:
  host: 0.0.0.0
  port: 0
lighthouse:
  am_lighthouse: false
  hosts: []
punchy:
  punch: true
  respond: true
firewall:
  outbound:
    - port: any
      proto: any
      host: any
  inbound:
    - port: 8080
      proto: tcp
      host: any
    - port: any
      proto: icmp
      host: any
`,
		indentPEM(string(conn.NebulaCACert), 4),
		indentPEM(string(conn.NebulaUICert), 4),
		indentPEM(string(conn.NebulaUIKey), 4),
		staticMap,
	)

	var cfg config.C
	if err := cfg.LoadString(cfgStr); err != nil {
		return nil, fmt.Errorf("load nebula config for node: %w", err)
	}

	logger := logrus.New()
	logger.SetLevel(logrus.WarnLevel)

	label := fmt.Sprintf("pulumi-ui-%s-node%d", conn.StackName, nc.NodeIndex)
	ctrl, err := nebula.Main(&cfg, false, label, logger, overlay.NewUserDeviceFromConfig)
	if err != nil {
		return nil, fmt.Errorf("start nebula for node: %w", err)
	}

	svc, err := service.New(ctrl)
	if err != nil {
		log.Printf("[mesh] service.New failed for %s node %d: %v (nebula goroutines may leak)", conn.StackName, nc.NodeIndex, err)
		return nil, fmt.Errorf("create nebula service for node: %w", err)
	}

	return &Tunnel{
		svc:       svc,
		stackName: conn.StackName,
		agentAddr: fmt.Sprintf("%s:%d", agentIP.String(), agentTCPPort),
		token:     conn.AgentToken,
		lastUsed:  time.Now(),
	}, nil
}

// Dial opens a TCP connection to the agent through the Nebula mesh.
func (t *Tunnel) Dial(ctx context.Context) (net.Conn, error) {
	t.mu.Lock()
	t.lastUsed = time.Now()
	t.mu.Unlock()
	return t.svc.DialContext(ctx, "tcp", t.agentAddr)
}

// HTTPClient returns an http.Client that routes through the Nebula tunnel.
func (t *Tunnel) HTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return t.Dial(ctx)
			},
		},
	}
}

// AgentURL returns the base HTTP URL for the agent on the mesh.
func (t *Tunnel) AgentURL() string {
	return "http://" + t.agentAddr
}

// AgentNebulaIP returns the Nebula VPN IP of the agent (without the port).
func (t *Tunnel) AgentNebulaIP() string {
	host, _, _ := net.SplitHostPort(t.agentAddr)
	return host
}

// Token returns the per-stack auth token.
func (t *Tunnel) Token() string {
	return t.token
}

// Close tears down the Nebula tunnel.
//
// WARNING: svc.Close() → control.Stop() → c.f.Close() triggers a race inside
// Nebula's interface.go: the outbound-reader goroutine may receive EOF before
// f.closed is set, causing Nebula to call os.Exit(2) and crash the server.
// Until this is fixed upstream (slackhq/nebula), we do NOT call svc.Close()
// here. The tunnel goroutines exit when the server process exits. The reaper
// removes the tunnel from the cache (freeing the Tunnel struct) without
// stopping the underlying Nebula service, which is acceptable — idle goroutines
// consume only a few hundred KB and Nebula auto-expires inactive connections.
func (t *Tunnel) Close() {
	// Intentionally a no-op: see the warning above.
	log.Printf("[mesh] tunnel for stack %s removed from cache (nebula service left running to avoid os.Exit(2) race)", t.stackName)
}

// CloseTunnel tears down a specific stack's tunnel.
func (m *Manager) CloseTunnel(stackName string) {
	m.mu.Lock()
	t, ok := m.tunnels[stackName]
	if ok {
		delete(m.tunnels, stackName)
	}
	m.mu.Unlock()
	if ok {
		t.Close()
	}
}

// Stop shuts down all tunnels and the reaper goroutine.
func (m *Manager) Stop() {
	close(m.done)
	m.mu.Lock()
	for name, t := range m.tunnels {
		t.Close()
		delete(m.tunnels, name)
	}
	m.mu.Unlock()
}

func (m *Manager) reaper() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.done:
			return
		case <-ticker.C:
			m.mu.Lock()
			for name, t := range m.tunnels {
				t.mu.Lock()
				idle := time.Since(t.lastUsed) > idleTimeout
				t.mu.Unlock()
				if idle {
					log.Printf("[mesh] closing idle tunnel for stack %s", name)
					t.Close()
					delete(m.tunnels, name)
				}
			}
			m.mu.Unlock()
		}
	}
}

func indentPEM(pem string, spaces int) string {
	prefix := ""
	for i := 0; i < spaces; i++ {
		prefix += " "
	}
	lines := ""
	for _, line := range splitLines(pem) {
		if line != "" {
			lines += prefix + line + "\n"
		}
	}
	return lines
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
