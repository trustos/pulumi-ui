# YAML Programs

User-defined programs are stored as Go-templated Pulumi YAML in the `custom_programs` database table. This document describes the template format, available functions, and how to write a program.

---

## Concept

Go's `text/template` engine is layered on top of Pulumi YAML — exactly what Helm does to Kubernetes YAML. The template is stored in the database. At runtime it is rendered with the stack's config values into plain, valid Pulumi YAML, which is written to a temp directory and executed via `UpsertStackLocalSource`.

```
DB (program_yaml column)         Runtime rendering              Pulumi execution
────────────────────────         ──────────────────────         ─────────────────
Go-templated YAML  ─────────→   text/template.Execute()  ────→ UpsertStackLocalSource
{{ range }}, {{ if }},           with config map + Sprig         on rendered plain YAML
custom OCI functions             + custom OCI helpers
```

**Key separation of concerns:**
- `{{ ... }}` — resolved by Go template at render time (structural decisions: how many resources, conditionals)
- `${ ... }` — resolved by Pulumi at apply time (cross-resource dependencies: actual OCIDs, output values)

---

## Template Context

```go
type TemplateContext struct {
    Config map[string]string  // all stack config values
}
```

Access config values in templates with `{{ .Config.keyName }}`.

---

## Sprig Functions

All [Sprig](https://masterminds.github.io/sprig/) functions are available (same library as Helm):

| Function | Description | Example |
|---|---|---|
| `until N` | `[0, 1, ..., N-1]` | `{{ range $i := until 3 }}` |
| `atoi` | string → int | `{{ until (atoi .Config.nodeCount) }}` |
| `b64enc` | base64 encode | `{{ .script \| b64enc }}` |
| `quote` | wrap in `"..."` | `{{ .Config.vcnCidr \| quote }}` |
| `default` | fallback value | `{{ .Config.shape \| default "VM.Standard.A1.Flex" }}` |
| `add` / `sub` | arithmetic | `{{ add $i 1 }}` |
| `printf` | formatted string | `{{ printf "node-%d" $i }}` |
| `trim` / `upper` / `lower` | string ops | `{{ .Config.name \| lower }}` |

---

## Custom OCI Helper Functions

| Function | Signature | Description |
|---|---|---|
| `instanceOcpus` | `(nodeIndex, nodeCount int) int` | OCPU count per node (Always Free allocation) |
| `instanceMemoryGb` | `(nodeIndex, nodeCount int) int` | Memory GiB per node |
| `cloudInit` | `(nodeIndex int, config map[string]string) string` | Renders and base64-encodes the cloud-init script |
| `groupRef` | `(groupName, domain, statement string) string` | IAM policy statement for old IDCS or new Identity Domain tenancies |

### `instanceOcpus` allocation table

| nodeCount | node 0 | node 1 | node 2 | node 3 | Total |
|---|---|---|---|---|---|
| 1 | 4 | — | — | — | 4 |
| 2 | 2 | 2 | — | — | 4 |
| 3 | 1 | 1 | 2 | — | 4 |
| 4 | 1 | 1 | 1 | 1 | 4 |

All configurations stay within the OCI Always Free quota of 4 OCPUs / 24 GB total.

### `groupRef` examples

```
{{ groupRef "AdminGroup" "" "manage dynamic-groups in tenancy" }}
→ Allow group AdminGroup to manage dynamic-groups in tenancy

{{ groupRef "AdminGroup" "Default" "manage dynamic-groups in tenancy" }}
→ Allow group 'Default'/AdminGroup to manage dynamic-groups in tenancy
```

---

## Config Section Parsing

The `config:` section of the YAML body drives the UI form. At program creation time, `ParseConfigFields()` derives a `[]ConfigField` from it:

| YAML type | Form field type |
|---|---|
| `String` | `text` |
| `Integer` / `Number` | `number` |
| `Boolean` | `select` (true/false options) |
| key == `imageId` | `oci-image` (convention) |
| key == `shape` | `oci-shape` (convention) |

### Optional `meta:` section

A `meta:` top-level key (stripped before execution) allows declaring UI groups and explicit `ui_type` overrides:

```yaml
meta:
  groups:
    - key: network
      label: "Network"
      fields: [vcnCidr, compartmentName]
    - key: compute
      label: "Compute"
      fields: [shape, imageId, bootVolSizeGb]
  fields:
    imageId:
      ui_type: oci-image
    shape:
      ui_type: oci-shape
```

---

## Security

- `fn::readFile` — stripped from user YAML before writing to disk via `SanitizeYAML()`. This prevents programs from reading server filesystem files (e.g. the database or encryption key).
- Programs are declarative — they can only make OCI API calls via the Pulumi OCI provider using credentials the user themselves provided.
- OCI credentials are injected via `stack.SetConfig()` (not environment variables) because the Pulumi OCI provider reads its config from the Pulumi config system.

---

## Writing a YAML Program

### Minimal example

```yaml
name: my-vcn
runtime: yaml
description: "Creates a compartment and VCN"

config:
  compartmentName:
    type: String
    default: my-compartment
  vcnCidr:
    type: String
    default: "10.0.0.0/16"

resources:
  my-compartment:
    type: oci:identity:Compartment
    properties:
      compartmentId: ${oci:tenancyOcid}
      name: {{ .Config.compartmentName }}
      description: "Created by Pulumi UI"
      enableDelete: true

  my-vcn:
    type: oci:core:Vcn
    properties:
      compartmentId: ${my-compartment.id}
      cidrBlock: {{ .Config.vcnCidr | quote }}
      displayName: my-vcn
      dnsLabel: myvcn

outputs:
  compartmentId: ${my-compartment.id}
  vcnId: ${my-vcn.id}
```

### Loop example — N instances

```yaml
config:
  nodeCount:
    type: Integer
    default: 3

resources:
  {{- range $i := until (atoi $.Config.nodeCount) }}
  instance-{{ $i }}:
    type: oci:core:Instance
    properties:
      compartmentId: ${nomad-compartment.id}
      displayName: {{ printf "node-%d" $i }}
      shape: {{ $.Config.shape }}
      shapeConfig:
        ocpus: {{ instanceOcpus $i (atoi $.Config.nodeCount) }}
        memoryInGbs: {{ instanceMemoryGb $i (atoi $.Config.nodeCount) }}
  {{- end }}
```

### Conditional example

```yaml
{{- if ne .Config.skipDynamicGroup "true" }}
  nomad-cluster-dg:
    type: oci:identity:DynamicGroup
    properties:
      compartmentId: ${oci:tenancyOcid}
      name: nomad-cluster-dg
      matchingRule: "ALL {instance.compartment.id = '${nomad-compartment.id}'}"
{{- end }}
```

### IAM policy with identity domain awareness

```yaml
  nomad-prereq-policy:
    type: oci:identity:Policy
    properties:
      compartmentId: ${oci:tenancyOcid}
      name: nomad-prereq
      statements:
        - {{ groupRef .Config.adminGroupName .Config.identityDomain "manage dynamic-groups in tenancy" | quote }}
        - {{ groupRef .Config.adminGroupName .Config.identityDomain "manage policies in tenancy" | quote }}
```

### Meta section for UI grouping

```yaml
meta:
  groups:
    - key: iam
      label: "IAM & Permissions"
      fields: [skipDynamicGroup, adminGroupName, identityDomain]
    - key: network
      label: "Network"
      fields: [compartmentName, vcnCidr]

config:
  skipDynamicGroup:
    type: String
    default: "false"
  adminGroupName:
    type: String
    default: ""
  identityDomain:
    type: String
    default: ""
  compartmentName:
    type: String
    default: my-compartment
  vcnCidr:
    type: String
    default: "10.0.0.0/16"
```

---

## OCI Resource Type Reference

Resource type tokens follow the pattern `oci:[Module]:[Resource]` (case-insensitive):

| Resource type | Token |
|---|---|
| Compartment | `oci:identity:Compartment` |
| Dynamic Group | `oci:identity:DynamicGroup` |
| Policy | `oci:identity:Policy` |
| VCN | `oci:core:Vcn` |
| Subnet | `oci:core:Subnet` |
| Internet Gateway | `oci:core:InternetGateway` |
| NAT Gateway | `oci:core:NatGateway` |
| Route Table | `oci:core:RouteTable` |
| Security List | `oci:core:SecurityList` |
| NSG | `oci:core:NetworkSecurityGroup` |
| Instance | `oci:core:Instance` |
| Block Volume | `oci:core:Volume` |
| Volume Attachment | `oci:core:VolumeAttachment` |
| Object Storage Bucket | `oci:objectstorage:Bucket` |
| DNS Zone | `oci:dns:Zone` |
| Network Load Balancer | `oci:networkloadbalancer:NetworkLoadBalancer` |

---

## Limitations vs Built-in Go Programs

| Capability | Built-in Go | YAML program |
|---|---|---|
| Loops (`range`) | Yes | Yes (via Go template) |
| Conditionals (`if`) | Yes | Yes (via Go template) |
| `pulumi.All(...).ApplyT(...)` | Yes | No — cross-resource values available only at apply time |
| Dynamic cloud-init with runtime OCIDs | Yes | No — `cloudInit()` uses static config values only |
| Arbitrary Go logic | Yes | No |
| No recompile needed | No | Yes |
| Stored in database | No | Yes |
| Editable via UI | No | Yes |

For programs requiring runtime Pulumi output chaining (like `nomad-cluster` which passes `compartmentID` and `subnetID` into the cloud-init script), use a built-in Go program.
