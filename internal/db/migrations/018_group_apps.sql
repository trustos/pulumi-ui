-- App selection and config for deployment groups.
-- Stored as JSON strings so group deploy Phase 6 can auto-deploy apps.
ALTER TABLE deployment_groups ADD COLUMN applications TEXT NOT NULL DEFAULT '';
ALTER TABLE deployment_groups ADD COLUMN app_config TEXT NOT NULL DEFAULT '';
