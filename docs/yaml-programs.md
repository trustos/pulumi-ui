# YAML Programs

A YAML program lets you define infrastructure as a Go-templated Pulumi YAML file stored in the database. Once created, it appears in the **New Stack** dialog alongside built-in programs and can be edited or deleted at any time — no server restart required.

---

## How It Works

```
DB (program_yaml column)         Runtime rendering              Pulumi execution
────────────────────────         ──────────────────────         ─────────────────
Go-templated YAML  ─────────→   text/template.Execute()  ────→ UpsertStackLocalSource
{{ range }}, {{ if }},           with config map + Sprig         on rendered plain YAML
custom OCI functions             + custom OCI helpers
```

Two layers of resolution — keep them straight:

| Syntax | Resolved by | When | Example |
|---|---|---|---|
| `{{ ... }}` | Go template | Before Pulumi runs | `{{ .Config.nodeCount }}` |
| `${ ... }` | Pulumi | At apply time | `${my-compartment.id}` |

Go template decides **structure** (loops, conditionals, how many resources). Pulumi resolves **runtime values** (actual OCIDs, outputs from other resources).

> **Important:** Go template processes the entire file — including YAML comments. Do **not** write `{{ }}` inside comments. Use plain text instead.

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
    type: oci:Identity/compartment:Compartment
    properties:
      compartmentId: ${oci:tenancyOcid}
      name: {{ .Config.compartmentName }}
      description: "Created by Pulumi"
      enableDelete: true

  my-vcn:
    type: oci:Core/vcn:Vcn
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
| key = `sshPublicKey` | SSH key picker | Convention — type must be `string` |

### Accessing Config Values

```yaml
# Simple value
name: {{ .Config.compartmentName }}

# Quoted (required for YAML string values containing special chars)
cidrBlock: {{ .Config.vcnCidr | quote }}

# As integer for arithmetic
{{ until (atoi .Config.nodeCount) }}
```

> **Defaults are applied automatically.** When a stack runs, declared `default:` values are merged into the config before the template renders. A field with a `default:` is always safe to reference as `{{ .Config.key }}` even if the user never edited it.

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
      fields: [shape, imageId, nodeCount, bootVolSizeGb, sshPublicKey]
  fields:
    imageId:
      ui_type: oci-image
      description: "Ubuntu Minimal image for your region"
    shape:
      ui_type: oci-shape
      label: "Instance Shape"
      description: "VM.Standard.A1.Flex is Always Free eligible"
    sshPublicKey:
      ui_type: ssh-public-key
      description: "Used to SSH into instances after deploy"
    nodeCount:
      description: "Number of compute nodes (1–4 for Always Free)"
```

`meta:` supports these per-field properties:

| Property | Description |
|---|---|
| `ui_type` | Override the form control. Options: `oci-image`, `oci-shape`, `ssh-public-key`, `text`, `number`, `select`, `textarea` |
| `label` | Override the auto-generated label (default: camelCase → Title Case) |
| `description` | Help text shown below the field in the stack form |

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

### Serialized Loops (NLB Pattern)

OCI Network Load Balancer rejects concurrent mutations with `409 Conflict`. Use a `dependsOn` chain:

```yaml
{{- $prevNlbResource := "my-nlb" }}
{{- range $port := list 80 443 4646 }}
  backend-set-{{ $port }}:
    type: oci:NetworkLoadBalancer/backendSet:BackendSet
    properties:
      networkLoadBalancerId: ${my-nlb.id}
      name: backend-set-{{ $port }}
      policy: FIVE_TUPLE
      healthChecker:
        protocol: TCP
        port: {{ $port }}
    options:
      dependsOn:
        - {{ printf "${%s}" $prevNlbResource }}
  {{- $prevNlbResource = printf "backend-set-%d" $port }}
{{- end }}
```

> **Note:** `{{ printf "${%s}" $prevResource }}` is the correct way to build a Pulumi interpolation string inside a Go template. Do NOT write `${{{ $prevResource }}}` — the Go template tokenizer sees `{{` and treats the inner `{` as part of the action body.

---

## Conditionals

```yaml
{{- if ne .Config.skipOptionalResource "true" }}
  optional-resource:
    type: oci:Identity/dynamicGroup:DynamicGroup
    properties:
      compartmentId: ${oci:tenancyOcid}
      name: my-dg
      matchingRule: "ALL {instance.compartment.id = '${my-compartment.id}'}"
{{- end }}
```

```yaml
{{- if .Config.adminGroupName }}
  prereq-policy:
    type: oci:Identity/policy:Policy
    properties:
      compartmentId: ${oci:tenancyOcid}
      name: prereq-policy
      description: "Admin access policy"
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

All [Sprig](https://masterminds.github.io/sprig/) functions are available (same library as Helm).

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

| nodeCount | node 0 | node 1 | node 2 | node 3 | Total |
|---|---|---|---|---|---|
| 1 | 4 | — | — | — | 4 |
| 2 | 2 | 2 | — | — | 4 |
| 3 | 1 | 1 | 2 | — | 4 |
| 4 | 1 | 1 | 1 | 1 | 4 |

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

Renders the Nomad/Consul cluster cloud-init script, gzip-compresses it, and returns it base64-encoded. Uses config values such as `nodeCount`, `nomadVersion`, `consulVersion`. If `ocpusPerNode` and `memoryGbPerNode` are present in config they take precedence over the per-node distribution logic.

```yaml
metadata:
  user_data: {{ cloudInit $i $.Config }}
```

The output is suitable for OCI instance `metadata.user_data`. `cloud-init` detects gzip via magic bytes and decompresses transparently. OCI has a 32 KB metadata limit — gzip keeps the encoded script well under that.

> **Limitation:** `cloudInit` runs at template render time, before Pulumi provisions any resources. `COMPARTMENT_OCID` and `SUBNET_OCID` are resolved at VM boot time via the OCI Instance Metadata Service (IMDS) inside `cloudinit.sh` — they are not injected at template render time. If your boot script needs a compartment or subnet OCID at render time, you must use a built-in Go program where `pulumi.All(...).ApplyT(...)` is available.

### `groupRef`

```
groupRef(groupName string, domain string, statement string) string
```

Generates an IAM policy statement for old IDCS tenancies (no domain) and new Identity Domain tenancies:

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

Resource type tokens follow the canonical pattern `oci:[Module]/[subpath]:[Resource]`. Short-form aliases (`oci:core:Vcn`) still work at runtime but will not receive schema assistance in the visual editor.

### Short-form vs Canonical

| Short-form (accepted) | Canonical (preferred) |
|---|---|
| `oci:core:Vcn` | `oci:Core/vcn:Vcn` |
| `oci:core:NatGateway` | `oci:Core/natGateway:NatGateway` |
| `oci:core:NetworkSecurityGroup` | `oci:Core/networkSecurityGroup:NetworkSecurityGroup` |
| `oci:identity:DynamicGroup` | `oci:Identity/dynamicGroup:DynamicGroup` |
| `oci:networkloadbalancer:Backend` | `oci:NetworkLoadBalancer/backend:Backend` |

**Use canonical form in new programs.** The visual editor's property autocomplete, required-field validation (Level 5), and the Resource Catalog all key off the canonical form.

### Identity

```yaml
# Compartment
my-compartment:
  type: oci:Identity/compartment:Compartment
  properties:
    compartmentId: ${oci:tenancyOcid}   # parent; use tenancyOcid for top-level
    name: {{ .Config.compartmentName }}
    description: "Created by Pulumi"
    enableDelete: true

# Dynamic Group
my-dg:
  type: oci:Identity/dynamicGroup:DynamicGroup
  properties:
    compartmentId: ${oci:tenancyOcid}
    name: my-dg
    description: "All instances in compartment"
    matchingRule: "ALL {instance.compartment.id = '${my-compartment.id}'}"

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
    availabilityDomain: ${availabilityDomains[0].name}  # from variables: fn::invoke
    displayName: my-instance
    shape: {{ .Config.shape | default "VM.Standard.A1.Flex" }}
    shapeConfig:
      ocpus: {{ instanceOcpus 0 1 }}
      memoryInGbs: {{ instanceMemoryGb 0 1 }}
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
    availabilityDomain: ${availabilityDomains[0].name}
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

# Backend — use targetId for instance-based routing
my-backend-0:
  type: oci:NetworkLoadBalancer/backend:Backend
  properties:
    networkLoadBalancerId: ${my-nlb.id}
    backendSetName: backend-set-80
    targetId: ${my-instance.id}   # use targetId for OCI instances
    port: 80

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
  type: oci:ObjectStorage/bucket:Bucket
  properties:
    compartmentId: ${my-compartment.id}
    namespace: {{ .Config.objectStorageNamespace }}
    name: {{ .Config.bucketName }}
    accessType: NoPublicAccess
    versioning: Enabled
```

### Availability Domain (variables block)

`availabilityDomain` is always resolved at apply time via a `variables:` block — never as a config field:

```yaml
variables:
  availabilityDomains:
    fn::invoke:
      function: oci:Identity/getAvailabilityDomains:getAvailabilityDomains
      arguments:
        compartmentId: ${oci:tenancyOcid}
      return: availabilityDomains

resources:
  my-instance:
    type: oci:Core/instance:Instance
    properties:
      availabilityDomain: ${availabilityDomains[0].name}
```

> `return: availabilityDomains` extracts the top-level array directly. Use `${availabilityDomains[0].name}` to access the first domain. Do not use array indexing inside the `return:` field — only top-level property names are accepted.

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

## Special Variables

```yaml
# Tenancy OCID — always available, no config needed
compartmentId: ${oci:tenancyOcid}

# Config values injected by the engine (always available)
# oci:tenancyOcid, oci:userOcid, oci:fingerprint, oci:privateKey, oci:region
```

---

## Validation

Programs are validated on every save. Validation runs six levels sequentially:

| Level | Name | What it checks |
|---|---|---|
| 1 | Template syntax | Can the Go template be parsed? |
| 2 | Template render | Can it render with all defaults applied? |
| 3 | YAML structure | Does the rendered output have `name`, `runtime: yaml`, and `resources`? |
| 4 | Config section | Are field types valid? Do `meta:` group references exist in `config:`? Empty `config:` is allowed. |
| 5 | Resource structure | Does each resource have a `type`? Are all required properties present (schema-validated)? |
| 6 | Resource types | Does each resource use a known or well-formed provider type token? |

A program that fails validation cannot be saved. Fix all errors shown in the editor panel before saving.

---

## Security

- `fn::readFile` — any line containing this is stripped before writing to disk via `SanitizeYAML()`. Programs cannot read server files.
- Programs can only call OCI APIs through the Pulumi OCI provider using credentials you have configured.
- OCI credentials are injected via Pulumi config, not environment variables (`oci:privateKey` inline — never a file path).

---

## Limitations vs Built-in Programs

| Capability | Built-in Go | YAML program |
|---|---|---|
| Loops (`range`) | Yes | Yes (via Go template) |
| Conditionals (`if`) | Yes | Yes (via Go template) |
| `pulumi.All(...).ApplyT(...)` | Yes | No — cross-resource values available only at apply time |
| Runtime OCIDs in cloud-init | Yes | No — `cloudInit()` uses static config values only |
| Arbitrary Go logic | Yes | No |
| No recompile needed | No | Yes |
| Stored in database | No | Yes |
| Editable via UI | No | Yes |

For programs requiring runtime Pulumi output chaining, use a built-in Go program. YAML programs can use `cloudInit` but it resolves only static config values — runtime OCIDs must be fetched inside the VM at boot time via the OCI IMDS.

---

## Troubleshooting

**`template: ... map has no entry for key "X"`**
: `{{ .Config.X }}` is referenced but the key has no `default:` and was never set. Add a `default:`.

**`{{ }}` syntax in comments causes a template parse error**
: Replace `{{ ... }}` in YAML comments with plain text.

**`yaml: unmarshal errors` after rendering**
: Rendered YAML is invalid. Common causes: unquoted string values with colons (use `| quote`), mismatched indentation inside `range` blocks.

**`fn::readFile` lines disappear**
: Intentional security sanitization. Use config fields to pass in file content instead.

**`atoi: parsing "": invalid syntax`**
: A config field used with `atoi` has no `default:` or the default is not a number string.

**`unable to find function oci:identity:getAvailabilityDomains`**
: Use the v4 canonical function token: `oci:Identity/getAvailabilityDomains:getAvailabilityDomains`.

**`availabilityDomains[0].name does not exist`**
: The `return:` field only accepts top-level property names. Use `return: availabilityDomains` and access with `${availabilityDomains[0].name}`.

**Visual editor shows no property autocomplete for a resource type**
: Use canonical type form (`oci:Core/natGateway:NatGateway` instead of `oci:core:NatGateway`). Only canonical forms are covered by the fallback schema.

**NLB Backend: `ipAddress` validation error when using `targetId`**
: Use `targetId: ${my-instance.id}` for OCI instances. `ipAddress` is only needed when routing to an external IP.

**NLB Backend: `targetId=null`**
: When using `getInstancePoolInstances` via `fn::invoke`, use `return: instances` and access with `${pool-instances[N].id}` (not `.instanceId` — use `.id`).

**`Metadata size is X bytes and cannot be larger than 32000 bytes`**
: The `cloudInit` template function gzip+base64 encodes the script. If you are base64-encoding manually without gzip, the result exceeds the 32 KB OCI metadata limit.

---

## Reference Programs

Two complete programs are available in the `docs/` directory:

| File | Description |
|---|---|
| `docs/nomad-cluster-program.yaml` | v1 — short-form type aliases. Functional but does not benefit from schema autocomplete. |
| `docs/nomad-cluster-v2-program.yaml` | v2 — canonical type names, 13 IAM policy statements, NLB NSG association, configurable backup retention, `getInstancePoolInstances` for NLB backend target IDs. |

The v2 program is the recommended starting point for new Nomad + Consul clusters.
