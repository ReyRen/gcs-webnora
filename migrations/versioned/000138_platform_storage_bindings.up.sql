CREATE TABLE IF NOT EXISTS platform_storage_bindings (
    tenant_id BIGINT PRIMARY KEY,
    provider TEXT NOT NULL,
    subject VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
