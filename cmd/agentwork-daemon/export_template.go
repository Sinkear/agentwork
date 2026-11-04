package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/eushing/agentwork/internal/events"
	"github.com/eushing/agentwork/internal/paths"
	"github.com/eushing/agentwork/internal/service"
	"github.com/eushing/agentwork/internal/store"
)

// exportTemplateCmd implements `agentwork-daemon export-template`: turn a live
// agent / squad / project (domain) into a registry-compatible template file
// for the user's own template repository.
func exportTemplateCmd(args []string) {
	fs := flag.NewFlagSet("export-template", flag.ExitOnError)
	kind := fs.String("kind", "", "squad | agent | project (required)")
	id := fs.String("id", "", "live id or exact name (required)")
	out := fs.String("out", "", "template repository root (required; created if missing)")
	slug := fs.String("template-id", "", "metadata.id override (default derived from the name)")
	squad := fs.String("squad", "", "project only: team squad id/name (default = unique squad assignee)")
	version := fs.String("version", "0.1.0", "metadata.version")
	withSkills := fs.Bool("with-skills", true, "embed referenced skill files under skills/<name>/")
	force := fs.Bool("force", false, "overwrite existing template/skill files and registry entry")
	db := fs.String("db", "", "SQLite path (default $AGENTWORK_HOME/agentwork.db)")
	_ = fs.Parse(args)
	if *kind == "" || *id == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: agentwork-daemon export-template -kind squad|agent|project -id ID_OR_NAME -out DIR [flags]")
		os.Exit(2)
	}
	if _, err := paths.EnsureHome(); err != nil {
		fmt.Fprintf(os.Stderr, "agentwork home: %v\n", err)
		os.Exit(1)
	}
	st, err := store.Open(*db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "store: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()
	svc := service.NewTemplateExportService(st, events.NewBus())
	res, err := svc.Export(context.Background(), service.TemplateExportOptions{
		Kind: *kind, ID: *id, OutDir: *out, IDSlug: *slug, Squad: *squad,
		Version: *version, WithSkills: *withSkills, Force: *force,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "export-template: %v\n", err)
		os.Exit(1)
	}
	raw, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(raw))
}
