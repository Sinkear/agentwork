package daemon

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/eushing/agentwork/internal/logging"
)

// ── scratch machine verification gate (findings-watchdog W1) ──
//
// The machine runs the domain's frozen Verify policy for a scratch run
// (cmd/agentwork-cli/scratch_verify.go) and uploads the bundle with
// run.finished. The daemon's job is judgment, not execution: require a
// passed bundle when the domain is frozen with a Verify policy, stamp it on
// the run for audit, and refuse delivery without one. Scratch domains with
// no checks keep the old behavior exactly (report-only delivery).

// scratchEvidenceKind mirrors cmd/agentwork-cli's constant. Duplicated on
// purpose: the CLI and the daemon are separate binaries and the wire format
// is the contract; a shared package for one string would hide the coupling.
const scratchEvidenceKind = "scratch-verify"

// scratchEvidence is the subset the daemon judges.
type scratchEvidence struct {
	Kind      string          `json:"kind"`
	Passed    bool            `json:"passed"`
	CheckedAt string          `json:"checked_at"`
	Report    string          `json:"report"`
	Manifest  json.RawMessage `json:"manifest,omitempty"`
}

// scratchEvidencePassed reports whether evidence is a passed scratch-verify
// bundle. Anything else — empty, another kind, unparsable JSON, passed=false
// — is a failure: a delivery gate must fail closed.
func scratchEvidencePassed(evidence string) bool {
	if strings.TrimSpace(evidence) == "" {
		return false
	}
	var ev scratchEvidence
	if err := json.Unmarshal([]byte(evidence), &ev); err != nil {
		return false
	}
	return ev.Kind == scratchEvidenceKind && ev.Passed
}

// scratchGateApplies reports whether the domain's frozen policy demands
// machine verification for a scratch deliverable.
func (d *Daemon) scratchGateApplies(ctx context.Context, domainID string) bool {
	checks, _, _, frozen := d.loadDomainChecks(ctx, domainID)
	return frozen && len(checks.Verify) > 0
}

// stampScratchEvidence is the completion-pipeline face of the gate: for a
// writable scratch role, require a passed bundle when the frozen policy has
// Verify commands, then persist it on the run. Returns a failure report for
// the pipeline (the run flips to failed); "" = accepted.
func (d *Daemon) stampScratchEvidence(ctx context.Context, runID, domainID, role, evidence string) string {
	if role == "consult" || role == "review" || role == "verify" {
		return "" // read-only roles: nothing to verify or gate
	}
	if !d.scratchGateApplies(ctx, domainID) {
		// No frozen Verify policy — evidence is optional. Stamp it when the
		// machine sent one so the audit trail keeps what actually ran.
		if strings.TrimSpace(evidence) != "" {
			if _, err := d.st.DB().ExecContext(ctx, `UPDATE run SET evidence=? WHERE id=?`, evidence, runID); err != nil {
				logging.Infof("machine: completion %s: stamp scratch evidence: %v", runID, err)
			}
		}
		return ""
	}
	if !scratchEvidencePassed(evidence) {
		return "verification evidence missing or failed: this scratch domain has a frozen Verify policy, and the run's machine verification did not pass (or uploaded no evidence). Re-run the goal so the machine executes the policy; the deliverable is not deliverable without it."
	}
	if _, err := d.st.DB().ExecContext(ctx, `UPDATE run SET evidence=? WHERE id=?`, evidence, runID); err != nil {
		logging.Infof("machine: completion %s: stamp scratch evidence: %v", runID, err)
	}
	logging.Infof("machine: completion %s: scratch verification evidence accepted", runID)
	return ""
}

// scratchDeliveryBlocker is the delivery face of the gate: when the domain
// is frozen with a Verify policy, the most recent COMPLETED writable run
// must carry a passed bundle. Returns (message, true) when delivery is
// allowed.
func (d *Daemon) scratchDeliveryBlocker(ctx context.Context, goalID, domainID string) (string, bool) {
	if !d.scratchGateApplies(ctx, domainID) {
		return "", true
	}
	var evidence string
	err := d.st.DB().QueryRowContext(ctx,
		`SELECT COALESCE(evidence,'') FROM run
		 WHERE goal_id=? AND role IN ('owner','subgoal') AND status='completed'
		 ORDER BY finished_at DESC LIMIT 1`, goalID).Scan(&evidence)
	if err != nil || !scratchEvidencePassed(evidence) {
		return "deliver: 交付校验未通过——该 scratch 域配置了冻结的 Verify 策略，但最近一次通过的运行没有机器验证证据（或验证未通过）。请重新执行目标（agent 运行会执行策略并上传证据）后再交付。", false
	}
	return "", true
}
