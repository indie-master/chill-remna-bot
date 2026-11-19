// migration-plan creates a private offline review document, never an import.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"remnabot/internal/migration"
)

func readPrivate(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("input must be a readable regular file within the size limit")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot open input")
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("input changed while opening")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, errors.New("cannot read input within the size limit")
	}
	return b, nil
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("migration-plan", flag.ContinueOnError)
	flags.SetOutput(stderr)
	in := flags.String("inventory", "", "complete private panel inventory (version 1)")
	claimsPath := flags.String("claims", "", "optional private verified ownership claims")
	out := flags.String("out", "", "new private output file; existing files are never overwritten")
	if err := flags.Parse(args); err != nil {
		return 1
	}
	if *in == "" || *out == "" || flags.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "inventory and out are required; no credentials or network access are used")
		return 1
	}
	raw, err := readPrivate(*in, 32<<20)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	var claims []migration.Claim
	if *claimsPath != "" {
		b, err := readPrivate(*claimsPath, 2<<20)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, err)
			return 1
		}
		if json.Unmarshal(b, &claims) != nil {
			_, _ = fmt.Fprintln(stderr, "invalid claims JSON")
			return 1
		}
	}
	plan, err := migration.Build(raw, claims)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "cannot create a new output file; existing files are never overwritten")
		return 1
	}
	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	err = encoder.Encode(plan)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		_ = os.Remove(*out)
		_, _ = fmt.Fprintln(stderr, "cannot persist plan")
		return 1
	}
	_, _ = fmt.Fprintln(stdout, "Private review plan saved. No accounts, payments or services were changed.")
	if !plan.Ready {
		_, _ = fmt.Fprintln(stdout, "Ownership requires administrator review. This document cannot be applied automatically.")
		return 2
	}
	return 0
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
