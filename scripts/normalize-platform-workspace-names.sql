-- Run once with psql -v ON_ERROR_STOP=1 after backing up affected tenant names.
-- Only untouched, automatically named personal workspaces of verified SSO owners
-- qualify. No user IDs, memberships, usernames, or knowledge records are changed.
BEGIN;

WITH accounts AS (
    SELECT user_id, min(account) AS email
    FROM platform_sso_identities
    GROUP BY user_id
    HAVING count(DISTINCT account) = 1
), candidates AS (
    SELECT DISTINCT t.id, t.name AS old_name, t.updated_at AS old_updated_at, a.email
    FROM tenants t
    JOIN users u ON u.tenant_id = t.id
    JOIN accounts a ON a.user_id = u.id AND a.email = u.email
    JOIN tenant_members m ON m.tenant_id = t.id AND m.user_id = u.id
    WHERE t.deleted_at IS NULL AND u.deleted_at IS NULL AND u.is_active
      AND m.deleted_at IS NULL AND m.role = 'owner' AND m.status = 'active'
      AND t.description = 'Default workspace'
      AND t.name = u.username || '''s Workspace'
      AND a.email ~ '^[^[:space:]@<>]+@[^[:space:]@<>]+$'
      AND lower(a.email) NOT LIKE '%@platform-sso.invalid'
), renamed AS (
    UPDATE tenants t SET name = c.email, updated_at = CURRENT_TIMESTAMP
    FROM candidates c WHERE t.id = c.id AND t.name = c.old_name
      AND t.updated_at = c.old_updated_at
    RETURNING t.id
)
SELECT count(*) AS renamed_default_workspaces FROM renamed;

COMMIT;
