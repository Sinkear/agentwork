package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eushing/agentwork/internal/events"
	"gopkg.in/yaml.v3"
)

func TestTemplateExportSquadRoundTrip(t *testing.T) {
	t.Setenv("AGENTWORK_HOME", t.TempDir())
	ctx := context.Background()
	st := newTestStore(t)
	bus := events.NewBus()
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO runtime (id,name,machine_id,args,env,status,created_at) VALUES ('rt','test-rt','','[]','{}','active','now')`); err != nil {
		t.Fatalf("insert runtime: %v", err)
	}
	as := NewAgentService(st, bus)
	leader, err := as.Create(ctx, Agent{Name: "leader", RuntimeID: "rt", Description: "leads", SystemPrompt: "delegate to mention://agent/abc", Env: map[string]string{"API_TOKEN": "secret"}})
	if err != nil {
		t.Fatalf("create leader: %v", err)
	}
	worker, err := as.Create(ctx, Agent{Name: "worker", RuntimeID: "rt", Description: "works", MaxConcurrent: 2})
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}
	ss := NewSquadService(st, bus)
	sq, err := ss.Create(ctx, Squad{Name: "demo squad", Description: "demo", LeaderID: leader.ID, Instructions: "go"})
	if err != nil {
		t.Fatalf("create squad: %v", err)
	}
	if _, err := ss.AddMember(ctx, sq.ID, "agent", worker.ID, "reviewer"); err != nil {
		t.Fatalf("add member: %v", err)
	}

	out := t.TempDir()
	svc := NewTemplateExportService(st, bus)
	res, err := svc.Export(ctx, TemplateExportOptions{
		Kind: "squad", ID: "demo squad", OutDir: out, WithSkills: false,
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if res.ID != "demo-squad" {
		t.Fatalf("id = %q", res.ID)
	}
	raw, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	meta, specAny, err := ParseTemplateForTest(string(raw))
	if err != nil {
		t.Fatalf("parse export: %v", err)
	}
	if meta.Kind != TemplateKindSquad || meta.Version != "0.1.0" {
		t.Fatalf("meta = %+v", meta)
	}
	spec, ok := specAny.(*squadSpec)
	if !ok || spec.Squad.Leader != "leader" || len(spec.Agents) != 2 || len(spec.Squad.Members) != 1 {
		t.Fatalf("spec = %#v", specAny)
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "mention://agent/") {
		t.Fatalf("expected hard-coded mention warning, got %v", res.Warnings)
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "plaintext") {
		t.Fatalf("expected secret env warning, got %v", res.Warnings)
	}

	regRaw, err := os.ReadFile(filepath.Join(out, "registry.yaml"))
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	var reg templateRegistry
	if err := yaml.Unmarshal(regRaw, &reg); err != nil {
		t.Fatalf("parse registry: %v", err)
	}
	if len(reg.Templates) != 1 || reg.Templates[0].ID != "demo-squad" || reg.Templates[0].Path != "squads/demo-squad.yaml" {
		t.Fatalf("registry = %+v", reg.Templates)
	}

	// Repeat without -force must fail loudly rather than overwrite.
	if _, err := svc.Export(ctx, TemplateExportOptions{Kind: "squad", ID: sq.ID, OutDir: out, WithSkills: false}); err == nil {
		t.Fatalf("expected duplicate export to fail")
	}
}

func TestTemplateExportProjectSkipsTerminalAndHumanGoals(t *testing.T) {
	t.Setenv("AGENTWORK_HOME", t.TempDir())
	ctx := context.Background()
	st := newTestStore(t)
	bus := events.NewBus()
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO runtime (id,name,machine_id,args,env,status,created_at) VALUES ('rt','test-rt','','[]','{}','active','now')`); err != nil {
		t.Fatalf("insert runtime: %v", err)
	}
	as := NewAgentService(st, bus)
	a, err := as.Create(ctx, Agent{Name: "builder", RuntimeID: "rt"})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	ss := NewSquadService(st, bus)
	sq, err := ss.Create(ctx, Squad{Name: "build squad", LeaderID: a.ID})
	if err != nil {
		t.Fatalf("create squad: %v", err)
	}
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO domain (id,type,name,default_branch,policy_text,issue_assignee,issue_assignee_type,created_at)
		 VALUES ('d1','scratch','demo project','main','keep it green',?,'squad','now')`, sq.ID); err != nil {
		t.Fatalf("insert domain: %v", err)
	}
	for _, g := range []struct {
		id, title, atype, aid, status string
	}{
		{"g1", "open squad goal", "squad", sq.ID, "active"},
		{"g2", "open agent goal", "agent", a.ID, "backlog"},
		{"g3", "done goal", "agent", a.ID, "done"},
		{"g4", "human goal", "human", "", "active"},
	} {
		if _, err := st.DB().ExecContext(ctx,
			`INSERT INTO goal (id,title,description,domain_id,assignee_type,assignee_id,status,created_at) VALUES (?,?,?,?,?,?,?,?)`,
			g.id, g.title, "", "d1", g.atype, g.aid, g.status, "now"); err != nil {
			t.Fatalf("insert goal: %v", err)
		}
	}
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO schedule (id,name,title_template,description,assignee_type,assignee_id,domain_id,cron_expression,timezone,enabled,created_at)
		 VALUES ('s1','nightly','nightly title','','squad',?, 'd1','0 3 * * *','UTC',1,'now')`, sq.ID); err != nil {
		t.Fatalf("insert schedule: %v", err)
	}

	out := t.TempDir()
	res, err := NewTemplateExportService(st, bus).Export(ctx, TemplateExportOptions{
		Kind: "project", ID: "demo project", OutDir: out, WithSkills: false,
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	raw, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	_, specAny, err := ParseTemplateForTest(string(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	spec, ok := specAny.(*projectSpec)
	if !ok {
		t.Fatalf("spec type %T", specAny)
	}
	if spec.Domain.Type != "scratch" || spec.Domain.IssueAssignee != "build squad" {
		t.Fatalf("domain = %+v", spec.Domain)
	}
	if spec.Team == nil || spec.Team.Squad.Name != "build squad" {
		t.Fatalf("team = %+v", spec.Team)
	}
	if len(spec.Goals) != 2 {
		t.Fatalf("goals = %+v", spec.Goals)
	}
	if spec.Goals[0].Title != "open squad goal" || !spec.Goals[0].Start || spec.Goals[0].AssigneeType != "squad" {
		t.Fatalf("first goal = %+v", spec.Goals[0])
	}
	if spec.Goals[1].Title != "open agent goal" || spec.Goals[1].Start {
		t.Fatalf("second goal = %+v", spec.Goals[1])
	}
	if len(spec.Schedules) != 1 || spec.Schedules[0].Name != "nightly" || spec.Schedules[0].Cron != "0 3 * * *" {
		t.Fatalf("schedules = %+v", spec.Schedules)
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "human") {
		t.Fatalf("expected human-goal warning, got %v", res.Warnings)
	}
}

func TestTemplateExportProjectKeepsGitURL(t *testing.T) {
	t.Setenv("AGENTWORK_HOME", t.TempDir())
	ctx := context.Background()
	st := newTestStore(t)
	bus := events.NewBus()
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO domain (id,type,name,git_url,default_branch,created_at)
		 VALUES ('d-repo','repo','demo repo','https://example.com/demo.git','main','now')`); err != nil {
		t.Fatalf("insert domain: %v", err)
	}
	out := t.TempDir()
	res, err := NewTemplateExportService(st, bus).Export(ctx, TemplateExportOptions{
		Kind: "project", ID: "demo repo", OutDir: out, WithSkills: false,
	})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	raw, err := os.ReadFile(res.Path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	_, specAny, err := ParseTemplateForTest(string(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	spec, ok := specAny.(*projectSpec)
	if !ok {
		t.Fatalf("spec type %T", specAny)
	}
	if spec.Domain.GitURL != "https://example.com/demo.git" {
		t.Fatalf("git_url = %q", spec.Domain.GitURL)
	}
	for _, w := range res.Warnings {
		if strings.Contains(w, "git_url") {
			t.Fatalf("unexpected warning: %v", res.Warnings)
		}
	}
}
