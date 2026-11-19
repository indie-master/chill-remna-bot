package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"remnabot/internal/model"
)

func TestUpgradeCheckCopiesLiveWALWithoutChangingSource(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	st, err := openSQLite(source, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertUser(ctx, 10001); err != nil {
		t.Fatal(err)
	}
	if err := st.AddBalance(ctx, 10001, 12345); err != nil {
		t.Fatal(err)
	}
	// Simulate the public upstream schema before this branch's additive steps.
	b := st.(*sqliteStore)
	for _, table := range []string{"subscription_selections", "subscription_callbacks", "subscription_operations", "customer_subscriptions"} {
		if _, err := b.db.ExecContext(ctx, "DROP TABLE "+table); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.db.ExecContext(ctx, "ALTER TABLE purchase_intents DROP COLUMN subscription_id"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.db.ExecContext(ctx, "DELETE FROM schema_migrations WHERE version >= 78"); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(dir, "check", "copy.db")
	got, err := CheckSQLiteUpgrade(ctx, source, destination)
	if err != nil {
		t.Fatal(err)
	}
	if got.SchemaBefore != 77 || got.SchemaAfter != 84 || got.Rows["users"] != 1 {
		t.Fatalf("wrong report: %+v", got)
	}
	var version int
	if err := b.db.QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil || version != 77 {
		t.Fatal("source was migrated")
	}
	copyStore, err := Open(model.DBSQLite, destination, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = copyStore.Close() }()
	u, err := copyStore.GetUser(ctx, 10001)
	if err != nil || u.Balance != 12345 {
		t.Fatal("copy lost WAL data")
	}
	if _, err := CheckSQLiteUpgrade(ctx, source, destination); err == nil {
		t.Fatal("existing destination overwritten")
	}
	if _, err := b.db.ExecContext(ctx, "INSERT INTO schema_migrations (version) VALUES (9999)"); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckSQLiteUpgrade(ctx, source, filepath.Join(dir, "newer.db")); err == nil {
		t.Fatal("unknown future schema accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, "newer.db")); !os.IsNotExist(err) {
		t.Fatal("future database was copied or changed")
	}
}
