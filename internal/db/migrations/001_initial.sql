CREATE TABLE IF NOT EXISTS credentials (
    key        TEXT    NOT NULL PRIMARY KEY,
    value      BLOB    NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE IF NOT EXISTS stacks (
    name        TEXT    NOT NULL PRIMARY KEY,
    program     TEXT    NOT NULL,
    config_yaml TEXT    NOT NULL,
    created_at  INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at  INTEGER NOT NULL DEFAULT (unixepoch())
);
