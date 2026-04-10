package agentinject

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var pulumiInterpRe = regexp.MustCompile(`\$\{[^}]+\}`)

const (
	AgentPort        = 41820
	AgentNLBPortBase = AgentPort + 1 // 41821 — first per-node NLB listener port
)

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

type discoveredResource struct {
	name      string
	category  string
	isPrivate bool // only meaningful for category "nlb"
}

type discoveredPoolResource struct {
	name          string
	size          int    // from properties.size; 0 if unresolvable (Pulumi interpolation)
	compartmentId string // from properties.compartmentId (for data source lookup)
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

	// Check if already injected (deploy-time __agent_* resources).
	for i := 0; i < len(resourcesNode.Content)-1; i += 2 {
		if len(resourcesNode.Content[i].Value) > 8 && resourcesNode.Content[i].Value[:8] == "__agent_" {
			return yamlBody, nil
		}
	}

	// Check if user-defined agent NSG rule already exists. If so, skip NSG
	// rule injection but STILL inject NLB backends — the user may have a
	// custom NSG rule (e.g., restricting source to NLB subnet) while still
	// needing auto-injected NLB backend sets and listeners.
	userDefinedNSG := hasUserDefinedAgentNSGRule(resourcesNode)

	var nsgs, nlbs, computes []discoveredResource
	var vcns, subnets []discoveredResource
	var pools []discoveredPoolResource

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
				isPriv := false
				if props := findMapValue(resNode, "properties"); props != nil {
					if v := findMapValue(props, "isPrivate"); v != nil {
						isPriv = v.Value == "true"
					}
				}
				nlbs = append(nlbs, discoveredResource{name: resName, category: cat, isPrivate: isPriv})
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
		if typeNode.Value == "oci:Core/instancePool:InstancePool" {
			size := 0
			compartmentId := ""
			if props := findMapValue(resNode, "properties"); props != nil {
				if sv := findMapValue(props, "size"); sv != nil {
					if n, err := fmt.Sscanf(sv.Value, "%d", new(int)); n == 1 && err == nil {
						fmt.Sscanf(sv.Value, "%d", &size)
					}
				}
				if cv := findMapValue(props, "compartmentId"); cv != nil {
					compartmentId = cv.Value
				}
			}
			pools = append(pools, discoveredPoolResource{name: resName, size: size, compartmentId: compartmentId})
		}
	}

	if len(nsgs) == 0 && len(nlbs) == 0 && len(computes) == 0 && len(pools) == 0 {
		return yamlBody, nil
	}

	modified := false

	// NSG injection: skip if user already defined an agent NSG rule (e.g.,
	// restricting Nebula source to NLB subnet instead of 0.0.0.0/0).
	if !userDefinedNSG {
		// When no VCN/subnet resources exist but compute does, try to extract
		// subnetId from the first compute instance's createVnicDetails. Use
		// fn::invoke to resolve the subnet's VCN at deploy time.
		if len(vcns) == 0 && len(subnets) == 0 && len(computes) > 0 && len(nsgs) == 0 && len(nlbs) == 0 {
			subnetRef, compartmentRef := extractSubnetFromCompute(resourcesNode, computes[0].name)
			if subnetRef != "" {
				variablesNode := findMapValue(root, "variables")
				if variablesNode == nil {
					variablesNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
					root.Content = append(root.Content,
						&yaml.Node{Kind: yaml.ScalarNode, Value: "variables"},
						variablesNode,
					)
				}
				addVariable(variablesNode, "__agent_subnet_info", buildSubnetLookupVariable(subnetRef))

				nsgName := "__agent_nsg"
				addResource(resourcesNode, nsgName, buildNSGResourceFromSubnetLookup(compartmentRef))
				ruleName := "__agent_nsg_rule"
				addResource(resourcesNode, ruleName, buildNSGRuleResource(nsgName))
				for _, compute := range computes {
					attachNSGToInstance(resourcesNode, compute.name, nsgName)
				}
				modified = true
			}
		}

		// When no NSG exists but we have a VCN and compute, create one with the agent rule
		if len(nsgs) == 0 && len(vcns) > 0 && len(computes) > 0 {
			vcn := vcns[0]
			compartmentRef := resolveCompartmentId(resourcesNode, vcn.name)
			nsgName := "__agent_nsg"
			addResource(resourcesNode, nsgName, buildNSGResource(compartmentRef, vcn.name))
			ruleName := "__agent_nsg_rule"
			addResource(resourcesNode, ruleName, buildNSGRuleResource(nsgName))
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
	}

	// For each existing public NLB, inject per-node backend sets and listeners.
	// Each compute node gets its own backend set + UDP listener at AgentNLBPortBase+i.
	// Private NLBs (isPrivate: true) are skipped — validate.go emits a warning.
	for _, nlb := range nlbs {
		if nlb.isPrivate {
			continue
		}
		// Find the last user resource that mutates this NLB so we can chain
		// agent backend sets after it. OCI NLB rejects concurrent mutations (409).
		prevNlbResource := nlb.name
		for i := 0; i < len(resourcesNode.Content)-1; i += 2 {
			resName := resourcesNode.Content[i].Value
			resBody := resourcesNode.Content[i+1]
			if resBody.Kind != yaml.MappingNode {
				continue
			}
			typeNode := findMapValue(resBody, "type")
			if typeNode == nil {
				continue
			}
			// Match any NLB child resource (BackendSet, Listener, Backend) that references this NLB
			switch typeNode.Value {
			case "oci:NetworkLoadBalancer/backendSet:BackendSet",
				"oci:NetworkLoadBalancer/listener:Listener",
				"oci:NetworkLoadBalancer/backend:Backend":
				if props := findMapValue(resBody, "properties"); props != nil {
					if nlbId := findMapValue(props, "networkLoadBalancerId"); nlbId != nil {
						if strings.Contains(nlbId.Value, nlb.name) {
							prevNlbResource = resName
						}
					}
				}
			}
		}

		for i, compute := range computes {
			port := AgentNLBPortBase + i
			bsName := fmt.Sprintf("__agent_bs_%s_%d", nlb.name, i)
			lnName := fmt.Sprintf("__agent_ln_%s_%d", nlb.name, i)
			addResource(resourcesNode, bsName, buildNLBBackendSetResourceNWithDep(nlb.name, i, prevNlbResource))
			addResource(resourcesNode, lnName, buildNLBListenerResourceN(nlb.name, bsName, port))
			beName := fmt.Sprintf("__agent_be_%s_%d", nlb.name, i)
			addResource(resourcesNode, beName, buildNLBBackendResource(nlb.name, bsName, compute.name, lnName))
			prevNlbResource = beName // chain next iteration after this backend
			modified = true
		}

		// Pool-as-entity injection: one shared backend set at AgentNLBPortBase.
		// Uses fn::invoke data source to look up pool member instance IDs
		// (InstancePool does not expose actualState.instances in Pulumi YAML).
		for _, pool := range pools {
			if pool.size == 0 {
				continue // dynamic size — cannot pre-configure backends
			}

			// Add a variable to look up pool member instances via data source
			varName := fmt.Sprintf("__agent_pool_%s_instances", pool.name)
			variablesNode := findMapValue(root, "variables")
			if variablesNode == nil {
				variablesNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
				root.Content = append(root.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "variables"},
					variablesNode,
				)
			}
			compartmentRef := pool.compartmentId
			if compartmentRef == "" {
				compartmentRef = "${oci:tenancyOcid}" // fallback
			}
			addVariable(variablesNode, varName, buildMappingNode(map[string]interface{}{
				"fn::invoke": map[string]interface{}{
					"function": "oci:Core/getInstancePoolInstances:getInstancePoolInstances",
					"arguments": map[string]interface{}{
						"compartmentId":  compartmentRef,
						"instancePoolId": fmt.Sprintf("${%s.id}", pool.name),
					},
					"return": "instances",
				},
			}))

			// Per-node: one backend set + listener + backend per pool instance,
			// each on a distinct UDP port (41821, 41822, ...) for per-node targeting.
			portOffset := len(computes) // start after per-compute ports
			for j := 0; j < pool.size; j++ {
				port := AgentNLBPortBase + portOffset + j
				nodeIdx := portOffset + j
				bsName := fmt.Sprintf("__agent_bs_%s_%d", nlb.name, nodeIdx)
				lnName := fmt.Sprintf("__agent_ln_%s_%d", nlb.name, nodeIdx)
				addResource(resourcesNode, bsName, buildNLBBackendSetResourceNWithDep(nlb.name, nodeIdx, prevNlbResource))
				addResource(resourcesNode, lnName, buildNLBListenerResourceN(nlb.name, bsName, port))
				beName := fmt.Sprintf("__agent_be_%s_%d", nlb.name, nodeIdx)
				targetRef := fmt.Sprintf("${%s[%d].instanceId}", varName, j)
				addResource(resourcesNode, beName, buildNLBBackendResourceByTarget(nlb.name, bsName, targetRef, lnName))
				prevNlbResource = beName
			}
			modified = true
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
					"max": AgentNLBPortBase + 32, // 41820-41853: direct + per-node NLB listeners
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
			"backendSetName":        fmt.Sprintf("${%s.name}", bsName),
			"port":                  AgentPort,
			"targetId":              fmt.Sprintf("${%s.id}", computeName),
		},
		"options": map[string]interface{}{
			"dependsOn": []string{fmt.Sprintf("${%s}", prevDep)},
		},
	})
}

// buildNLBBackendSetResourceN creates a per-node backend set.
// nodeIdx >= 0 produces name "agent-backend-set-N"; nodeIdx < 0 produces "agent-backend-set-pool".
func buildNLBBackendSetResourceN(nlbName string, nodeIdx int) *yaml.Node {
	name := fmt.Sprintf("agent-backend-set-%d", nodeIdx)
	if nodeIdx < 0 {
		name = "agent-backend-set-pool"
	}
	return buildMappingNode(map[string]interface{}{
		"type": "oci:NetworkLoadBalancer/backendSet:BackendSet",
		"properties": map[string]interface{}{
			"networkLoadBalancerId": fmt.Sprintf("${%s.id}", nlbName),
			"name":                  name,
			"policy":                "FIVE_TUPLE",
			"healthChecker": map[string]interface{}{
				"protocol": "TCP",
				"port":     22,
			},
			"isPreserveSource": false,
		},
	})
}

// buildNLBBackendSetResourceNWithDep creates a per-node backend set with an explicit dependsOn.
// This ensures the backend set is serialized after the previous NLB operation to avoid 409 Conflict.
func buildNLBBackendSetResourceNWithDep(nlbName string, nodeIdx int, prevDep string) *yaml.Node {
	name := fmt.Sprintf("agent-backend-set-%d", nodeIdx)
	if nodeIdx < 0 {
		name = "agent-backend-set-pool"
	}
	return buildMappingNode(map[string]interface{}{
		"type": "oci:NetworkLoadBalancer/backendSet:BackendSet",
		"properties": map[string]interface{}{
			"networkLoadBalancerId": fmt.Sprintf("${%s.id}", nlbName),
			"name":                 name,
			"policy":               "FIVE_TUPLE",
			"healthChecker": map[string]interface{}{
				"protocol": "TCP",
				"port":     22,
			},
			"isPreserveSource": false,
		},
		"options": map[string]interface{}{
			"dependsOn": []string{fmt.Sprintf("${%s}", prevDep)},
		},
	})
}

// buildNLBListenerResourceN creates a listener on the given UDP port.
func buildNLBListenerResourceN(nlbName, bsName string, port int) *yaml.Node {
	return buildMappingNode(map[string]interface{}{
		"type": "oci:NetworkLoadBalancer/listener:Listener",
		"properties": map[string]interface{}{
			"networkLoadBalancerId":  fmt.Sprintf("${%s.id}", nlbName),
			"name":                   fmt.Sprintf("agent-listener-%d", port),
			"defaultBackendSetName":  fmt.Sprintf("${%s.name}", bsName),
			"port":                   port,
			"protocol":               "UDP",
		},
		"options": map[string]interface{}{
			"dependsOn": []string{fmt.Sprintf("${%s}", bsName)},
		},
	})
}

// buildNLBBackendResourceByTarget creates a backend resource using a raw targetId expression.
func buildNLBBackendResourceByTarget(nlbName, bsName, targetRef, prevDep string) *yaml.Node {
	return buildMappingNode(map[string]interface{}{
		"type": "oci:NetworkLoadBalancer/backend:Backend",
		"properties": map[string]interface{}{
			"networkLoadBalancerId": fmt.Sprintf("${%s.id}", nlbName),
			"backendSetName":        fmt.Sprintf("${%s.name}", bsName),
			"port":                  AgentPort,
			"targetId":              targetRef,
		},
		"options": map[string]interface{}{
			"dependsOn": []string{fmt.Sprintf("${%s}", prevDep)},
		},
	})
}

// extractSubnetFromCompute extracts the subnetId and compartmentId from a
// compute instance's createVnicDetails. Returns empty strings if not found.
// Handles both proper YAML mapping nodes and flow-mapping scalar strings.
func extractSubnetFromCompute(resourcesNode *yaml.Node, instanceName string) (subnetRef, compartmentRef string) {
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

		if cid := findMapValue(props, "compartmentId"); cid != nil {
			compartmentRef = cid.Value
		}

		vnicDetails := findMapValue(props, "createVnicDetails")
		if vnicDetails != nil && vnicDetails.Kind == yaml.ScalarNode {
			vnicDetails = promoteScalarToMapping(props, "createVnicDetails")
		}
		if vnicDetails != nil && vnicDetails.Kind == yaml.MappingNode {
			if sid := findMapValue(vnicDetails, "subnetId"); sid != nil {
				subnetRef = sid.Value
			}
		}
		return
	}
	return
}

func addVariable(variablesNode *yaml.Node, name string, valueNode *yaml.Node) {
	variablesNode.Content = append(variablesNode.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: name},
		valueNode,
	)
}

// buildSubnetLookupVariable creates a Pulumi fn::invoke that looks up a subnet
// to get its vcnId at deploy time.
func buildSubnetLookupVariable(subnetRef string) *yaml.Node {
	return buildMappingNode(map[string]interface{}{
		"fn::invoke": map[string]interface{}{
			"function":  "oci:Core/getSubnet:getSubnet",
			"arguments": map[string]interface{}{"subnetId": subnetRef},
		},
	})
}

func buildNSGResourceFromSubnetLookup(compartmentRef string) *yaml.Node {
	return buildMappingNode(map[string]interface{}{
		"type": "oci:Core/networkSecurityGroup:NetworkSecurityGroup",
		"properties": map[string]interface{}{
			"compartmentId": compartmentRef,
			"vcnId":         "${__agent_subnet_info.vcnId}",
			"displayName":   "pulumi-ui-agent-nsg",
		},
	})
}

func buildNLBResourceFromSubnetRef(compartmentRef, subnetRef string) *yaml.Node {
	return buildMappingNode(map[string]interface{}{
		"type": "oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer",
		"properties": map[string]interface{}{
			"compartmentId":               compartmentRef,
			"subnetId":                    subnetRef,
			"displayName":                 "pulumi-ui-agent-nlb",
			"isPrivate":                   false,
			"isPreserveSourceDestination": false,
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
// If createVnicDetails doesn't exist, it's created. If it exists as a scalar
// (YAML flow-mapping string like "{ subnetId: ..., assignPublicIp: true }"),
// it's promoted to a proper mapping node before modification.
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

		vnicDetails := findMapValue(props, "createVnicDetails")
		if vnicDetails != nil && vnicDetails.Kind == yaml.ScalarNode {
			vnicDetails = promoteScalarToMapping(props, "createVnicDetails")
		}

		if vnicDetails != nil && vnicDetails.Kind == yaml.MappingNode {
			nsgIds := findMapValue(vnicDetails, "nsgIds")
			if nsgIds != nil && nsgIds.Kind == yaml.SequenceNode {
				nsgIds.Content = append(nsgIds.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: nsgRef})
			} else {
				vnicDetails.Content = append(vnicDetails.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: "nsgIds"},
					&yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{
						{Kind: yaml.ScalarNode, Value: nsgRef},
					}},
				)
			}
		} else {
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

// promoteScalarToMapping parses a scalar value like "{ subnetId: x, assignPublicIp: true }"
// as YAML, and replaces the scalar node in-place with the resulting mapping node.
// Pulumi interpolations (${...}) are temporarily quoted so the YAML parser
// can handle them, then restored to unquoted form in the result.
// Returns the new mapping node, or nil if parsing fails.
func promoteScalarToMapping(parent *yaml.Node, key string) *yaml.Node {
	for i := 0; i < len(parent.Content)-1; i += 2 {
		if parent.Content[i].Value != key {
			continue
		}
		scalar := parent.Content[i+1]
		if scalar.Kind != yaml.ScalarNode {
			return scalar
		}
		// Quote ${...} interpolations so they survive YAML flow-mapping parsing.
		safe := pulumiInterpRe.ReplaceAllStringFunc(scalar.Value, func(s string) string {
			return `"` + s + `"`
		})
		var parsed yaml.Node
		if err := yaml.Unmarshal([]byte(safe), &parsed); err != nil {
			return nil
		}
		if parsed.Kind != yaml.DocumentNode || len(parsed.Content) == 0 {
			return nil
		}
		promoted := parsed.Content[0]
		if promoted.Kind != yaml.MappingNode {
			return nil
		}
		parent.Content[i+1] = promoted
		return promoted
	}
	return nil
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

// hasUserDefinedAgentNSGRule scans resources for any NSG security rule
// that opens UDP port 41820 (the Nebula agent port). If found, the user
// has explicitly defined agent networking in the editor and deploy-time
// injection should be skipped.
func hasUserDefinedAgentNSGRule(resourcesNode *yaml.Node) bool {
	const nsgRuleType = "oci:Core/networkSecurityGroupSecurityRule:NetworkSecurityGroupSecurityRule"

	for i := 0; i < len(resourcesNode.Content)-1; i += 2 {
		resNode := resourcesNode.Content[i+1]
		if resNode.Kind != yaml.MappingNode {
			continue
		}
		typeNode := findMapValue(resNode, "type")
		if typeNode == nil || typeNode.Value != nsgRuleType {
			continue
		}
		props := findMapValue(resNode, "properties")
		if props == nil {
			continue
		}
		// Check protocol is UDP ("17")
		proto := findMapValue(props, "protocol")
		if proto == nil || (proto.Value != "17" && proto.Value != `"17"`) {
			continue
		}
		// Check udpOptions.destinationPortRange includes 41820
		udpOpts := findMapValue(props, "udpOptions")
		if udpOpts == nil {
			continue
		}
		portRange := findMapValue(udpOpts, "destinationPortRange")
		if portRange == nil {
			continue
		}
		minNode := findMapValue(portRange, "min")
		maxNode := findMapValue(portRange, "max")
		if minNode == nil || maxNode == nil {
			continue
		}
		if minNode.Value == "41820" || maxNode.Value == "41820" {
			return true
		}
	}
	return false
}
