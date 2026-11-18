package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileSecretsLoad(t *testing.T) {
	t.Setenv("ADMIN_TELEGRAM_ID", "12345")
	for key, content := range map[string]string{
		"BOT_TOKEN": "tok\r\n", "SECRET_KEY": " key-material \n",
		"DATABASE_URL": "postgres://example.invalid/db\n", "CADDY_AUTH_API_TOKEN": "proxy-key\n",
	} {
		t.Setenv(key, "")
		path := filepath.Join(t.TempDir(), "value")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key+"_FILE", path)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.BotToken != "tok" || c.SecretKey != " key-material " || c.DatabaseURL != "postgres://example.invalid/db" || c.CaddyAuthToken != "proxy-key" {
		t.Fatal("file secret values were changed")
	}
}

func TestMultiSubscriptionsFlagIsExplicit(t *testing.T) {
	t.Setenv("BOT_TOKEN", "synthetic")
	t.Setenv("BOT_TOKEN_FILE", "")
	t.Setenv("ADMIN_TELEGRAM_ID", "12345")
	for _, tc := range []struct {
		raw            string
		enabled, valid bool
	}{{"", false, true}, {"false", false, true}, {"true", true, true}, {"maybe", false, false}} {
		t.Setenv("MULTI_SUBSCRIPTIONS_ENABLED", tc.raw)
		cfg, err := Load()
		if !tc.valid {
			if err == nil {
				t.Fatal("invalid mode accepted")
			}
			continue
		}
		if err != nil || cfg.MultiSubscriptions != tc.enabled {
			t.Fatalf("flag %q: %+v %v", tc.raw, cfg, err)
		}
	}
}

func TestSecretEnvValidation(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
	}{
		{"empty", ""}, {"blank", " \n"}, {"multiline", "first\nsecond"},
		{"nul", "value\x00"}, {"oversize", strings.Repeat("x", maxSecretBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "private-value")
			if err := os.WriteFile(path, []byte(tc.raw), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("BOT_TOKEN", "")
			t.Setenv("BOT_TOKEN_FILE", path)
			_, err := secretEnv("BOT_TOKEN")
			if err == nil || strings.Contains(err.Error(), path) || strings.Contains(err.Error(), "first") {
				t.Fatal("secret rejected without disclosing its path or value")
			}
		})
	}
}

func TestSecretEnvConflictAndBadPaths(t *testing.T) {
	t.Setenv("BOT_TOKEN", "legacy")
	t.Setenv("BOT_TOKEN_FILE", "nonexistent")
	if _, err := secretEnv("BOT_TOKEN"); err == nil {
		t.Fatal("ambiguous secret accepted")
	}
	t.Setenv("BOT_TOKEN", "")
	if _, err := secretEnv("BOT_TOKEN"); err == nil {
		t.Fatal("missing file accepted")
	}
	t.Setenv("BOT_TOKEN_FILE", t.TempDir())
	if _, err := secretEnv("BOT_TOKEN"); err == nil {
		t.Fatal("directory accepted")
	}
}

func TestSecretEnvLegacyKeyUnchanged(t *testing.T) {
	t.Setenv("SECRET_KEY", " legacy key \n")
	t.Setenv("SECRET_KEY_FILE", "")
	got, err := secretEnv("SECRET_KEY")
	if err != nil || got != " legacy key \n" {
		t.Fatal("legacy encryption key material changed")
	}
}

func TestSecretEnvSymlinkRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "value")
	if err := os.WriteFile(path, []byte("tok"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Skip("symlink creation unavailable")
	}
	t.Setenv("BOT_TOKEN", "")
	t.Setenv("BOT_TOKEN_FILE", link)
	if _, err := secretEnv("BOT_TOKEN"); err == nil {
		t.Fatal("symlink accepted")
	}
}
