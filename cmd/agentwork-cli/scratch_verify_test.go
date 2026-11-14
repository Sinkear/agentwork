package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eushing/agentwork/internal/link"
)

// TestRunScratchChecksPasses: setup then verify both run, in order, and a
// clean exit passes the gate.
func TestRunScratchChecksPasses(t *testing.T) {
	dir := t.TempDir()
	report, passed, results := runScratchChecks(context.Background(), dir, &link.RunChecks{
		Setup:  []string{"echo prepared"},
		Verify: []string{"echo verified"},
	})
	if !passed {
		t.Fatalf("expected pass, report:\n%s", report)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 commands, got %d: %+v", len(results), results)
	}
	if !strings.Contains(report, "prepared") || !strings.Contains(report, "verified") {
		t.Fatalf("report lost command output:\n%s", report)
	}
}

// TestRunScratchChecksFailsVerify: a non-zero verify command fails the gate
// and the report names the command + exit code.
func TestRunScratchChecksFailsVerify(t *testing.T) {
	dir := t.TempDir()
	report, passed, _ := runScratchChecks(context.Background(), dir, &link.RunChecks{
		Verify: []string{"echo boom; exit 3"},
	})
	if passed {
		t.Fatal("non-zero verify must fail")
	}
	if !strings.Contains(report, "exit 3") || !strings.Contains(report, "verify failed") {
		t.Fatalf("report lacks the failure attribution:\n%s", report)
	}
}

// TestRunScratchChecksSetupFailureStopsVerify: a setup failure is
// environment attribution and must not run the verify commands against an
// unprepared environment.
func TestRunScratchChecksSetupFailureStopsVerify(t *testing.T) {
	dir := t.TempDir()
	report, passed, results := runScratchChecks(context.Background(), dir, &link.RunChecks{
		Setup:  []string{"exit 2"},
		Verify: []string{"echo never"},
	})
	if passed {
		t.Fatal("setup failure must fail the gate")
	}
	if len(results) != 1 {
		t.Fatalf("verify must not run after setup failure, got %d result(s): %+v", len(results), results)
	}
	if strings.Contains(report, "never") {
		t.Fatalf("verify output leaked into the report:\n%s", report)
	}
	if !strings.Contains(report, "setup failed") {
		t.Fatalf("report lacks setup attribution:\n%s", report)
	}
}

// TestBuildScratchEvidenceEmbedsManifest: the agent's reports/manifest.json
// rides the evidence bundle verbatim so version receipts survive delivery.
func TestBuildScratchEvidenceEmbedsManifest(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "reports"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"versions":{"artifact":"1.0.4","env":"1.0.3","template":"1.0.2"},"files":[{"path":"x","sha256":"abc"}]}`
	if err := os.WriteFile(filepath.Join(dir, "reports", "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	checks := &link.RunChecks{Verify: []string{"true"}}
	report, passed, results := runScratchChecks(context.Background(), dir, checks)
	if !passed {
		t.Fatalf("expected pass, report:\n%s", report)
	}
	evidence := buildScratchEvidence(dir, checks, results, passed, report)
	var ev scratchVerifyEvidence
	if err := json.Unmarshal([]byte(evidence), &ev); err != nil {
		t.Fatalf("evidence not JSON: %v\n%s", err, evidence)
	}
	if ev.Kind != scratchEvidenceKind || !ev.Passed {
		t.Fatalf("bad evidence header: %+v", ev)
	}
	if len(ev.Manifest) == 0 || !strings.Contains(string(ev.Manifest), "1.0.4") {
		t.Fatalf("manifest not embedded: %q", ev.Manifest)
	}
	if len(ev.Commands) != 1 || ev.Commands[0].Cmd != "verify: true" {
		t.Fatalf("commands missing: %+v", ev.Commands)
	}
}

// TestBuildScratchEvidenceIgnoresMalformedManifest: a broken manifest is
// noted, not fatal (Verify commands are the judgment).
func TestBuildScratchEvidenceIgnoresMalformedManifest(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "reports"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "reports", "manifest.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	evidence := buildScratchEvidence(dir, &link.RunChecks{}, nil, true, "ok")
	var ev scratchVerifyEvidence
	if err := json.Unmarshal([]byte(evidence), &ev); err != nil {
		t.Fatalf("evidence not JSON: %v", err)
	}
	if len(ev.Manifest) != 0 {
		t.Fatalf("malformed manifest must not be embedded: %q", ev.Manifest)
	}
	if !strings.Contains(ev.Report, "not valid JSON") {
		t.Fatalf("report should note the malformed manifest: %q", ev.Report)
	}
}

// TestBuildEnvScratchReportDir: scratch runs get an explicit workdir/goal dir
// and a durable reports/ dir (W2: never /tmp); repo runs get WORKDIR only.
func TestBuildEnvScratchReportDir(t *testing.T) {
	e := &executor{serverURL: "http://127.0.0.1:1"}
	dir := t.TempDir()
	env := e.buildEnv(link.RunDispatchParams{GoalID: "g", RunID: "r", AgentID: "a", Scratch: true}, dir)
	joined := strings.Join(env, "\n")
	for _, key := range []string{"AGENTWORK_WORKDIR=" + dir, "AGENTWORK_GOAL_DIR=" + dir, "AGENTWORK_REPORT_DIR=" + filepath.Join(dir, "reports")} {
		if !strings.Contains(joined, key) {
			t.Errorf("missing env %q", key)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "reports")); err != nil {
		t.Errorf("reports dir not created: %v", err)
	}

	repoDir := t.TempDir()
	repoEnv := strings.Join(e.buildEnv(link.RunDispatchParams{Scratch: false}, repoDir), "\n")
	if !strings.Contains(repoEnv, "AGENTWORK_WORKDIR="+repoDir) {
		t.Error("repo run should still get AGENTWORK_WORKDIR")
	}
	if strings.Contains(repoEnv, "AGENTWORK_REPORT_DIR=") {
		t.Error("repo run must not get a scratch report dir")
	}
}
