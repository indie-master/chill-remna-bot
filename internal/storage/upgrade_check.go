package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type UpgradeCheck struct {
	SchemaBefore int              `json:"schemaBefore"`
	SchemaAfter  int              `json:"schemaAfter"`
	Rows         map[string]int64 `json:"rows"`
}

// CheckSQLiteUpgrade opens the original read-only and uses SQLite's consistent
// snapshot to create a new file. Only that copy is migrated. No settings, key
// material or panel credentials are loaded or decrypted.
func CheckSQLiteUpgrade(ctx context.Context, source, destination string) (*UpgradeCheck, error) {
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return nil, errors.New("source must be an existing nonempty regular SQLite file")
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("destination must not exist")
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return nil, errors.New("cannot create private check directory")
	}
	abs, err := filepath.Abs(source)
	if err != nil {
		return nil, errors.New("invalid source path")
	}
	if strings.ContainsAny(abs, "?#") {
		return nil, errors.New("source path must not contain SQLite URI parameters")
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(abs)+"?mode=ro&_pragma=busy_timeout(10000)")
	if err != nil {
		return nil, errors.New("cannot open source read-only")
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	versions, err := db.QueryContext(ctx, "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		return nil, errors.New("source has no readable migration history")
	}
	known := map[int]bool{}
	entries, err := migrationsFS.ReadDir("migrations/sqlite")
	if err != nil {
		_ = versions.Close()
		return nil, err
	}
	for _, entry := range entries {
		v, err := strconv.Atoi(strings.SplitN(entry.Name(), "_", 2)[0])
		if err != nil {
			_ = versions.Close()
			return nil, err
		}
		known[v] = true
	}
	done := map[int]bool{}
	result := &UpgradeCheck{Rows: map[string]int64{}}
	for versions.Next() {
		var v int
		if err := versions.Scan(&v); err != nil {
			_ = versions.Close()
			return nil, errors.New("invalid migration history")
		}
		if !known[v] {
			_ = versions.Close()
			return nil, errors.New("source schema is newer or belongs to another branch")
		}
		done[v] = true
		if v > result.SchemaBefore {
			result.SchemaBefore = v
		}
	}
	err = versions.Err()
	_ = versions.Close()
	if err != nil || result.SchemaBefore == 0 {
		return nil, errors.New("empty or unreadable migration history")
	}
	for v := range known {
		if v <= result.SchemaBefore && !done[v] {
			return nil, errors.New("source migration history has gaps")
		}
	}
	// SQLite reads the current committed WAL snapshot, including while the bot
	// is running. The source is never checkpointed or opened for writes here.
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", destination); err != nil {
		return nil, errors.New("cannot create consistent SQLite copy")
	}
	if err := os.Chmod(destination, 0600); err != nil {
		return nil, errors.New("cannot protect check copy")
	}
	copyStore, err := openSQLite(destination, nil)
	if err != nil {
		return nil, errors.New("cannot open check copy")
	}
	defer func() { _ = copyStore.Close() }()
	copyDB := copyStore.(*sqliteStore).db
	for _, table := range []string{"users", "payments", "settings"} {
		var n int64
		// #nosec G202 -- table comes from the fixed list above.
		if err := copyDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
			return nil, errors.New("source core tables are incomplete")
		}
		result.Rows[table] = n
	}
	if err := copyStore.Migrate(ctx); err != nil {
		return nil, errors.New("migration of the private copy failed")
	}
	for table, before := range result.Rows {
		var after int64
		// #nosec G202 -- keys originate only from the fixed core-table list.
		if err := copyDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&after); err != nil || before != after {
			return nil, errors.New("migration changed core row counts")
		}
	}
	var integrity string
	if err := copyDB.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		return nil, errors.New("check copy failed integrity validation")
	}
	fk, err := copyDB.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return nil, errors.New("cannot validate foreign keys")
	}
	bad := fk.Next()
	err = fk.Err()
	_ = fk.Close()
	if bad || err != nil {
		return nil, errors.New("check copy has inconsistent foreign keys")
	}
	// A version number is not sufficient when a different fork reused it.
	baseline, err := openSQLite(":memory:", nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = baseline.Close() }()
	if err := baseline.Migrate(ctx); err != nil {
		return nil, err
	}
	for _, table := range backupTables {
		expected, err := baseline.(*sqliteStore).tableColumns(ctx, baseline.(*sqliteStore).db, table)
		if err != nil {
			return nil, err
		}
		actual, err := copyStore.(*sqliteStore).tableColumns(ctx, copyDB, table)
		if err != nil || len(actual) != len(expected) {
			return nil, fmt.Errorf("incompatible columns in table %s", table)
		}
		byName := map[string]colInfo{}
		for _, column := range actual {
			byName[column.name] = column
		}
		for i := range expected {
			if byName[expected[i].name] != expected[i] {
				return nil, fmt.Errorf("incompatible columns in table %s", table)
			}
		}
	}
	if err := copyDB.QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&result.SchemaAfter); err != nil {
		return nil, err
	}
	return result, nil
}
