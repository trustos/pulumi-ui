package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/trustos/pulumi-ui/internal/db"
	"github.com/trustos/pulumi-ui/internal/programs"
)

// ListPrograms returns all programs (built-in + custom) without YAML bodies.
func (h *Handler) ListPrograms(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(programs.List())
}

// GetProgram returns a single program. For custom programs the full YAML body
// is included in the response.
func (h *Handler) GetProgram(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	// Check custom programs first (they carry the YAML body).
	if h.CustomPrograms != nil {
		if cp, err := h.CustomPrograms.Get(name); err == nil {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(cp)
			return
		}
	}

	// Fall back to built-in programs (no YAML body).
	prog, ok := programs.Get(name)
	if !ok {
		http.Error(w, "program not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(programs.ProgramMeta{
		Name:         prog.Name(),
		DisplayName:  prog.DisplayName(),
		Description:  prog.Description(),
		ConfigFields: prog.ConfigFields(),
	})
}

type createProgramRequest struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	ProgramYAML string `json:"programYaml"`
}

// ValidateProgramHandler runs all validation levels against a YAML body and
// returns the result. Always responds 200; callers check the "valid" field.
func (h *Handler) ValidateProgramHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProgramYAML string `json:"programYaml"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	errs := programs.ValidateProgram(req.ProgramYAML)
	if errs == nil {
		errs = []programs.ValidationError{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"valid":  len(errs) == 0,
		"errors": errs,
	})
}

// CreateProgram saves a new user-defined YAML program and registers it.
func (h *Handler) CreateProgram(w http.ResponseWriter, r *http.Request) {
	var req createProgramRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if req.Name == "" || req.DisplayName == "" || req.ProgramYAML == "" {
		http.Error(w, "name, displayName, and programYaml are required", http.StatusBadRequest)
		return
	}
	if errs := programs.ValidateProgram(req.ProgramYAML); len(errs) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(errs)
		return
	}
	if _, ok := programs.Get(req.Name); ok {
		http.Error(w, "a program with that name already exists", http.StatusConflict)
		return
	}

	cp := db.CustomProgram{
		Name:        req.Name,
		DisplayName: req.DisplayName,
		Description: req.Description,
		ProgramYAML: req.ProgramYAML,
	}
	if err := h.CustomPrograms.Create(cp); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Register in memory so it's available immediately without restart.
	programs.RegisterYAML(req.Name, req.DisplayName, req.Description, req.ProgramYAML)
	log.Printf("[programs] registered custom program %q", req.Name)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(cp)
}

type updateProgramRequest struct {
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	ProgramYAML string `json:"programYaml"`
}

// UpdateProgram replaces the YAML body of an existing custom program.
func (h *Handler) UpdateProgram(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	// Block updates to built-in programs.
	if prog, ok := programs.Get(name); ok {
		if _, isYAML := prog.(programs.YAMLProgramProvider); !isYAML {
			http.Error(w, "built-in programs cannot be modified", http.StatusMethodNotAllowed)
			return
		}
	}

	var req updateProgramRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.ProgramYAML == "" {
		http.Error(w, "programYaml is required", http.StatusBadRequest)
		return
	}
	if errs := programs.ValidateProgram(req.ProgramYAML); len(errs) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		json.NewEncoder(w).Encode(errs)
		return
	}

	cp, err := h.CustomPrograms.Get(name)
	if err != nil {
		http.Error(w, "program not found", http.StatusNotFound)
		return
	}

	displayName := req.DisplayName
	if displayName == "" {
		displayName = cp.DisplayName
	}

	if err := h.CustomPrograms.Update(name, displayName, req.Description, req.ProgramYAML); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Re-register with updated body (replaces old entry).
	programs.Deregister(name)
	programs.RegisterYAML(name, displayName, req.Description, req.ProgramYAML)
	log.Printf("[programs] updated custom program %q", name)

	updated, _ := h.CustomPrograms.Get(name)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(updated)
}

// ForkProgram generates a starter YAML from a built-in program's config fields.
func (h *Handler) ForkProgram(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	prog, ok := programs.Get(name)
	if !ok {
		http.Error(w, "program not found", http.StatusNotFound)
		return
	}
	// Only built-in programs can be forked.
	if _, isYAML := prog.(programs.YAMLProgramProvider); isYAML {
		http.Error(w, "custom programs cannot be forked (edit them directly)", http.StatusBadRequest)
		return
	}

	yaml := buildForkYAML(prog)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"programYaml": yaml})
}

func buildForkYAML(prog programs.Program) string {
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
	sb.WriteString("\nresources:\n  # TODO: add your resources here\n  # Example:\n  # my-compartment:\n  #   type: oci:Identity/compartment:Compartment\n  #   properties:\n  #     compartmentId: ${oci:tenancyOcid}\n  #     name: {{ .Config.compartmentName }}\n  #     description: Created by Pulumi\n  #     enableDelete: true\n\noutputs:\n  # TODO: export resource IDs here\n")
	return sb.String()
}

// DeleteProgram removes a custom program from the DB and registry.
// Blocked if any stacks reference the program.
func (h *Handler) DeleteProgram(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	// Block deletes of built-in programs.
	if prog, ok := programs.Get(name); ok {
		if _, isYAML := prog.(programs.YAMLProgramProvider); !isYAML {
			http.Error(w, "built-in programs cannot be deleted", http.StatusMethodNotAllowed)
			return
		}
	}

	// Check for stacks that reference this program.
	stacks, err := h.Stacks.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, s := range stacks {
		if s.Program == name {
			http.Error(w, "program is referenced by one or more stacks and cannot be deleted", http.StatusConflict)
			return
		}
	}

	if err := h.CustomPrograms.Delete(name); err != nil {
		http.Error(w, "program not found", http.StatusNotFound)
		return
	}
	programs.Deregister(name)
	log.Printf("[programs] deleted custom program %q", name)

	w.WriteHeader(http.StatusNoContent)
}
