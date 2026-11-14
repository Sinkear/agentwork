package daemon

import (
	"context"
	"time"

	"github.com/eushing/agentwork/internal/logging"
)

// verifySweepInterval is how often the daemon heals verifying sub-goals
// (findings §W6, contract 7.3). The run-end reconcile (ReconcileVerifyRun)
// already heals the normal case; this sweep is the durable safety net for
// everything the event path cannot see: a daemon crash between a verify
// run's terminal UPDATE and its reconcile, a verdict that never reached the
// DB, or a sub-goal whose verify run vanished. It never invents a verdict —
// it re-dispatches once or parks the goal for a human.
const verifySweepInterval = 2 * time.Minute

// runVerifySweeper keeps the verify-integrity sweeper on a 2-minute cadence
// until ctx is cancelled. The startup pass is run synchronously by Run right
// after ReconcileAllActive (so a restart heals stuck verifying sub-goals
// before dispatch resumes); this loop covers the live window.
func (d *Daemon) runVerifySweeper(ctx context.Context) {
	tick := time.NewTicker(verifySweepInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			d.sweepVerifyingSubGoals(ctx)
		}
	}
}

// sweepVerifyingSubGoals runs ONE verify-integrity pass: heal every
// verifying sub-goal whose latest verify run is terminal without a verdict
// (same policy as ReconcileVerifyRun — one re-dispatch, then park), and
// raise the one-hour stale alarm for the rest. Errors are logged, not fatal:
// a transient DB error must not take the daemon down, and the next tick
// retries.
func (d *Daemon) sweepVerifyingSubGoals(ctx context.Context) {
	if d.goalSvc == nil {
		return
	}
	requeued, parked, alerted, err := d.goalSvc.SweepVerifying(ctx)
	if err != nil {
		logging.Errorf("daemon: verify sweep: %v", err)
		return
	}
	if requeued+parked+alerted > 0 {
		logging.Infof("daemon: verify sweep: %d re-queued, %d parked, %d stale alarm(s)", requeued, parked, alerted)
	}
}
