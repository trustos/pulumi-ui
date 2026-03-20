CREATE TABLE passphrases (
    id         TEXT    PRIMARY KEY,
    name       TEXT    NOT NULL UNIQUE,
    value      BLOB    NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);

ALTER TABLE stacks ADD COLUMN passphrase_id TEXT REFERENCES passphrases(id);
