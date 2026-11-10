\set ON_ERROR_STOP on
-- Temporary tables only; a closed session removes the entire fixture.
SET search_path = pg_temp;
CREATE TEMP TABLE tenants (id bigint PRIMARY KEY, name text, description text,
    updated_at timestamptz DEFAULT now(), deleted_at timestamptz);
CREATE TEMP TABLE users (id text PRIMARY KEY, username text, email text,
    tenant_id bigint, is_active boolean DEFAULT true, deleted_at timestamptz);
CREATE TEMP TABLE tenant_members (tenant_id bigint, user_id text, role text,
    status text DEFAULT 'active', deleted_at timestamptz);
CREATE TEMP TABLE platform_sso_identities (user_id text, account text);

INSERT INTO users (id, username, email, tenant_id)
VALUES ('owner','renyuan-1234abcd','renyuan@test.com',1),
       ('custom','custom-1234abcd','custom@test.com',2),
       ('local','local-1234abcd','local@test.com',3),
       ('viewer','viewer-1234abcd','viewer@test.com',4),
       ('placeholder','placeholder-1234abcd','id@platform-sso.invalid',5),
       ('ambiguous','ambiguous-1234abcd','ambiguous@test.com',6),
       ('suspended','suspended-1234abcd','suspended@test.com',7);
INSERT INTO tenants (id, name, description)
SELECT tenant_id, username || '''s Workspace', 'Default workspace' FROM users;
UPDATE tenants SET name='自定义项目空间' WHERE id=2;
INSERT INTO tenant_members (tenant_id,user_id,role)
SELECT tenant_id,id,'owner' FROM users;
UPDATE tenant_members SET role='viewer' WHERE user_id='viewer';
UPDATE tenant_members SET status='suspended' WHERE user_id='suspended';
INSERT INTO platform_sso_identities SELECT id,email FROM users WHERE id <> 'local';
INSERT INTO platform_sso_identities VALUES ('ambiguous','another@test.com');

\ir ../normalize-platform-workspace-names.sql
DO $$ BEGIN
    IF (SELECT name FROM tenants WHERE id=1) <> 'renyuan@test.com'
       OR (SELECT name FROM tenants WHERE id=2) <> '自定义项目空间'
       OR (SELECT count(*) FROM tenants t JOIN users u ON u.tenant_id=t.id
           WHERE t.id>2 AND t.name=u.username || '''s Workspace') <> 5
       OR (SELECT username FROM users WHERE id='owner') <> 'renyuan-1234abcd'
       OR (SELECT count(*) FROM tenant_members WHERE role='owner') <> 6
    THEN RAISE EXCEPTION 'workspace naming scope or identity changed'; END IF;
END $$;
\ir ../normalize-platform-workspace-names.sql
DO $$ BEGIN
    IF (SELECT count(*) FROM tenants WHERE name='renyuan@test.com') <> 1
    THEN RAISE EXCEPTION 'workspace normalization is not idempotent'; END IF;
END $$;
