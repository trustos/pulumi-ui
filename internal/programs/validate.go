package programs

import (
	"regexp"
	"strconv"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

// ValidationLevel identifies which validation stage produced an error.
type ValidationLevel int

const (
	LevelTemplateParse     ValidationLevel = 1
	LevelTemplateRender    ValidationLevel = 2
	LevelRenderedYAML      ValidationLevel = 3
	LevelConfigSection     ValidationLevel = 4
	LevelResourceStructure ValidationLevel = 5
)

// ValidationError is one structured error produced by ValidateProgram.
// Field is the YAML key or template variable involved, or "" for
// document-level errors. Line is 1-based; 0 means the line is unknown.
type ValidationError struct {
	Level   ValidationLevel `json:"level"`
	Field   string          `json:"field,omitempty"`
	Message string          `json:"message"`
	Line    int             `json:"line,omitempty"`
}

var templateLineRe = regexp.MustCompile(`program:(\d+)`)
var missingKeyRe = regexp.MustCompile(`map has no entry for key "([^"]+)"`)

// ValidateProgram runs all five validation levels and returns every error
// found. Levels run sequentially; rendering-dependent levels are skipped if
// the template cannot be parsed or rendered.
func ValidateProgram(yamlBody string) []ValidationError {
	var errs []ValidationError

	// Level 4 is independent — run first so config errors are always reported.
	errs = append(errs, validateConfigSection(yamlBody)...)

	// Level 1 — template syntax.
	l1 := validateTemplateParse(yamlBody)
	errs = append(errs, l1...)
	if len(l1) > 0 {
		return errs // cannot render if parse failed
	}

	// Level 2 — render with synthesised defaults.
	rendered, l2 := validateTemplateRender(yamlBody)
	errs = append(errs, l2...)
	if len(l2) > 0 {
		return errs // cannot validate structure without a rendered body
	}

	// Level 3 — rendered YAML structure.
	errs = append(errs, validateRenderedYAML(rendered)...)

	// Level 5 — resource type structure (needs rendered body).
	errs = append(errs, validateResourceStructure(rendered)...)

	return errs
}

// --- Level 1: template parse ------------------------------------------------

func validateTemplateParse(yamlBody string) []ValidationError {
	_, err := template.New("program").
		Delims("{{", "}}").
		Funcs(buildFuncMap()).
		Option("missingkey=error").
		Parse(yamlBody)
	if err == nil {
		return nil
	}
	line := extractLine(err.Error())
	msg := cleanTemplateError(err.Error())
	return []ValidationError{{Level: LevelTemplateParse, Message: msg, Line: line}}
}

// --- Level 2: render with synthesised config --------------------------------

func validateTemplateRender(yamlBody string) (string, []ValidationError) {
	fields, _, _ := ParseConfigFields(yamlBody)
	cfg := buildValidationConfig(fields)

	rendered, err := RenderTemplate(yamlBody, cfg)
	if err == nil {
		return rendered, nil
	}

	field := ""
	if m := missingKeyRe.FindStringSubmatch(err.Error()); len(m) > 1 {
		field = m[1]
	}
	line := extractLine(err.Error())
	msg := cleanTemplateError(err.Error())
	return "", []ValidationError{{Level: LevelTemplateRender, Field: field, Message: msg, Line: line}}
}

// buildValidationConfig returns a config map populated with declared defaults,
// filling zero values for fields that declare no default.
func buildValidationConfig(fields []ConfigField) map[string]string {
	cfg := make(map[string]string, len(fields))
	for _, f := range fields {
		if f.Default != "" {
			cfg[f.Key] = f.Default
		} else {
			switch f.Type {
			case "number":
				cfg[f.Key] = "0"
			case "select": // Boolean
				cfg[f.Key] = "false"
			default:
				cfg[f.Key] = ""
			}
		}
	}
	return cfg
}

// --- Level 3: rendered YAML structure ---------------------------------------

func validateRenderedYAML(rendered string) []ValidationError {
	var doc struct {
		Name      string                 `yaml:"name"`
		Runtime   string                 `yaml:"runtime"`
		Resources map[string]interface{} `yaml:"resources"`
	}
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		return []ValidationError{{
			Level:   LevelRenderedYAML,
			Message: "rendered output is not valid YAML: " + err.Error(),
		}}
	}

	var errs []ValidationError
	if strings.TrimSpace(doc.Name) == "" {
		errs = append(errs, ValidationError{Level: LevelRenderedYAML, Field: "name", Message: "top-level 'name' key is missing or empty"})
	}
	if doc.Runtime != "yaml" {
		errs = append(errs, ValidationError{
			Level:   LevelRenderedYAML,
			Field:   "runtime",
			Message: "runtime must be 'yaml'" + func() string {
				if doc.Runtime != "" {
					return ", got '" + doc.Runtime + "'"
				}
				return " (key missing)"
			}(),
		})
	}
	if doc.Resources == nil {
		errs = append(errs, ValidationError{Level: LevelRenderedYAML, Field: "resources", Message: "'resources' key is missing"})
	} else if len(doc.Resources) == 0 {
		errs = append(errs, ValidationError{Level: LevelRenderedYAML, Field: "resources", Message: "'resources' map has no entries"})
	}
	return errs
}

// --- Level 4: config section structure (raw YAML, template-stripped) --------

var validFieldKeyRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9]*$`)
var validTypes = map[string]bool{"String": true, "Integer": true, "Number": true, "Boolean": true}

func validateConfigSection(yamlBody string) []ValidationError {
	stripped := truncateAtResources(yamlBody)

	var doc pulumiYAMLConfig
	if err := yaml.Unmarshal([]byte(stripped), &doc); err != nil {
		return []ValidationError{{
			Level:   LevelConfigSection,
			Field:   "config",
			Message: "could not parse YAML structure: " + err.Error(),
		}}
	}

	var errs []ValidationError

	if len(doc.Config) == 0 {
		errs = append(errs, ValidationError{
			Level:   LevelConfigSection,
			Field:   "config",
			Message: "config: section is missing or empty — add at least one field",
		})
		// Still check meta consistency below if it exists.
	}

	for key, cf := range doc.Config {
		if !validFieldKeyRe.MatchString(key) {
			errs = append(errs, ValidationError{
				Level:   LevelConfigSection,
				Field:   key,
				Message: "config field key '" + key + "' must start with a letter and contain only letters and digits (no hyphens or underscores)",
			})
		}
		if cf.Type != "" && !validTypes[cf.Type] {
			errs = append(errs, ValidationError{
				Level:   LevelConfigSection,
				Field:   key,
				Message: "config field '" + key + "' has unknown type '" + cf.Type + "' — use String, Integer, Number, or Boolean",
			})
		}
	}

	if doc.Meta != nil {
		seen := map[string]string{} // fieldKey → groupKey (first occurrence)
		for _, g := range doc.Meta.Groups {
			for _, fk := range g.Fields {
				if _, exists := doc.Config[fk]; !exists && len(doc.Config) > 0 {
					errs = append(errs, ValidationError{
						Level:   LevelConfigSection,
						Field:   "meta.groups",
						Message: "meta group '" + g.Key + "' references field '" + fk + "' which is not declared in config:",
					})
				}
				if prev, dup := seen[fk]; dup {
					errs = append(errs, ValidationError{
						Level:   LevelConfigSection,
						Field:   "meta.groups",
						Message: "field '" + fk + "' appears in both group '" + prev + "' and group '" + g.Key + "'",
					})
				} else {
					seen[fk] = g.Key
				}
			}
		}
	}

	return errs
}

// --- Level 5: resource structure (rendered YAML) ----------------------------

var resourceTypeRe = regexp.MustCompile(`^[a-z][a-z0-9]*:[a-z][a-z0-9]*:[A-Za-z][A-Za-z0-9]*$`)

func validateResourceStructure(rendered string) []ValidationError {
	var doc struct {
		Resources map[string]struct {
			Type string `yaml:"type"`
			Get  string `yaml:"get"`
		} `yaml:"resources"`
	}
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		return nil // Level 3 already catches YAML parse errors
	}

	var errs []ValidationError
	for name, res := range doc.Resources {
		if res.Get != "" {
			continue // data-source resource, no type: required
		}
		if res.Type == "" {
			errs = append(errs, ValidationError{
				Level:   LevelResourceStructure,
				Field:   name,
				Message: "resource '" + name + "' is missing a 'type:' field",
			})
			continue
		}
		if !resourceTypeRe.MatchString(res.Type) {
			errs = append(errs, ValidationError{
				Level:   LevelResourceStructure,
				Field:   name,
				Message: "resource '" + name + "' has invalid type '" + res.Type + "' — expected pattern provider:module:Resource, e.g. oci:core:Instance",
			})
		}
	}
	return errs
}

// --- helpers ----------------------------------------------------------------

func extractLine(errMsg string) int {
	if m := templateLineRe.FindStringSubmatch(errMsg); len(m) > 1 {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

// cleanTemplateError strips the repetitive "template render: template: program:N:M: "
// prefix from Go template error messages, leaving just the actionable part.
func cleanTemplateError(msg string) string {
	// Strip outermost wrappers added by RenderTemplate.
	msg = strings.TrimPrefix(msg, "template render: ")
	msg = strings.TrimPrefix(msg, "template parse: ")
	// Strip "template: program:N:M: " prefix.
	if idx := strings.Index(msg, ": executing"); idx >= 0 {
		// Keep from "executing" onwards.
		msg = msg[idx+2:]
	} else if idx := strings.Index(msg, "error calling"); idx >= 0 {
		msg = msg[idx:]
	} else if colon := strings.LastIndex(msg, ": "); colon >= 0 && strings.Contains(msg[:colon], "program:") {
		msg = msg[colon+2:]
	}
	return msg
}
