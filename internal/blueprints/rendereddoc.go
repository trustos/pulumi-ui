package blueprints

import (
	"fmt"

	"github.com/trustos/pulumi-ui/internal/cloud"
	"gopkg.in/yaml.v3"
)

// ParseResourceGraph parses rendered Pulumi YAML into a typed
// cloud.ResourceGraph. Used by Level-8 runtime validation and by any
// future provider-specific cross-field check that needs a walkable
// resource tree. Returns a graph with empty maps on parse failure so
// downstream validators can continue without a nil-check cascade.
func ParseResourceGraph(rendered string) (cloud.ResourceGraph, error) {
	var doc struct {
		Resources map[string]rawResource `yaml:"resources"`
		Variables map[string]any         `yaml:"variables"`
		Outputs   map[string]any         `yaml:"outputs"`
	}
	if err := yaml.Unmarshal([]byte(rendered), &doc); err != nil {
		return cloud.ResourceGraph{
			Resources: map[string]cloud.ResourceNode{},
			Variables: map[string]any{},
			Outputs:   map[string]any{},
		}, fmt.Errorf("parse rendered yaml: %w", err)
	}

	graph := cloud.ResourceGraph{
		Resources: make(map[string]cloud.ResourceNode, len(doc.Resources)),
		Variables: doc.Variables,
		Outputs:   doc.Outputs,
	}
	if graph.Variables == nil {
		graph.Variables = map[string]any{}
	}
	if graph.Outputs == nil {
		graph.Outputs = map[string]any{}
	}

	for name, raw := range doc.Resources {
		graph.Resources[name] = cloud.ResourceNode{
			Type:       raw.Type,
			Name:       name,
			Properties: normalizeAnyMap(raw.Properties),
			Options:    normalizeAnyMap(raw.Options),
		}
	}
	return graph, nil
}

type rawResource struct {
	Type       string         `yaml:"type"`
	Properties map[string]any `yaml:"properties"`
	Options    map[string]any `yaml:"options"`
}

// normalizeAnyMap converts yaml.v3's map[interface{}]interface{} nested
// maps into map[string]any recursively, so downstream code can index
// with string keys without type-asserting at every level.
func normalizeAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = normalizeAny(v)
	}
	return out
}

func normalizeAny(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return normalizeAnyMap(x)
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			if ks, ok := k.(string); ok {
				out[ks] = normalizeAny(vv)
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = normalizeAny(vv)
		}
		return out
	default:
		return v
	}
}
