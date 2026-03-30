package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/trustos/pulumi-ui/internal/blueprints"
	"github.com/trustos/pulumi-ui/internal/db"
)

// ListBlueprints returns all blueprints (built-in + custom) without YAML bodies.
func (h *Handler) ListBlueprints(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(h.Registry.List())
}

// GetBlueprint returns a single blueprint. For custom blueprints the full YAML body
// is included in the response.
func (h *Handler) GetBlueprint(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	// Check custom blueprints first (they carry the YAML body).
	if h.CustomBlueprints != nil {
		if cp, err := h.CustomBlueprints.Get(name); err == nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(cp)
			return
		}
	}

	// Fall back to built-in blueprints (no YAML body).
	prog, ok := h.Registry.Get(name)
	if !ok {
		http.Error(w, "blueprint not found", http.StatusNotFound)
		return
	}
	meta := blueprints.BlueprintMeta{
		Name:         prog.Name(),
		DisplayName:  prog.DisplayName(),
		Description:  prog.Description(),
		ConfigFields: prog.ConfigFields(),
		IsBuiltin:    true, // only reached for built-in blueprints (custom blueprints return from DB path above)
	}
	if ap, ok := prog.(blueprints.ApplicationProvider); ok {
		meta.Applications = ap.Applications()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(meta)
}

type createBlueprintRequest struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	BlueprintYAML string `json:"blueprintYaml"`
}

// ValidateBlueprintHandler runs all validation levels against a YAML body and
// returns the result. Always responds 200; callers check the "valid" field.
func (h *Handler) ValidateBlueprintHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BlueprintYAML string `json:"blueprintYaml"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	errs := blueprints.ValidateBlueprint(req.BlueprintYAML)
	if errs == nil {
		errs = []blueprints.ValidationError{}
	}
	blocking := false
	for _, e := range errs {
		if e.Level < blueprints.LevelAgentAccess {
			blocking = true
			break
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"valid":  !blocking,
		"errors": errs,
	})
}

// CreateBlueprint saves a new user-defined YAML blueprint and registers it.
func (h *Handler) CreateBlueprint(w http.ResponseWriter, r *http.Request) {
	var req createBlueprintRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if req.Name == "" || req.DisplayName == "" || req.BlueprintYAML == "" {
		http.Error(w, "name, displayName, and blueprintYaml are required", http.StatusBadRequest)
		return
	}
	if errs := blueprints.ValidateBlueprint(req.BlueprintYAML); hasBlockingErrors(errs) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(errs)
		return
	}
	if _, ok := h.Registry.Get(req.Name); ok {
		http.Error(w, "a blueprint with that name already exists", http.StatusConflict)
		return
	}

	cp := db.CustomBlueprint{
		Name:        req.Name,
		DisplayName: req.DisplayName,
		Description: req.Description,
		BlueprintYAML: req.BlueprintYAML,
	}
	if err := h.CustomBlueprints.Create(cp); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Register in memory so it's available immediately without restart.
	blueprints.RegisterYAML(h.Registry, req.Name, req.DisplayName, req.Description, req.BlueprintYAML)
	log.Printf("[blueprints] registered custom blueprint %q", req.Name)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(cp)
}

type updateBlueprintRequest struct {
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	BlueprintYAML string `json:"blueprintYaml"`
}

// UpdateBlueprint replaces the YAML body of an existing custom blueprint.
func (h *Handler) UpdateBlueprint(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	// Block updates to built-in blueprints.
	if prog, ok := h.Registry.Get(name); ok {
		if _, isYAML := prog.(blueprints.YAMLBlueprintProvider); !isYAML {
			http.Error(w, "built-in blueprints cannot be modified", http.StatusMethodNotAllowed)
			return
		}
	}

	var req updateBlueprintRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.BlueprintYAML == "" {
		http.Error(w, "blueprintYaml is required", http.StatusBadRequest)
		return
	}
	if errs := blueprints.ValidateBlueprint(req.BlueprintYAML); hasBlockingErrors(errs) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(errs)
		return
	}

	cp, err := h.CustomBlueprints.Get(name)
	if err != nil {
		http.Error(w, "blueprint not found", http.StatusNotFound)
		return
	}

	displayName := req.DisplayName
	if displayName == "" {
		displayName = cp.DisplayName
	}

	if err := h.CustomBlueprints.Update(name, displayName, req.Description, req.BlueprintYAML); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Re-register with updated body (replaces old entry).
	h.Registry.Deregister(name)
	blueprints.RegisterYAML(h.Registry, name, displayName, req.Description, req.BlueprintYAML)
	log.Printf("[blueprints] updated custom blueprint %q", name)

	updated, _ := h.CustomBlueprints.Get(name)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(updated)
}

// ForkBlueprint generates a starter YAML from a built-in blueprint's config fields.
func (h *Handler) ForkBlueprint(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	prog, ok := h.Registry.Get(name)
	if !ok {
		http.Error(w, "blueprint not found", http.StatusNotFound)
		return
	}
	// Custom (user-created) blueprints should be edited directly, not forked.
	// Built-in blueprints (including YAML built-ins like nomad-cluster) can be forked.
	if !h.Registry.IsBuiltin(name) {
		http.Error(w, "custom blueprints cannot be forked (edit them directly)", http.StatusBadRequest)
		return
	}

	// For built-in YAML blueprints, return their YAML body directly.
	if yamlProg, ok := prog.(blueprints.YAMLBlueprintProvider); ok {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"blueprintYaml": yamlProg.YAMLBody()})
		return
	}

	// For Go-based built-in blueprints, generate a config stub.
	yaml := buildForkYAML(prog)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"blueprintYaml": yaml})
}

func buildForkYAML(prog blueprints.Blueprint) string {
	// If the blueprint provides a full YAML fork template, use it directly.
	if fp, ok := prog.(blueprints.ForkableBlueprint); ok {
		return fp.ForkYAML()
	}

	// Fallback: generate a config-only stub for blueprints without a fork template.
	var sb strings.Builder
	sb.WriteString("name: ")
	sb.WriteString(prog.Name())
	sb.WriteString("-custom\nruntime: yaml\ndescription: \"Forked from ")
	sb.WriteString(prog.DisplayName())
	sb.WriteString("\"\n\nconfig:\n")
	for _, f := range prog.ConfigFields() {
		sb.WriteString("  ")
		sb.WriteString(f.Key)
		sb.WriteString(":\n    type: string\n")
		if f.Default != "" {
			sb.WriteString("    default: \"")
			sb.WriteString(f.Default)
			sb.WriteString("\"\n")
		}
	}
	sb.WriteString("\nresources:\n  # TODO: add your resources here\n  # Example:\n  # my-compartment:\n  #   type: oci:identity:Compartment\n  #   properties:\n  #     compartmentId: ${oci:tenancyOcid}\n  #     name: {{ .Config.compartmentName }}\n  #     description: Created by Pulumi\n  #     enableDelete: true\n\noutputs:\n  # TODO: export resource IDs here\n")
	return sb.String()
}

// DeleteBlueprint removes a custom blueprint from the DB and registry.
// Blocked if any stacks reference the blueprint.
func (h *Handler) DeleteBlueprint(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	// Block deletes of built-in blueprints.
	if prog, ok := h.Registry.Get(name); ok {
		if _, isYAML := prog.(blueprints.YAMLBlueprintProvider); !isYAML {
			http.Error(w, "built-in blueprints cannot be deleted", http.StatusMethodNotAllowed)
			return
		}
	}

	// Check for stacks that reference this blueprint.
	stacks, err := h.Stacks.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, s := range stacks {
		if s.Blueprint == name {
			http.Error(w, "blueprint is referenced by one or more stacks and cannot be deleted", http.StatusConflict)
			return
		}
	}

	if err := h.CustomBlueprints.Delete(name); err != nil {
		http.Error(w, "blueprint not found", http.StatusNotFound)
		return
	}
	h.Registry.Deregister(name)
	log.Printf("[blueprints] deleted custom blueprint %q", name)

	w.WriteHeader(http.StatusNoContent)
}

func hasBlockingErrors(errs []blueprints.ValidationError) bool {
	for _, e := range errs {
		if e.Level < blueprints.LevelAgentAccess {
			return true
		}
	}
	return false
}
