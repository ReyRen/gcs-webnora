DROP INDEX IF EXISTS idx_auth_tokens_platform_sso_session_id;
ALTER TABLE auth_tokens DROP COLUMN platform_sso_session_id;
DROP TABLE IF EXISTS platform_sso_sessions;
DROP TABLE IF EXISTS platform_sso_identities;
