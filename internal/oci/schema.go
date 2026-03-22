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
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
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
		// Refresh in background so the cache stays warm after provider upgrades.
		go func() {
			if s := fetchLiveSchema(); len(s) > 0 {
				log.Printf("[oci-schema] background refresh: updated to %d resource types", len(s))
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
		saveCache(s)
		return s, "live"
	}

	// 3. Hardcoded fallback.
	fb := fallbackSchema()
	log.Printf("[oci-schema] using hardcoded fallback (%d resource types)", len(fb))
	return fb, "fallback"
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
func parseSchema(data []byte) map[string]ResourceSchema {
	var raw struct {
		Resources map[string]struct {
			Description     string `json:"description"`
			InputProperties map[string]struct {
				Type        string `json:"type"`
				Description string `json:"description"`
			} `json:"inputProperties"`
			// "properties" in Pulumi schema = output attributes (what the resource exposes
			// after creation — includes id, echoed inputs, and computed values like
			// defaultDhcpOptionsId, ipAddresses, etc.)
			OutputProperties map[string]struct {
				Type        string `json:"type"`
				Description string `json:"description"`
			} `json:"properties"`
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
		outputs := make(map[string]PropertySchema, len(v.OutputProperties))
		for pk, pv := range v.OutputProperties {
			t := pv.Type
			if t == "" {
				t = "string"
			}
			outputs[pk] = PropertySchema{
				Type:        t,
				Description: pv.Description,
			}
		}
		result[k] = ResourceSchema{
			Description: v.Description,
			Inputs:      inputs,
			Outputs:     outputs,
		}
	}
	return result
}

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
				"direction":              {Type: "string", Required: true, Description: "INGRESS or EGRESS"},
				"protocol":               {Type: "string", Required: true, Description: "6=TCP, 17=UDP, all"},
				"source":                 {Type: "string", Required: false},
				"sourceType":             {Type: "string", Required: false},
				"destination":            {Type: "string", Required: false},
				"destinationType":        {Type: "string", Required: false},
				"description":            {Type: "string", Required: false},
				"tcpOptions":             {Type: "object", Required: false},
				"udpOptions":             {Type: "object", Required: false},
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
				"protocol":              {Type: "string", Required: true, Description: "TCP or UDP"},
			},
		},
		"oci:NetworkLoadBalancer/backendSet:BackendSet": {
			Description: "NLB backend set",
			Inputs: map[string]PropertySchema{
				"networkLoadBalancerId": {Type: "string", Required: true},
				"name":                  {Type: "string", Required: true},
				"policy":                {Type: "string", Required: true, Description: "FIVE_TUPLE, THREE_TUPLE, TWO_TUPLE"},
				"healthChecker":         {Type: "object", Required: true},
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
				"instanceDetails": {Type: "object", Required: false, Description: "Launch details (instanceType, launchDetails)"},
			},
		},
		"oci:Core/instancePool:InstancePool": {
			Description: "InstancePool — manages N identical instances from an InstanceConfiguration",
			Inputs: map[string]PropertySchema{
				"compartmentId":           {Type: "string", Required: true},
				"instanceConfigurationId": {Type: "string", Required: true},
				"size":                    {Type: "integer", Required: true, Description: "Number of instances in the pool"},
				"displayName":             {Type: "string", Required: false},
				"placementConfigurations": {Type: "array", Required: true, Description: "Array of {availabilityDomain, primarySubnetId}"},
			},
		},
	}
}
