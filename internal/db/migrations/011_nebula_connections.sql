-- Migration 005 created stack_connections with (nomad_addr, nomad_token).
-- That schema is incompatible with the Nebula mesh design and was never
-- populated in production. SQLite cannot ALTER columns, so drop and recreate.

DROP TABLE IF EXISTS stack_connections;

CREATE TABLE IF NOT EXISTS stack_connections (
    stack_name       TEXT NOT NULL PRIMARY KEY REFERENCES stacks(name) ON DELETE CASCADE,
    nebula_ca_cert   BLOB NOT NULL,      -- Nebula CA certificate (PEM)
    nebula_ca_key    BLOB NOT NULL,      -- Nebula CA private key (AES-GCM encrypted)
    nebula_ui_cert   BLOB NOT NULL,      -- pulumi-ui's Nebula cert (PEM)
    nebula_ui_key    BLOB NOT NULL,      -- pulumi-ui's Nebula private key (AES-GCM encrypted)
    nebula_subnet    TEXT NOT NULL,       -- assigned /24, e.g. "10.42.7.0/24"
    lighthouse_addr  TEXT,               -- "nlb-ip:41820"; NULL until deploy-apps completes
    agent_nebula_ip  TEXT,               -- agent's Nebula virtual IP; NULL until connected
    connected_at     INTEGER NOT NULL DEFAULT (unixepoch()),
    last_seen_at     INTEGER,
    cluster_info     TEXT                 -- JSON: nomad version, node count, etc.
);

CREATE TABLE IF NOT EXISTS nebula_subnet_counter (
    id    INTEGER PRIMARY KEY CHECK (id = 1),  -- singleton row
    next  INTEGER NOT NULL DEFAULT 1           -- next /24 index (1 → 10.42.1.0/24, etc.)
);
INSERT OR IGNORE INTO nebula_subnet_counter (id, next) VALUES (1, 1);
