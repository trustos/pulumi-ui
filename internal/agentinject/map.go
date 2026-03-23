package agentinject

// UserDataPath describes where user_data is found within a Pulumi resource's
// properties. PropertyPath is the sequence of nested YAML/JSON keys leading to
// the user_data value.
type UserDataPath struct {
	PropertyPath []string
}

// ComputeResources maps Pulumi resource type tokens to their user_data
// property paths. The engine uses this map to detect which resources need
// agent bootstrap injection.
//
// To add a new cloud provider, add entries here. The multipart MIME
// composition and agent bootstrap script are provider-agnostic.
var ComputeResources = map[string]UserDataPath{
	// OCI — metadata is a flat string map; user_data is a key within it.
	"oci:Core/instance:Instance": {
		PropertyPath: []string{"metadata", "user_data"},
	},
	// OCI — InstanceConfiguration nests metadata inside launch details.
	"oci:Core/instanceConfiguration:InstanceConfiguration": {
		PropertyPath: []string{"instanceDetails", "launchDetails", "metadata", "user_data"},
	},

	// Future providers (uncomment when adding support):
	// "aws:ec2/instance:Instance":               {PropertyPath: []string{"userData"}},
	// "aws:ec2/launchTemplate:LaunchTemplate":    {PropertyPath: []string{"userData"}},
	// "gcp:compute/instance:Instance":            {PropertyPath: []string{"metadata", "startup-script"}},
}

// IsComputeResource reports whether the given Pulumi resource type token
// is a known compute resource that carries user_data.
func IsComputeResource(resourceType string) bool {
	_, ok := ComputeResources[resourceType]
	return ok
}
