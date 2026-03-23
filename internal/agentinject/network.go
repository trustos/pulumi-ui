package agentinject

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

const AgentPort = 41820

// NetworkingResourceTypes maps Pulumi resource type tokens to a category
// for networking injection detection.
var NetworkingResourceTypes = map[string]string{
	"oci:Core/networkSecurityGroup:NetworkSecurityGroup":             "nsg",
	"oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer": "nlb",
}

// ContextResourceTypes used to infer VCN/subnet for creating networking.
var ContextResourceTypes = map[string]string{
	"oci:Core/vcn:Vcn":       "vcn",
	"oci:Core/subnet:Subnet": "subnet",
}

// InjectNetworkingIntoYAML parses a rendered Pulumi YAML, detects existing
// networking resources (NSGs, NLBs), and appends new resources that open the
// agent port for connectivity. Compute instances are detected for NLB backends.
//
// Injected resources use a "__agent_" prefix to avoid naming collisions.
// If agent networking resources already exist (detected by prefix), injection
// is skipped to prevent duplicates.
func InjectNetworkingIntoYAML(yamlBody string) (string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(yamlBody), &doc); err != nil {
		return yamlBody, fmt.Errorf("agentinject network: parse YAML: %w", err)
	}

	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return yamlBody, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return yamlBody, nil
	}

	resourcesNode := findMapValue(root, "resources")
	if resourcesNode == nil || resourcesNode.Kind != yaml.MappingNode {
		return yamlBody, nil
	}

	// Check if already injected
	for i := 0; i < len(resourcesNode.Content)-1; i += 2 {
		if len(resourcesNode.Content[i].Value) > 8 && resourcesNode.Content[i].Value[:8] == "__agent_" {
			return yamlBody, nil
		}
	}

	// Discover existing resources by type
	type discoveredResource struct {
		name     string
		category string
	}
	var nsgs, nlbs, computes []discoveredResource
	var vcns, subnets []discoveredResource

	for i := 0; i < len(resourcesNode.Content)-1; i += 2 {
		resName := resourcesNode.Content[i].Value
		resNode := resourcesNode.Content[i+1]
		if resNode.Kind != yaml.MappingNode {
			continue
		}
		typeNode := findMapValue(resNode, "type")
		if typeNode == nil {
			continue
		}
		if cat, ok := NetworkingResourceTypes[typeNode.Value]; ok {
			switch cat {
			case "nsg":
				nsgs = append(nsgs, discoveredResource{name: resName, category: cat})
			case "nlb":
				nlbs = append(nlbs, discoveredResource{name: resName, category: cat})
			}
		}
		if cat, ok := ContextResourceTypes[typeNode.Value]; ok {
			switch cat {
			case "vcn":
				vcns = append(vcns, discoveredResource{name: resName, category: cat})
			case "subnet":
				subnets = append(subnets, discoveredResource{name: resName, category: cat})
			}
		}
		if IsComputeResource(typeNode.Value) {
			computes = append(computes, discoveredResource{name: resName, category: "compute"})
		}
	}

	if len(nsgs) == 0 && len(nlbs) == 0 && len(computes) == 0 {
		return yamlBody, nil
	}

	modified := false

	// When no NSG exists but we have a VCN and compute, create one with the agent rule
	if len(nsgs) == 0 && len(vcns) > 0 && len(computes) > 0 {
		vcn := vcns[0]
		compartmentRef := resolveCompartmentId(resourcesNode, vcn.name)
		nsgName := "__agent_nsg"
		addResource(resourcesNode, nsgName, buildNSGResource(compartmentRef, vcn.name))
		ruleName := "__agent_nsg_rule"
		addResource(resourcesNode, ruleName, buildNSGRuleResource(nsgName))
		// Attach NSG to each compute instance
		for _, compute := range computes {
			attachNSGToInstance(resourcesNode, compute.name, nsgName)
		}
		modified = true
	}

	// For each existing NSG, add an ingress rule for Nebula UDP port
	for _, nsg := range nsgs {
		ruleName := fmt.Sprintf("__agent_nsg_rule_%s", nsg.name)
		addResource(resourcesNode, ruleName, buildNSGRuleResource(nsg.name))
		modified = true
	}

	// When no NLB exists but we have a subnet and compute, create one with agent backend set
	if len(nlbs) == 0 && len(subnets) > 0 && len(computes) > 0 {
		subnet := subnets[0]
		compartmentRef := resolveCompartmentId(resourcesNode, subnet.name)
		nlbName := "__agent_nlb"
		addResource(resourcesNode, nlbName, buildNLBResource(compartmentRef, subnet.name))
		bsName := "__agent_bs"
		lnName := "__agent_ln"
		addResource(resourcesNode, bsName, buildNLBBackendSetResource(nlbName))
		addResource(resourcesNode, lnName, buildNLBListenerResource(nlbName, bsName))
		prevDep := lnName
		for _, compute := range computes {
			beName := fmt.Sprintf("__agent_be_%s", compute.name)
			addResource(resourcesNode, beName, buildNLBBackendResource(nlbName, bsName, compute.name, prevDep))
			prevDep = beName
		}
		modified = true
	}

	// For each existing NLB, add a backend set + listener + backends for each compute
	for _, nlb := range nlbs {
		bsName := fmt.Sprintf("__agent_bs_%s", nlb.name)
		lnName := fmt.Sprintf("__agent_ln_%s", nlb.name)

		addResource(resourcesNode, bsName, buildNLBBackendSetResource(nlb.name))
		addResource(resourcesNode, lnName, buildNLBListenerResource(nlb.name, bsName))
		modified = true

		prevDep := lnName
		for _, compute := range computes {
			beName := fmt.Sprintf("__agent_be_%s_%s", nlb.name, compute.name)
			addResource(resourcesNode, beName, buildNLBBackendResource(nlb.name, bsName, compute.name, prevDep))
			prevDep = beName
		}
	}

	if !modified {
		return yamlBody, nil
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return yamlBody, fmt.Errorf("agentinject network: marshal YAML: %w", err)
	}
	return string(out), nil
}

func addResource(resourcesNode *yaml.Node, name string, resNode *yaml.Node) {
	resourcesNode.Content = append(resourcesNode.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: name},
		resNode,
	)
}

func buildNSGResource(compartmentRef, vcnName string) *yaml.Node {
	return buildMappingNode(map[string]interface{}{
		"type": "oci:Core/networkSecurityGroup:NetworkSecurityGroup",
		"properties": map[string]interface{}{
			"compartmentId": compartmentRef,
			"vcnId":         fmt.Sprintf("${%s.id}", vcnName),
			"displayName":   "pulumi-ui-agent-nsg",
		},
	})
}

func buildNLBResource(compartmentRef, subnetName string) *yaml.Node {
	return buildMappingNode(map[string]interface{}{
		"type": "oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer",
		"properties": map[string]interface{}{
			"compartmentId":                compartmentRef,
			"subnetId":                     fmt.Sprintf("${%s.id}", subnetName),
			"displayName":                  "pulumi-ui-agent-nlb",
			"isPrivate":                    false,
			"isPreserveSourceDestination":  false,
		},
	})
}

func buildNSGRuleResource(nsgName string) *yaml.Node {
	return buildMappingNode(map[string]interface{}{
		"type": "oci:Core/networkSecurityGroupSecurityRule:NetworkSecurityGroupSecurityRule",
		"properties": map[string]interface{}{
			"networkSecurityGroupId": fmt.Sprintf("${%s.id}", nsgName),
			"direction":             "INGRESS",
			"protocol":              "17",
			"source":                "0.0.0.0/0",
			"sourceType":            "CIDR_BLOCK",
			"udpOptions": map[string]interface{}{
				"destinationPortRange": map[string]interface{}{
					"min": AgentPort,
					"max": AgentPort,
				},
			},
		},
	})
}

func buildNLBBackendSetResource(nlbName string) *yaml.Node {
	return buildMappingNode(map[string]interface{}{
		"type": "oci:NetworkLoadBalancer/backendSet:BackendSet",
		"properties": map[string]interface{}{
			"networkLoadBalancerId": fmt.Sprintf("${%s.id}", nlbName),
			"name":                 "agent-backend-set",
			"policy":               "FIVE_TUPLE",
			"healthChecker": map[string]interface{}{
				"protocol": "TCP",
				"port":     22,
			},
			"isPreserveSource": false,
		},
	})
}

func buildNLBListenerResource(nlbName, bsName string) *yaml.Node {
	return buildMappingNode(map[string]interface{}{
		"type": "oci:NetworkLoadBalancer/listener:Listener",
		"properties": map[string]interface{}{
			"networkLoadBalancerId":  fmt.Sprintf("${%s.id}", nlbName),
			"name":                  "agent-listener",
			"defaultBackendSetName": fmt.Sprintf("${%s.name}", bsName),
			"port":                  AgentPort,
			"protocol":              "UDP",
		},
		"options": map[string]interface{}{
			"dependsOn": []string{fmt.Sprintf("${%s}", bsName)},
		},
	})
}

func buildNLBBackendResource(nlbName, bsName, computeName, prevDep string) *yaml.Node {
	return buildMappingNode(map[string]interface{}{
		"type": "oci:NetworkLoadBalancer/backend:Backend",
		"properties": map[string]interface{}{
			"networkLoadBalancerId": fmt.Sprintf("${%s.id}", nlbName),
			"backendSetName":       fmt.Sprintf("${%s.name}", bsName),
			"port":                 AgentPort,
			"targetId":             fmt.Sprintf("${%s.id}", computeName),
		},
		"options": map[string]interface{}{
			"dependsOn": []string{fmt.Sprintf("${%s}", prevDep)},
		},
	})
}

// resolveCompartmentId extracts the compartmentId from a named resource's
// properties. Returns the raw value (e.g. a Pulumi interpolation or literal).
func resolveCompartmentId(resourcesNode *yaml.Node, resName string) string {
	for i := 0; i < len(resourcesNode.Content)-1; i += 2 {
		if resourcesNode.Content[i].Value != resName {
			continue
		}
		resNode := resourcesNode.Content[i+1]
		if resNode.Kind != yaml.MappingNode {
			continue
		}
		props := findMapValue(resNode, "properties")
		if props == nil || props.Kind != yaml.MappingNode {
			continue
		}
		cid := findMapValue(props, "compartmentId")
		if cid != nil {
			return cid.Value
		}
	}
	return "${oci:tenancyOcid}"
}

// attachNSGToInstance adds the given NSG to an instance's createVnicDetails.nsgIds.
// If createVnicDetails doesn't exist, it's created.
func attachNSGToInstance(resourcesNode *yaml.Node, instanceName, nsgName string) {
	for i := 0; i < len(resourcesNode.Content)-1; i += 2 {
		if resourcesNode.Content[i].Value != instanceName {
			continue
		}
		resNode := resourcesNode.Content[i+1]
		if resNode.Kind != yaml.MappingNode {
			continue
		}
		props := findMapValue(resNode, "properties")
		if props == nil || props.Kind != yaml.MappingNode {
			continue
		}

		nsgRef := fmt.Sprintf("${%s.id}", nsgName)

		// Look for createVnicDetails
		vnicDetails := findMapValue(props, "createVnicDetails")
		if vnicDetails != nil && vnicDetails.Kind == yaml.MappingNode {
			// Add nsgIds to existing createVnicDetails
			nsgIds := findMapValue(vnicDetails, "nsgIds")
			if nsgIds != nil && nsgIds.Kind == yaml.SequenceNode {
				nsgIds.Content = append(nsgIds.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: nsgRef})
			} else {
				// Add nsgIds key
				vnicDetails.Content = append(vnicDetails.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "nsgIds"},
					&yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{
						{Kind: yaml.ScalarNode, Value: nsgRef},
					}},
				)
			}
		} else {
			// Create createVnicDetails with nsgIds
			props.Content = append(props.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "createVnicDetails"},
				buildMappingNode(map[string]interface{}{
					"nsgIds": []string{nsgRef},
				}),
			)
		}
		return
	}
}

// buildMappingNode constructs a yaml.Node tree from a Go map. Supports nested
// maps, string slices, strings, ints, and bools.
func buildMappingNode(data map[string]interface{}) *yaml.Node {
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	// Use a stable key order: type, properties, options
	orderedKeys := []string{"type", "properties", "options"}
	seen := map[string]bool{}
	for _, k := range orderedKeys {
		if v, ok := data[k]; ok {
			node.Content = append(node.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: k},
				toYAMLNode(v),
			)
			seen[k] = true
		}
	}
	for k, v := range data {
		if seen[k] {
			continue
		}
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: k},
			toYAMLNode(v),
		)
	}
	return node
}

func toYAMLNode(v interface{}) *yaml.Node {
	switch val := v.(type) {
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: val}
	case int:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%d", val), Tag: "!!int"}
	case bool:
		s := "false"
		if val {
			s = "true"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Value: s, Tag: "!!bool"}
	case map[string]interface{}:
		return buildMappingNode(val)
	case []string:
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, item := range val {
			seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: item})
		}
		return seq
	default:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", val)}
	}
}
