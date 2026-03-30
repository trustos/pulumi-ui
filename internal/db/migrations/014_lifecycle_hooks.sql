CREATE TABLE IF NOT EXISTS lifecycle_hooks (
    id          TEXT PRIMARY KEY,
    stack_name  TEXT NOT NULL,
    trigger     TEXT NOT NULL,       -- e.g. "pre-up", "post-up", "pre-destroy", "post-destroy"
    type        TEXT NOT NULL,       -- "agent-exec" or "webhook"
    priority    INTEGER NOT NULL DEFAULT 100,
    continue_on_error INTEGER NOT NULL DEFAULT 0,
    command     TEXT,                -- shell command (for agent-exec type)
    node_index  INTEGER,            -- target node index (for agent-exec type)
    url         TEXT,                -- webhook URL (for webhook type)
    source      TEXT,                -- e.g. "catalog:postgres-backup" or "user"
    description TEXT,
    created_at  INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE INDEX idx_lifecycle_hooks_stack ON lifecycle_hooks(stack_name);
CREATE INDEX idx_lifecycle_hooks_trigger ON lifecycle_hooks(stack_name, trigger);
