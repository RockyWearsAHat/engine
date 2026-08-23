package quota

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Parsing `/usage` means parsing text meant for humans, which is a liability:
// upstream can reword it and we would not know. Three things contain that risk.
//
//  1. Every pattern is anchored on the STRUCTURE (a "N% used" limit line, a
//     "Last 24h · N requests" header) rather than on exact prose, so wording
//     tweaks around the numbers survive.
//  2. Anything unrecognised is kept verbatim in Raw / RawBehaviours instead of
//     dropped, so a drift shows up as unexplained text rather than as a
//     confident wrong number.
//  3. A parse that finds no limit line at all returns an error, and the caller
//     turns that into an Unknown snapshot. The one outcome this must never
//     produce is a clean-looking 0%.

var (
	// "Current session: 4% used · resets Aug 23 at 8pm (America/Denver)"
	// "Current week (all models): 14% used · resets Aug 29 at 3pm (America/Denver)"
	// "Current week (Fable): 0% used"
	//
	// The separator is a middle dot in practice; accept any of ·/|/- so a
	// punctuation change does not cost us the reset time.
	limitLineRe = regexp.MustCompile(`^\s*Current\s+(session|week)\s*(?:\(([^)]*)\))?\s*:\s*([0-9]+(?:\.[0-9]+)?)\s*%\s*used\s*(?:[·|\-–—]\s*resets\s+(.+?))?\s*$`)

	// "Last 24h · 6283 requests · 141 sessions"
	periodHeadRe = regexp.MustCompile(`^\s*Last\s+([0-9]+[a-zA-Z]+)\s*[·|\-–—]\s*([0-9,]+)\s+requests\s*[·|\-–—]\s*([0-9,]+)\s+sessions\s*$`)

	// "  65% of your usage was at >150k context"
	behaviourRe = regexp.MustCompile(`^\s*([0-9]+(?:\.[0-9]+)?)\s*%\s+of\s+your\s+usage\s+(.*?)\s*$`)

	// "  Top MCP servers: dx 13%, claude-in-chrome 5%, sara 1%"
	topRe = regexp.MustCompile(`^\s*Top\s+([A-Za-z][A-Za-z ]*?)\s*:\s*(.+?)\s*$`)

	// One "name 13%" entry inside a Top list. The name may contain spaces,
	// slashes and colons (`/frontend-design:frontend-design 5%`), so the percent
	// is anchored at the END rather than the name being anchored at the start.
	shareRe = regexp.MustCompile(`^(.*\S)\s+([0-9]+(?:\.[0-9]+)?)\s*%$`)

	// "Aug 23 at 8pm (America/Denver)" / "Aug 29 at 3:05pm" / "8pm"
	resetRe = regexp.MustCompile(`^(?:([A-Za-z]{3,9})\s+([0-9]{1,2})\s+at\s+)?([0-9]{1,2})(?::([0-9]{2}))?\s*([ap]\.?m\.?)?\s*(?:\(([^)]+)\))?\s*$`)
)

// ErrNoLimits is returned when the output carried no recognisable limit line.
// This is the "we are being lied to by our own parser" guard: it converts a
// wholesale format change into a loud Unknown rather than a quiet 0%.
var ErrNoLimits = errors.New("quota: no limit lines found in /usage output")

// ParseUsage turns the text body of `claude -p "/usage"` into a Snapshot.
//
// now is the reference instant used to resolve the printed reset times, which
// carry no year (and sometimes no date). Injected rather than read from the
// clock so the behaviour is testable across year and day boundaries.
func ParseUsage(text string, now time.Time) (Snapshot, error) {
	s := Snapshot{Ok: true, FetchedAt: now, Raw: text}

	var (
		cur       *Period // period block currently being filled
		sawLimit  bool
		inDrivers bool
	)

	for _, rawLine := range strings.Split(text, "\n") {
		line := strings.TrimRight(rawLine, " \t\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// ── limit lines ──────────────────────────────────────────────────────
		if m := limitLineRe.FindStringSubmatch(line); m != nil {
			kind, qualifier, pctStr, resetStr := m[1], strings.TrimSpace(m[2]), m[3], strings.TrimSpace(m[4])
			pct, err := strconv.ParseFloat(pctStr, 64)
			if err != nil {
				continue
			}
			w := Window{Percent: pct, Label: trimmed}
			if resetStr != "" {
				if at, ok := parseReset(resetStr, now); ok {
					w.ResetsAt, w.HasReset = at, true
				}
			}
			switch {
			case kind == "session":
				w.Name = "session"
				s.Session = w
			case isAllModels(qualifier):
				w.Name = "week"
				s.Week = w
			default:
				// A per-model weekly sub-limit: "Current week (Fable)".
				w.Name = "week:" + strings.ToLower(qualifier)
				s.PerModel = append(s.PerModel, w)
			}
			sawLimit = true
			continue
		}

		// ── attribution ──────────────────────────────────────────────────────
		if m := periodHeadRe.FindStringSubmatch(line); m != nil {
			span := m[1]
			p := Period{
				Span:     span,
				Requests: atoiLoose(m[2]),
				Sessions: atoiLoose(m[3]),
			}
			// Point cur at the field this span owns, so later indented lines
			// accumulate into the right block. An unrecognised span is parsed
			// into a scratch Period and discarded rather than misfiled.
			switch span {
			case "24h":
				s.Last24h = p
				cur = &s.Last24h
			case "7d":
				s.Last7d = p
				cur = &s.Last7d
			default:
				scratch := p
				cur = &scratch
			}
			inDrivers = true
			continue
		}

		if !inDrivers || cur == nil {
			// Leading prose. The first non-empty line before any limit is the
			// plan note ("You are currently using your subscription ...").
			if s.PlanNote == "" && !sawLimit {
				s.PlanNote = trimmed
			}
			continue
		}

		if m := behaviourRe.FindStringSubmatch(line); m != nil {
			pct, err := strconv.ParseFloat(m[1], 64)
			if err != nil {
				continue
			}
			if cur.Behaviours == nil {
				cur.Behaviours = map[string]float64{}
			}
			cur.RawBehaviours = append(cur.RawBehaviours, trimmed)
			if key := behaviourKey(m[2]); key != "" {
				// Keep the largest reading if a phrasing maps twice; never sum,
				// because these are independent characteristics, not parts.
				if old, ok := cur.Behaviours[key]; !ok || pct > old {
					cur.Behaviours[key] = pct
				}
			}
			continue
		}

		if m := topRe.FindStringSubmatch(line); m != nil {
			shares := parseShares(m[2])
			if len(shares) == 0 {
				continue
			}
			switch normaliseTopKind(m[1]) {
			case "skills":
				cur.TopSkills = append(cur.TopSkills, shares...)
			case "subagents":
				cur.TopSubagents = append(cur.TopSubagents, shares...)
			case "plugins":
				cur.TopPlugins = append(cur.TopPlugins, shares...)
			case "mcp servers":
				cur.TopMCPServers = append(cur.TopMCPServers, shares...)
			}
			continue
		}
	}

	if !sawLimit {
		return Snapshot{Ok: false, Err: ErrNoLimits.Error(), FetchedAt: now, Raw: text}, ErrNoLimits
	}
	return s, nil
}

// isAllModels recognises the aggregate weekly line. Matched loosely because the
// qualifier is prose ("all models"), not an identifier.
func isAllModels(q string) bool {
	q = strings.ToLower(strings.TrimSpace(q))
	return q == "" || strings.Contains(q, "all model")
}

// behaviourKey maps the tail of a behaviour line onto a canonical lever key.
// Matching is on the distinguishing noun rather than the full sentence so
// rewording ("was at" -> "came from") does not break it. An unrecognised
// behaviour returns "" and survives only in RawBehaviours.
func behaviourKey(tail string) string {
	t := strings.ToLower(tail)
	switch {
	case strings.Contains(t, "context"):
		return BehaviourHighContext
	case strings.Contains(t, "subagent"):
		return BehaviourSubagentHeavy
	case strings.Contains(t, "parallel"):
		return BehaviourParallelSessions
	case strings.Contains(t, "active for"), strings.Contains(t, "long"):
		return BehaviourLongSessions
	default:
		return ""
	}
}

func normaliseTopKind(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	switch {
	case strings.HasPrefix(k, "skill"):
		return "skills"
	case strings.HasPrefix(k, "subagent"):
		return "subagents"
	case strings.HasPrefix(k, "plugin"):
		return "plugins"
	case strings.Contains(k, "mcp"):
		return "mcp servers"
	}
	return k
}

// parseShares splits "dx 13%, claude-in-chrome 5%, sara 1%" into Shares.
func parseShares(s string) []Share {
	var out []Share
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		m := shareRe.FindStringSubmatch(part)
		if m == nil {
			continue
		}
		pct, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			continue
		}
		out = append(out, Share{Name: strings.TrimSpace(m[1]), Percent: pct})
	}
	return out
}

// parseReset resolves a printed reset ("Aug 23 at 8pm (America/Denver)") to an
// absolute instant.
//
// The printed form carries no year, and the session line often carries no date
// at all, so both are inferred from now — in the PRINTED timezone, not ours,
// because "8pm (America/Denver)" read on a UTC box is four hours off otherwise.
// Inference rules:
//   - with a date: assume the current year, and roll forward a year if that puts
//     the reset more than a day in the past (a Dec 31 reset read on Jan 1).
//   - without a date: assume today, and roll forward a day if that is already
//     past (an 8pm reset read at 9pm means 8pm tomorrow).
//
// Returns ok=false rather than a guess when the string does not match; callers
// then treat the window as having no known reset instead of a wrong one.
func parseReset(s string, now time.Time) (time.Time, bool) {
	m := resetRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return time.Time{}, false
	}
	monStr, dayStr, hourStr, minStr, ampm, tzStr := m[1], m[2], m[3], m[4], strings.ToLower(m[5]), m[6]

	loc := now.Location()
	if tzStr != "" {
		if l, err := time.LoadLocation(strings.TrimSpace(tzStr)); err == nil {
			loc = l
		}
	}
	ref := now.In(loc)

	hour, err := strconv.Atoi(hourStr)
	if err != nil || hour < 0 || hour > 23 {
		return time.Time{}, false
	}
	minute := 0
	if minStr != "" {
		if minute, err = strconv.Atoi(minStr); err != nil || minute < 0 || minute > 59 {
			return time.Time{}, false
		}
	}
	ampm = strings.ReplaceAll(ampm, ".", "")
	switch ampm {
	case "pm":
		if hour < 12 {
			hour += 12
		}
	case "am":
		if hour == 12 {
			hour = 0
		}
	case "":
		// 24-hour clock as printed; leave as-is.
	}

	if monStr == "" {
		// Time only: today in the printed zone, rolled to tomorrow if past.
		at := time.Date(ref.Year(), ref.Month(), ref.Day(), hour, minute, 0, 0, loc)
		if at.Before(ref) {
			at = at.AddDate(0, 0, 1)
		}
		return at, true
	}

	mon, ok := parseMonth(monStr)
	if !ok {
		return time.Time{}, false
	}
	day, err := strconv.Atoi(dayStr)
	if err != nil || day < 1 || day > 31 {
		return time.Time{}, false
	}
	at := time.Date(ref.Year(), mon, day, hour, minute, 0, 0, loc)
	if at.Before(ref.AddDate(0, 0, -1)) {
		at = at.AddDate(1, 0, 0)
	}
	return at, true
}

var months = map[string]time.Month{
	"jan": time.January, "feb": time.February, "mar": time.March,
	"apr": time.April, "may": time.May, "jun": time.June,
	"jul": time.July, "aug": time.August, "sep": time.September,
	"oct": time.October, "nov": time.November, "dec": time.December,
}

func parseMonth(s string) (time.Month, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) < 3 {
		return 0, false
	}
	m, ok := months[s[:3]]
	return m, ok
}

// atoiLoose parses an integer that may carry thousands separators.
func atoiLoose(s string) int {
	n, err := strconv.Atoi(strings.ReplaceAll(strings.TrimSpace(s), ",", ""))
	if err != nil {
		return 0
	}
	return n
}
