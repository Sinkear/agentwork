package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/eushing/agentwork/internal/link"
)

// ── scratch machine verification (findings-watchdog W1) ──
//
// A scratch domain has no git repo, so the daemon cannot verify a
// deliverable against a diff. The domain's frozen acceptance policy still
// applies: the MACHINE (which owns the files) runs setup → verify in the
// run workdir after the agent exits, and uploads a signed evidence bundle
// with run.finished. The daemon refuses delivery without a passed bundle
// when the domain is frozen with a Verify policy (deliver.go).
//
// The evidence is deliberately self-describing (kind/passed/commands/report
// + the agent-authored reports/manifest.json) so a human can audit WHY a run
// passed without re-running anything.

const (
	// scratchEvidenceKind identifies the bundle; deliver.go refuses any
	// other kind (a repo-path evidence bundle must not satisfy a scratch
	// gate and vice versa).
	scratchEvidenceKind = "scratch-verify"
	// scratchEvidenceMaxBytes caps the uploaded bundle. It rides the run
	// finished RPC; a runaway command log must not bloat the link.
	scratchEvidenceMaxBytes = 64 << 10
	// scratchCommandOutputCap caps ONE command's captured output — the
	// report keeps the head+tail (see capOutput).
	scratchCommandOutputCap = 4 << 10
	// scratchManifestMaxBytes caps the embedded reports/manifest.json.
	scratchManifestMaxBytes = 16 << 10
	// scratchDefaultTimeout mirrors the daemon's verify_timeout default.
	scratchDefaultTimeout = 600
)

// scratchCommandResult is one executed policy command.
type scratchCommandResult struct {
	Cmd    string `json:"cmd"`
	Exit   int    `json:"exit"`
	Output string `json:"output,omitempty"`
}

// scratchVerifyEvidence is the uploaded bundle.
type scratchVerifyEvidence struct {
	Kind      string                 `json:"kind"`
	Passed    bool                   `json:"passed"`
	CheckedAt string                 `json:"checked_at"`
	Workdir   string                 `json:"workdir,omitempty"`
	Report    string                 `json:"report,omitempty"`
	Commands  []scratchCommandResult `json:"commands,omitempty"`
	// Manifest is the agent-authored reports/manifest.json, verbatim when it
	// parses as JSON (versions/artifacts receipt — the version-mismatch
	// signal a human/domain check can read at delivery).
	Manifest json.RawMessage `json:"manifest,omitempty"`
}

// runScratchChecks executes checks.Setup then checks.Verify in workdir.
// Setup failures are environment attribution; verify failures are policy
// failures — both fail the run, and the report marks which phase failed.
// Each command runs under verify_timeout (default 600s).
func runScratchChecks(ctx context.Context, workdir string, checks *link.RunChecks) (report string, passed bool, results []scratchCommandResult) {
	if checks == nil {
		return "", true, nil
	}
	timeout := checks.VerifyTimeout
	if timeout <= 0 {
		timeout = scratchDefaultTimeout
	}
	var b strings.Builder
	passed = true
	runPhase := func(phase string, cmds []string) {
		for _, cmd := range cmds {
			cctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
			out, exit := runScratchCommand(cctx, workdir, cmd)
			cancel()
			results = append(results, scratchCommandResult{Cmd: phase + ": " + cmd, Exit: exit, Output: capOutput(out)})
			label := "$ " + cmd
			if phase != "verify" {
				label = "$ (" + phase + ") " + cmd
			}
			b.WriteString(label + "\n")
			if strings.TrimSpace(out) != "" {
				b.WriteString(capOutput(out) + "\n")
			}
			if exit != 0 {
				b.WriteString("→ exit " + itoa(exit) + " (" + phase + " failed)\n")
				passed = false
				return
			}
			b.WriteString("→ exit 0\n")
		}
	}
	runPhase("setup", checks.Setup)
	if passed {
		runPhase("verify", checks.Verify)
	}
	return b.String(), passed, results
}

// runScratchCommand runs one shell command in dir with combined output.
func runScratchCommand(ctx context.Context, dir, command string) (string, int) {
	cmd := exec.CommandContext(ctx, "bash", "-lc", command)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return string(out), ee.ExitCode()
	}
	return string(out) + "\n" + err.Error(), 1
}

// buildScratchEvidence assembles the JSON bundle. When reports/manifest.json
// exists and parses, it is embedded verbatim; a malformed manifest is noted
// in the report instead of failing the gate (the manifest is advisory — the
// Verify commands are the judgment).
func buildScratchEvidence(workdir string, checks *link.RunChecks, results []scratchCommandResult, passed bool, report string) string {
	ev := scratchVerifyEvidence{
		Kind:      scratchEvidenceKind,
		Passed:    passed,
		CheckedAt: time.Now().UTC().Format(time.RFC3339),
		Workdir:   workdir,
		Report:    report,
		Commands:  results,
	}
	if checks == nil {
		ev.Manifest = nil
	}
	if b, err := os.ReadFile(filepath.Join(workdir, "reports", "manifest.json")); err == nil {
		if len(b) > scratchManifestMaxBytes {
			b = b[:scratchManifestMaxBytes]
		}
		if json.Valid(b) {
			ev.Manifest = json.RawMessage(b)
		} else {
			ev.Report += "\n(reports/manifest.json is not valid JSON — ignored)\n"
		}
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return ""
	}
	if len(b) > scratchEvidenceMaxBytes {
		ev.Report = capOutput(ev.Report)
		ev.Commands = nil
		b, _ = json.Marshal(ev)
	}
	return string(b)
}

// capOutput keeps the head and tail of a long command output.
func capOutput(s string) string {
	if len(s) <= scratchCommandOutputCap {
		return s
	}
	half := (scratchCommandOutputCap - 20) / 2
	return s[:half] + "\n… (truncated) …\n" + s[len(s)-half:]
}

// itoa is a tiny exit-code formatter (avoids pulling strconv into the
// report path just for one number — readability over micro-optimization).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
