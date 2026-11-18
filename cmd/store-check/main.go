package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"remnabot/internal/storage"
	_ "remnabot/internal/storage/drivers"
)

func main() {
	source := flag.String("source", "", "existing SQLite file (read-only)")
	copyPath := flag.String("copy", "", "new private disposable check file")
	flag.Parse()
	if *source == "" || *copyPath == "" {
		fmt.Fprintln(os.Stderr, "--source and --copy are required")
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, err := storage.CheckSQLiteUpgrade(ctx, *source, *copyPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Upgrade check failed:", err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		os.Exit(1)
	}
}
