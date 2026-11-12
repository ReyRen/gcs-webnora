CREATE TABLE IF NOT EXISTS user_model_reference_policy (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    policy_version INTEGER NOT NULL DEFAULT 0,
    verified_at DATETIME
);
INSERT OR IGNORE INTO user_model_reference_policy (id, policy_version) VALUES (1, 0);
