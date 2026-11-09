ALTER TABLE auth_tokens DROP COLUMN IF EXISTS platform_sso_session_id;
DROP TABLE IF EXISTS platform_sso_sessions;
DROP TABLE IF EXISTS platform_sso_identities;
