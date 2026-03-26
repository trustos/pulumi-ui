-- Per-node Nebula certificates for multi-instance stacks.
-- Each OCI instance gets its own identity so the UI server can open
-- independent peer-to-peer Nebula tunnels to every node.
--
-- node_index: 0-based; Nebula IP is subnet.2 + node_index
--   e.g. node 0 → 10.42.1.2, node 1 → 10.42.1.3, ...
--
-- agent_real_ip: public IP (or NLB IP) discovered after deploy,
--   written by engine post-up scan of instance-{i}-publicIp outputs.

CREATE TABLE stack_node_certs (
    stack_name    TEXT    NOT NULL,
    node_index    INTEGER NOT NULL,
    nebula_cert   BLOB    NOT NULL,   -- Nebula node certificate (PEM, plaintext)
    nebula_key    BLOB    NOT NULL,   -- Nebula node private key (AES-GCM encrypted)
    nebula_ip     TEXT    NOT NULL,   -- Nebula IP with prefix, e.g. "10.42.1.2/24"
    agent_real_ip TEXT,              -- public IP discovered after deploy
    PRIMARY KEY (stack_name, node_index)
);
