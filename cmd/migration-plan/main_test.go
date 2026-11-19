package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPlanCommandPrivateAndNonDestructive(t *testing.T) {
	dir := t.TempDir()
	in, out := filepath.Join(dir, "inventory.json"), filepath.Join(dir, "plan.json")
	raw := `{"version":1,"complete":true,"total":1,"users":[{"uuid":"example-account","telegramId":10001,"username":"client","expireAt":"2027-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(in, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	args := []string{"-inventory", in, "-out", out}
	if code := run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d err=%s", code, stderr.String())
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatal("private output permissions missing")
	}
	if strings.Contains(stdout.String(), "10001") || strings.Contains(stdout.String(), "client") {
		t.Fatal("customer identity printed to console")
	}
	if code := run(args, &stdout, &stderr); code != 1 {
		t.Fatal("existing output overwritten")
	}
	after, _ := os.ReadFile(in)
	if string(after) != raw {
		t.Fatal("source was modified")
	}
}

func TestPlanCommandUnresolvedAndInvalid(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "inventory.json")
	raw := `{"version":1,"complete":true,"total":1,"users":[{"id":25,"username":"client","expireAt":"2027-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(in, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-inventory", in, "-out", filepath.Join(dir, "review.json")}, &stdout, &stderr); code != 2 {
		t.Fatal("unresolved ownership not reported")
	}
	if code := run([]string{"-inventory", dir, "-out", filepath.Join(dir, "invalid.json")}, &stdout, &stderr); code != 1 {
		t.Fatal("non-regular input accepted")
	}
	if code := run(nil, &stdout, &stderr); code != 1 {
		t.Fatal("missing arguments accepted")
	}
}
