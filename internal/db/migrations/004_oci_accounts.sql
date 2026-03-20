CREATE TABLE IF NOT EXISTS oci_accounts (
    id             TEXT    NOT NULL PRIMARY KEY,
    user_id        TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name           TEXT    NOT NULL,
    tenancy_ocid   TEXT    NOT NULL,
    region         TEXT    NOT NULL,
    user_ocid      BLOB    NOT NULL,
    fingerprint    BLOB    NOT NULL,
    private_key    BLOB    NOT NULL,
    ssh_public_key BLOB    NOT NULL,
    status         TEXT    NOT NULL DEFAULT 'unverified',
    verified_at    INTEGER,
    created_at     INTEGER NOT NULL DEFAULT (unixepoch())
);

ALTER TABLE stacks ADD COLUMN oci_account_id TEXT REFERENCES oci_accounts(id);
