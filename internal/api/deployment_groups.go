package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/trustos/pulumi-ui/internal/blueprints"
	"github.com/trustos/pulumi-ui/internal/db"
	"github.com/trustos/pulumi-ui/internal/stacks"
)

// CreateGroupRequest is the JSON body for POST /api/groups.
type CreateGroupRequest struct {
	Name      string                  `json:"name"`
	Blueprint string                  `json:"blueprint"`
	Members   []CreateGroupMemberReq  `json:"members"`
	Config    map[string]string       `json:"config"`    // shared config across all stacks
	PassphraseID string              `json:"passphraseId"`
}

type CreateGroupMemberReq struct {
	AccountID string            `json:"accountId"`
	Role      string            `json:"role"`
	Config    map[string]string `json:"config,omitempty"` // per-member config overrides (merged with shared config)
}

// GroupSummary is the JSON response for listing groups.
type GroupSummary struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Blueprint string            `json:"blueprint"`
	Status    string            `json:"status"`
	Members   []GroupMemberView `json:"members"`
	DeployLog string            `json:"deployLog,omitempty"`
	CreatedAt int64             `json:"createdAt"`
}

type GroupMemberView struct {
	StackName string  `json:"stackName"`
	Role      string  `json:"role"`
	AccountID *string `json:"accountId"`
	Order     int     `json:"order"`
}

// ListGroups returns all deployment groups.
func (h *PlatformHandler) ListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := h.Groups.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	result := make([]GroupSummary, 0, len(groups))
	for _, g := range groups {
		members, _ := h.Groups.ListMembers(g.ID) //nolint: best-effort for listing
		views := make([]GroupMemberView, 0, len(members))
		for _, m := range members {
			views = append(views, GroupMemberView{
				StackName: m.StackName,
				Role:      m.Role,
				AccountID: m.AccountID,
				Order:     m.DeployOrder,
			})
		}
		result = append(result, GroupSummary{
			ID:        g.ID,
			Name:      g.Name,
			Blueprint: g.Blueprint,
			Status:    g.Status,
			Members:   views,
			CreatedAt: g.CreatedAt,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// GetGroup returns a single deployment group with its members.
func (h *PlatformHandler) GetGroup(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	g, err := h.Groups.Get(id)
	if err != nil || g == nil {
		http.Error(w, "group not found", http.StatusNotFound)
		return
	}

	members, _ := h.Groups.ListMembers(g.ID)
	views := make([]GroupMemberView, 0, len(members))
	for _, m := range members {
		views = append(views, GroupMemberView{
			StackName: m.StackName,
			Role:      m.Role,
			AccountID: m.AccountID,
			Order:     m.DeployOrder,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(GroupSummary{
		ID:        g.ID,
		Name:      g.Name,
		Blueprint: g.Blueprint,
		Status:    g.Status,
		Members:   views,
		DeployLog: g.DeployLog,
		CreatedAt: g.CreatedAt,
	})
}

// CreateGroup creates a deployment group and all its member stacks.
// It generates a gossip key, assigns sequential CIDRs, and creates
// the stacks in the database (not yet deployed).
func (h *PlatformHandler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	var body CreateGroupRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body.Name == "" || body.Blueprint == "" || len(body.Members) < 2 {
		http.Error(w, "name, blueprint, and at least 2 members are required", http.StatusBadRequest)
		return
	}

	// Validate blueprint exists and has multiAccount metadata.
	prog, ok := h.Registry.Get(body.Blueprint)
	if !ok {
		http.Error(w, "unknown blueprint: "+body.Blueprint, http.StatusBadRequest)
		return
	}
	var multiAccount *blueprints.MultiAccountMeta
	if map_, ok := prog.(blueprints.MultiAccountProvider); ok {
		multiAccount = map_.MultiAccount()
	}
	if multiAccount == nil {
		http.Error(w, "blueprint does not support multi-account deployment", http.StatusBadRequest)
		return
	}

	// Validate roles.
	primaryCount := 0
	for _, m := range body.Members {
		if m.Role == "primary" {
			primaryCount++
		}
		if m.AccountID == "" {
			http.Error(w, "each member must have an accountId", http.StatusBadRequest)
			return
		}
	}
	if primaryCount != 1 {
		http.Error(w, "exactly one member must have role 'primary'", http.StatusBadRequest)
		return
	}

	// Generate gossip key (32 bytes, base64 — same as consul keygen).
	gossipKeyBytes := make([]byte, 32)
	if _, err := rand.Read(gossipKeyBytes); err != nil {
		http.Error(w, "failed to generate gossip key", http.StatusInternalServerError)
		return
	}
	gossipKey := base64.StdEncoding.EncodeToString(gossipKeyBytes)

	// Create group.
	groupID := uuid.New().String()
	sharedConfigJSON, _ := json.Marshal(body.Config)
	group := &db.DeploymentGroup{
		ID:           groupID,
		Name:         body.Name,
		Blueprint:    body.Blueprint,
		Status:       "configuring",
		SharedConfig: strPtr(string(sharedConfigJSON)),
	}
	if err := h.Groups.Create(group); err != nil {
		http.Error(w, "failed to create group: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Build per-member configs first, then fill missing values from primary.
	type memberInfo struct {
		stackName    string
		role         string
		deployOrder  int
		accountID    string
		config       map[string]string
	}
	memberInfos := make([]memberInfo, 0, len(body.Members))
	workerIdx := 0
	var primaryConfig map[string]string

	for i, m := range body.Members {
		var mi memberInfo
		mi.accountID = m.AccountID

		if m.Role == "primary" {
			mi.stackName = body.Name + "-primary"
			mi.role = "primary"
			mi.deployOrder = 0
		} else {
			mi.stackName = fmt.Sprintf("%s-worker-%d", body.Name, workerIdx+1)
			mi.role = "worker"
			mi.deployOrder = 1
			workerIdx++
		}

		// Build per-stack config: blueprint defaults → shared → per-member → auto-filled.
		stackConfig := make(map[string]string)
		if yamlProg, ok := prog.(blueprints.YAMLBlueprintProvider); ok {
			stackConfig = blueprints.ApplyConfigDefaults(yamlProg.YAMLBody(), stackConfig)
		}
		for k, v := range body.Config {
			stackConfig[k] = v
		}
		for k, v := range m.Config {
			if v != "" {
				stackConfig[k] = v
			}
		}
		stackConfig["role"] = mi.role
		stackConfig["gossipKey"] = gossipKey

		for _, prc := range multiAccount.PerRoleConfig {
			stackConfig[prc.Key] = strings.ReplaceAll(prc.Pattern, "{index}", fmt.Sprintf("%d", i))
		}

		mi.config = stackConfig
		if mi.role == "primary" {
			primaryConfig = stackConfig
		}
		memberInfos = append(memberInfos, mi)
	}

	// Fill missing config values on workers from the primary's config.
	// Common fields like imageId, shape, sshPublicKey are typically the same
	// across accounts in the same region but have no blueprint default.
	if primaryConfig != nil {
		for i := range memberInfos {
			if memberInfos[i].role == "primary" {
				continue
			}
			for k, v := range primaryConfig {
				if _, exists := memberInfos[i].config[k]; !exists || memberInfos[i].config[k] == "" {
					if v != "" {
						memberInfos[i].config[k] = v
					}
				}
			}
			// Re-apply role (primary fill-from might have overwritten it)
			memberInfos[i].config["role"] = memberInfos[i].role
		}
	}

	// Create stacks in DB.
	for _, mi := range memberInfos {
		cfg := &stacks.StackConfig{
			APIVersion: "pulumi.io/v1",
			Kind:       "Stack",
			Metadata: stacks.StackMetadata{
				Name:      mi.stackName,
				Blueprint: body.Blueprint,
			},
			Config: mi.config,
		}
		yamlStr, _ := cfg.ToYAML()
		accountID := mi.accountID
		passphraseID := body.PassphraseID
		if err := h.Stacks.Upsert(mi.stackName, body.Blueprint, yamlStr, &accountID, &passphraseID, nil, &accountID); err != nil {
			http.Error(w, fmt.Sprintf("failed to create stack %s: %v", mi.stackName, err), http.StatusInternalServerError)
			return
		}

		// Add to group.
		if err := h.Groups.AddMember(&db.GroupMember{
			GroupID:     groupID,
			StackName:   mi.stackName,
			Role:        mi.role,
			DeployOrder: mi.deployOrder,
			AccountID:   &accountID,
		}); err != nil {
			http.Error(w, fmt.Sprintf("failed to add member %s: %v", mi.stackName, err), http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"id": groupID, "name": body.Name})
}

// DeleteGroup removes a deployment group (stacks remain).
func (h *PlatformHandler) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.Groups.Delete(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func strPtr(s string) *string { return &s }
