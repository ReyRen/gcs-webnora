-- The original fork used the preceding version for SSO. Reapply the upstream
-- idempotent vault schema so those deployed databases receive both features.
-- Per-user saved logins for the sandbox browser's secure credential form.
-- The password is only in secret_ciphertext (AES-256-GCM, SYSTEM_AES_KEY).
CREATE TABLE IF NOT EXISTS credential_vault_items (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    user_id VARCHAR(128) NOT NULL,
    kind VARCHAR(16) NOT NULL DEFAULT 'login',
    title VARCHAR(200) NOT NULL,
    site VARCHAR(255) NOT NULL,
    domains JSONB NOT NULL DEFAULT '[]',
    username VARCHAR(256) NOT NULL,
    secret_ciphertext TEXT NOT NULL,
    last_used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_credential_vault_items_owner_site_user
    ON credential_vault_items(tenant_id, user_id, site, username) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_credential_vault_items_deleted_at ON credential_vault_items(deleted_at);

CREATE TABLE IF NOT EXISTS platform_sso_identities (
    provider TEXT NOT NULL,
    subject TEXT NOT NULL,
    user_id TEXT NOT NULL,
    account TEXT NOT NULL DEFAULT '',
    display_name TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (provider, subject)
);
CREATE INDEX IF NOT EXISTS idx_platform_sso_identities_user_id ON platform_sso_identities(user_id);
CREATE TABLE IF NOT EXISTS platform_sso_sessions (
    id TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    subject TEXT NOT NULL,
    user_id TEXT NOT NULL,
    access_token TEXT NOT NULL,
    refresh_token TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL,
    checked_at TIMESTAMPTZ NOT NULL,
    is_revoked BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_platform_sso_sessions_user_id ON platform_sso_sessions(user_id);
ALTER TABLE auth_tokens ADD COLUMN IF NOT EXISTS platform_sso_session_id TEXT;
CREATE INDEX IF NOT EXISTS idx_auth_tokens_platform_sso_session_id ON auth_tokens(platform_sso_session_id);
