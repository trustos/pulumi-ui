package mesh

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
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
	connStore *db.StackConnectionStore

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

func (m *Manager) connect(conn *db.StackConnection) (*Tunnel, error) {
	agentNebulaIP, err := nebulaHelper.AgentAddress(conn.NebulaSubnet)
	if err != nil {
		return nil, fmt.Errorf("compute agent nebula IP: %w", err)
	}
	agentIP := agentNebulaIP.Addr().String()

	realIP := *conn.AgentRealIP
	staticMap := fmt.Sprintf("'%s': ['%s:%d']", agentIP, realIP, nebulaUDPPort)

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
firewall:
  outbound:
    - port: any
      proto: any
      host: any
  inbound:
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
		// ctrl.Stop() must NOT be called here — Nebula's main loop calls
		// os.Exit(0) after "Goodbye", which would terminate the server process.
		// Accept the goroutine leak on this rare error path.
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
// Only svc.Close() is called — ctrl.Stop() must NOT be used here because
// Nebula's main loop calls os.Exit(0) after logging "Goodbye", which would
// terminate the entire server process. The service package manages the full
// Nebula lifecycle in userspace mode and cleans up all goroutines on Close.
func (t *Tunnel) Close() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[mesh] recovered panic closing tunnel for stack %s: %v", t.stackName, r)
		}
	}()
	if t.svc != nil {
		t.svc.Close()
	}
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
