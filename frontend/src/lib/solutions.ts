/**
 * Solution cards define pre-configured deployment recipes.
 * Each card maps to a program + pre-selected applications with
 * minimal user input. The derivedConfig function transforms user
 * fields into the full config + appConfig needed by putStack().
 */

export interface SolutionField {
  key: string;
  label: string;
  type: "text" | "email";
  required: boolean;
  placeholder?: string;
  description?: string;
}

export interface SolutionCard {
  id: string;
  name: string;
  description: string;
  icon: string; // emoji or short text
  program: string; // program name (e.g., "nomad-cluster")
  applications: string[]; // app keys to pre-select
  userFields: SolutionField[];
  /** Default infra config overrides (merged with program defaults) */
  configOverrides?: Record<string, string>;
  /** Transform user input into full putStack parameters */
  deriveConfig: (input: Record<string, string>) => {
    config: Record<string, string>;
    applications: Record<string, boolean>;
    appConfig: Record<string, string>;
  };
}

// ── Solution: NocoBase ──────────────────────────────────────────────────

const nocobaseSolution: SolutionCard = {
  id: "nocobase",
  name: "NocoBase",
  description:
    "No-code platform with PostgreSQL, Traefik ingress, and automated backups on a Nomad cluster",
  icon: "🧩",
  program: "nomad-cluster",
  applications: ["traefik", "postgres", "pgadmin", "postgres-backup", "nocobase"],
  configOverrides: { nodeCount: "1" },
  userFields: [
    {
      key: "email",
      label: "Email",
      type: "email",
      required: true,
      placeholder: "admin@example.com",
      description: "Used for Let's Encrypt certificates and pgAdmin login",
    },
  ],
  deriveConfig: (input) => {
    return {
      config: {
        nodeCount: "1",
        compartmentName: "nomad-compartment",
        compartmentDescription: "Compartment for Nomad cluster",
        vcnCidr: "10.0.0.0/16",
        publicSubnetCidr: "10.0.1.0/24",
        privateSubnetCidr: "10.0.2.0/24",
        shape: "VM.Standard.A1.Flex",
        ocpusPerNode: "4",
        memoryGbPerNode: "24",
        bootVolSizeGb: "200",
        nomadVersion: "1.11.3",
        consulVersion: "1.22.6",
      },
      applications: {
        traefik: true,
        postgres: true,
        pgadmin: true,
        "postgres-backup": true,
        nocobase: true,
      },
      appConfig: {
        "traefik.acmeEmail": input.email || "",
        "postgres.dbUser": "admin",
        "pgadmin.email": input.email || "",
        "nocobase.dbName": "nocobase",
        // Domains are set post-deploy via the Applications tab
        // dbPassword and appKey are auto-generated via Consul KV
      },
    };
  },
};

// ── Solution: Nomad Cluster (infra only) ────────────────────────────────

const nomadClusterSolution: SolutionCard = {
  id: "nomad-cluster",
  name: "Nomad Cluster",
  description:
    "Docker + Consul + Nomad infrastructure on OCI ARM instances. Add applications later.",
  icon: "⚡",
  program: "nomad-cluster",
  applications: ["traefik"],
  userFields: [
    {
      key: "email",
      label: "Email",
      type: "email",
      required: true,
      placeholder: "admin@example.com",
      description: "Used for Let's Encrypt certificates",
    },
  ],
  deriveConfig: (input) => ({
    config: {
      nodeCount: "3",
      compartmentName: "nomad-compartment",
      compartmentDescription: "Compartment for Nomad cluster",
      vcnCidr: "10.0.0.0/16",
      publicSubnetCidr: "10.0.1.0/24",
      privateSubnetCidr: "10.0.2.0/24",
      shape: "VM.Standard.A1.Flex",
      ocpusPerNode: "1",
      memoryGbPerNode: "6",
      bootVolSizeGb: "50",
      nomadVersion: "1.11.3",
      consulVersion: "1.22.6",
    },
    applications: { traefik: true },
    appConfig: {
      "traefik.acmeEmail": input.email || "",
    },
  }),
};

// ── Export ───────────────────────────────────────────────────────────────

export const solutions: SolutionCard[] = [
  nocobaseSolution,
  nomadClusterSolution,
];
