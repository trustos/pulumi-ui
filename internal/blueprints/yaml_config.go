package blueprints

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// pulumiYAMLConfig is the minimal structure of a Pulumi YAML blueprint's
// config section that we need to parse for UI form generation.
type pulumiYAMLConfig struct {
	Name   string                       `yaml:"name"`
	Config map[string]pulumiConfigField `yaml:"config"`
	Meta   *pulumiMeta                  `yaml:"meta"`
}

type pulumiConfigField struct {
	Type    string `yaml:"type"`
	Default string `yaml:"default"`
}

type pulumiMeta struct {
	Groups       []pulumiMetaGroup          `yaml:"groups"`
	Fields       map[string]pulumiMetaField `yaml:"fields"`
	AgentAccess  bool                       `yaml:"agentAccess"`
	Applications []pulumiMetaApp            `yaml:"applications"`
	MultiAccount *MultiAccountMeta          `yaml:"multiAccount"`
}

type pulumiMetaApp struct {
	Key          string              `yaml:"key"`
	Name         string              `yaml:"name"`
	Description  string              `yaml:"description"`
	Tier         string              `yaml:"tier"`
	Target       string              `yaml:"target"`
	Required     bool                `yaml:"required"`
	DefaultOn    bool                `yaml:"defaultOn"`
	DependsOn    []string            `yaml:"dependsOn"`
	ConfigFields []pulumiMetaAppCF   `yaml:"configFields"`
	ConsulEnv    map[string]string   `yaml:"consulEnv"`
	Port         int                 `yaml:"port"`
	Hooks        []pulumiMetaAppHook `yaml:"hooks"`
}

type pulumiMetaAppHook struct {
	Trigger         string `yaml:"trigger"`
	Type            string `yaml:"type"`
	Command         string `yaml:"command"`
	ContinueOnError bool   `yaml:"continueOnError"`
	Priority        int    `yaml:"priority"`
	Description     string `yaml:"description"`
}

type pulumiMetaAppCF struct {
	Key         string `yaml:"key"`
	Label       string `yaml:"label"`
	Type        string `yaml:"type"`
	Required    bool   `yaml:"required"`
	Default     string `yaml:"default"`
	Description string `yaml:"description"`
	Secret      bool   `yaml:"secret"`
}

type pulumiMetaGroup struct {
	Key    string   `yaml:"key"`
	Label  string   `yaml:"label"`
	Fields []string `yaml:"fields"`
}

type pulumiMetaField struct {
	UIType      string `yaml:"ui_type"`
	Description string `yaml:"description"`
	Label       string `yaml:"label"`
}

// ParseConfigFields parses the config: section of a Pulumi YAML body and
// returns the derived ConfigField slice plus a clean YAML body with the
// meta: section stripped (Pulumi ignores unknown top-level keys, but we
// strip it to keep the execution input tidy).
//
// Convention-based ui_type overrides: fields named "imageId" → oci-image,
// "shape" → oci-shape, "compartmentId" → oci-compartment,
// "availabilityDomain" → oci-ad. These can also be declared explicitly
// under meta.fields.
// truncateAtResources returns the portion of a Pulumi YAML body before the
// top-level "resources:" key. The meta: and config: sections always precede
// resources: and never contain Go template expressions, so we can safely parse
// them with a plain YAML library without having to neutralise template syntax.
func truncateAtResources(yamlBody string) string {
	lines := strings.Split(yamlBody, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(line, "resources:") {
			break
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func ParseConfigFields(yamlBody string) ([]ConfigField, string, error) {
	// Only parse up to the resources: section. Template expressions in resource
	// property values (e.g. "name: {{ .Config.foo }}") would otherwise cause
	// the YAML parser to fail, since "{" introduces a YAML flow mapping.
	parseable := truncateAtResources(yamlBody)

	var doc pulumiYAMLConfig
	if err := yaml.Unmarshal([]byte(parseable), &doc); err != nil {
		return nil, yamlBody, err
	}

	// Build group membership index from meta.groups.
	groupByField := map[string]string{}      // fieldKey → groupKey
	groupLabelByKey := map[string]string{}   // groupKey → groupLabel
	groupOrder := []string{}                 // stable ordering
	if doc.Meta != nil {
		for _, g := range doc.Meta.Groups {
			groupOrder = append(groupOrder, g.Key)
			groupLabelByKey[g.Key] = g.Label
			for _, fk := range g.Fields {
				groupByField[fk] = g.Key
			}
		}
	}

	// Build ui_type override index from meta.fields.
	uiTypeByField := map[string]string{}
	if doc.Meta != nil {
		for fk, mf := range doc.Meta.Fields {
			if mf.UIType != "" {
				uiTypeByField[fk] = mf.UIType
			}
		}
	}

	// YAML maps are unordered; to preserve a stable field order we re-parse
	// the truncated document as a yaml.Node so we can walk the mapping in order.
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(parseable), &root); err != nil {
		return nil, yamlBody, err
	}

	// yaml.Unmarshal into a Node gives us a Document node wrapping the actual
	// mapping. Find the "config" key and iterate its children in order.
	orderedKeys := extractOrderedMapKeys(&root, "config")

	fields := make([]ConfigField, 0, len(orderedKeys))
	for _, key := range orderedKeys {
		cf := doc.Config[key]

		fieldType := yamlTypeToFieldType(key, cf.Type, uiTypeByField)

		gKey := groupByField[key]
		gLabel := groupLabelByKey[gKey]

		// Description and label from meta.fields take precedence over auto-derived values.
		description := ""
		label := keyToLabel(key)
		if doc.Meta != nil {
			if mf, ok := doc.Meta.Fields[key]; ok {
				if mf.Description != "" {
					description = mf.Description
				}
				if mf.Label != "" {
					label = mf.Label
				}
			}
		}

		fields = append(fields, ConfigField{
			Key:         key,
			Label:       label,
			Type:        fieldType,
			Required:    fieldType == "ssh-public-key" && cf.Default == "",
			Default:     cf.Default,
			Description: description,
			Group:       gKey,
			GroupLabel:  gLabel,
		})
	}

	// Strip the meta: section from the returned clean YAML.
	clean := stripMetaSection(yamlBody)

	return fields, clean, nil
}

// yamlTypeToFieldType converts a Pulumi YAML config type string to a
// ConfigField type string, with convention- and metadata-based overrides.
func yamlTypeToFieldType(key, pulumiType string, uiTypeByField map[string]string) string {
	// Explicit ui_type from meta.fields wins.
	if t, ok := uiTypeByField[key]; ok {
		return t
	}
	// Convention-based key overrides — OCI picker types.
	switch key {
	case "imageId":
		return "oci-image"
	case "shape":
		return "oci-shape"
	case "sshPublicKey":
		return "ssh-public-key"
	case "compartmentId":
		return "oci-compartment"
	case "availabilityDomain":
		return "oci-ad"
	// Convention-based numeric fields — common OCI/infra integer parameters.
	case "ocpus", "memoryInGbs", "bootVolSizeGb", "nodeCount":
		return "number"
	}
	// Pulumi type → form field type.
	switch strings.ToLower(pulumiType) {
	case "integer", "number":
		return "number"
	case "boolean":
		return "select"
	default:
		return "text"
	}
}

// keyToLabel converts a camelCase key to a Title Case label.
// e.g. "vcnCidr" → "Vcn Cidr", "nodeCount" → "Node Count".
func keyToLabel(key string) string {
	var b strings.Builder
	for i, ch := range key {
		if i > 0 && ch >= 'A' && ch <= 'Z' {
			b.WriteRune(' ')
		}
		if i == 0 {
			if ch >= 'a' && ch <= 'z' {
				b.WriteRune(ch - 32)
			} else {
				b.WriteRune(ch)
			}
		} else {
			b.WriteRune(ch)
		}
	}
	return b.String()
}

// extractOrderedMapKeys walks a yaml.Node document and returns the keys of
// the named top-level mapping in document order.
func extractOrderedMapKeys(root *yaml.Node, mapKey string) []string {
	if root == nil || len(root.Content) == 0 {
		return nil
	}
	docNode := root.Content[0] // Document → MappingNode
	if docNode.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(docNode.Content); i += 2 {
		keyNode := docNode.Content[i]
		valNode := docNode.Content[i+1]
		if keyNode.Value == mapKey && valNode.Kind == yaml.MappingNode {
			keys := make([]string, 0, len(valNode.Content)/2)
			for j := 0; j+1 < len(valNode.Content); j += 2 {
				keys = append(keys, valNode.Content[j].Value)
			}
			return keys
		}
	}
	return nil
}

// ApplyConfigDefaults merges default values from the blueprint's config: section
// into the provided config map. User-supplied values take priority; only keys
// absent from config (or present with an empty value) are filled from defaults.
// This ensures {{ .Config.key }} never fails for fields that carry a default.
func ApplyConfigDefaults(yamlBody string, config map[string]string) map[string]string {
	fields, _, _ := ParseConfigFields(yamlBody)
	merged := make(map[string]string, len(config)+len(fields))
	for _, f := range fields {
		if f.Default != "" {
			merged[f.Key] = f.Default
		}
	}
	for k, v := range config {
		merged[k] = v
	}
	return merged
}

// ParseMultiAccount parses the meta.multiAccount section of a YAML blueprint.
// Returns nil if no multi-account metadata is declared.
func ParseMultiAccount(yamlBody string) *MultiAccountMeta {
	parseable := truncateAtResources(yamlBody)
	var doc pulumiYAMLConfig
	if err := yaml.Unmarshal([]byte(parseable), &doc); err != nil || doc.Meta == nil {
		return nil
	}
	return doc.Meta.MultiAccount
}

// ParseAgentAccess returns true if the YAML blueprint declares agentAccess: true
// in its meta: section.
func ParseAgentAccess(yamlBody string) bool {
	parseable := truncateAtResources(yamlBody)
	var doc pulumiYAMLConfig
	if err := yaml.Unmarshal([]byte(parseable), &doc); err != nil || doc.Meta == nil {
		return false
	}
	return doc.Meta.AgentAccess
}

// ParseApplications parses the meta.applications section of a YAML blueprint
// and returns the corresponding ApplicationDef slice. Returns nil if no
// applications are declared.
func ParseApplications(yamlBody string) []ApplicationDef {
	parseable := truncateAtResources(yamlBody)
	var doc pulumiYAMLConfig
	if err := yaml.Unmarshal([]byte(parseable), &doc); err != nil || doc.Meta == nil {
		return nil
	}
	if len(doc.Meta.Applications) == 0 {
		return nil
	}
	apps := make([]ApplicationDef, 0, len(doc.Meta.Applications))
	for _, ma := range doc.Meta.Applications {
		app := ApplicationDef{
			Key:         ma.Key,
			Name:        ma.Name,
			Description: ma.Description,
			Tier:        ApplicationTier(ma.Tier),
			Target:      TargetMode(ma.Target),
			Required:    ma.Required,
			DefaultOn:   ma.DefaultOn,
			DependsOn:   ma.DependsOn,
			ConsulEnv:   ma.ConsulEnv,
			Port:        ma.Port,
		}
		for _, cf := range ma.ConfigFields {
			fieldType := cf.Type
			if fieldType == "" {
				fieldType = "text"
			}
			app.ConfigFields = append(app.ConfigFields, ConfigField{
				Key:         cf.Key,
				Label:       cf.Label,
				Type:        fieldType,
				Required:    cf.Required,
				Default:     cf.Default,
				Description: cf.Description,
				Secret:      cf.Secret,
			})
		}
		for _, h := range ma.Hooks {
			app.Hooks = append(app.Hooks, ApplicationHook{
				Trigger:         h.Trigger,
				Type:            h.Type,
				Command:         h.Command,
				ContinueOnError: h.ContinueOnError,
				Priority:        h.Priority,
				Description:     h.Description,
			})
		}
		apps = append(apps, app)
	}
	return apps
}

// stripMetaSection removes the `meta:` top-level block from a YAML body by
// simple line scanning. This is a conservative approach: we drop lines from
// the "meta:" key line until the next non-indented key or end of file.
func stripMetaSection(yamlBody string) string {
	lines := strings.Split(yamlBody, "\n")
	out := make([]string, 0, len(lines))
	inMeta := false
	for _, line := range lines {
		if !inMeta {
			if strings.HasPrefix(line, "meta:") {
				inMeta = true
				continue
			}
			out = append(out, line)
		} else {
			// A non-indented, non-blank line ends the meta block.
			trimmed := strings.TrimLeft(line, " \t")
			if trimmed == "" || line[0] == ' ' || line[0] == '\t' {
				continue
			}
			inMeta = false
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
