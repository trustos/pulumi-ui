# Dynamic Programs: Execution Model

This document describes the hybrid program execution model — how built-in Go programs and user-defined YAML programs are executed differently.

---

## Two Execution Paths

| Type | Stored as | Executed via | Examples |
|---|---|---|---|
| Built-in | Go source (compiled) | `UpsertStackInlineSource` | `nomad-cluster`, `test-vcn` |
| User-defined YAML | Go-templated Pulumi YAML in `custom_programs` DB table | `UpsertStackLocalSource` | VCN, bucket, single instance, DNS zone |

---

## Why Pulumi YAML for User-defined Programs

Pulumi has a first-class YAML runtime (`runtime: yaml`). A YAML program lives in a directory with a `Pulumi.yaml` file and is executed by `pulumi-language-yaml`, which ships inside the Pulumi CLI tarball installed in the Docker image. No additional Docker changes are needed.

Pure Pulumi YAML has limitations — it cannot express loops, conditionals, or dynamic computation. To overcome this, programs are stored as **Go-templated YAML** (exactly like Helm templates for Kubernetes YAML). The Go `text/template` engine renders structural decisions (how many nodes, whether IAM is skipped) before Pulumi runs; Pulumi then resolves cross-resource references (`${resource.property}`) at apply time.

---

## Evaluated Alternatives (Rejected)

### Custom TOML DSL → Go SDK Translation
Rejected: enormous implementation effort, same expressiveness limits as plain YAML, permanent maintenance burden as the OCI provider evolves, and it would reinvent Pulumi YAML with no documentation or community support.

### Go Code Generation + Runtime Compilation
Rejected: requires adding the Go toolchain (~600 MB) to the Docker image, 30–120 second cold build time, complex module cache management in containers, and arbitrary code execution by anyone with DB write access.

---

## Engine Resolution

The engine checks whether a program implements `YAMLProgramProvider` via type assertion:

```go
func (e *Engine) resolveStack(ctx, stackName string, prog Program, cfg map[string]string, envVars map[string]string, creds Credentials) (auto.Stack, func(), error) {
    if yp, ok := prog.(YAMLProgramProvider); ok {
        return e.getOrCreateYAMLStack(ctx, stackName, yp, cfg, envVars, creds)
    }
    stack, err := e.getOrCreateStack(ctx, stackName, prog, cfg, envVars)
    return stack, func() {}, err
}
```

This means all four operations (Up, Destroy, Refresh, Preview) automatically use the correct path for any program type.

---

## OCI Credential Injection Difference

Built-in Go programs read OCI credentials from environment variables (`OCI_TENANCY_OCID`, `OCI_USER_OCID`, etc.) set by `buildEnvVars()`. YAML programs cannot read environment variables directly — the Pulumi OCI provider (a Terraform bridge) reads its configuration from Pulumi config keys. The engine injects credentials via `stack.SetConfig()` after creating the YAML stack:

```go
stack.SetConfig(ctx, "oci:tenancyOcid",    auto.ConfigValue{Value: oci.TenancyOCID})
stack.SetConfig(ctx, "oci:userOcid",       auto.ConfigValue{Value: oci.UserOCID})
stack.SetConfig(ctx, "oci:fingerprint",    auto.ConfigValue{Value: oci.Fingerprint})
stack.SetConfig(ctx, "oci:privateKeyPath", auto.ConfigValue{Value: keyPath})
stack.SetConfig(ctx, "oci:region",         auto.ConfigValue{Value: oci.Region})
```

---

## Security

YAML programs are declarative and cannot execute arbitrary code. The only actions they take are OCI API calls via the Pulumi OCI provider, using credentials the user provided. This is identical risk to built-in programs.

One specific concern: `fn::readFile`. Pulumi YAML supports `fn::readFile: /some/path`, which reads server filesystem files. **Mitigation:** `programs.SanitizeYAML()` strips any line containing `fn::readFile` before the rendered YAML is written to disk.
