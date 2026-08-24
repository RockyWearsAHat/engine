package ai

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The parallel orchestrator's whole reason to exist is that two plan steps can
// be in flight at once. Nothing asserted that. The dispatcher had a ceiling it
// never applied, a cap that came from a literal, and no test that ever observed
// two workers overlapping — so "parallel" was a claim about the design rather
// than a property of the code.
//
// These two tests pin the property from both sides: with room, work overlaps;
// with the governor's ceiling at one, the same plan runs strictly serially.
// Together they are what makes the quota tier a real lever rather than a number
// in a JSON response.

// onePlanStep is the smallest planner output that parsePlanFromText accepts —
// the numbered-markdown shape buildPlannerPromptWithContext asks for.
//
// Several event-loop tests used to stub the planner with "[]" and then inject a
// team by hand from the "execute" phase callback. That is not a shape any
// production path produces, and it quietly depended on an empty plan being
// survivable — which is exactly the defect that let a real run spin its whole
// iteration budget in six seconds.
const onePlanStep = "1. Build the thing\n   Implement it and pin the behaviour with a test.\n   Acceptance: `go test ./...` passes\n"

// plannerPromptMarker identifies the planner's prompt in a ChatFn stub. Matching
// the real prompt matters: the stubs used to look for the string "project
// planner", which the shared planner prompt does not contain, so they silently
// fell through to the generic branch and returned prose where a plan was due.
const plannerPromptMarker = "numbered build plan"

// concurrencyProbe records how many workers were inside ChatFn at once.
type concurrencyProbe struct {
	mu      sync.Mutex
	inFlgt  int
	peak    int
	entered chan struct{}
	once    sync.Once
	want    int
}

func newConcurrencyProbe(want int) *concurrencyProbe {
	return &concurrencyProbe{entered: make(chan struct{}), want: want}
}

func (p *concurrencyProbe) enter() {
	p.mu.Lock()
	p.inFlgt++
	if p.inFlgt > p.peak {
		p.peak = p.inFlgt
	}
	reached := p.inFlgt >= p.want
	p.mu.Unlock()
	if reached {
		p.once.Do(func() { close(p.entered) })
	}
}

func (p *concurrencyProbe) leave() {
	p.mu.Lock()
	p.inFlgt--
	p.mu.Unlock()
}

func (p *concurrencyProbe) peakSeen() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.peak
}

// dispatcherForPlan builds a brain with n independent plan steps, one team per
// step, and a dispatcher whose ceiling comes from capFn — the same shape the
// event orchestrator builds in startEventOrchestrator.
func dispatcherForPlan(t *testing.T, n int, capFn func() int, chat func(*ChatContext, string)) (*TeamDispatcher, *OrchestrationBrain, []string) {
	t.Helper()
	projectDir := t.TempDir()
	brain, err := NewOrchestrationBrain(projectDir, "owner", "repo", "brief", "conc")
	if err != nil {
		t.Fatalf("brain: %v", err)
	}
	steps := make([]PlanStep, n)
	ids := make([]string, n)
	for i := range steps {
		steps[i] = PlanStep{Index: i + 1, Title: "Step", Body: "Do the thing", Acceptance: "it works"}
	}
	if err := brain.UpdatePlan(steps); err != nil {
		t.Fatalf("update plan: %v", err)
	}
	for i := range steps {
		id := "team-" + string(rune('a'+i))
		ids[i] = id
		// No Deps: these steps are independent, which is the precondition the
		// orchestrator itself requires before it will run anything in parallel.
		if err := brain.AddTeam(id, "api", []int{i}, nil); err != nil {
			t.Fatalf("add team %s: %v", id, err)
		}
	}
	cfg := OrchestratorConfig{ProjectPath: projectDir, SessionIDPrefix: "conc", ChatFn: chat}
	return NewTeamDispatcherWithCap(brain, NewEventBus(), cfg, capFn, NewAgentCommsHub()), brain, ids
}

func TestTeamDispatcher_RunsStepsConcurrently(t *testing.T) {
	// Hold every worker until all three have arrived: that asserts the ceiling
	// is actually spent, not merely that two things once overlapped.
	probe := newConcurrencyProbe(3)
	chat := func(ctx *ChatContext, prompt string) {
		probe.enter()
		defer probe.leave()
		// Hold the worker open until the others have also arrived. If the
		// dispatcher were serial this blocks until the deadline and the peak
		// stays at 1, which is exactly the failure we want to see.
		select {
		case <-probe.entered:
		case <-time.After(3 * time.Second):
		}
		ctx.OnChunk("signal_done", false)
	}

	dispatcher, brain, ids := dispatcherForPlan(t, 3, func() int { return 3 }, chat)
	defer dispatcher.Stop()

	for _, id := range ids {
		if err := dispatcher.DispatchTeam(id); err != nil {
			t.Fatalf("dispatch %s: %v", id, err)
		}
	}
	dispatcher.Wait()

	if got := probe.peakSeen(); got < 2 {
		t.Fatalf("expected at least 2 plan steps in flight at once, peak was %d", got)
	}
	t.Logf("peak concurrent plan steps: %d (ceiling 3)", probe.peakSeen())

	for _, id := range ids {
		team, err := brain.GetTeam(id)
		if err != nil {
			t.Fatalf("get team %s: %v", id, err)
		}
		if team.Status != "done" {
			t.Fatalf("team %s status = %q, want done", id, team.Status)
		}
	}
}

func TestTeamDispatcher_QuotaCeilingSerialisesWork(t *testing.T) {
	// Same plan, same teams, ceiling of one: the governor's tightest setting has
	// to actually hold the run to a single step at a time.
	probe := newConcurrencyProbe(2)
	var calls atomic.Int64
	chat := func(ctx *ChatContext, prompt string) {
		probe.enter()
		defer probe.leave()
		calls.Add(1)
		// A real second worker would have arrived within this window if the
		// ceiling were not enforced.
		select {
		case <-probe.entered:
			// Only reachable if two workers overlapped, which is the bug.
		case <-time.After(150 * time.Millisecond):
		}
		ctx.OnChunk("signal_done", false)
	}

	dispatcher, _, ids := dispatcherForPlan(t, 3, func() int { return 1 }, chat)
	defer dispatcher.Stop()

	var rejected int
	for _, id := range ids {
		if err := dispatcher.DispatchTeam(id); err != nil {
			if err == ErrTeamCapReached {
				rejected++
				continue
			}
			t.Fatalf("dispatch %s: %v", id, err)
		}
	}
	dispatcher.Wait()

	if got := probe.peakSeen(); got != 1 {
		t.Fatalf("ceiling of 1 allowed %d concurrent steps", got)
	}
	if rejected == 0 {
		t.Fatal("expected the ceiling to reject at least one dispatch, none were rejected")
	}
	t.Logf("ceiling 1: peak concurrency %d, %d dispatch(es) held back by the cap", probe.peakSeen(), rejected)
}

// A parallel run has to report progress, not just produce it.
//
// OnPlanUpdate is the only channel the outside world has: the task API turns it
// into stepsDone, and SARA and the console read that. The event bus the
// dispatcher emits on has no subscribers outside this package.
//
// The parallel path used to fire OnPlanUpdate exactly once, when the plan was
// created. A live eleven-step run therefore reported "0 of 11" from start to
// finish while steadily building the project — and a supervisor watching a
// number that never moves cannot tell progress from a hang, which is precisely
// the judgement it exists to make.
func TestTeamDispatcher_ReportsProgressPerStep(t *testing.T) {
	projectDir := t.TempDir()
	brain, err := NewOrchestrationBrain(projectDir, "owner", "repo", "brief", "prog")
	if err != nil {
		t.Fatalf("brain: %v", err)
	}
	const steps = 3
	plan := make([]PlanStep, steps)
	for i := range plan {
		plan[i] = PlanStep{Index: i + 1, Title: "Step", Body: "Do the thing", Acceptance: "it works"}
	}
	if err := brain.UpdatePlan(plan); err != nil {
		t.Fatalf("update plan: %v", err)
	}
	ids := make([]string, steps)
	for i := range plan {
		ids[i] = "team-" + string(rune('a'+i))
		if err := brain.AddTeam(ids[i], "api", []int{i}, nil); err != nil {
			t.Fatalf("add team: %v", err)
		}
	}

	// Written from the team worker goroutines, so guarded.
	var mu sync.Mutex
	var updates, highest int

	cfg := OrchestratorConfig{
		ProjectPath:     projectDir,
		SessionIDPrefix: "prog",
		ChatFn:          func(ctx *ChatContext, _ string) { ctx.OnChunk("signal_done", false) },
		OnPlanUpdate: func(st *OrchestrationState) {
			done := 0
			for _, s := range st.Plan {
				if s.Done {
					done++
				}
			}
			mu.Lock()
			updates++
			if done > highest {
				highest = done
			}
			mu.Unlock()
		},
	}

	dispatcher := NewTeamDispatcherWithCap(brain, NewEventBus(), cfg, func() int { return steps }, NewAgentCommsHub())
	defer dispatcher.Stop()
	for _, id := range ids {
		if err := dispatcher.DispatchTeam(id); err != nil {
			t.Fatalf("dispatch %s: %v", id, err)
		}
	}
	dispatcher.Wait()

	mu.Lock()
	gotUpdates, gotHighest := updates, highest
	mu.Unlock()

	if gotUpdates < steps {
		t.Errorf("OnPlanUpdate fired %d time(s) for %d completed steps — progress is not being reported per step", gotUpdates, steps)
	}
	// The final update must see the whole plan done. Reporting a rising number
	// that stops short of the truth is its own kind of lie.
	if gotHighest != steps {
		t.Errorf("highest reported done-count = %d, want %d", gotHighest, steps)
	}
	t.Logf("%d progress updates for %d steps, peaking at %d/%d done", gotUpdates, steps, gotHighest, steps)
}
