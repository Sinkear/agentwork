package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/eushing/agentwork/internal/backup"
	"github.com/eushing/agentwork/internal/paths"
)

// backupCmd implements `agentwork-daemon backup`: a consistent SQLite snapshot
// that is safe to run while the daemon is up, plus optional run files.
func backupCmd(args []string) {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	out := fs.String("out", "", "output directory (required)")
	db := fs.String("db", "", "SQLite path (default $AGENTWORK_HOME/agentwork.db)")
	withFiles := fs.Bool("with-files", false, "also archive runs/skills and agents.yaml")
	_ = fs.Parse(args)
	if *out == "" {
		fmt.Fprintln(os.Stderr, "usage: agentwork-daemon backup -out DIR [-db PATH] [-with-files]")
		os.Exit(2)
	}
	if _, err := paths.EnsureHome(); err != nil {
		fmt.Fprintf(os.Stderr, "agentwork home: %v\n", err)
		os.Exit(1)
	}
	res, err := backup.Create(context.Background(), backup.Options{
		DBPath:    *db,
		OutDir:    *out,
		WithFiles: *withFiles,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "backup: %v\n", err)
		os.Exit(1)
	}
	raw, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(raw))
}
