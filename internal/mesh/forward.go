package mesh

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// PortForward represents an active local TCP listener that proxies connections
// through a Nebula tunnel to a remote port on a specific node.
type PortForward struct {
	ID         string `json:"id"`
	StackName  string `json:"stackName"`
	NodeIndex  int    `json:"nodeIndex"`
	RemotePort int    `json:"remotePort"`
	LocalPort  int    `json:"localPort"`
	LocalAddr  string `json:"localAddr"`
	CreatedAt  int64  `json:"createdAt"`

	listener net.Listener
	manager  *Manager
	cancel   context.CancelFunc
	connWg   sync.WaitGroup
	active   atomic.Int32 // active connection count
}

// ActiveConns returns the number of active proxied connections.
func (pf *PortForward) ActiveConns() int {
	return int(pf.active.Load())
}

// ForwardManager tracks active port forwards across all stacks.
type ForwardManager struct {
	meshManager *Manager

	mu       sync.Mutex
	forwards map[string]*PortForward // keyed by ID
	nextID   int
}

// NewForwardManager creates a new port forward manager.
func NewForwardManager(meshManager *Manager) *ForwardManager {
	return &ForwardManager{
		meshManager: meshManager,
		forwards:    make(map[string]*PortForward),
	}
}

// Start creates a local TCP listener that proxies connections through the
// Nebula tunnel to remotePort on the given node. If localPort is 0, an
// ephemeral port is chosen. Returns the PortForward with the actual local port.
func (fm *ForwardManager) Start(stackName string, nodeIndex, remotePort, localPort int) (*PortForward, error) {
	if fm.meshManager == nil {
		return nil, fmt.Errorf("mesh manager not available")
	}
	// Verify the tunnel can be established before binding a port.
	if nodeIndex >= 0 {
		if _, err := fm.meshManager.GetTunnelForNode(stackName, nodeIndex); err != nil {
			return nil, fmt.Errorf("mesh tunnel: %w", err)
		}
	} else {
		if _, err := fm.meshManager.GetTunnel(stackName); err != nil {
			return nil, fmt.Errorf("mesh tunnel: %w", err)
		}
	}

	listenAddr := fmt.Sprintf("127.0.0.1:%d", localPort)
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", listenAddr, err)
	}

	actualPort := ln.Addr().(*net.TCPAddr).Port

	fm.mu.Lock()
	fm.nextID++
	id := fmt.Sprintf("fwd-%d", fm.nextID)
	fm.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())

	pf := &PortForward{
		ID:         id,
		StackName:  stackName,
		NodeIndex:  nodeIndex,
		RemotePort: remotePort,
		LocalPort:  actualPort,
		LocalAddr:  ln.Addr().String(),
		CreatedAt:  time.Now().Unix(),
		listener:   ln,
		manager:    fm.meshManager,
		cancel:     cancel,
	}

	fm.mu.Lock()
	fm.forwards[id] = pf
	fm.mu.Unlock()

	go pf.acceptLoop(ctx)

	log.Printf("[forward] %s: localhost:%d → %s node %d port %d",
		id, actualPort, stackName, nodeIndex, remotePort)

	return pf, nil
}

// Stop closes a port forward by ID. Active connections are given 3 seconds
// to drain before being force-closed. This prevents the DELETE request from
// hanging indefinitely when browsers keep connections alive (e.g., Nomad UI).
func (fm *ForwardManager) Stop(id string) error {
	fm.mu.Lock()
	pf, ok := fm.forwards[id]
	if !ok {
		fm.mu.Unlock()
		return fmt.Errorf("port forward %q not found", id)
	}
	delete(fm.forwards, id)
	fm.mu.Unlock()

	pf.cancel()
	pf.listener.Close()

	// Wait for active connections to drain with a timeout.
	done := make(chan struct{})
	go func() {
		pf.connWg.Wait()
		close(done)
	}()
	select {
	case <-done:
		// Clean drain
	case <-time.After(3 * time.Second):
		log.Printf("[forward] %s: force-closed after drain timeout (%d active connections)", id, pf.ActiveConns())
	}

	log.Printf("[forward] %s stopped", id)
	return nil
}

// List returns all active port forwards, optionally filtered by stack name.
func (fm *ForwardManager) List(stackName string) []*PortForward {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	var result []*PortForward
	for _, pf := range fm.forwards {
		if stackName == "" || pf.StackName == stackName {
			result = append(result, pf)
		}
	}
	return result
}

// StopAll closes all port forwards for a given stack.
func (fm *ForwardManager) StopAll(stackName string) {
	fm.mu.Lock()
	var toStop []*PortForward
	for _, pf := range fm.forwards {
		if pf.StackName == stackName {
			toStop = append(toStop, pf)
		}
	}
	for _, pf := range toStop {
		delete(fm.forwards, pf.ID)
	}
	fm.mu.Unlock()

	for _, pf := range toStop {
		pf.cancel()
		pf.listener.Close()
		pf.connWg.Wait()
		log.Printf("[forward] %s stopped (stack cleanup)", pf.ID)
	}
}

func (pf *PortForward) acceptLoop(ctx context.Context) {
	for {
		conn, err := pf.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				log.Printf("[forward] %s: accept error: %v", pf.ID, err)
				return
			}
		}

		pf.connWg.Add(1)
		pf.active.Add(1)
		go func() {
			defer pf.connWg.Done()
			defer pf.active.Add(-1)
			pf.handleConn(ctx, conn)
		}()
	}
}

func (pf *PortForward) handleConn(ctx context.Context, local net.Conn) {
	defer local.Close()

	// Resolve tunnel fresh on each connection — the idle reaper may have
	// replaced the tunnel since the forward was created.
	var tunnel *Tunnel
	var err error
	if pf.NodeIndex >= 0 {
		tunnel, err = pf.manager.GetTunnelForNode(pf.StackName, pf.NodeIndex)
	} else {
		tunnel, err = pf.manager.GetTunnel(pf.StackName)
	}
	if err != nil {
		log.Printf("[forward] %s: get tunnel failed: %v", pf.ID, err)
		return
	}

	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	remote, err := tunnel.DialPort(dialCtx, pf.RemotePort)
	if err != nil {
		log.Printf("[forward] %s: dial remote port %d failed: %v", pf.ID, pf.RemotePort, err)
		return
	}
	defer remote.Close()

	done := make(chan struct{})

	// local → remote
	go func() {
		if _, err := io.Copy(remote, local); err != nil {
			log.Printf("[forward] %s: local→remote copy error: %v", pf.ID, err)
		}
		close(done)
	}()

	// remote → local
	if _, err := io.Copy(local, remote); err != nil {
		log.Printf("[forward] %s: remote→local copy error: %v", pf.ID, err)
	}

	<-done
}
