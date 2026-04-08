-- Deployment groups: coordinated multi-account stack deployments.
-- A group links multiple stacks with defined roles and deployment order.
-- The blueprint's meta.multiAccount wiring declarations control how
-- outputs from one role's deployment feed into another role's config.

CREATE TABLE IF NOT EXISTS deployment_groups (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    blueprint   TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'configuring',
    shared_config TEXT,  -- JSON: config fields shared across all stacks in the group
    created_at  INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at  INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE IF NOT EXISTS stack_group_membership (
    group_id      TEXT NOT NULL REFERENCES deployment_groups(id) ON DELETE CASCADE,
    stack_name    TEXT NOT NULL,
    role          TEXT NOT NULL,
    deploy_order  INTEGER NOT NULL DEFAULT 0,
    account_id    TEXT,
    PRIMARY KEY (group_id, stack_name)
);
