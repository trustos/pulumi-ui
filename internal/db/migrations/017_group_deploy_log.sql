-- Add deploy log storage to deployment groups.
-- Persists the full SSE log from group deploy operations so it survives
-- page navigation and server restarts.

ALTER TABLE deployment_groups ADD COLUMN deploy_log TEXT NOT NULL DEFAULT '';
