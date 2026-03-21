CREATE TABLE IF NOT EXISTS ssh_keys (
    id         TEXT    PRIMARY KEY,
    user_id    TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT    NOT NULL,
    public_key TEXT    NOT NULL,
    private_key BLOB,  -- nullable: AES-encrypted private key PEM, stored only if provided/generated
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);
