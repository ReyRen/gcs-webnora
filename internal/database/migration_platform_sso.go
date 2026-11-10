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

// The first fork shipped SSO as SQLite 52, before upstream assigned 52 to the
// credential vault. PostgreSQL 137 uses ADD COLUMN IF NOT EXISTS, but SQLite
// cannot conditionally add a column. For that one legacy state, atomically run
// the new 53 migration without re-adding its existing column and record 53.
// Fresh databases and upstream-only databases follow the regular migrator.
func reconcileLegacyPlatformSSOSQLite(dsn, dbPath string, m *migrate.Migrate) error {
	version, dirty, err := m.Version()
	if err == migrate.ErrNilVersion || (err == nil && (version != 52 || dirty)) {
		return nil
	}
	if err != nil {
		return err
	}
	if dbPath == "" {
		u, err := url.Parse(dsn)
		if err != nil {
			return err
		}
		// Match golang-migrate's sqlite3 URL-to-DSN conversion.
		dbPath = strings.Replace(migrate.FilterCustomQuery(u).String(), "sqlite3://", "", 1)
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	var existing int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('auth_tokens') WHERE name = 'platform_sso_session_id'`).Scan(&existing); err != nil {
		return err
	}
	if existing == 0 {
		return nil
	}
	content, err := os.ReadFile(filepath.Join("migrations", "sqlite", "000053_platform_sso.up.sql"))
	if err != nil {
		return err
	}
	const addColumn = "ALTER TABLE auth_tokens ADD COLUMN platform_sso_session_id TEXT;"
	if strings.Count(string(content), addColumn) != 1 {
		return fmt.Errorf("unexpected platform SSO compatibility migration")
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := tx.QueryRow("SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty); err != nil {
		return err
	}
	if version != 52 || dirty {
		return fmt.Errorf("SQLite migration state changed during platform SSO upgrade")
	}
	if _, err := tx.Exec(strings.Replace(string(content), addColumn, "", 1)); err != nil {
		return fmt.Errorf("upgrade legacy platform SSO schema: %w", err)
	}
	if _, err := tx.Exec("UPDATE schema_migrations SET version = 53 WHERE version = 52 AND dirty = false"); err != nil {
		return err
	}
	return tx.Commit()
}
