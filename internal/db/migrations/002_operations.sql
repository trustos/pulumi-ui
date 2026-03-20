CREATE TABLE IF NOT EXISTS operations (
    id          TEXT    NOT NULL PRIMARY KEY,
    stack_name  TEXT    NOT NULL,
    operation   TEXT    NOT NULL,
    status      TEXT    NOT NULL,
    log         TEXT    NOT NULL DEFAULT '',
    started_at  INTEGER NOT NULL DEFAULT (unixepoch()),
    finished_at INTEGER
);

CREATE INDEX IF NOT EXISTS idx_operations_stack ON operations(stack_name, started_at DESC);
