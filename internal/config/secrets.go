package config

import (
	"fmt"
	"io"
	"os"
	"strings"
)

const maxSecretBytes = 4096

// secretEnv preserves legacy environment values exactly. File-backed values
// accept one trailing line ending, so SECRET_KEY material is not reinterpreted.
// Errors intentionally omit paths, contents and wrapped OS errors.
func secretEnv(key string) (string, error) {
	value := os.Getenv(key)
	path := strings.TrimSpace(os.Getenv(key + "_FILE"))
	if path == "" {
		return value, nil
	}
	if value != "" {
		return "", fmt.Errorf("set either %s or %s_FILE", key, key)
	}
	bad := func() (string, error) {
		return "", fmt.Errorf("%s_FILE must name a readable nonempty regular file (max %d bytes)", key, maxSecretBytes)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSecretBytes {
		return bad()
	}
	f, err := os.Open(path)
	if err != nil {
		return bad()
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return bad()
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxSecretBytes+1))
	if err != nil || len(raw) > maxSecretBytes {
		return bad()
	}
	s := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	if strings.TrimSpace(s) == "" || strings.ContainsAny(s, "\x00\r\n") {
		return bad()
	}
	return s, nil
}
