package database

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/golang-migrate/migrate/v4"
)

// An upstream IM-only database may report the version already used for SSO
// by this fork. Check the schema before replaying the released SSO migration.
// SQLite 52 also has a legacy fork variant that predates the credential vault.
func reconcilePlatformSSO(dsn, dbPath string, m *migrate.Migrate) error {
	sqlite := strings.HasPrefix(dsn, "sqlite3://")
	version, dirty, err := m.Version()
	if err == migrate.ErrNilVersion || (err == nil && dirty) {
		return nil
	}
	if err != nil {
		return err
	}

	legacySQLite := sqlite && version == 52
	dir, number, driver := "versioned", "000137", "postgres"
	if sqlite {
		if !legacySQLite && version != 53 {
			return nil
		}
		dir, number, driver = "sqlite", "000053", "sqlite3"
	} else if version != 137 {
		return nil
	}

	connection := dbPath
	if !sqlite || connection == "" {
		u, err := url.Parse(dsn)
		if err != nil {
			return err
		}
		connection = migrate.FilterCustomQuery(u).String()
		if sqlite {
			connection = strings.TrimPrefix(connection, "sqlite3://")
		}
	}

	db, err := sql.Open(driver, connection)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var current uint
	if !sqlite {
		if _, err := tx.Exec("LOCK TABLE schema_migrations IN ACCESS EXCLUSIVE MODE"); err != nil {
			return err
		}
	}
	if err := tx.QueryRow("SELECT version, dirty FROM schema_migrations").Scan(&current, &dirty); err != nil {
		return err
	}
	if current != version || dirty {
		return fmt.Errorf("migration state changed during platform SSO reconciliation")
	}

	var hasSSOColumn, hasIMIndex bool
	if sqlite {
		err = tx.QueryRow("SELECT EXISTS (SELECT 1 FROM pragma_table_info('auth_tokens')"+
			" WHERE name = 'platform_sso_session_id'),"+
			" EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'index' AND tbl_name = 'im_channel_sessions'"+
			" AND name = 'idx_channel_lookup' AND sql LIKE '%thread_id = %')").Scan(&hasSSOColumn, &hasIMIndex)
	} else {
		err = tx.QueryRow("SELECT EXISTS (SELECT 1 FROM information_schema.columns"+
			" WHERE table_schema = current_schema()"+
			" AND table_name = 'auth_tokens' AND column_name = 'platform_sso_session_id'),"+
			" EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = current_schema()"+
			" AND tablename = 'im_channel_sessions'"+
			" AND indexname = 'idx_channel_lookup' AND indexdef LIKE '%thread_id%')").Scan(&hasSSOColumn, &hasIMIndex)
	}
	if err != nil {
		return err
	}
	if legacySQLite {
		if !hasSSOColumn {
			return nil
		}
	} else {
		if hasSSOColumn {
			return nil
		}
		if !hasIMIndex {
			return fmt.Errorf("platform SSO schema and upstream IM index are both missing at migration %d", version)
		}
	}

	content, err := os.ReadFile(filepath.Join("migrations", dir, number+"_platform_sso.up.sql"))
	if err != nil {
		return err
	}
	script := string(content)
	if legacySQLite {
		const addColumn = "ALTER TABLE auth_tokens ADD COLUMN platform_sso_session_id TEXT;"
		if strings.Count(script, addColumn) != 1 {
			return fmt.Errorf("unexpected platform SSO compatibility migration")
		}
		script = strings.Replace(script, addColumn, "", 1)
	}
	if _, err := tx.Exec(script); err != nil {
		return fmt.Errorf("reconcile platform SSO schema: %w", err)
	}
	if legacySQLite {
		_, err := tx.Exec("UPDATE schema_migrations SET version = 53 WHERE version = 52 AND dirty = false")
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
