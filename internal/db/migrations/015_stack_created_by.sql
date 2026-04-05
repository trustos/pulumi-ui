-- Track which OCI account originally created the stack.
-- Separate from oci_account_id (which account to USE for operations).
ALTER TABLE stacks ADD COLUMN created_by_account_id TEXT REFERENCES oci_accounts(id);

-- Backfill: existing stacks get their current oci_account_id as creator.
UPDATE stacks SET created_by_account_id = oci_account_id WHERE oci_account_id IS NOT NULL;
