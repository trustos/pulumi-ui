-- Add dedicated agent cert/key (separate identity from UI cert) and
-- per-stack auth token. Also add agent_real_ip for the instance's actual
-- public/NLB IP (used in Nebula static_host_map).

ALTER TABLE stack_connections ADD COLUMN agent_cert  BLOB;      -- Nebula agent certificate (PEM)
ALTER TABLE stack_connections ADD COLUMN agent_key   BLOB;      -- Nebula agent private key (AES-GCM encrypted)
ALTER TABLE stack_connections ADD COLUMN agent_token TEXT NOT NULL DEFAULT '';  -- per-stack Bearer token (hex)
ALTER TABLE stack_connections ADD COLUMN agent_real_ip TEXT;     -- public or NLB IP for reaching the agent
