package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

type startForwardRequest struct {
	RemotePort int `json:"remotePort"`
	LocalPort  int `json:"localPort"`
	NodeIndex  int `json:"nodeIndex"`
}

type portForwardResponse struct {
	ID         string `json:"id"`
	StackName  string `json:"stackName"`
	NodeIndex  int    `json:"nodeIndex"`
	RemotePort int    `json:"remotePort"`
	LocalPort  int    `json:"localPort"`
	LocalAddr  string `json:"localAddr"`
	ActiveConn int    `json:"activeConns"`
	CreatedAt  int64  `json:"createdAt"`
}

// StartPortForward creates a local TCP listener that proxies to a remote port
// through the Nebula mesh.
func (h *NetworkHandler) StartPortForward(w http.ResponseWriter, r *http.Request) {
	stackName := chi.URLParam(r, "name")

	var req startForwardRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.RemotePort <= 0 || req.RemotePort > 65535 {
		http.Error(w, "remotePort must be 1-65535", http.StatusBadRequest)
		return
	}

	if h.ForwardManager == nil {
		http.Error(w, "port forwarding not available", http.StatusServiceUnavailable)
		return
	}

	pf, err := h.ForwardManager.Start(stackName, req.NodeIndex, req.RemotePort, req.LocalPort)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(portForwardResponse{
		ID:         pf.ID,
		StackName:  pf.StackName,
		NodeIndex:  pf.NodeIndex,
		RemotePort: pf.RemotePort,
		LocalPort:  pf.LocalPort,
		LocalAddr:  pf.LocalAddr,
		ActiveConn: pf.ActiveConns(),
		CreatedAt:  pf.CreatedAt,
	})
}

// StopPortForward closes an active port forward.
func (h *NetworkHandler) StopPortForward(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if h.ForwardManager == nil {
		http.Error(w, "port forwarding not available", http.StatusServiceUnavailable)
		return
	}

	if err := h.ForwardManager.Stop(id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ListPortForwards returns all active port forwards for a stack.
func (h *NetworkHandler) ListPortForwards(w http.ResponseWriter, r *http.Request) {
	stackName := chi.URLParam(r, "name")

	if h.ForwardManager == nil {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
		return
	}

	forwards := h.ForwardManager.List(stackName)
	result := make([]portForwardResponse, 0, len(forwards))
	for _, pf := range forwards {
		result = append(result, portForwardResponse{
			ID:         pf.ID,
			StackName:  pf.StackName,
			NodeIndex:  pf.NodeIndex,
			RemotePort: pf.RemotePort,
			LocalPort:  pf.LocalPort,
			LocalAddr:  pf.LocalAddr,
			ActiveConn: pf.ActiveConns(),
			CreatedAt:  pf.CreatedAt,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
