package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootstrapGuardRejectsOtherDatabasesWithoutDisclosingDSN(t *testing.T) {
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{"db_kind":"sqlite","dsn":"/data/bot.db"}`, true},
		{`{"db_kind":"postgres","dsn":"postgres://name:private-test-password@database/db"}`, false},
		{`{"db_kind":"sqlite","dsn":"/different/file.db"}`, false},
		{`{}`, false}, {`invalid-json`, false},
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "bootstrap.json"), []byte(tc.body), 0600); err != nil {
			t.Fatal(err)
		}
		err := validateBootstrap(dir)
		if (err == nil) != tc.valid {
			t.Fatalf("unexpected bootstrap decision: %v", err)
		}
		if err != nil && strings.Contains(err.Error(), "private-test-password") {
			t.Fatal("DSN disclosed")
		}
	}
}
