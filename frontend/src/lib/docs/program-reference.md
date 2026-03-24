# YAML Program Reference

A YAML program lets you define infrastructure as a Go-templated Pulumi YAML file stored in the database. Once created, it appears in the **New Stack** dialog alongside built-in programs and can be edited or deleted at any time — no server restart required.

---

## Editing Modes

Every program opens in one of two modes, switchable at any time via the **Visual / YAML** toggle.

### Visual Editor

A structured, form-based editor for building programs without writing YAML. Use it to:

- Define **Config Fields** — the parameters shown when creating a stack
- Add **Sections** — logical groups of resources (e.g. "Networking", "Compute")
- Add **Resources**, **Loops**, and **Conditionals** to each section
- Define **Outputs** — values exposed after a successful deploy

#### Config Fields panel (right sidebar, top)

Each config field has:

| Property | Required | Description |
|---|---|---|
| `key` | Yes | camelCase identifier, e.g. `nodeCount`. Referenced as `{{ .Config.nodeCount }}` in YAML |
| `type` | Yes | `string`, `integer`, `number`, or `boolean` |
| `default` | No | Pre-filled value in the stack form |
| `description` | No | Help text shown below the field in the stack form |
| `group` | No | Group name — fields with the same group are visually grouped in the form |

The **key name** also determines the form control type by convention:

| Key | Form control |
|---|---|
| `imageId` | OCI image picker |
| `shape` | OCI shape picker |
| `sshPublicKey` | SSH key picker |

#### Outputs panel (right sidebar, bottom)

Each output entry is a key-value pair. The value is a Pulumi interpolation, e.g. `${my-instance.publicIp}`. Outputs are exposed in the Stack detail view after a successful deploy.

#### Sections (left sidebar)

Sections are cosmetic containers — they map to `# --- section: id ---` markers in the generated YAML. Use them to split large programs into focused chunks (Networking, Compute, Load Balancer, etc.).

#### Resources

Click **+ Resource** (or **+ Resource** inside a loop) to open the Resource Catalog. Select a resource type, or add one directly by typing the type into the resource card's **Type** field.

When you set a type and leave the field (blur), all required properties for that type are automatically added as empty rows — so you only need to fill in values, not figure out which properties are mandatory.

**Property value picker** — each property value field has a `⊕` button on the right. Clicking it shows a dropdown of all defined config fields, variables, and resource outputs. Selecting one inserts the appropriate reference — `{{ .Config.fieldKey }}` for config, `${varName}` for variables, or `${resource.attr}` for resources.

**Structured object properties** — properties like `createVnicDetails`, `sourceDetails`, `shapeConfig`, and `routeRules` have sub-field definitions from the OCI provider schema. Instead of a raw text input, these properties show a structured editor with:
- Named sub-field rows (e.g. `subnetId`, `assignPublicIp` for `createVnicDetails`)
- Required sub-fields marked with `*`
- The same `⊕` reference picker on each sub-field
- Config and resource reference chips (colored badges showing `config fieldName` or `ref resource.id`)
- Optional sub-fields available via `+ fieldName` buttons
- For array properties (like `routeRules`), add/remove item controls

If the structured editor cannot parse an existing value, it falls back to a raw text input.

**Renaming a resource** — when you change a resource's name in the visual editor (the text field at the top of the resource card), all references to it are updated automatically when you leave the field:
- `${oldName.id}` becomes `${newName.id}` in all property values
- `${oldName[0].name}` becomes `${newName[0].name}` in indexed references
- `dependsOn` checkboxes update to reference the new name
- Output values like `${oldName.publicIp}` update to `${newName.publicIp}`

This works across all sections, including inside loops and conditionals.

In **YAML mode**, place your cursor on a resource name and press **F2** (or right-click → "Rename Resource"). Type the new name — all `${oldName...}` references are updated automatically.

#### Loop blocks

A loop repeats all resources inside it for each iteration. Three source modes:

| Mode | Generated template | Use case |
|---|---|---|
| N times (from config field) | `{{- range $i := until (atoi $.Config.nodeCount) }}` | Cluster nodes, replicated resources |
| Fixed list of values | `{{- range $port := list 80 443 8080 }}` | Per-port NLB rules |
| Custom expression | `{{- range $x := ... }}` | Advanced — any Sprig expression |

**Variable** — defaults to `$i` for numeric loops, `$port` for list loops. Must start with `$`.

**Serialize operations** — adds a `dependsOn` chain between resources inside the loop so they are created sequentially. Required for OCI Network Load Balancer port mutations (OCI returns `409 Conflict` if NLB resources are mutated concurrently).

Loops can be nested. Resource names inside a loop are automatically suffixed with the loop variable (`instance` → `instance-{{ $i }}`).

#### Conditional blocks

Wraps resources in a `{{- if condition }}...{{- else }}...{{- end }}` block. Type the condition expression directly, e.g. `eq .Config.enableMonitoring "true"`.

#### Synced / YAML-edited status

The mode bar shows:

| Status | Meaning |
|---|---|
| **Synced** | Visual model and YAML are in sync |
| **Edited in YAML** | YAML was changed; switching to Visual will re-parse it |
| **Partially structured** | Some sections contain advanced templating shown as code blocks |

### YAML Editor

A Monaco editor with YAML syntax highlighting and live validation squiggles. Edit the raw Go-templated Pulumi YAML directly. Validation errors appear as red underlines on the relevant line.

---

## How It Works

```
Your template (stored in DB)
        │
        ▼  text/template.Execute()
        │  with stack config values + Sprig + OCI helpers
        ▼
Plain Pulumi YAML
        │
        ▼  Agent bootstrap injection (if ApplicationProvider or agentAccess)
        │  Detects compute resources, composes multipart MIME user_data
        ▼
        ▼  Networking injection (if agentAccess)
        │  Auto-adds NSG rules + NLB resources for agent port
        ▼
Final Pulumi YAML (written to temp dir)
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

### Meta Section (UI Groups, Labels, Descriptions)

Add a `meta:` block at the top of your file to enrich the stack config form. It is stripped before Pulumi executes the program.

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

`meta:` supports these properties per field:

| Property | Description |
|---|---|
| `ui_type` | Override the form control. Options: `oci-image`, `oci-shape`, `ssh-public-key`, `text`, `number`, `select`, `textarea` |
| `label` | Override the auto-generated label (default: camelCase → Title Case) |
| `description` | Help text shown below the field in the stack form |

Fields not listed in any group appear at the bottom ungrouped.

`meta:` also supports a top-level `agentAccess` flag:

```yaml
meta:
  agentAccess: true
```

When `agentAccess: true`, the engine automatically injects the Nebula mesh + agent bootstrap into compute resources and adds NSG/NLB networking rules for agent connectivity. See the [Agent Bootstrap Injection](#agent-bootstrap-injection) section for details.

**You can toggle this from the editor UI** — the **Agent Connect** button in the program editor header works in both visual and YAML modes. When enabled, an informational banner lists exactly what will be auto-injected at deploy time:
- `user_data` — Nebula mesh + agent bootstrap on each compute instance
- NSG security rules — UDP ingress on port 41820
- NLB backend set + listener — UDP health check and listener on port 41820
- NLB backends — links each compute instance to the backend set

Injected resources use the `__agent_` prefix to avoid naming collisions with your resources.

The visual editor writes `meta:` automatically when you add groups or descriptions to config fields via the Config Fields panel.

---

## Resource Types

Resource type tokens follow the canonical Pulumi format `provider:Module/subtype:Resource`.

```
oci:Core/instance:Instance
oci:Core/vcn:Vcn
oci:Identity/compartment:Compartment
oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer
```

The shorthand `oci:core:Instance` also works but the full form is preferred — it is what the visual editor generates and what the OCI schema references.

### Required Properties

Every known resource type has a set of required properties. Programs cannot be saved if a resource is missing any of them. The required properties come from the OCI provider schema (served at `/api/oci-schema`) and are automatically added as empty rows when you set a resource type in the visual editor.

Common required properties:

| Resource type | Required properties |
|---|---|
| `oci:Core/instance:Instance` | `compartmentId`, `availabilityDomain`, `shape`, `sourceDetails`, `createVnicDetails` |
| `oci:Core/vcn:Vcn` | `compartmentId`, `cidrBlock` |
| `oci:Core/subnet:Subnet` | `compartmentId`, `vcnId`, `cidrBlock` |
| `oci:Core/internetGateway:InternetGateway` | `compartmentId`, `vcnId` |
| `oci:Core/routeTable:RouteTable` | `compartmentId`, `vcnId` |
| `oci:Core/securityList:SecurityList` | `compartmentId`, `vcnId` |
| `oci:Identity/compartment:Compartment` | `compartmentId`, `name`, `description` |
| `oci:Identity/policy:Policy` | `compartmentId`, `name`, `description`, `statements` |
| `oci:Core/instanceConfiguration:InstanceConfiguration` | `compartmentId`, `instanceDetails` |
| `oci:Core/instancePool:InstancePool` | `compartmentId`, `instanceConfigurationId`, `size`, `placementConfigurations` |
| `oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer` | `compartmentId`, `displayName`, `subnetId` |
| `oci:NetworkLoadBalancer/backendSet:BackendSet` | `networkLoadBalancerId`, `name`, `policy`, `healthChecker` |
| `oci:NetworkLoadBalancer/backend:Backend` | `networkLoadBalancerId`, `backendSetName`, `port`, `ipAddress` |
| `oci:NetworkLoadBalancer/listener:Listener` | `networkLoadBalancerId`, `name`, `defaultBackendSetName`, `port`, `protocol` |

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

### Serialized Loops (NLB Pattern)

OCI Network Load Balancer rejects concurrent mutations with `409 Conflict`. Use a `dependsOn` chain to ensure sequential execution:

```yaml
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
{{- end }}
```

In the visual editor, enable **Serialize operations** on a loop to generate this pattern automatically.

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

Renders the program-specific cloud-init script using Go `text/template`, gzip-compresses it, and returns it base64-encoded. The script conditionally installs applications (Docker, Consul, Nomad) based on which applications the program declares. Uses config values such as `nodeCount`, `nomadVersion`, `consulVersion`. If `ocpusPerNode` and `memoryGbPerNode` are present they take precedence over the per-node distribution logic.

```yaml
metadata:
  user_data: {{ cloudInit $i $.Config }}
```

The output is suitable for OCI instance `metadata.user_data`. `cloud-init` detects gzip via magic bytes and decompresses transparently. OCI has a 32 KB metadata limit — gzip keeps the encoded script well under that.

> **Note:** The management agent (Nebula mesh + pulumi-ui-agent) is **not** included in the `cloudInit` output. Agent injection is handled automatically by the engine for all compute resources — see [Agent Bootstrap Injection](#agent-bootstrap-injection) below.

> **Limitation:** `COMPARTMENT_OCID` and `SUBNET_OCID` are resolved at VM boot time via the OCI Instance Metadata Service (IMDS) inside `cloudinit.sh` — they are not injected at template render time.

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

## OCI Resource Reference

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
    availabilityDomain: {{ .Config.availabilityDomain }}
    shape: {{ .Config.shape | default "VM.Standard.A1.Flex" }}
    displayName: my-instance
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

# Instance Configuration (for InstancePool-based clusters)
my-ic:
  type: oci:Core/instanceConfiguration:InstanceConfiguration
  properties:
    compartmentId: ${my-compartment.id}
    displayName: my-ic
    instanceDetails:
      instanceType: compute
      launchDetails:
        compartmentId: ${my-compartment.id}
        availabilityDomain: ${availabilityDomains[0].name}
        shape: {{ .Config.shape | quote }}
        shapeConfig:
          ocpus: {{ .Config.ocpusPerNode }}
          memoryInGbs: {{ .Config.memoryGbPerNode }}
        sourceDetails:
          sourceType: image
          imageId: {{ .Config.imageId | quote }}
          bootVolumeSizeInGbs: {{ .Config.bootVolSizeGb | quote }}
        createVnicDetails:
          subnetId: ${my-subnet.id}
          assignPublicIp: false
          nsgIds:
            - ${my-nsg.id}
        metadata:
          ssh_authorized_keys: {{ .Config.sshPublicKey | quote }}
          user_data: {{ cloudInit 0 .Config }}

# Instance Pool (homogeneous cluster)
my-pool:
  type: oci:Core/instancePool:InstancePool
  options:
    dependsOn:
      - ${my-ic}
  properties:
    compartmentId: ${my-compartment.id}
    instanceConfigurationId: ${my-ic.id}
    size: {{ .Config.nodeCount }}
    displayName: my-pool
    placementConfigurations:
      - availabilityDomain: ${availabilityDomains[0].name}
        primarySubnetId: ${my-subnet.id}

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
```

### Load Balancer

```yaml
# Network Load Balancer
my-nlb:
  type: oci:NetworkLoadBalancer/networkLoadBalancer:NetworkLoadBalancer
  properties:
    compartmentId: ${my-compartment.id}
    displayName: my-nlb
    subnetId: ${my-subnet.id}
    isPrivate: false

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

# Backend
my-backend-0:
  type: oci:NetworkLoadBalancer/backend:Backend
  properties:
    networkLoadBalancerId: ${my-nlb.id}
    backendSetName: backend-set-80
    ipAddress: ${my-instance.privateIp}
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

---

## Special Variables

```yaml
# Tenancy OCID — always available, no config needed
compartmentId: ${oci:tenancyOcid}

# OCI provider config keys injected by the engine (always available):
# oci:tenancyOcid, oci:userOcid, oci:fingerprint, oci:privateKey, oci:region
```

---

## Agent Bootstrap Injection

When a stack is deployed, the engine **automatically** injects a management agent into every compute resource's `user_data`. You do not need to add this yourself — it happens transparently after your template renders.

### What gets injected

- **Nebula mesh** — a lightweight overlay network (UDP port 41820) that allows pulumi-ui to communicate securely with each instance over a private encrypted tunnel, even if the instances are in private subnets with no public IP.
- **pulumi-ui-agent** — a small HTTP agent that exposes `/exec`, `/upload`, `/health`, and `/services` endpoints over the Nebula tunnel. This enables post-deploy application management.

### Which resources are affected

The engine detects compute resource types that accept `user_data` metadata:

| Resource type | user_data path |
|---|---|
| `oci:Core/instance:Instance` | `metadata.user_data` |
| `oci:Core/instanceConfiguration:InstanceConfiguration` | `instanceDetails.launchDetails.metadata.user_data` |

If your program already sets `user_data` (e.g. via `cloudInit`), the engine wraps both scripts in a multipart MIME message so `cloud-init` executes them in order.

### What you need to know

- **Not automatic for all programs** — agent injection is active only when a program implements `ApplicationProvider` (built-in Go programs with an application catalog) or declares `meta.agentAccess: true` (YAML programs opting into agent connectivity). Programs without either are unaffected.
- **Networking auto-injection** — for YAML programs with `meta.agentAccess: true`, the engine also auto-adds NSG security rules (UDP 41820) and NLB backend set/listener for the agent port. Built-in Go programs manage their own networking.
- **Per-stack PKI** — each stack gets its own certificate authority. The agent authenticates to pulumi-ui using per-stack Nebula certificates.
- **Do not hardcode agent/Nebula setup** in your `cloudinit.sh` or YAML templates. The engine handles it when enabled.

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

Outputs appear in the Stack detail view and can be referenced by other stacks or external tooling.

---

## Validation

Programs are validated on every save (and checked live as you type in the YAML editor). Validation runs six levels sequentially:

| Level | Name | What it checks |
|---|---|---|
| 1 | Template syntax | Can the Go template be parsed? |
| 2 | Template render | Can it render with all defaults applied? |
| 3 | YAML structure | Does the rendered output have `name`, `runtime: yaml`, and `resources`? |
| 4 | Config section | Are field types valid? Do `meta:` group references exist in `config:`? |
| 5 | Resource structure | Does each resource have a valid type token? Are all required properties present (schema-validated)? |
| 6 | Variable references | Does every `${varName}` in resource properties reference a name defined in `variables:` or `resources:`? Provider config refs (containing `:`) are skipped. |

A program that fails any level cannot be saved. The visual editor shows errors inline before even calling the backend. In visual mode, `collectVisualErrors()` additionally checks for undefined `${varName}` references client-side.

---

## Security

- `fn::readFile` — any line containing this is stripped before writing to disk. Programs cannot read server files.
- Programs can only call OCI APIs through the Pulumi OCI provider using the credentials configured for the account.
- OCI credentials are injected via Pulumi config, not environment variables (`oci:privateKey` inline — never a file path).

---

## Limitations vs Built-in Programs

| Capability | Built-in Go | YAML program |
|---|---|---|
| Loops | Yes (Go `for`) | Yes (Go template `range`) |
| Conditionals | Yes | Yes (Go template `if`) |
| Cross-resource values in cloud-init | Yes (`pulumi.All(...).ApplyT(...)`) | No — only static config values |
| Arbitrary Go logic | Yes | No |
| No server restart needed | No | Yes |
| Editable via visual editor | No | Yes |
| Stored in database | No | Yes |
| Agent bootstrap injection | Automatic (when `ApplicationProvider`) | Automatic (when `ApplicationProvider` or `meta.agentAccess: true`) |
| Agent networking injection | Manual (program provisions NSG/NLB) | Automatic when `meta.agentAccess: true` |

Agent bootstrap injection requires the program to implement `ApplicationProvider` (built-in Go) or declare `meta.agentAccess: true` (YAML). YAML programs with `agentAccess` also get automatic NSG rules and NLB resources for the agent port. If your program needs to pass a runtime-resolved OCID (e.g. a subnet ID) into a cloud-init script, you must implement it as a built-in Go program.

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
  fields:
    imageId:
      ui_type: oci-image
      description: "Ubuntu Minimal recommended"
    shape:
      ui_type: oci-shape
      description: "VM.Standard.A1.Flex is Always Free eligible"
    sshPublicKey:
      ui_type: ssh-public-key
    nodeCount:
      description: "Number of nodes (1–4 for Always Free)"

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
      shape: {{ $.Config.shape }}
      displayName: {{ printf "node-%d" $i }}
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

## Troubleshooting

**`template: ... map has no entry for key "X"`**
: `{{ .Config.X }}` is referenced but the key has no `default:` and was never set by the user. Add a `default:` to the config field.

**`{{ }}` syntax in comments causes a template parse error**
: Go template processes the entire file including YAML comments. Replace `{{ ... }}` in comments with plain text.

**`yaml: unmarshal errors` after rendering**
: The rendered YAML is invalid. Common causes: unquoted string values with colons (use `| quote`), mismatched indentation inside `range` blocks.

**`fn::readFile` lines disappear**
: Intentional security sanitization. Use config fields to pass in file content instead.

**Pulumi `${resource.property}` not resolving**
: The resource name in `${ }` must exactly match the resource key after Go template rendering (after `{{ range }}` expands).

**`atoi: parsing "": invalid syntax`**
: A config field used with `atoi` has no `default:`. Add `default: "1"` (or any integer string) to the config block.

**Missing required property errors on save**
: The visual editor and validation pipeline check that all required properties are present. Use the auto-populate feature: set the resource type and leave the field — required properties are added automatically. Fill in values using the `{}` config field picker.

**Duplicate resource key errors**
: Resources inside a loop must have unique names per iteration. The visual editor appends the loop variable automatically (`instance` → `instance-{{ $i }}`). In YAML mode, include the variable explicitly in the resource name.
