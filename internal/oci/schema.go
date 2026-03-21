package oci

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"sync"
)

type PropertySchema struct {
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
}

type ResourceSchema struct {
	Description string                    `json:"description,omitempty"`
	Inputs      map[string]PropertySchema `json:"inputs"`
}

var (
	schemaOnce  sync.Once
	schemaCache map[string]ResourceSchema
)

// GetSchema loads and caches the OCI resource schema.
// It tries `pulumi schema get oci` first; on failure returns the hardcoded fallback.
func GetSchema() map[string]ResourceSchema {
	schemaOnce.Do(func() {
		schemaCache = loadSchema()
	})
	return schemaCache
}

func loadSchema() map[string]ResourceSchema {
	out, err := exec.Command("pulumi", "schema", "get", "oci").Output()
	if err == nil {
		result := parseSchema(out)
		if len(result) > 0 {
			return result
		}
	}
	return fallbackSchema()
}

func parseSchema(data []byte) map[string]ResourceSchema {
	var raw struct {
		Resources map[string]struct {
			Description     string `json:"description"`
			InputProperties map[string]struct {
				Type        string `json:"type"`
				Description string `json:"description"`
			} `json:"inputProperties"`
			RequiredInputs []string `json:"requiredInputs"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	result := make(map[string]ResourceSchema, len(raw.Resources))
	for k, v := range raw.Resources {
		required := make(map[string]bool, len(v.RequiredInputs))
		for _, r := range v.RequiredInputs {
			required[r] = true
		}
		inputs := make(map[string]PropertySchema, len(v.InputProperties))
		for pk, pv := range v.InputProperties {
			t := pv.Type
			if t == "" {
				t = "string"
			}
			inputs[pk] = PropertySchema{
				Type:        t,
				Required:    required[pk],
				Description: pv.Description,
			}
		}
		result[k] = ResourceSchema{
			Description: v.Description,
			Inputs:      inputs,
		}
	}
	return result
}

// SchemaHandler serves GET /api/oci-schema — no auth required.
func SchemaHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"resources": GetSchema(),
	})
}

func fallbackSchema() map[string]ResourceSchema {
	// Common OCI resource types used in Pulumi programs.
	return map[string]ResourceSchema{
		"oci:Core/vcn:Vcn": {
			Description: "Virtual Cloud Network",
			Inputs: map[string]PropertySchema{
				"compartmentId": {Type: "string", Required: true, Description: "OCID of the compartment"},
				"cidrBlock":     {Type: "string", Required: false, Description: "CIDR block (deprecated; use cidrBlocks)"},
				"cidrBlocks":    {Type: "array", Required: false, Description: "List of CIDR blocks"},
				"displayName":   {Type: "string", Required: false, Description: "Display name"},
				"dnsLabel":      {Type: "string", Required: false, Description: "DNS label (no hyphens, ≤15 chars)"},
			},
		},
		"oci:Core/subnet:Subnet": {
			Description: "Subnet within a VCN",
			Inputs: map[string]PropertySchema{
				"compartmentId":          {Type: "string", Required: true},
				"vcnId":                  {Type: "string", Required: true},
				"cidrBlock":              {Type: "string", Required: true},
				"displayName":            {Type: "string", Required: false},
				"dnsLabel":               {Type: "string", Required: false},
				"routeTableId":           {Type: "string", Required: false},
				"securityListIds":        {Type: "array", Required: false},
				"prohibitPublicIpOnVnic": {Type: "boolean", Required: false},
			},
		},
		"oci:Core/internetGateway:InternetGateway": {
			Description: "Internet Gateway for a VCN",
			Inputs: map[string]PropertySchema{
				"compartmentId": {Type: "string", Required: true},
				"vcnId":         {Type: "string", Required: true},
				"displayName":   {Type: "string", Required: false},
				"enabled":       {Type: "boolean", Required: false},
			},
		},
		"oci:Core/routeTable:RouteTable": {
			Description: "Route table for a VCN",
			Inputs: map[string]PropertySchema{
				"compartmentId": {Type: "string", Required: true},
				"vcnId":         {Type: "string", Required: true},
				"routeRules":    {Type: "array", Required: false},
				"displayName":   {Type: "string", Required: false},
			},
		},
		"oci:Core/securityList:SecurityList": {
			Description: "Security list (stateful firewall rules) for a VCN",
			Inputs: map[string]PropertySchema{
				"compartmentId":        {Type: "string", Required: true},
				"vcnId":                {Type: "string", Required: true},
				"displayName":          {Type: "string", Required: false},
				"egressSecurityRules":  {Type: "array", Required: false},
				"ingressSecurityRules": {Type: "array", Required: false},
			},
		},
		"oci:Core/instance:Instance": {
			Description: "Compute instance",
			Inputs: map[string]PropertySchema{
				"compartmentId":      {Type: "string", Required: true},
				"availabilityDomain": {Type: "string", Required: true},
				"shape":              {Type: "string", Required: true},
				"displayName":        {Type: "string", Required: false},
				"sourceDetails":      {Type: "object", Required: true, Description: "Boot volume source"},
				"createVnicDetails":  {Type: "object", Required: false},
				"metadata":           {Type: "object", Required: false, Description: "cloud-init userdata etc."},
				"shapeConfig":        {Type: "object", Required: false, Description: "Flex shape config (ocpus, memoryInGbs)"},
			},
		},
		"oci:Core/volume:Volume": {
			Description: "Block storage volume",
			Inputs: map[string]PropertySchema{
				"compartmentId":      {Type: "string", Required: true},
				"availabilityDomain": {Type: "string", Required: true},
				"displayName":        {Type: "string", Required: false},
				"sizeInGbs":          {Type: "string", Required: false},
			},
		},
		"oci:Core/volumeAttachment:VolumeAttachment": {
			Description: "Attaches a block volume to an instance",
			Inputs: map[string]PropertySchema{
				"attachmentType": {Type: "string", Required: true, Description: "paravirtualized or iscsi"},
				"instanceId":     {Type: "string", Required: true},
				"volumeId":       {Type: "string", Required: true},
				"displayName":    {Type: "string", Required: false},
			},
		},
		"oci:Identity/compartment:Compartment": {
			Description: "Identity compartment",
			Inputs: map[string]PropertySchema{
				"compartmentId": {Type: "string", Required: true, Description: "Parent compartment (tenancy OCID for root)"},
				"name":          {Type: "string", Required: true},
				"description":   {Type: "string", Required: true},
				"enableDelete":  {Type: "boolean", Required: false},
			},
		},
		"oci:Identity/policy:Policy": {
			Description: "IAM policy",
			Inputs: map[string]PropertySchema{
				"compartmentId": {Type: "string", Required: true},
				"name":          {Type: "string", Required: true},
				"description":   {Type: "string", Required: true},
				"statements":    {Type: "array", Required: true},
			},
		},
		"oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer": {
			Description: "Network Load Balancer",
			Inputs: map[string]PropertySchema{
				"compartmentId":               {Type: "string", Required: true},
				"displayName":                 {Type: "string", Required: true},
				"subnetId":                    {Type: "string", Required: true},
				"isPrivate":                   {Type: "boolean", Required: false},
				"isPreserveSourceDestination": {Type: "boolean", Required: false},
			},
		},
		"oci:NetworkLoadBalancer/listener:Listener": {
			Description: "NLB listener",
			Inputs: map[string]PropertySchema{
				"networkLoadBalancerId":  {Type: "string", Required: true},
				"name":                   {Type: "string", Required: true},
				"defaultBackendSetName":  {Type: "string", Required: true},
				"port":                   {Type: "integer", Required: true},
				"protocol":               {Type: "string", Required: true, Description: "TCP or UDP"},
			},
		},
		"oci:NetworkLoadBalancer/backendSet:BackendSet": {
			Description: "NLB backend set",
			Inputs: map[string]PropertySchema{
				"networkLoadBalancerId": {Type: "string", Required: true},
				"name":                 {Type: "string", Required: true},
				"policy":               {Type: "string", Required: true, Description: "FIVE_TUPLE, THREE_TUPLE, TWO_TUPLE"},
				"healthChecker":        {Type: "object", Required: true},
			},
		},
		"oci:NetworkLoadBalancer/backend:Backend": {
			Description: "NLB backend (individual server in a backend set)",
			Inputs: map[string]PropertySchema{
				"networkLoadBalancerId": {Type: "string", Required: true},
				"backendSetName":        {Type: "string", Required: true},
				"port":                  {Type: "integer", Required: true},
				"ipAddress":             {Type: "string", Required: true},
				"weight":                {Type: "integer", Required: false},
			},
		},
	}
}
