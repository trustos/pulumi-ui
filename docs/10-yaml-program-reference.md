# YAML Program Reference

A YAML program lets you define infrastructure as a Go-templated Pulumi YAML file stored in the database. Once created, it appears in the **New Stack** dialog alongside built-in programs and can be edited or deleted at any time — no server restart required.

---

## How It Works

```
Your template (stored in DB)
        │
        ▼  text/template.Execute()
        │  with stack config values + Sprig + OCI helpers
        ▼
Plain Pulumi YAML (written to temp dir)
        │
        ▼  pulumi up / preview / destroy / refresh
        │
        ▼
OCI resources
```

There are two layers of resolution — keep them straight:

| Syntax | Resolved by | When | Example |
|---|---|---|---|
| `{{ ... }}` | Go template | Before Pulumi runs | `{{ .Config.nodeCount }}` |
| `${ ... }` | Pulumi | At apply time | `${my-compartment.id}` |

Go template decides **structure** (loops, conditionals, how many resources). Pulumi resolves **runtime values** (actual OCIDs, outputs from other resources).

> **Important:** Go template processes the entire file — including YAML comments. Do **not** write `{{ }}` inside comments. Use plain text instead:
>
> ```yaml
> # Good — plain text
> # Template directives (double-brace) are resolved before Pulumi runs.
>
> # Bad — Go template will try to parse this and fail
> # {{ ... }} expressions are resolved before Pulumi runs.
> ```

---

## Minimal Program

```yaml
name: my-vcn
runtime: yaml
description: "Creates a compartment and VCN"

config:
  compartmentName:
    type: string
    default: my-compartment
  vcnCidr:
    type: string
    default: "10.0.0.0/16"

resources:
  my-compartment:
    type: oci:identity:Compartment
    properties:
      compartmentId: ${oci:tenancyOcid}
      name: {{ .Config.compartmentName }}
      description: "Created by Pulumi"
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

**Required top-level keys:** `name`, `runtime: yaml`, `resources`.

---

## Config Section

The `config:` block defines the fields shown in the UI form when creating a stack.

```yaml
config:
  myField:
    type: string        # string | integer | number | boolean
    default: "hello"    # optional
```

### Field Types → Form Controls

| YAML type | Form field | Notes |
|---|---|---|
| `string` | Text input | Default for most values |
| `integer` | Number input | Use for counts, sizes |
| `number` | Number input | Use for floats |
| `boolean` | Select (true/false) | |
| key = `imageId` | OCI image picker | Convention — type must be `string` |
| key = `shape` | OCI shape picker | Convention — type must be `string` |

### Accessing Config Values

```yaml
# Simple value
name: {{ .Config.compartmentName }}

# Quoted (required for YAML string values containing special chars)
cidrBlock: {{ .Config.vcnCidr | quote }}

# As integer for arithmetic
{{ until (atoi .Config.nodeCount) }}
```

> **Defaults are applied automatically.** When a stack runs, declared `default:` values are merged into the config before the template renders. A field with a `default:` is always safe to reference as `{{ .Config.key }}` even if the user never edited it. You only need `| default "..."` for additional fallbacks or for fields that intentionally have no declared default.

### Meta Section (UI Groups)

Add a `meta:` block at the top of your file to group config fields in the UI form and override field types. It is stripped before Pulumi executes the program.

```yaml
meta:
  groups:
    - key: network
      label: "Network"
      fields: [compartmentName, vcnCidr, subnetCidr]
    - key: compute
      label: "Compute"
      fields: [shape, imageId, nodeCount, bootVolSizeGb]
  fields:
    imageId:
      ui_type: oci-image
    shape:
      ui_type: oci-shape

config:
  compartmentName:
    type: string
    default: my-compartment
  vcnCidr:
    type: string
    default: "10.0.0.0/16"
  # ... rest of config
```

Fields not listed in any group appear at the bottom ungrouped.

---

## Loops

Use Sprig's `until` to iterate over a range of integers.

```yaml
config:
  nodeCount:
    type: integer
    default: 3

resources:
  {{- range $i := until (atoi $.Config.nodeCount) }}
  instance-{{ $i }}:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ${my-compartment.id}
      displayName: {{ printf "node-%d" $i }}
      availabilityDomain: {{ $.Config.availabilityDomain }}
      shape: {{ $.Config.shape }}
      shapeConfig:
        ocpus: {{ instanceOcpus $i (atoi $.Config.nodeCount) }}
        memoryInGbs: {{ instanceMemoryGb $i (atoi $.Config.nodeCount) }}
  {{- end }}
```

> **Note:** Use `$.Config` (global `$` prefix) inside `range` blocks to access config. The loop variable `$i` shadows `.` inside the loop body.

### Iterating a Fixed List of Values

```yaml
{{- range $port := list 4646 4647 4648 }}
  nsg-rule-{{ $port }}:
    type: oci:Core/networkSecurityGroupSecurityRule:NetworkSecurityGroupSecurityRule
    properties:
      networkSecurityGroupId: ${my-nsg.id}
      direction: INGRESS
      protocol: "6"
      tcpOptions:
        destinationPortRange:
          min: {{ $port }}
          max: {{ $port }}
{{- end }}
```

### Nested Loops

```yaml
{{- range $port := list 80 443 }}
  {{- range $i := until (atoi $.Config.nodeCount) }}
  nlb-backend-{{ $port }}-{{ $i }}:
    type: oci:NetworkLoadBalancer/backend:Backend
    properties:
      backendSetName: backend-set-{{ $port }}
      networkLoadBalancerId: ${my-nlb.id}
      ipAddress: ${instance-{{ $i }}.privateIp}
      port: {{ $port }}
  {{- end }}
{{- end }}
```

---

## Conditionals

```yaml
{{- if ne .Config.skipOptionalResource "true" }}
  optional-resource:
    type: oci:identity:DynamicGroup
    properties:
      compartmentId: ${oci:tenancyOcid}
      name: my-dg
      matchingRule: "ALL {instance.compartment.id = '${my-compartment.id}'}"
{{- end }}
```

```yaml
{{- if .Config.adminGroupName }}
  prereq-policy:
    type: oci:identity:Policy
    properties:
      compartmentId: ${oci:tenancyOcid}
      name: prereq-policy
      statements:
        - {{ groupRef .Config.adminGroupName .Config.identityDomain "manage dynamic-groups in tenancy" | quote }}
{{- end }}
```

### Common Conditions

| Template | Meaning |
|---|---|
| `{{- if eq .Config.x "value" }}` | string equals |
| `{{- if ne .Config.x "true" }}` | string not equals |
| `{{- if .Config.x }}` | non-empty string |
| `{{- if not .Config.x }}` | empty string |
| `{{- if gt (atoi .Config.n) 2 }}` | integer greater than |

---

## Sprig Functions

All [Sprig](https://masterminds.github.io/sprig/) functions are available. The most useful ones:

### Strings

| Function | Example | Result |
|---|---|---|
| `quote` | `{{ "hello" \| quote }}` | `"hello"` |
| `upper` / `lower` | `{{ .Config.name \| lower }}` | `my-name` |
| `trim` | `{{ .Config.name \| trim }}` | removes whitespace |
| `replace` | `{{ .Config.name \| replace "_" "-" }}` | replaces chars |
| `printf` | `{{ printf "node-%d" $i }}` | `node-0` |
| `b64enc` | `{{ .Config.script \| b64enc }}` | base64 encoded |

### Numbers

| Function | Example | Result |
|---|---|---|
| `atoi` | `{{ atoi .Config.nodeCount }}` | string → int |
| `add` | `{{ add $i 1 }}` | `$i + 1` |
| `sub` | `{{ sub 10 $i }}` | `10 - $i` |
| `mul` | `{{ mul $i 50 }}` | `$i * 50` |
| `div` | `{{ div 100 4 }}` | `25` |

### Lists

| Function | Example | Result |
|---|---|---|
| `list` | `{{ list 80 443 8080 }}` | `[80, 443, 8080]` |
| `until` | `{{ until 3 }}` | `[0, 1, 2]` |
| `len` | `{{ len (until 4) }}` | `4` |

### Logic

| Function | Example |
|---|---|
| `default` | `{{ .Config.shape \| default "VM.Standard.A1.Flex" }}` |
| `ternary` | `{{ ternary "yes" "no" (eq .Config.x "1") }}` |
| `coalesce` | `{{ coalesce .Config.a .Config.b "fallback" }}` |
| `empty` | `{{ if empty .Config.name }}` |

---

## Custom OCI Functions

Four helper functions are built into the template engine specifically for OCI.

### `instanceOcpus`

```
instanceOcpus(nodeIndex int, nodeCount int) int
```

Returns the OCPU count for a node, distributing OCI Always Free quota (4 total OCPUs) across nodes:

| nodeCount | node 0 | node 1 | node 2 | node 3 |
|---|---|---|---|---|
| 1 | 4 | — | — | — |
| 2 | 2 | 2 | — | — |
| 3 | 1 | 1 | 2 | — |
| 4 | 1 | 1 | 1 | 1 |

```yaml
ocpus: {{ instanceOcpus $i (atoi $.Config.nodeCount) }}
```

### `instanceMemoryGb`

```
instanceMemoryGb(nodeIndex int, nodeCount int) int
```

Returns memory in GiB for a node (`ocpus × 6`). Total stays within Always Free 24 GB limit.

```yaml
memoryInGbs: {{ instanceMemoryGb $i (atoi $.Config.nodeCount) }}
```

### `cloudInit`

```
cloudInit(nodeIndex int, config map[string]string) string
```

Renders the Nomad/Consul cluster cloud-init script and returns it base64-encoded. Uses config values such as `shape`, `nodeCount`, `nomadVersion`, `consulVersion`.

```yaml
metadata:
  userDataScript: {{ cloudInit $i $.Config }}
```

> **Limitation:** `COMPARTMENT_OCID` and `SUBNET_OCID` placeholders in the cloud-init script are left empty because those values are only available at Pulumi apply time (as `${resource.id}` outputs), not at template render time.

### `groupRef`

```
groupRef(groupName string, domain string, statement string) string
```

Generates an IAM policy statement, handling both old IDCS tenancies (no domain) and new Identity Domain tenancies:

```yaml
# Old IDCS tenancy (domain = "")
{{ groupRef "AdminGroup" "" "manage policies in tenancy" }}
# → Allow group AdminGroup to manage policies in tenancy

# New Identity Domain tenancy
{{ groupRef "AdminGroup" "Default" "manage policies in tenancy" }}
# → Allow group 'Default'/AdminGroup to manage policies in tenancy
```

```yaml
statements:
  - {{ groupRef .Config.adminGroupName .Config.identityDomain "manage dynamic-groups in tenancy" | quote }}
  - {{ groupRef .Config.adminGroupName .Config.identityDomain "manage policies in tenancy" | quote }}
```

---

## OCI Resource Types

### Short-form vs Canonical Type Names

The OCI Pulumi provider accepts both short-form aliases and canonical SDK names:

| Short-form (accepted) | Canonical (preferred) |
|---|---|
| `oci:core:Vcn` | `oci:Core/vcn:Vcn` |
| `oci:core:NatGateway` | `oci:Core/natGateway:NatGateway` |
| `oci:core:NetworkSecurityGroup` | `oci:Core/networkSecurityGroup:NetworkSecurityGroup` |
| `oci:identity:DynamicGroup` | `oci:Identity/dynamicGroup:DynamicGroup` |
| `oci:networkloadbalancer:Backend` | `oci:NetworkLoadBalancer/backend:Backend` |

**Use canonical form in new programs.** The visual editor's property autocomplete,
required-field validation (Level 5), and the Resource Catalog all key off the
canonical form. Short-form aliases still work at runtime but will not get schema
assistance in the editor.

Resource type tokens follow the canonical pattern `oci:[Module]/[subpath]:[Resource]`.

### Identity

```yaml
# Compartment
my-compartment:
  type: oci:Identity/compartment:Compartment
  properties:
    compartmentId: ${oci:tenancyOcid}   # parent compartment; use tenancyOcid for top-level
    name: {{ .Config.compartmentName }}
    description: "My compartment"
    enableDelete: true

# Dynamic Group
my-dg:
  type: oci:Identity/dynamicGroup:DynamicGroup
  properties:
    compartmentId: ${oci:tenancyOcid}
    name: my-dg
    matchingRule: "ALL {instance.compartment.id = '${my-compartment.id}'}"
    description: "All instances in compartment"

# Policy
my-policy:
  type: oci:Identity/policy:Policy
  properties:
    compartmentId: ${oci:tenancyOcid}
    name: my-policy
    description: "My policy"
    statements:
      - "Allow dynamic-group my-dg to use instances in compartment my-compartment"
      - {{ groupRef .Config.adminGroupName .Config.identityDomain "manage all-resources in compartment my-compartment" | quote }}
```

### Networking

```yaml
# VCN
my-vcn:
  type: oci:Core/vcn:Vcn
  properties:
    compartmentId: ${my-compartment.id}
    cidrBlock: {{ .Config.vcnCidr | quote }}
    displayName: my-vcn
    dnsLabel: myvcn

# Internet Gateway
my-igw:
  type: oci:Core/internetGateway:InternetGateway
  properties:
    compartmentId: ${my-compartment.id}
    vcnId: ${my-vcn.id}
    displayName: my-igw
    enabled: true

# NAT Gateway
my-nat:
  type: oci:Core/natGateway:NatGateway
  properties:
    compartmentId: ${my-compartment.id}
    vcnId: ${my-vcn.id}
    displayName: my-nat

# Route Table (public — routes to internet gateway)
public-rt:
  type: oci:Core/routeTable:RouteTable
  properties:
    compartmentId: ${my-compartment.id}
    vcnId: ${my-vcn.id}
    displayName: public-rt
    routeRules:
      - networkEntityId: ${my-igw.id}
        destination: "0.0.0.0/0"
        destinationType: CIDR_BLOCK

# Security List
my-sl:
  type: oci:Core/securityList:SecurityList
  properties:
    compartmentId: ${my-compartment.id}
    vcnId: ${my-vcn.id}
    displayName: my-sl
    ingressSecurityRules:
      - protocol: "6"        # TCP
        source: "0.0.0.0/0"
        tcpOptions:
          max: 22
          min: 22
      - protocol: "all"
        source: {{ .Config.vcnCidr | quote }}
    egressSecurityRules:
      - protocol: "all"
        destination: "0.0.0.0/0"

# Subnet
my-subnet:
  type: oci:Core/subnet:Subnet
  properties:
    compartmentId: ${my-compartment.id}
    vcnId: ${my-vcn.id}
    cidrBlock: {{ .Config.subnetCidr | quote }}
    displayName: my-subnet
    dnsLabel: mysubnet
    routeTableId: ${public-rt.id}
    securityListIds:
      - ${my-sl.id}

# Network Security Group
my-nsg:
  type: oci:Core/networkSecurityGroup:NetworkSecurityGroup
  properties:
    compartmentId: ${my-compartment.id}
    vcnId: ${my-vcn.id}
    displayName: my-nsg

# NSG Rule
my-nsg-rule-ssh:
  type: oci:Core/networkSecurityGroupSecurityRule:NetworkSecurityGroupSecurityRule
  properties:
    networkSecurityGroupId: ${my-nsg.id}
    direction: INGRESS
    protocol: "6"
    source: "0.0.0.0/0"
    sourceType: CIDR_BLOCK
    tcpOptions:
      destinationPortRange:
        min: 22
        max: 22
```

### Compute

```yaml
# Instance (Ampere A1 shape)
my-instance:
  type: oci:Core/instance:Instance
  properties:
    compartmentId: ${my-compartment.id}
    availabilityDomain: {{ .Config.availabilityDomain }}
    displayName: my-instance
    shape: {{ .Config.shape | default "VM.Standard.A1.Flex" }}
    shapeConfig:
      ocpus: {{ instanceOcpus 0 1 }}       # 4 OCPUs for single node
      memoryInGbs: {{ instanceMemoryGb 0 1 }} # 24 GB for single node
    createVnicDetails:
      subnetId: ${my-subnet.id}
      assignPublicIp: true
      nsgIds:
        - ${my-nsg.id}
    sourceDetails:
      sourceType: image
      imageId: {{ .Config.imageId }}
      bootVolumeSizeInGbs: {{ .Config.bootVolSizeGb | default "50" }}
    metadata:
      ssh_authorized_keys: {{ .Config.sshPublicKey }}
      user_data: {{ cloudInit 0 $.Config }}

# Block Volume
my-volume:
  type: oci:Core/volume:Volume
  properties:
    compartmentId: ${my-compartment.id}
    availabilityDomain: {{ .Config.availabilityDomain }}
    displayName: my-volume
    sizeInGbs: {{ .Config.volumeSizeGb | default "50" }}

# Volume Attachment (paravirtualized)
my-vol-attach:
  type: oci:Core/volumeAttachment:VolumeAttachment
  properties:
    attachmentType: paravirtualized
    instanceId: ${my-instance.id}
    volumeId: ${my-volume.id}
    isReadOnly: false
    isPvEncryptionInTransitEnabled: false
```

### Load Balancer

```yaml
# Network Load Balancer
my-nlb:
  type: oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer
  properties:
    compartmentId: ${my-compartment.id}
    displayName: my-nlb
    isPrivate: false
    subnetId: ${my-subnet.id}

# Backend Set
my-backend-set:
  type: oci:NetworkLoadBalancer/backendSet:BackendSet
  properties:
    networkLoadBalancerId: ${my-nlb.id}
    name: backend-set-80
    policy: FIVE_TUPLE
    healthChecker:
      protocol: TCP
      port: 80

# Backend — use targetId for instance-based routing (no IP needed)
my-backend-0:
  type: oci:NetworkLoadBalancer/backend:Backend
  properties:
    networkLoadBalancerId: ${my-nlb.id}
    backendSetName: backend-set-80
    targetId: ${my-instance.id}   # use targetId for OCI instances
    port: 80
    # ipAddress: ${my-instance.privateIp}  # alternative when targetId unavailable

# Listener
my-listener:
  type: oci:NetworkLoadBalancer/listener:Listener
  properties:
    networkLoadBalancerId: ${my-nlb.id}
    name: listener-80
    defaultBackendSetName: backend-set-80
    port: 80
    protocol: TCP
```

### Storage

```yaml
# Object Storage Bucket
my-bucket:
  type: oci:objectstorage:Bucket
  properties:
    compartmentId: ${my-compartment.id}
    namespace: {{ .Config.objectStorageNamespace }}
    name: {{ .Config.bucketName }}
    accessType: NoPublicAccess
    versioning: Enabled
```

---

## Special Variables

```yaml
# Tenancy OCID — always available, no config needed
compartmentId: ${oci:tenancyOcid}

# Config values injected by the engine (always available)
# oci:tenancyOcid, oci:userOcid, oci:fingerprint, oci:privateKeyPath, oci:region
```

---

## Outputs

Declare stack outputs to expose values after a successful `pulumi up`:

```yaml
outputs:
  compartmentId: ${my-compartment.id}
  vcnId: ${my-vcn.id}
  instancePublicIp: ${my-instance-0.publicIp}
  nlbIp: ${my-nlb.ipAddresses[0].ipAddress}
```

---

## Security

- `fn::readFile` — any line containing this is stripped before writing to disk. Programs cannot read server files.
- Programs can only call OCI APIs through the Pulumi OCI provider using the credentials you have configured for the account.
- OCI credentials are injected via Pulumi config, not environment variables (the OCI provider reads from the Pulumi config system).

---

## Limitations vs Built-in Programs

| Capability | Built-in Go | YAML program |
|---|---|---|
| Loops | Yes (Go `for`) | Yes (Go template `range`) |
| Conditionals | Yes | Yes (Go template `if`) |
| Cross-resource values in cloud-init | Yes (`pulumi.All(...).ApplyT(...)`) | No — only static config values |
| Arbitrary Go logic | Yes | No |
| No server restart needed | No | Yes |
| Editable via UI | No | Yes |
| Stored in database | No | Yes |

If your program needs to pass a runtime-resolved OCID (e.g. a subnet ID) into a cloud-init script, you must implement it as a built-in Go program.

---

## Full Example — Three-Node Compute Cluster

```yaml
meta:
  groups:
    - key: network
      label: "Network"
      fields: [compartmentName, vcnCidr, subnetCidr]
    - key: compute
      label: "Compute"
      fields: [nodeCount, shape, imageId, bootVolSizeGb, availabilityDomain, sshPublicKey]

name: compute-cluster
runtime: yaml
description: "N-node compute cluster with shared VCN"

config:
  compartmentName:
    type: string
    default: compute-cluster
  vcnCidr:
    type: string
    default: "10.0.0.0/16"
  subnetCidr:
    type: string
    default: "10.0.1.0/24"
  nodeCount:
    type: integer
    default: 2
  shape:
    type: string
    default: "VM.Standard.A1.Flex"
  imageId:
    type: string
    default: ""
  bootVolSizeGb:
    type: integer
    default: 50
  availabilityDomain:
    type: string
    default: ""
  sshPublicKey:
    type: string
    default: ""

resources:
  cluster-compartment:
    type: oci:Identity/compartment:Compartment
    properties:
      compartmentId: ${oci:tenancyOcid}
      name: {{ .Config.compartmentName }}
      description: "Compute cluster compartment"
      enableDelete: true

  cluster-vcn:
    type: oci:Core/vcn:Vcn
    properties:
      compartmentId: ${cluster-compartment.id}
      cidrBlock: {{ .Config.vcnCidr | quote }}
      displayName: cluster-vcn
      dnsLabel: clustervcn

  cluster-igw:
    type: oci:Core/internetGateway:InternetGateway
    properties:
      compartmentId: ${cluster-compartment.id}
      vcnId: ${cluster-vcn.id}
      displayName: cluster-igw
      enabled: true

  cluster-rt:
    type: oci:Core/routeTable:RouteTable
    properties:
      compartmentId: ${cluster-compartment.id}
      vcnId: ${cluster-vcn.id}
      displayName: cluster-rt
      routeRules:
        - networkEntityId: ${cluster-igw.id}
          destination: "0.0.0.0/0"
          destinationType: CIDR_BLOCK

  cluster-sl:
    type: oci:Core/securityList:SecurityList
    properties:
      compartmentId: ${cluster-compartment.id}
      vcnId: ${cluster-vcn.id}
      displayName: cluster-sl
      ingressSecurityRules:
        - protocol: "6"
          source: "0.0.0.0/0"
          tcpOptions:
            min: 22
            max: 22
        - protocol: "all"
          source: {{ .Config.vcnCidr | quote }}
      egressSecurityRules:
        - protocol: "all"
          destination: "0.0.0.0/0"

  cluster-subnet:
    type: oci:Core/subnet:Subnet
    properties:
      compartmentId: ${cluster-compartment.id}
      vcnId: ${cluster-vcn.id}
      cidrBlock: {{ .Config.subnetCidr | quote }}
      displayName: cluster-subnet
      dnsLabel: clustersubnet
      routeTableId: ${cluster-rt.id}
      securityListIds:
        - ${cluster-sl.id}

  {{- range $i := until (atoi $.Config.nodeCount) }}
  node-{{ $i }}:
    type: oci:Core/instance:Instance
    properties:
      compartmentId: ${cluster-compartment.id}
      availabilityDomain: {{ $.Config.availabilityDomain }}
      displayName: {{ printf "node-%d" $i }}
      shape: {{ $.Config.shape }}
      shapeConfig:
        ocpus: {{ instanceOcpus $i (atoi $.Config.nodeCount) }}
        memoryInGbs: {{ instanceMemoryGb $i (atoi $.Config.nodeCount) }}
      createVnicDetails:
        subnetId: ${cluster-subnet.id}
        assignPublicIp: true
      sourceDetails:
        sourceType: image
        imageId: {{ $.Config.imageId }}
        bootVolumeSizeInGbs: {{ $.Config.bootVolSizeGb }}
      metadata:
        ssh_authorized_keys: {{ $.Config.sshPublicKey }}
  {{- end }}

outputs:
  compartmentId: ${cluster-compartment.id}
  vcnId: ${cluster-vcn.id}
  {{- range $i := until (atoi $.Config.nodeCount) }}
  node{{ $i }}PublicIp: ${node-{{ $i }}.publicIp}
  {{- end }}
```

---

## Validation

Programs are validated on every save (and checked live as you type in the editor). Validation runs six levels sequentially:

| Level | Name | What it checks |
|---|---|---|
| 1 | Template syntax | Can the Go template be parsed? |
| 2 | Template render | Can it render with all defaults applied? |
| 3 | YAML structure | Does the rendered output have `name`, `runtime: yaml`, and `resources`? |
| 4 | Config section | Are field types valid? Do `meta:` group references exist in `config:`? Empty `config:` is allowed. |
| 5 | Resource structure | Does each resource have a `type`? Are all required properties present (schema-validated)? |
| 6 | Resource types | Does each resource use a known or well-formed provider type token? |

A program that fails validation cannot be saved. Fix all errors shown in the editor panel before saving.

> **Empty config is valid.** Programs with no config fields (e.g. a VCN-only program
> with hardcoded values) pass Level 4 without error. The `config:` block is optional.

---

## Troubleshooting

**`template: ... map has no entry for key "X"`**
: `{{ .Config.X }}` is referenced but the key has no `default:` and was never set by the user. Add a `default:` to the config field, or the validator will catch this before the program can be saved.

**`{{ }}` syntax in comments causes a template parse error**
: Go template processes the entire file including YAML comments. Replace `{{ ... }}` in comments with plain text (e.g., `double-brace directives`).

**`yaml: unmarshal errors` after rendering**
: The rendered YAML is invalid. Common causes: unquoted string values with colons (use `| quote`), mismatched indentation inside `range` blocks.

**`fn::readFile` lines disappear**
: This is intentional security sanitization. Use config fields to pass in file content instead.

**Pulumi `${resource.property}` not resolving**
: The resource name in `${ }` must exactly match the resource key after Go template rendering (after `{{ range }}` expands).

**`atoi: parsing "": invalid syntax`**
: A config field used with `atoi` has no `default:` or the default is not a number string. Add `default: "1"` (or any integer string) to the config block.

**Visual editor shows no property autocomplete for a resource type**
: The resource type is not in the OCI schema fallback. Either use `pulumi schema get oci`
  to load the full live schema, or use canonical type form (`oci:Core/natGateway:NatGateway`
  instead of `oci:core:NatGateway`). Only canonical forms are covered by the fallback.

**Validation error: "missing required property 'X'"**
: Level 5 schema validation found a required property missing. Add the property
  (even with an empty value — it will be caught by visual-mode pre-save checks and
  can be filled in before saving).

**NLB Backend: `ipAddress` validation error when using `targetId`**
: The schema marks both as optional. Use `targetId: ${my-instance.id}` for OCI
  instances (routing by instance OCID). `ipAddress` is only needed when routing to
  an IP that Pulumi cannot discover automatically (e.g. an external server).

**Section rename or delete not working**
: Section rename uses the pencil (✎) icon visible on hover. Section delete uses the
  × icon (also on hover). The first section cannot be deleted — it is always required.
  Deleting a section with resources shows a confirmation dialog.

---

## Reference Programs

Two complete programs are available in the `docs/` directory as reference:

| File | Description |
|---|---|
| `docs/nomad-cluster-program.yaml` | v1 — short-form type aliases (`oci:core:NatGateway`). Functional but does not benefit from schema autocomplete for types added after v1. |
| `docs/nomad-cluster-v2-program.yaml` | v2 — canonical type names throughout, 13 IAM policy statements (vs 10 in v1), NLB NSG association, configurable backup retention via `glusterBackupRetentionDays` config field. |

The v2 program is the recommended starting point for new Nomad + Consul clusters.
It covers the full production architecture: compartment, IAM, VCN with public/private
subnets, NAT gateway, 4 NSGs, N compute instances, GlusterFS block volumes with
backup policy, and a public NLB on ports 80/443/4646.
