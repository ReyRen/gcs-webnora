CREATE TABLE IF NOT EXISTS platform_sso_identities (
    provider TEXT NOT NULL, subject TEXT NOT NULL, user_id TEXT NOT NULL,
    account TEXT NOT NULL DEFAULT '', display_name TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY(provider, subject)
);
CREATE INDEX IF NOT EXISTS idx_platform_sso_identities_user_id ON platform_sso_identities(user_id);
CREATE TABLE IF NOT EXISTS platform_sso_sessions (
    id TEXT PRIMARY KEY, provider TEXT NOT NULL, subject TEXT NOT NULL, user_id TEXT NOT NULL,
    access_token TEXT NOT NULL, refresh_token TEXT NOT NULL DEFAULT '',
    expires_at DATETIME NOT NULL, checked_at DATETIME NOT NULL,
    is_revoked BOOLEAN NOT NULL DEFAULT FALSE,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_platform_sso_sessions_user_id ON platform_sso_sessions(user_id);
ALTER TABLE auth_tokens ADD COLUMN platform_sso_session_id TEXT;
CREATE INDEX IF NOT EXISTS idx_auth_tokens_platform_sso_session_id ON auth_tokens(platform_sso_session_id);
