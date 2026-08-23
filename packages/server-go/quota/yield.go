package quota

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Yield is the objective. Everything else in this package is machinery for
// moving it.
//
// WHAT THE NUMBER IS
//
//	yield = delivered value / quota percentage points consumed
//
// Read it as "results per percent of window". Three percent spent writing
// hundreds of projects the user is happy with scores enormously better than a
// hundred percent spent on two, and it does so without any tuning knob: a
// hundred good results for 3% is a yield of ~33, two good results for 100% is a
// yield of 0.02. A plain honest ratio already separates those by three orders of
// magnitude, so this deliberately does NOT add an exponent or a bonus curve to
// exaggerate it. The ratio is the truth; distorting it would only make the score
// harder to reason about and easier to game.
//
// WHY THIS AND NOT UTILIZATION
//
// The intuitive target — "use the whole weekly limit" — is not merely a weaker
// objective, it is the wrong direction. Utilization rewards spending, so the way
// to score well is to burn the window, and the engine that burns the window is
// the engine that stops for days. Yield rewards the opposite: every percent NOT
// spent on a result that was going to happen anyway raises the score, and the
// capacity stays available for the next thing. Quota saved is not quota wasted.
//
// THE FAILURE MODE THIS MUST NOT HAVE
//
// A pure efficiency objective has an obvious degenerate solution: the cheapest
// way to spend zero quota is to build nothing, and the second cheapest is to
// emit slop that someone else has to fix. Both score infinitely well on a naive
// ratio, and an engine tuned on one would get quieter and worse while its
// dashboard got greener.
//
// So value is defined by whether the USER was happy, not by whether the engine
// finished, and quality is a GATE rather than a term in the ratio. Yield ranks
// configurations only among those that clear the satisfaction bar; below the bar
// a configuration is out no matter how cheap it was. Frugality is how more good
// results get delivered — never something bought with worse ones.
//
// WHY SATISFACTION HAS TO COME FROM OUTSIDE
//
// The engine's own success signal ("the process exited zero and produced text")
// cannot see the difference between work that landed and work that was quietly
// thrown away. Only the user, or a supervisor speaking for them, knows that. So
// ratings arrive LATER than the runs they describe, through RateProject, and are
// attributed back across the runs that produced the project.

// Satisfaction is how the delivered work was actually received.
type Satisfaction int

const (
	// SatisfactionUnknown: not yet rated. Contributes no value and no evidence.
	// Crucially it is not "bad" — unrated work must not drag a config down, or
	// every config decays toward zero simply because feedback is sparse.
	SatisfactionUnknown Satisfaction = iota
	// SatisfactionRejected: consumed quota and delivered nothing usable.
	SatisfactionRejected
	// SatisfactionRework: usable only after significant fixing. Positive, because
	// something did arrive, but small — the fix-up cost is real and usually paid
	// out of the same windows.
	SatisfactionRework
	// SatisfactionAccepted: did the job.
	SatisfactionAccepted
	// SatisfactionPraised: the user was actively happy with it.
	SatisfactionPraised
)

func (s Satisfaction) String() string {
	switch s {
	case SatisfactionRejected:
		return "rejected"
	case SatisfactionRework:
		return "rework"
	case SatisfactionAccepted:
		return "accepted"
	case SatisfactionPraised:
		return "praised"
	}
	return "unrated"
}

// ParseSatisfaction reads a rating from a string, for API and CLI callers.
func ParseSatisfaction(s string) (Satisfaction, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "rejected", "reject", "bad":
		return SatisfactionRejected, true
	case "rework", "fixed", "partial":
		return SatisfactionRework, true
	case "accepted", "accept", "ok", "good":
		return SatisfactionAccepted, true
	case "praised", "praise", "great", "excellent":
		return SatisfactionPraised, true
	case "", "unknown", "unrated":
		return SatisfactionUnknown, true
	}
	return SatisfactionUnknown, false
}

// Value is the numerator of yield: how much a result of this quality is worth.
//
// Praise is worth appreciably more than mere acceptance because "results the
// user is happy with" is the actual goal — an engine indifferent between
// delighting and merely satisfying will drift toward the cheaper of the two, and
// the cheaper one is always merely satisfying.
//
// Rejection is NEGATIVE, not zero. Zero would make a rejected run merely
// worthless, when in fact it spent quota, spent the user's attention, and left
// the work still to do. A configuration that reliably produces rejects must sink
// below one that produces nothing at all.
func (s Satisfaction) Value() float64 {
	switch s {
	case SatisfactionRejected:
		return -0.5
	case SatisfactionRework:
		return 0.25
	case SatisfactionAccepted:
		return 1.0
	case SatisfactionPraised:
		return 1.75
	}
	return 0
}

// Good reports whether this rating counts as a result the user was happy with.
// This is the quality gate: rework does not clear it.
func (s Satisfaction) Good() bool {
	return s == SatisfactionAccepted || s == SatisfactionPraised
}

// minQuotaCost floors the denominator of a yield ratio.
//
// Without a floor, a run too small to move any measurable quota divides by ~0
// and reports an astronomical yield, so the highest-scoring strategy becomes
// "do many trivial things". A hundredth of a percent is below the resolution of
// anything we can observe anyway, so treating smaller costs as equal to it loses
// no real information and closes the hole.
const minQuotaCost = 0.01

// Yield is value per quota percentage point. Returns 0 for an unmeasured or
// valueless record rather than an infinity, so it can be sorted safely.
func yieldOf(value, quotaPct float64) float64 {
	if value == 0 {
		return 0
	}
	if quotaPct < minQuotaCost {
		quotaPct = minQuotaCost
	}
	return value / quotaPct
}

// Rating is one piece of feedback about delivered work.
type Rating struct {
	// Project identifies the unit of delivered work being rated. It must match
	// the ProjectID recorded on the outcomes that produced it.
	Project string `json:"project"`
	// Satisfaction is how it was received.
	Satisfaction Satisfaction `json:"-"`
	// SatisfactionName is the string form, for JSON callers.
	SatisfactionName string `json:"satisfaction"`
	// Note is optional free text, kept for the report so a human can see WHY a
	// configuration scores as it does.
	Note string    `json:"note,omitempty"`
	At   time.Time `json:"at"`
}

// contribution records that a (role, config) did some share of the work on a
// project, so a rating arriving later can be attributed back to it.
type contribution struct {
	key      string
	role     string
	config   Config
	tokens   int64
	quotaPct float64
	at       time.Time
}

// project accumulates the runs that went into one unit of delivered work, until
// a rating arrives for it.
type project struct {
	id      string
	parts   []contribution
	tokens  int64
	rated   bool
	rating  Satisfaction
	firstAt time.Time
	lastAt  time.Time
}

// pendingPart and pendingProject are the on-disk form of the project buckets
// that have not been rated yet.
//
// They have to be persisted, and the reason is worth stating: a rating comes
// from a person, minutes or hours after the work, and the engine restarts
// between the two routinely. If the buckets lived only in memory, every restart
// would silently make the preceding work unrateable — RateProject would return
// no match, and the praise or complaint would be thrown away. Feedback is the
// scarcest input this score has; losing it to a process boundary is not
// acceptable. Rated buckets are already folded into the totals and deleted, so
// only the pending ones appear here.
type pendingPart struct {
	Role     string    `json:"role"`
	Config   Config    `json:"config"`
	Tokens   int64     `json:"tokens"`
	QuotaPct float64   `json:"quotaPct,omitempty"`
	At       time.Time `json:"at"`
}

type pendingProject struct {
	ID      string        `json:"id"`
	Parts   []pendingPart `json:"parts"`
	Tokens  int64         `json:"tokens"`
	FirstAt time.Time     `json:"firstAt"`
	LastAt  time.Time     `json:"lastAt"`
}

// pendingLocked snapshots the unrated buckets for Save. Caller holds the lock.
func (l *Ledger) pendingLocked() []pendingProject {
	out := make([]pendingProject, 0, len(l.projectOrder))
	for _, id := range l.projectOrder {
		p, ok := l.projects[id]
		if !ok || p.rated || len(p.parts) == 0 {
			continue
		}
		pp := pendingProject{ID: p.id, Tokens: p.tokens, FirstAt: p.firstAt, LastAt: p.lastAt}
		for _, c := range p.parts {
			pp.Parts = append(pp.Parts, pendingPart{
				Role: c.role, Config: c.config, Tokens: c.tokens, QuotaPct: c.quotaPct, At: c.at,
			})
		}
		out = append(out, pp)
	}
	return out
}

// restorePending rebuilds the unrated buckets from disk, preserving order so
// eviction still drops the oldest first.
func (l *Ledger) restorePending(saved []pendingProject) {
	for _, pp := range saved {
		id := strings.TrimSpace(pp.ID)
		if id == "" || len(pp.Parts) == 0 {
			continue
		}
		if _, exists := l.projects[id]; exists {
			continue
		}
		p := &project{id: id, tokens: pp.Tokens, firstAt: pp.FirstAt, lastAt: pp.LastAt}
		for _, part := range pp.Parts {
			p.parts = append(p.parts, contribution{
				key:      statKey(part.Role, part.Config),
				role:     part.Role,
				config:   part.Config,
				tokens:   part.Tokens,
				quotaPct: part.QuotaPct,
				at:       part.At,
			})
		}
		l.projects[id] = p
		l.projectOrder = append(l.projectOrder, id)
	}
	l.evictProjectsLocked()
}

// Scoreboard is the headline: the score SARA is trying to raise, and enough
// context to see whether it is actually rising.
type Scoreboard struct {
	// Yield is delivered value per percent of quota, over all rated work.
	Yield float64 `json:"yield"`
	// Known is false when nothing has been rated yet. A yield of 0 from no
	// evidence must not read as "performing badly".
	Known bool `json:"known"`

	// Value and QuotaPct are the raw numerator and denominator.
	Value    float64 `json:"value"`
	QuotaPct float64 `json:"quotaPct"`

	Projects int `json:"projects"`
	Praised  int `json:"praised"`
	Accepted int `json:"accepted"`
	Rework   int `json:"rework"`
	Rejected int `json:"rejected"`

	// GoodRate is the share of rated work the user was happy with. This is the
	// gate; a rising Yield with a falling GoodRate is the engine getting cheaper
	// by getting worse, and is the one pattern that must never be read as
	// improvement.
	GoodRate float64 `json:"goodRate"`

	// QuotaPerGood is the inverse view, in the units the limit is denominated in:
	// percentage points of window per result the user was happy with. Lower is
	// better. This is usually the more legible number of the two.
	QuotaPerGood float64 `json:"quotaPerGood"`

	// Trend compares recent yield against the earlier baseline, as a ratio.
	// >1 means improving. Zero when there is not yet enough history.
	Trend float64 `json:"trend,omitempty"`

	Since string `json:"since,omitempty"`
}

// Improving reports whether the engine is getting better on BOTH axes — more
// results per percent, without a drop in how well those results land.
func (s Scoreboard) Improving() bool {
	return s.Known && s.Trend > 1 && s.GoodRate >= 0.5
}

// Summary renders the scoreboard as the one line worth putting in front of a
// human.
func (s Scoreboard) Summary() string {
	if !s.Known {
		return "yield: no rated work yet — score unknown (not zero)"
	}
	b := fmt.Sprintf("yield %.1f results/%%  (%.2f%% of a window per good result)  %d rated: %d praised, %d accepted, %d rework, %d rejected",
		s.Yield, s.QuotaPerGood, s.Projects, s.Praised, s.Accepted, s.Rework, s.Rejected)
	if s.Trend > 0 {
		switch {
		case s.Trend > 1.05:
			b += fmt.Sprintf("  ↑ %.0f%% better than baseline", (s.Trend-1)*100)
		case s.Trend < 0.95:
			b += fmt.Sprintf("  ↓ %.0f%% worse than baseline", (1-s.Trend)*100)
		default:
			b += "  → flat versus baseline"
		}
	}
	return b
}

// RecordProjectPart notes that a run contributed to a project. Called for every
// dispatch; cheap, and the only way a later rating can find its way back to the
// configurations that earned it.
func (l *Ledger) recordProjectPart(o Outcome, quotaPct float64) {
	id := strings.TrimSpace(o.ProjectID)
	if id == "" {
		return
	}
	p, ok := l.projects[id]
	if !ok {
		p = &project{id: id, firstAt: o.At}
		l.projects[id] = p
		l.projectOrder = append(l.projectOrder, id)
		l.evictProjectsLocked()
	}
	p.parts = append(p.parts, contribution{
		key:      statKey(o.Role, o.Config),
		role:     o.Role,
		config:   o.Config,
		tokens:   o.Tokens,
		quotaPct: quotaPct,
		at:       o.At,
	})
	p.tokens += o.Tokens
	p.lastAt = o.At
}

// maxTrackedProjects bounds the unrated backlog. Ratings that arrive after this
// many newer projects have started are dropped rather than misattributed; an
// engine that never receives feedback must not grow without limit.
const maxTrackedProjects = 512

func (l *Ledger) evictProjectsLocked() {
	for len(l.projectOrder) > maxTrackedProjects {
		oldest := l.projectOrder[0]
		l.projectOrder = l.projectOrder[1:]
		delete(l.projects, oldest)
	}
}

// RateProject folds user feedback into every configuration that contributed to
// the project, and into the global score.
//
// Attribution is by SHARE OF SPEND. If a project took four runs and one of them
// burned 70% of the tokens, that run earns 70% of the praise and 70% of the
// blame. Splitting evenly instead would let an expensive run hide behind cheap
// ones — the exact configuration the score exists to find.
//
// Returns false when the project is unknown, which happens when feedback arrives
// after the backlog has rolled over, or for work this engine did not do.
func (l *Ledger) RateProject(projectID string, s Satisfaction, note string) bool {
	id := strings.TrimSpace(projectID)
	if id == "" || s == SatisfactionUnknown {
		return false
	}

	l.mu.Lock()
	p, ok := l.projects[id]
	if !ok || len(p.parts) == 0 {
		l.mu.Unlock()
		return false
	}
	p.rated, p.rating = true, s
	// Rating closes this bucket and the project starts accumulating again from
	// empty. A project is rated once per delivery, not once ever, and without
	// this the second thing SARA ships from the same path would be unrateable.
	delete(l.projects, id)
	for i, existing := range l.projectOrder {
		if existing == id {
			l.projectOrder = append(l.projectOrder[:i], l.projectOrder[i+1:]...)
			break
		}
	}

	value := s.Value()
	totalTokens := p.tokens
	var projectQuota float64
	for _, c := range p.parts {
		projectQuota += l.quotaCostLocked(c.tokens, c.quotaPct)
	}

	for _, c := range p.parts {
		st, ok := l.stats[c.key]
		if !ok {
			st = &Stat{Role: c.role, Config: c.config}
			l.stats[c.key] = st
		}
		// Share of the project's spend that this run was responsible for.
		share := 1.0
		if totalTokens > 0 {
			share = float64(c.tokens) / float64(totalTokens)
		} else if n := len(p.parts); n > 0 {
			share = 1 / float64(n)
		}
		st.Value += value * share
		st.RatedShare += share
		switch s {
		case SatisfactionPraised:
			st.Praised += share
		case SatisfactionAccepted:
			st.Accepted += share
		case SatisfactionRework:
			st.Rework += share
		case SatisfactionRejected:
			st.Rejected += share
		}
	}

	l.totalValue += value
	l.totalQuota += projectQuota
	l.ratedProjects++
	switch s {
	case SatisfactionPraised:
		l.praised++
	case SatisfactionAccepted:
		l.accepted++
	case SatisfactionRework:
		l.rework++
	case SatisfactionRejected:
		l.rejected++
	}
	l.history = append(l.history, yieldPoint{At: l.now(), Value: value, QuotaPct: projectQuota})
	if len(l.history) > maxHistoryPoints {
		l.history = l.history[len(l.history)-maxHistoryPoints:]
	}
	if note != "" {
		l.notes = append(l.notes, Rating{Project: id, Satisfaction: s, SatisfactionName: s.String(), Note: note, At: l.now()})
		if len(l.notes) > 32 {
			l.notes = l.notes[len(l.notes)-32:]
		}
	}
	l.dirty = true
	l.mu.Unlock()

	_ = l.Save()
	return true
}

// yieldPoint is one rated project, for trend.
type yieldPoint struct {
	At       time.Time `json:"at"`
	Value    float64   `json:"value"`
	QuotaPct float64   `json:"quotaPct"`
}

const maxHistoryPoints = 512

// quotaCostLocked estimates what a run cost in window percentage points.
//
// Prefers the measured delta when one was observed. Otherwise converts tokens
// through the learned calibration. Before any calibration exists it returns 0,
// which is honest — we genuinely cannot price the run yet — and callers treat a
// zero denominator as "unknown", never as "free".
func (l *Ledger) quotaCostLocked(tokens int64, measured float64) float64 {
	if measured > 0 {
		return measured
	}
	if l.pctPerMTok > 0 && tokens > 0 {
		return float64(tokens) / 1e6 * l.pctPerMTok
	}
	return 0
}

// statQuotaCostLocked estimates the total window percentage a configuration has
// consumed across all its runs.
//
// Prefers the token-derived figure once a calibration exists, because it covers
// every run uniformly. The measured-delta sum only covers the minority of runs
// where the percentage happened to tick over, so using it as a total would
// understate cost — and understating the denominator inflates yield, which would
// bias the score toward exactly the configurations we have measured least.
// The second return reports whether the cost is actually measured. An unmeasured
// cost must not be treated as a cheap one: yieldOf floors the denominator, so a
// configuration with no spend history would post value/minQuotaCost — and since
// attributed value grows with share of spend, that hands the best apparent yield
// to whichever configuration spent the MOST. That is the exact inversion of what
// this score exists to reward, so callers ranking on yield have to exclude these
// and treat them as under-sampled instead.
func (l *Ledger) statQuotaCostLocked(s Stat) (float64, bool) {
	if l.pctPerMTok > 0 && s.Tokens > 0 {
		return float64(s.Tokens) / 1e6 * l.pctPerMTok, true
	}
	if s.QuotaPct > 0 {
		return s.QuotaPct, true
	}
	return 0, false
}

// Scoreboard computes the current score.
func (l *Ledger) Scoreboard() Scoreboard {
	l.mu.RLock()
	defer l.mu.RUnlock()

	sb := Scoreboard{
		Value:    l.totalValue,
		QuotaPct: l.totalQuota,
		Projects: l.ratedProjects,
		Praised:  l.praised,
		Accepted: l.accepted,
		Rework:   l.rework,
		Rejected: l.rejected,
	}
	if l.ratedProjects == 0 {
		return sb
	}
	sb.Known = true
	sb.Yield = yieldOf(l.totalValue, l.totalQuota)
	good := l.praised + l.accepted
	sb.GoodRate = float64(good) / float64(l.ratedProjects)
	if good > 0 {
		q := l.totalQuota
		if q < minQuotaCost {
			q = minQuotaCost
		}
		sb.QuotaPerGood = q / float64(good)
	}
	if len(l.history) > 0 {
		sb.Since = l.history[0].At.Format(time.RFC3339)
	}
	sb.Trend = l.trendLocked()
	return sb
}

// trendLocked compares the most recent half of rated history against the older
// half. Halves rather than a fixed window so the comparison stays meaningful
// whether there are twenty projects or two thousand.
func (l *Ledger) trendLocked() float64 {
	const minForTrend = 6
	if len(l.history) < minForTrend {
		return 0
	}
	mid := len(l.history) / 2
	sum := func(pts []yieldPoint) (v, q float64) {
		for _, p := range pts {
			v += p.Value
			q += p.QuotaPct
		}
		return
	}
	oldV, oldQ := sum(l.history[:mid])
	newV, newQ := sum(l.history[mid:])
	oldY, newY := yieldOf(oldV, oldQ), yieldOf(newV, newQ)
	if oldY <= 0 {
		return 0
	}
	return newY / oldY
}

// ScoreReport renders the scoreboard plus the per-role detail behind it.
func (l *Ledger) ScoreReport() string {
	sb := l.Scoreboard()
	var b strings.Builder
	b.WriteString(sb.Summary())

	l.mu.RLock()
	stats := make([]Stat, 0, len(l.stats))
	for _, s := range l.stats {
		if s.RatedShare > 0 {
			stats = append(stats, *s)
		}
	}
	// A configuration with no measured spend has no yield to report. Printing one
	// anyway would divide by the floor and rank the most expensive configuration
	// first, so these are shown as unknown and sorted last.
	costs := make(map[string]float64, len(stats))
	costKnown := make(map[string]bool, len(stats))
	for _, s := range stats {
		k := statKey(s.Role, s.Config)
		costs[k], costKnown[k] = l.statQuotaCostLocked(s)
	}
	notes := append([]Rating(nil), l.notes...)
	l.mu.RUnlock()

	if len(stats) == 0 {
		return b.String()
	}

	yieldFor := func(s Stat) (float64, bool) {
		k := statKey(s.Role, s.Config)
		if !costKnown[k] {
			return 0, false
		}
		return yieldOf(s.Value, costs[k]), true
	}

	sort.SliceStable(stats, func(i, j int) bool {
		if stats[i].Role != stats[j].Role {
			return stats[i].Role < stats[j].Role
		}
		yi, oki := yieldFor(stats[i])
		yj, okj := yieldFor(stats[j])
		if oki != okj {
			return oki
		}
		return yi > yj
	})

	b.WriteString("\n\nyield by configuration (higher is better):\n")
	role := ""
	for _, s := range stats {
		if s.Role != role {
			role = s.Role
			fmt.Fprintf(&b, "%s:\n", role)
		}
		y, ok := yieldFor(s)
		shown := "     ?"
		if ok {
			shown = fmt.Sprintf("%6.1f", y)
		}
		fmt.Fprintf(&b, "  %-34s yield %s  %3.0f%% good  (%.1f rated)\n",
			s.Config.String(), shown, s.GoodRate()*100, s.RatedShare)
	}
	if len(notes) > 0 {
		b.WriteString("\nrecent feedback:\n")
		for _, n := range notes[maxInt(0, len(notes)-5):] {
			fmt.Fprintf(&b, "  %-9s %s\n", n.SatisfactionName, n.Note)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// isFinitePositive guards sort comparisons against NaN/Inf leaking in from a
// division we failed to floor.
func isFinitePositive(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0
}
