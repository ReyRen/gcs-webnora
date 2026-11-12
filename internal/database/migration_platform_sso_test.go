package database

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestSQLitePlatformSSOUpgradePaths(t *testing.T) {
	root := sqliteRepoRoot(t)
	for _, state := range []string{"fresh", "upstream", "legacy", "legacy-url"} {
		t.Run(state, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "sso.db")
			opts := MigrationOptions{SQLiteDBPath: path}
			if state != "fresh" {
				version := 52
				if state == "legacy" || state == "legacy-url" {
					version = 51
				}
				chdirAndRestore(t, copySQLiteMigrationsThrough(t, root, version))
				require.NoError(t, RunMigrationsWithOptions("sqlite3://unused", opts))
			}
			chdirAndRestore(t, root)
			db := openSQLiteDB(t, path)
			if state == "legacy" || state == "legacy-url" {
				legacy, err := os.ReadFile(filepath.Join(root, "internal/database/testdata/platform_sso_legacy.sqlite.sql"))
				require.NoError(t, err)
				_, err = db.Exec(string(legacy))
				require.NoError(t, err)
				_, err = db.Exec("UPDATE schema_migrations SET version=52")
				require.NoError(t, err)
				insertPlatformSSOSentinel(t, db)
			}
			if state == "upstream" {
				insertVaultSentinel(t, db)
			}
			dsn := "sqlite3://unused"
			if state == "legacy-url" {
				dsn, opts = "sqlite3://"+path, MigrationOptions{}
			}
			require.NoError(t, RunMigrationsWithOptions(dsn, opts))
			require.NoError(t, RunMigrationsWithOptions(dsn, opts), "restarting is idempotent")
			version, dirty := sqliteMigrationState(t, db)
			require.Equal(t, expectedSQLiteMigrationVersion, version)
			require.False(t, dirty)
			require.True(t, sqliteTableExists(t, db, "credential_vault_items"))
			require.True(t, sqliteTableExists(t, db, "platform_sso_identities"))
			require.True(t, sqliteColumnExists(t, db, "auth_tokens", "platform_sso_session_id"))
			if state == "legacy" || state == "legacy-url" {
				requirePlatformSSOSentinel(t, db)
			}
			if state == "upstream" {
				requireVaultSentinel(t, db)
			}
		})
	}
}

func TestPostgresPlatformSSOUpgradePaths(t *testing.T) {
	dsn := os.Getenv("WEKNORA_MIGRATION_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set a disposable PostgreSQL DSN to run migration integration tests")
	}
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer admin.Close()
	root := sqliteRepoRoot(t)
	chdirAndRestore(t, root)
	for _, state := range []string{"fresh", "upstream", "legacy"} {
		t.Run(state, func(t *testing.T) {
			// Each path uses a new database; never reset the database in the DSN.
			name := fmt.Sprintf("weknora_merge_test_%s_%d", state, time.Now().UnixNano())
			_, err := admin.Exec("CREATE DATABASE " + pq.QuoteIdentifier(name))
			require.NoError(t, err)
			t.Cleanup(func() {
				_, err := admin.Exec("DROP DATABASE " + pq.QuoteIdentifier(name) + " WITH (FORCE)")
				require.NoError(t, err)
			})
			testURL := *u
			testURL.Path = "/" + name
			testDSN := testURL.String()
			db, err := sql.Open("postgres", testDSN)
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			if state != "fresh" {
				m, err := migrate.New("file://migrations/versioned", testDSN)
				require.NoError(t, err)
				version := uint(136)
				if state == "legacy" {
					version = 135
				}
				require.NoError(t, m.Migrate(version))
				_, closeErr := m.Close()
				require.NoError(t, closeErr)
			}
			if state == "legacy" {
				legacy, err := os.ReadFile(filepath.Join(root, "internal/database/testdata/platform_sso_legacy.postgres.sql"))
				require.NoError(t, err)
				_, err = db.Exec(string(legacy))
				require.NoError(t, err)
				_, err = db.Exec("UPDATE schema_migrations SET version=136")
				require.NoError(t, err)
				insertPlatformSSOSentinel(t, db)
			}
			if state == "upstream" {
				insertVaultSentinel(t, db)
			}
			require.NoError(t, RunMigrationsWithOptions(testDSN, MigrationOptions{}))
			require.NoError(t, RunMigrationsWithOptions(testDSN, MigrationOptions{}))
			var version int
			var dirty bool
			require.NoError(t, db.QueryRow("SELECT version,dirty FROM schema_migrations").Scan(&version, &dirty))
			require.Equal(t, 139, version)
			require.False(t, dirty)
			var tables int
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM information_schema.tables
				WHERE table_schema='public' AND table_name IN (
				'platform_sso_identities','platform_sso_sessions','credential_vault_items','platform_storage_bindings'
				)`).Scan(&tables))
			require.Equal(t, 4, tables)
			if state == "legacy" {
				requirePlatformSSOSentinel(t, db)
			}
			if state == "upstream" {
				requireVaultSentinel(t, db)
			}
		})
	}
}

func insertPlatformSSOSentinel(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO users(id,username,email,password_hash) VALUES ('existing-user','existing-user','existing@example.invalid','unused-hash');
INSERT INTO platform_sso_identities(provider,subject,user_id) VALUES ('https://sso.invalid','446','existing-user');
INSERT INTO platform_sso_sessions(id,provider,subject,user_id,access_token,refresh_token,expires_at,checked_at)
VALUES ('existing-session','https://sso.invalid','446','existing-user','encrypted-access','encrypted-refresh',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
INSERT INTO auth_tokens(id,user_id,token,token_type,expires_at,platform_sso_session_id)
VALUES ('existing-token','existing-user','local-token','access_token',CURRENT_TIMESTAMP,'existing-session')`)
	require.NoError(t, err)
}

func requirePlatformSSOSentinel(t *testing.T, db *sql.DB) {
	t.Helper()
	var user, access, refresh, session string
	require.NoError(t, db.QueryRow("SELECT user_id FROM platform_sso_identities WHERE subject='446'").Scan(&user))
	require.Equal(t, "existing-user", user)
	require.NoError(t, db.QueryRow("SELECT access_token,refresh_token FROM platform_sso_sessions WHERE id='existing-session'").Scan(&access, &refresh))
	require.Equal(t, "encrypted-access", access)
	require.Equal(t, "encrypted-refresh", refresh)
	require.NoError(t, db.QueryRow("SELECT platform_sso_session_id FROM auth_tokens WHERE id='existing-token'").Scan(&session))
	require.Equal(t, "existing-session", session)
}

func insertVaultSentinel(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO credential_vault_items(id,tenant_id,user_id,title,site,username,secret_ciphertext,created_at,updated_at)
VALUES ('vault-item',7,'existing-user','Login','example.invalid','account','encrypted-secret',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	require.NoError(t, err)
}

func requireVaultSentinel(t *testing.T, db *sql.DB) {
	t.Helper()
	var secret string
	require.NoError(t, db.QueryRow("SELECT secret_ciphertext FROM credential_vault_items WHERE id='vault-item'").Scan(&secret))
	require.Equal(t, "encrypted-secret", secret)
}
