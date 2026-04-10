package agentinject

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// InjectIntoYAML parses a rendered Pulumi YAML body, walks all resources,
// detects compute resource types via ComputeResources, and composes their
// user_data with the agent bootstrap via multipart MIME.
//
// agentVarsList provides per-node bootstrap configuration. The i-th compute
// resource (in document order) uses agentVarsList[i]; if i ≥ len(agentVarsList)
// the last entry is reused (fallback / single-node mode). Pass a single-element
// slice for the legacy single-agent behaviour.
//
// Resources that already contain the agent bootstrap marker are skipped.
// Resources without any existing user_data get the agent bootstrap as their
// sole user_data. Returns the modified YAML string and the number of compute
// instances that were injected (useful for logging).
func InjectIntoYAML(yamlBody string, agentVarsList []AgentVars) (string, int, error) {
	if len(agentVarsList) == 0 {
		return yamlBody, 0, nil
	}

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(yamlBody), &doc); err != nil {
		return yamlBody, 0, fmt.Errorf("agentinject: parse YAML: %w", err)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return yamlBody, 0, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return yamlBody, 0, nil
	}

	resourcesNode := findMapValue(root, "resources")
	if resourcesNode == nil || resourcesNode.Kind != yaml.MappingNode {
		return yamlBody, 0, nil
	}

	modified := false
	computeIndex := 0
	for i := 0; i < len(resourcesNode.Content)-1; i += 2 {
		valueNode := resourcesNode.Content[i+1]
		if valueNode.Kind != yaml.MappingNode {
			continue
		}

		typeNode := findMapValue(valueNode, "type")
		if typeNode == nil {
			continue
		}

		udPath, ok := ComputeResources[typeNode.Value]
		if !ok {
			continue
		}

		propsNode := findMapValue(valueNode, "properties")
		if propsNode == nil || propsNode.Kind != yaml.MappingNode {
			computeIndex++
			continue
		}

		// Pick the vars for this node; clamp to last entry for extra instances.
		// For InstancePool templates (InstanceConfiguration), embed ALL node
		// certs so each pool instance can select its own cert at boot time.
		idx := computeIndex
		if idx >= len(agentVarsList) {
			idx = len(agentVarsList) - 1
		}
		isPoolTemplate := typeNode.Value == "oci:Core/instanceConfiguration:InstanceConfiguration"
		var agentScript []byte
		if isPoolTemplate && len(agentVarsList) > 1 {
			agentScript = RenderAgentBootstrapForPool(agentVarsList)
		} else {
			agentScript = RenderAgentBootstrap(agentVarsList[idx])
		}

		if injectUserData(propsNode, udPath.PropertyPath, agentScript) {
			modified = true
		}
		computeIndex++
	}

	if !modified {
		return yamlBody, 0, nil
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return yamlBody, 0, fmt.Errorf("agentinject: marshal YAML: %w", err)
	}
	return string(out), computeIndex, nil
}

// injectUserData navigates a YAML node tree along the given property path
// and composes or creates the user_data value with the agent bootstrap.
// Returns true if the YAML was modified.
func injectUserData(node *yaml.Node, path []string, agentScript []byte) bool {
	if len(path) == 0 || node == nil {
		return false
	}

	current := node
	// Navigate to the parent of the leaf, creating intermediate maps as needed.
	for _, key := range path[:len(path)-1] {
		child := findMapValue(current, key)
		if child == nil {
			newMap := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			current.Content = append(current.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: key},
				newMap,
			)
			current = newMap
		} else if child.Kind != yaml.MappingNode {
			return false
		} else {
			current = child
		}
	}

	leafKey := path[len(path)-1]
	existing := findMapValue(current, leafKey)

	if existing != nil && existing.Kind == yaml.ScalarNode {
		decoded, ok := DecodeUserData(existing.Value)
		if ok && HasAgentBootstrap(decoded) {
			return false
		}

		var programScript []byte
		if ok {
			programScript = decoded
		} else {
			programScript = []byte(existing.Value)
		}
		composed := ComposeAndEncode(programScript, agentScript)
		existing.Value = composed
		existing.Style = 0
		return true
	}

	// No existing user_data — inject agent bootstrap as the sole value.
	encoded := GzipBase64(agentScript)
	setMapValue(current, leafKey, encoded)
	return true
}

// findMapValue returns the value node for a key in a YAML mapping node.
func findMapValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(mapping.Content)-1; i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// setMapValue sets or creates a key-value pair in a YAML mapping node.
func setMapValue(mapping *yaml.Node, key, value string) {
	for i := 0; i < len(mapping.Content)-1; i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1].Value = value
			mapping.Content[i+1].Kind = yaml.ScalarNode
			mapping.Content[i+1].Style = 0
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Value: value},
	)
}
