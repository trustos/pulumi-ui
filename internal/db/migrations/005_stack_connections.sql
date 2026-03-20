CREATE TABLE IF NOT EXISTS stack_connections (
    stack_name   TEXT NOT NULL PRIMARY KEY REFERENCES stacks(name) ON DELETE CASCADE,
    nomad_addr   TEXT NOT NULL,
    nomad_token  BLOB NOT NULL,
    connected_at INTEGER NOT NULL DEFAULT (unixepoch())
);
