package oci

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

type PropertySchema struct {
	Type        string                    `json:"type"`
	Required    bool                      `json:"required"`
	Description string                    `json:"description,omitempty"`
	Enum        []string                  `json:"enum,omitempty"`
	Properties  map[string]PropertySchema `json:"properties,omitempty"`
	Items       *PropertySchema           `json:"items,omitempty"`
}

type ResourceSchema struct {
	Description string                    `json:"description,omitempty"`
	Inputs      map[string]PropertySchema `json:"inputs"`
	Outputs     map[string]PropertySchema `json:"outputs,omitempty"`
}

var (
	loadOnce    sync.Once
	schemaMu    sync.RWMutex
	schemaCache map[string]ResourceSchema
	schemaSource string // "live", "cache", or "fallback"
	cacheDir    string // set via SetDataDir before first GetSchema call
)

// SetDataDir tells the schema package where to read/write its on-disk cache
// ($dataDir/oci-schema-cache.json). Call this from main.go before the HTTP
// server starts.
func SetDataDir(dir string) {
	cacheDir = dir
}

// GetSchema returns the OCI resource schema, loading it on the first call.
// Load order:
//  1. On-disk cache ($DATA_DIR/oci-schema-cache.json) — fast, survives restarts.
//     If cache hit: serve immediately and refresh in background.
//  2. Live fetch via `pulumi schema get oci` (tries PATH + common install dirs).
//     If live fetch succeeds: write to disk cache for next startup.
//  3. Hardcoded fallback covering the resource types used by the standard programs.
// GetSchema returns the current schema map.
func GetSchema() map[string]ResourceSchema {
	loadOnce.Do(func() {
		s, src := loadSchema()
		schemaMu.Lock()
		schemaCache = s
		schemaSource = src
		schemaMu.Unlock()
	})
	schemaMu.RLock()
	defer schemaMu.RUnlock()
	return schemaCache
}

// GetSchemaSource returns how the current schema was loaded:
// "live" (from provider), "cache" (from disk), or "fallback" (hardcoded).
func GetSchemaSource() string {
	schemaMu.RLock()
	defer schemaMu.RUnlock()
	if schemaSource == "" {
		return "fallback"
	}
	return schemaSource
}

// RefreshSchema forces a live re-fetch and updates the in-memory cache and
// the on-disk cache. Returns the newly loaded schema (or the existing one if
// re-fetch fails).
func RefreshSchema() map[string]ResourceSchema {
	log.Println("[oci-schema] manual refresh requested")
	s := fetchLiveSchema()
	if s == nil {
		log.Println("[oci-schema] refresh: live fetch failed, keeping existing schema")
		schemaMu.RLock()
		defer schemaMu.RUnlock()
		return schemaCache
	}
	log.Printf("[oci-schema] refresh: loaded %d resource types from live schema", len(s))
	mergeEnumsFromFallback(s)
	saveCache(s)
	schemaMu.Lock()
	schemaCache = s
	schemaSource = "live"
	schemaMu.Unlock()
	return s
}

// SchemaHandler serves GET /api/oci-schema — no auth required.
func SchemaHandler(w http.ResponseWriter, r *http.Request) {
	s := GetSchema()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"resources": s,
		"count":     len(s),
		"source":    GetSchemaSource(),
	})
}

// SchemaRefreshHandler serves POST /api/oci-schema/refresh — forces a live
// re-fetch and returns the updated schema. Useful after a provider upgrade.
func SchemaRefreshHandler(w http.ResponseWriter, r *http.Request) {
	s := RefreshSchema()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"resources": s,
		"count":     len(s),
		"source":    GetSchemaSource(),
	})
}

// ── Internal ──────────────────────────────────────────────────────────────────

func loadSchema() (map[string]ResourceSchema, string) {
	// 1. Disk cache — fastest path, works across restarts without re-running pulumi.
	if s := loadCache(); len(s) > 0 {
		log.Printf("[oci-schema] loaded %d resource types from disk cache", len(s))
		mergeEnumsFromFallback(s)
		// Refresh in background so the cache stays warm after provider upgrades.
		go func() {
			if s := fetchLiveSchema(); len(s) > 0 {
				log.Printf("[oci-schema] background refresh: updated to %d resource types", len(s))
				mergeEnumsFromFallback(s)
				saveCache(s)
				schemaMu.Lock()
				schemaCache = s
				schemaSource = "live"
				schemaMu.Unlock()
			}
		}()
		return s, "cache"
	}

	// 2. Live fetch — blocks startup briefly but gives the full provider schema.
	if s := fetchLiveSchema(); len(s) > 0 {
		mergeEnumsFromFallback(s)
		saveCache(s)
		return s, "live"
	}

	// 3. Hardcoded fallback.
	fb := fallbackSchema()
	log.Printf("[oci-schema] using hardcoded fallback (%d resource types)", len(fb))
	return fb, "fallback"
}

// mergeEnumsFromFallback copies Enum values from the fallback schema into a
// live/cached schema. The live Pulumi schema doesn't expose enum constraints
// on individual properties, so we overlay them from our curated fallback.
func mergeEnumsFromFallback(schema map[string]ResourceSchema) {
	fb := fallbackSchema()
	for resType, fbRes := range fb {
		liveRes, ok := schema[resType]
		if !ok {
			continue
		}
		mergeEnumsIntoProps(liveRes.Inputs, fbRes.Inputs)
		schema[resType] = liveRes
	}
}

func mergeEnumsIntoProps(live, fallback map[string]PropertySchema) {
	for key, fbProp := range fallback {
		if len(fbProp.Enum) == 0 {
			continue
		}
		if liveProp, ok := live[key]; ok && len(liveProp.Enum) == 0 {
			liveProp.Enum = fbProp.Enum
			live[key] = liveProp
		}
		// Recurse into nested properties (e.g., healthChecker.protocol)
		if fbProp.Properties != nil {
			if liveProp, ok := live[key]; ok && liveProp.Properties != nil {
				mergeEnumsIntoProps(liveProp.Properties, fbProp.Properties)
				live[key] = liveProp
			}
		}
	}
}

// fetchLiveSchema tries to get the schema from the installed Pulumi OCI provider.
// Returns nil if all attempts fail.
//
// Pulumi v3.x uses `pulumi package get-schema oci` (new CLI shape).
// Older versions used `pulumi schema get oci`. Both are tried in order.
func fetchLiveSchema() map[string]ResourceSchema {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Each entry is {binary, args...}
	type candidate struct {
		bin  string
		args []string
	}
	var tries []candidate
	for _, bin := range pulumiBinCandidates() {
		tries = append(tries,
			candidate{bin, []string{"package", "get-schema", "oci"}}, // v3.x
			candidate{bin, []string{"schema", "get", "oci"}},         // legacy
		)
	}

	for _, c := range tries {
		out, err := exec.CommandContext(ctx, c.bin, c.args...).Output()
		if err != nil {
			continue
		}
		s := parseSchema(out)
		if len(s) > 0 {
			log.Printf("[oci-schema] loaded %d resource types via `%s %s`", len(s), c.bin, c.args[0]+" "+c.args[1]+" "+c.args[2])
			return s
		}
	}
	return nil
}

// pulumiBinCandidates returns candidate Pulumi binary paths to try, starting
// with the one in PATH (the common case when pulumi is properly installed).
func pulumiBinCandidates() []string {
	candidates := []string{"pulumi"} // PATH lookup first
	if home, err := os.UserHomeDir(); err == nil {
		// ~/.pulumi/bin/pulumi is the default install location for `curl -sSfL
		// https://get.pulumi.com | sh` and the Pulumi installer
		candidates = append(candidates, filepath.Join(home, ".pulumi", "bin", "pulumi"))
	}
	candidates = append(candidates,
		"/usr/local/bin/pulumi",
		"/usr/bin/pulumi",
		"/opt/pulumi/pulumi",
	)
	return candidates
}

func cachePath() string {
	if cacheDir == "" {
		return ""
	}
	return filepath.Join(cacheDir, "oci-schema-cache.json")
}

func loadCache() map[string]ResourceSchema {
	p := cachePath()
	if p == "" {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var m map[string]ResourceSchema
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return m
}

func saveCache(s map[string]ResourceSchema) {
	p := cachePath()
	if p == "" {
		return
	}
	data, err := json.Marshal(s)
	if err != nil {
		return
	}
	if err := os.WriteFile(p, data, 0644); err != nil {
		log.Printf("[oci-schema] could not write cache to %s: %v", p, err)
	}
}

// parseSchema converts the raw JSON output of `pulumi schema get oci` into
// the ResourceSchema map used by the rest of the application.
// It resolves $ref pointers into the top-level "types" section to populate
// sub-field schemas for object/array-of-object properties (up to 3 levels).
func parseSchema(data []byte) map[string]ResourceSchema {
	var raw struct {
		Resources map[string]struct {
			Description     string                   `json:"description"`
			InputProperties map[string]rawProperty    `json:"inputProperties"`
			OutputProperties map[string]rawProperty   `json:"properties"`
			RequiredInputs  []string                  `json:"requiredInputs"`
		} `json:"resources"`
		Types map[string]rawTypeDef `json:"types"`
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
			inputs[pk] = resolveProperty(pv, required[pk], raw.Types, 0)
		}
		outputs := make(map[string]PropertySchema, len(v.OutputProperties))
		for pk, pv := range v.OutputProperties {
			outputs[pk] = resolveProperty(pv, false, raw.Types, 0)
		}
		result[k] = ResourceSchema{
			Description: v.Description,
			Inputs:      inputs,
			Outputs:     outputs,
		}
	}
	return result
}

// rawProperty mirrors the JSON shape of a single property in the Pulumi schema.
type rawProperty struct {
	Type        string       `json:"type"`
	Ref         string       `json:"$ref"`
	Description string       `json:"description"`
	Items       *rawProperty `json:"items"`
}

// rawTypeDef mirrors a named type in the top-level "types" section.
type rawTypeDef struct {
	Type        string                  `json:"type"`
	Properties  map[string]rawProperty  `json:"properties"`
	Required    []string                `json:"required"`
	Description string                  `json:"description"`
}

const maxRefDepth = 3

// resolveProperty converts a raw Pulumi schema property into our PropertySchema,
// following $ref pointers into the types map for object sub-fields.
func resolveProperty(rp rawProperty, isRequired bool, types map[string]rawTypeDef, depth int) PropertySchema {
	ps := PropertySchema{
		Type:        rp.Type,
		Required:    isRequired,
		Description: rp.Description,
	}

	// If there's a $ref, resolve it from the types map
	if rp.Ref != "" && depth < maxRefDepth {
		token := refToToken(rp.Ref)
		if td, ok := types[token]; ok {
			if ps.Type == "" {
				ps.Type = "object"
			}
			ps.Properties = resolveTypeDef(td, types, depth+1)
		}
	}

	// For arrays, resolve the element schema
	if rp.Items != nil && depth < maxRefDepth {
		if ps.Type == "" {
			ps.Type = "array"
		}
		itemSchema := resolveProperty(*rp.Items, false, types, depth+1)
		ps.Items = &itemSchema
	}

	// Default type to "string" if still empty
	if ps.Type == "" {
		ps.Type = "string"
	}

	return ps
}

// resolveTypeDef converts a rawTypeDef into a map of sub-field PropertySchemas.
func resolveTypeDef(td rawTypeDef, types map[string]rawTypeDef, depth int) map[string]PropertySchema {
	if len(td.Properties) == 0 {
		return nil
	}
	reqSet := make(map[string]bool, len(td.Required))
	for _, r := range td.Required {
		reqSet[r] = true
	}
	result := make(map[string]PropertySchema, len(td.Properties))
	for pk, pv := range td.Properties {
		result[pk] = resolveProperty(pv, reqSet[pk], types, depth)
	}
	return result
}

// refToToken extracts the type token from a $ref string.
// e.g. "#/types/oci:Core/InstanceCreateVnicDetails:InstanceCreateVnicDetails"
// → "oci:Core/InstanceCreateVnicDetails:InstanceCreateVnicDetails"
func refToToken(ref string) string {
	const prefix = "#/types/"
	if len(ref) > len(prefix) {
		return ref[len(prefix):]
	}
	if ref == prefix {
		return ""
	}
	return ref
}

// strings package not needed; using prefix slicing directly

// fallbackSchema is the last-resort schema used when neither the disk cache
// nor a live `pulumi schema get oci` fetch is available.
// It covers the resource types used by the standard nomad-cluster programs.
func fallbackSchema() map[string]ResourceSchema {
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
				"dhcpOptionsId":          {Type: "string", Required: false},
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
		"oci:Core/natGateway:NatGateway": {
			Description: "NAT Gateway for private subnet outbound traffic",
			Inputs: map[string]PropertySchema{
				"compartmentId": {Type: "string", Required: true},
				"vcnId":         {Type: "string", Required: true},
				"displayName":   {Type: "string", Required: false},
				"blockTraffic":  {Type: "boolean", Required: false},
			},
		},
		"oci:Core/routeTable:RouteTable": {
			Description: "Route table for a VCN",
			Inputs: map[string]PropertySchema{
				"compartmentId": {Type: "string", Required: true},
				"vcnId":         {Type: "string", Required: true},
				"routeRules": {Type: "array", Required: false, Items: &PropertySchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"destination":    {Type: "string", Required: true, Description: "CIDR block for the route rule destination"},
					"networkEntityId": {Type: "string", Required: true, Description: "OCID of the target (IGW, NAT, DRG, etc.)"},
					"description":    {Type: "string", Required: false},
				},
			}},
				"displayName":   {Type: "string", Required: false},
			},
		},
		"oci:Core/securityList:SecurityList": {
			Description: "Security list (stateful firewall rules) for a VCN",
			Inputs: map[string]PropertySchema{
				"compartmentId":        {Type: "string", Required: true},
				"vcnId":                {Type: "string", Required: true},
				"displayName":          {Type: "string", Required: false},
				"egressSecurityRules": {Type: "array", Required: false, Items: &PropertySchema{
					Type: "object",
					Properties: map[string]PropertySchema{
						"protocol":        {Type: "string", Required: true, Description: "6=TCP, 17=UDP, 1=ICMP, all", Enum: []string{"6", "17", "1", "all"}},
						"destination":     {Type: "string", Required: true, Description: "Destination CIDR"},
						"destinationType": {Type: "string", Required: false, Enum: []string{"CIDR_BLOCK", "SERVICE_CIDR_BLOCK"}},
						"description":     {Type: "string", Required: false},
						"stateless":       {Type: "boolean", Required: false},
						"tcpOptions":      {Type: "object", Required: false, Properties: map[string]PropertySchema{
							"min": {Type: "integer", Required: false},
							"max": {Type: "integer", Required: false},
						}},
						"udpOptions":      {Type: "object", Required: false, Properties: map[string]PropertySchema{
							"min": {Type: "integer", Required: false},
							"max": {Type: "integer", Required: false},
						}},
					},
				}},
				"ingressSecurityRules": {Type: "array", Required: false, Items: &PropertySchema{
					Type: "object",
					Properties: map[string]PropertySchema{
						"protocol":   {Type: "string", Required: true, Description: "6=TCP, 17=UDP, 1=ICMP, all", Enum: []string{"6", "17", "1", "all"}},
						"source":     {Type: "string", Required: true, Description: "Source CIDR"},
						"sourceType": {Type: "string", Required: false, Enum: []string{"CIDR_BLOCK", "SERVICE_CIDR_BLOCK"}},
						"description": {Type: "string", Required: false},
						"stateless":  {Type: "boolean", Required: false},
						"tcpOptions": {Type: "object", Required: false, Properties: map[string]PropertySchema{
							"min": {Type: "integer", Required: false},
							"max": {Type: "integer", Required: false},
						}},
						"udpOptions": {Type: "object", Required: false, Properties: map[string]PropertySchema{
							"min": {Type: "integer", Required: false},
							"max": {Type: "integer", Required: false},
						}},
					},
				}},
			},
		},
		"oci:Core/networkSecurityGroup:NetworkSecurityGroup": {
			Description: "Network Security Group (NSG)",
			Inputs: map[string]PropertySchema{
				"compartmentId": {Type: "string", Required: true},
				"vcnId":         {Type: "string", Required: true},
				"displayName":   {Type: "string", Required: false},
			},
		},
		"oci:Core/networkSecurityGroupSecurityRule:NetworkSecurityGroupSecurityRule": {
			Description: "Security rule within an NSG",
			Inputs: map[string]PropertySchema{
				"networkSecurityGroupId": {Type: "string", Required: true},
				"direction":              {Type: "string", Required: true, Description: "INGRESS or EGRESS", Enum: []string{"INGRESS", "EGRESS"}},
				"protocol":               {Type: "string", Required: true, Description: "6=TCP, 17=UDP, 1=ICMP, all", Enum: []string{"6", "17", "1", "all"}},
				"source":                 {Type: "string", Required: false},
				"sourceType":             {Type: "string", Required: false, Enum: []string{"CIDR_BLOCK", "SERVICE_CIDR_BLOCK", "NETWORK_SECURITY_GROUP"}},
				"destination":            {Type: "string", Required: false},
				"destinationType":        {Type: "string", Required: false, Enum: []string{"CIDR_BLOCK", "SERVICE_CIDR_BLOCK", "NETWORK_SECURITY_GROUP"}},
				"description":            {Type: "string", Required: false},
				"tcpOptions": {Type: "object", Required: false, Properties: map[string]PropertySchema{
					"destinationPortRange": {Type: "object", Required: false, Properties: map[string]PropertySchema{
						"min": {Type: "integer", Required: true, Description: "Lower bound of the port range"},
						"max": {Type: "integer", Required: true, Description: "Upper bound of the port range"},
					}},
				}},
				"udpOptions": {Type: "object", Required: false, Properties: map[string]PropertySchema{
					"destinationPortRange": {Type: "object", Required: false, Properties: map[string]PropertySchema{
						"min": {Type: "integer", Required: true, Description: "Lower bound of the port range"},
						"max": {Type: "integer", Required: true, Description: "Upper bound of the port range"},
					}},
				}},
			},
		},
		"oci:Core/instance:Instance": {
			Description: "Compute instance",
			Inputs: map[string]PropertySchema{
				"compartmentId":      {Type: "string", Required: true},
				"availabilityDomain": {Type: "string", Required: true},
				"shape":              {Type: "string", Required: true},
				"displayName":        {Type: "string", Required: false},
				"sourceDetails": {Type: "object", Required: true, Description: "Boot volume source", Properties: map[string]PropertySchema{
					"sourceType":        {Type: "string", Required: true, Description: "image or bootVolume", Enum: []string{"image", "bootVolume"}},
					"imageId":           {Type: "string", Required: false, Description: "OCID of the image"},
					"bootVolumeSizeInGbs": {Type: "string", Required: false, Description: "Size of the boot volume in GB"},
				}},
				"createVnicDetails": {Type: "object", Required: false, Properties: map[string]PropertySchema{
					"subnetId":        {Type: "string", Required: false, Description: "OCID of the subnet"},
					"assignPublicIp":  {Type: "boolean", Required: false, Description: "Whether to assign a public IP"},
					"nsgIds":          {Type: "array", Required: false, Description: "List of NSG OCIDs"},
					"displayName":     {Type: "string", Required: false},
					"hostnameLabel":   {Type: "string", Required: false},
				}},
				"metadata": {Type: "object", Required: false, Description: "cloud-init userdata etc."},
				"shapeConfig": {Type: "object", Required: false, Description: "Flex shape config (ocpus, memoryInGbs)", Properties: map[string]PropertySchema{
					"ocpus":        {Type: "number", Required: false, Description: "Number of OCPUs"},
					"memoryInGbs":  {Type: "number", Required: false, Description: "Total memory in GB"},
				}},
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
				"attachmentType": {Type: "string", Required: true, Description: "paravirtualized or iscsi", Enum: []string{"paravirtualized", "iscsi"}},
				"instanceId":     {Type: "string", Required: true},
				"volumeId":       {Type: "string", Required: true},
				"displayName":    {Type: "string", Required: false},
			},
		},
		"oci:Core/volumeBackupPolicy:VolumeBackupPolicy": {
			Description: "Backup policy for block volumes",
			Inputs: map[string]PropertySchema{
				"compartmentId": {Type: "string", Required: true},
				"displayName":   {Type: "string", Required: false},
				"schedules":     {Type: "array", Required: false, Description: "Array of backup schedule objects"},
			},
		},
		"oci:Core/volumeBackupPolicyAssignment:VolumeBackupPolicyAssignment": {
			Description: "Assigns a backup policy to a volume",
			Inputs: map[string]PropertySchema{
				"assetId":  {Type: "string", Required: true, Description: "Volume OCID to protect"},
				"policyId": {Type: "string", Required: true},
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
		"oci:Identity/dynamicGroup:DynamicGroup": {
			Description: "IAM dynamic group matched by instance attributes",
			Inputs: map[string]PropertySchema{
				"compartmentId": {Type: "string", Required: true, Description: "Tenancy OCID (dynamic groups are tenancy-scoped)"},
				"name":          {Type: "string", Required: true},
				"description":   {Type: "string", Required: true},
				"matchingRule":  {Type: "string", Required: true, Description: "e.g. ALL {instance.compartment.id = 'ocid...'}"},
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
				"networkSecurityGroupIds":     {Type: "array", Required: false},
			},
		},
		"oci:NetworkLoadBalancer/listener:Listener": {
			Description: "NLB listener",
			Inputs: map[string]PropertySchema{
				"networkLoadBalancerId": {Type: "string", Required: true},
				"name":                  {Type: "string", Required: true},
				"defaultBackendSetName": {Type: "string", Required: true},
				"port":                  {Type: "integer", Required: true},
				"protocol":              {Type: "string", Required: true, Description: "TCP or UDP", Enum: []string{"TCP", "UDP"}},
			},
		},
		"oci:NetworkLoadBalancer/backendSet:BackendSet": {
			Description: "NLB backend set",
			Inputs: map[string]PropertySchema{
				"networkLoadBalancerId": {Type: "string", Required: true},
				"name":                  {Type: "string", Required: true},
				"policy":                {Type: "string", Required: true, Description: "FIVE_TUPLE, THREE_TUPLE, TWO_TUPLE", Enum: []string{"FIVE_TUPLE", "THREE_TUPLE", "TWO_TUPLE"}},
				"healthChecker": {Type: "object", Required: true, Properties: map[string]PropertySchema{
					"protocol":          {Type: "string", Required: true, Description: "TCP, UDP, or HTTP", Enum: []string{"TCP", "UDP", "HTTP"}},
					"port":              {Type: "integer", Required: true, Description: "Health check port"},
					"urlPath":           {Type: "string", Required: false, Description: "URL path for HTTP health checks"},
					"returnCode":        {Type: "integer", Required: false, Description: "Expected HTTP return code"},
					"intervalInMillis":  {Type: "integer", Required: false, Description: "Interval between checks in ms"},
					"timeoutInMillis":   {Type: "integer", Required: false, Description: "Timeout per check in ms"},
					"retries":           {Type: "integer", Required: false, Description: "Number of retries before marking unhealthy"},
				}},
				"isPreserveSource":      {Type: "boolean", Required: false},
			},
		},
		"oci:NetworkLoadBalancer/backend:Backend": {
			Description: "NLB backend (individual server in a backend set)",
			Inputs: map[string]PropertySchema{
				"networkLoadBalancerId": {Type: "string", Required: true},
				"backendSetName":        {Type: "string", Required: true},
				"port":                  {Type: "integer", Required: true},
				"ipAddress":             {Type: "string", Required: false, Description: "IP address (alternative to targetId)"},
				"targetId":              {Type: "string", Required: false, Description: "Instance OCID for direct routing"},
				"weight":                {Type: "integer", Required: false},
			},
		},
		"oci:Core/instanceConfiguration:InstanceConfiguration": {
			Description: "InstanceConfiguration — template used by an InstancePool to launch homogeneous instances",
			Inputs: map[string]PropertySchema{
				"compartmentId":   {Type: "string", Required: true},
				"displayName":     {Type: "string", Required: false},
				"instanceDetails": {Type: "object", Required: false, Description: "Launch details (instanceType, launchDetails)", Properties: map[string]PropertySchema{
					"instanceType":  {Type: "string", Required: true, Description: "compute or instanceOptions", Enum: []string{"compute", "instanceOptions"}},
					"launchDetails": {Type: "object", Required: false, Description: "Launch configuration for the instances"},
				}},
			},
		},
		"oci:Core/instancePool:InstancePool": {
			Description: "InstancePool — manages N identical instances from an InstanceConfiguration",
			Inputs: map[string]PropertySchema{
				"compartmentId":           {Type: "string", Required: true},
				"instanceConfigurationId": {Type: "string", Required: true},
				"size":                    {Type: "integer", Required: true, Description: "Number of instances in the pool"},
				"displayName":             {Type: "string", Required: false},
				"placementConfigurations": {Type: "array", Required: true, Description: "Array of {availabilityDomain, primarySubnetId}", Items: &PropertySchema{
					Type: "object",
					Properties: map[string]PropertySchema{
						"availabilityDomain": {Type: "string", Required: true, Description: "AD for the pool instances"},
						"primarySubnetId":    {Type: "string", Required: true, Description: "OCID of the primary subnet"},
					},
				}},
			},
		},
	}
}
