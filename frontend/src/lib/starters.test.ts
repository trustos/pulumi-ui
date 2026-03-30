import { describe, it, expect } from "vitest";
import { starters } from "./starters";

describe("NocoBase starter", () => {
  const nocobase = starters.find((s) => s.id === "nocobase")!;

  it("exists", () => {
    expect(nocobase).toBeDefined();
    expect(nocobase.blueprint).toBe("nomad-full-stack");
  });

  it("pre-selects correct applications", () => {
    expect(nocobase.applications).toContain("traefik");
    expect(nocobase.applications).toContain("postgres");
    expect(nocobase.applications).toContain("pgadmin");
    expect(nocobase.applications).toContain("postgres-backup");
    expect(nocobase.applications).toContain("nocobase");
  });

  it("derives config with all infrastructure defaults", () => {
    const result = nocobase.deriveConfig({ email: "admin@example.com" });

    expect(result.config.nodeCount).toBe("1");
    expect(result.config.compartmentName).toBe("nocobase-nomad");
    expect(result.config.vcnCidr).toBe("10.0.0.0/16");
    expect(result.config.shape).toBe("VM.Standard.A1.Flex");
    expect(result.config.nomadVersion).toBeTruthy();
    expect(result.config.consulVersion).toBeTruthy();
  });

  it("derives compute resource defaults for single-node (4 ocpus, 24gb, 200gb)", () => {
    const result = nocobase.deriveConfig({});

    expect(result.config.ocpusPerNode).toBe("4");
    expect(result.config.memoryGbPerNode).toBe("24");
    expect(result.config.bootVolSizeGb).toBe("200");
  });

  it("derives compartmentName default", () => {
    const result = nocobase.deriveConfig({});
    expect(result.config.compartmentName).toBe("nocobase-nomad");
  });

  it("derives appConfig from email", () => {
    const result = nocobase.deriveConfig({ email: "admin@test.com" });

    expect(result.appConfig["traefik.acmeEmail"]).toBe("admin@test.com");
    expect(result.appConfig["pgadmin.email"]).toBe("admin@test.com");
    expect(result.appConfig["nocobase.dbName"]).toBe("nocobase");
  });

  it("selects all required applications", () => {
    const result = nocobase.deriveConfig({ email: "x@x.com" });

    expect(result.applications.traefik).toBe(true);
    expect(result.applications.postgres).toBe(true);
    expect(result.applications.pgadmin).toBe(true);
    expect(result.applications["postgres-backup"]).toBe(true);
    expect(result.applications.nocobase).toBe(true);
  });

  it("requires only email field", () => {
    const required = nocobase.userFields.filter((f) => f.required);
    expect(required).toHaveLength(1);
    expect(required[0].key).toBe("email");
  });

  it("does not include backupSchedule in deriveConfig (wizard handles it separately)", () => {
    const result = nocobase.deriveConfig({ email: "admin@test.com" });

    // backupSchedule is added by the StarterWizard component (hardcoded default '0 4 * * *'),
    // NOT by deriveConfig. Verify it's absent from both config and appConfig.
    expect(result.config).not.toHaveProperty("backupSchedule");
    expect(result.appConfig).not.toHaveProperty(
      "postgres-backup.backupSchedule",
    );
  });

  it("includes all expected infrastructure config fields", () => {
    const result = nocobase.deriveConfig({});
    const configKeys = Object.keys(result.config);

    // Verify all fields that the wizard initializes from are present
    expect(configKeys).toContain("nodeCount");
    expect(configKeys).toContain("compartmentName");
    expect(configKeys).toContain("vcnCidr");
    expect(configKeys).toContain("publicSubnetCidr");
    expect(configKeys).toContain("privateSubnetCidr");
    expect(configKeys).toContain("shape");
    expect(configKeys).toContain("ocpusPerNode");
    expect(configKeys).toContain("memoryGbPerNode");
    expect(configKeys).toContain("bootVolSizeGb");
    expect(configKeys).toContain("nomadVersion");
    expect(configKeys).toContain("consulVersion");
  });
});

describe("Nomad Cluster starter", () => {
  const nomad = starters.find((s) => s.id === "nomad-cluster")!;

  it("exists", () => {
    expect(nomad).toBeDefined();
    expect(nomad.blueprint).toBe("nomad-cluster");
  });

  it("has no pre-selected applications (infra only)", () => {
    expect(nomad.applications).toEqual([]);
  });

  it("has no user fields", () => {
    expect(nomad.userFields).toEqual([]);
  });

  it("derives config with 3 nodes by default", () => {
    const result = nomad.deriveConfig({});
    expect(result.config.nodeCount).toBe("3");
  });

  it("derives compute resource defaults for multi-node (1 ocpu, 6gb, 50gb)", () => {
    const result = nomad.deriveConfig({});

    expect(result.config.ocpusPerNode).toBe("1");
    expect(result.config.memoryGbPerNode).toBe("6");
    expect(result.config.bootVolSizeGb).toBe("50");
  });

  it("derives compartmentName default", () => {
    const result = nomad.deriveConfig({});
    expect(result.config.compartmentName).toBe("nomad-compartment");
  });

  it("derives empty applications and appConfig", () => {
    const result = nomad.deriveConfig({});
    expect(result.applications).toEqual({});
    expect(result.appConfig).toEqual({});
  });
});
