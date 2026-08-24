package ai

import (
	"testing"

	"github.com/engine/server/quota"
)

// Quota movement is only attributable to a run that had the binding window to
// itself. The plan allows a maxConcurrency of 3, so overlapping runs are the
// normal case, and a delta measured across two of them is the sum of the pair —
// which would then be recorded against each one separately and inflate the
// learned cost of both.
func TestOverlappingRunsAreNotMeasurable(t *testing.T) {
	g := &quotaGate{}

	solo := g.beginQuotaObservation()
	if !g.endQuotaObservation(solo) {
		t.Error("a run that never overlapped anything should be measurable")
	}

	first := g.beginQuotaObservation()
	second := g.beginQuotaObservation()
	if g.endQuotaObservation(first) {
		t.Error("the earlier run was overlapped by the later one and must not be measured")
	}
	if g.endQuotaObservation(second) {
		t.Error("the later run started while another was open and must not be measured")
	}

	// Overlap does not poison the future: once both are closed, the next run is
	// alone again.
	after := g.beginQuotaObservation()
	if !g.endQuotaObservation(after) {
		t.Error("a run starting after the overlap cleared should be measurable again")
	}
}

// The dispatch path has error returns that never reach quotaAfter — a failed
// stdout pipe, a `claude` binary that will not start. If those left an
// observation open, every later run would look overlapped and calibration would
// stop for the life of the process.
func TestAbandonedObservationDoesNotTaintLaterRuns(t *testing.T) {
	g := &quotaGate{}

	abandoned := g.beginQuotaObservation()
	g.endQuotaObservation(abandoned)
	// Closing twice is what the deferred release does after quotaAfter already
	// closed the bracket; it must not be mistaken for a second run.
	if g.endQuotaObservation(abandoned) {
		t.Error("closing an observation twice must not report it as a fresh measurable run")
	}

	next := g.beginQuotaObservation()
	if !g.endQuotaObservation(next) {
		t.Error("a released observation left the gate tainted")
	}
}

// The binding window is whichever one the governor says constrains us, and that
// can be a per-model sub-limit that neither session nor week covers. Guessing
// "week, else session" would read the wrong number and calibrate against it.
func TestBindingWindowFollowsTheGovernorsChoice(t *testing.T) {
	st := quota.Status{Accounts: []quota.AccountStatus{{
		Name:       "default",
		Ok:         true,
		FetchedAt:  "2026-08-23T22:00:00Z",
		Session:    quota.WindowStatus{Name: "session", Known: true, Percent: 3},
		Week:       quota.WindowStatus{Name: "week", Known: true, Percent: 19},
		PerModel:   []quota.WindowStatus{{Name: "week:opus", Known: true, Percent: 71}},
		Assessment: quota.Assessment{Binding: "week:opus"},
	}}}

	pct, fetched, known := bindingWindow(st, "default")
	if !known {
		t.Fatal("a readable account reported an unknown binding window")
	}
	if pct != 71 {
		t.Errorf("binding percent = %v, want the per-model sub-limit 71", pct)
	}
	if fetched != "2026-08-23T22:00:00Z" {
		t.Errorf("fetchedAt = %q, want the probe identity behind the reading", fetched)
	}

	if _, _, known := bindingWindow(st, "other"); known {
		t.Error("an account that is not in the status must not report a known reading")
	}
}

// "We could not read the window" and "the window is at 0%" lead to opposite
// decisions, and a Percent of 0 looks identical to both. Only Known separates
// them, so an unreadable account must never seed the ledger.
func TestUnreadableAccountIsNotMeasured(t *testing.T) {
	st := quota.Status{Accounts: []quota.AccountStatus{{
		Name:       "default",
		Ok:         false,
		Session:    quota.WindowStatus{Name: "session", Known: false},
		Week:       quota.WindowStatus{Name: "week", Known: false},
		Assessment: quota.Assessment{Binding: "week"},
	}}}

	if _, _, known := bindingWindow(st, "default"); known {
		t.Error("an account that could not be probed reported a usable reading")
	}
}

// A reading taken from the same probe as the "before" one is that probe handed
// back twice, not a measurement: Status shares the prober's 90s cache, so any
// run shorter than the TTL sees a delta of exactly 0. Recording that 0 would
// look like a run that cost nothing, which is the cheapest possible
// configuration and would win every recommendation.
func TestUnmeasurableRunsRecordZeroNotAFalseDelta(t *testing.T) {
	g := &quotaGate{}

	// No observation was opened (the reading was unusable at dispatch time).
	if got := g.closeQuota(QuotaDispatch{}, true); got != 0 {
		t.Errorf("an unopened observation measured %v, want 0", got)
	}

	// An overlapped run closes its bracket but reports nothing, and must not
	// consult the governor to do so — g.governor is nil here, so a measurement
	// attempt would panic.
	id := g.beginQuotaObservation()
	other := g.beginQuotaObservation()
	if got := g.closeQuota(QuotaDispatch{obsID: id, pctBefore: 19}, true); got != 0 {
		t.Errorf("an overlapped run measured %v, want 0", got)
	}
	g.endQuotaObservation(other)

	// A run we are not recording still releases its observation, or the next run
	// would inherit the taint.
	skipped := g.beginQuotaObservation()
	if got := g.closeQuota(QuotaDispatch{obsID: skipped, pctBefore: 19}, false); got != 0 {
		t.Errorf("a non-recorded run measured %v, want 0", got)
	}
	next := g.beginQuotaObservation()
	if !g.endQuotaObservation(next) {
		t.Error("a non-recorded run did not release its observation")
	}
}
