package ai

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Bounding read_file: a window, not a firehose.
//
// `read_file` returned `fc.Content` verbatim with no size limit of any kind —
// the only unbounded path from disk into a prompt in the whole tool set.
// `list_directory` has had a compaction budget for a long time, `search_files`
// truncates at 4 MiB, `shell` truncates at 4 MiB; `read_file` had nothing. A
// builder that opens a 3,700-line file to change one function pays for all
// 3,700 lines, and then pays for them again on every subsequent turn of the
// same step, because the tool result stays in the message array.
//
// That is also outside the governed context budget. `governedContextBudget` is
// applied once, before the agentic loop starts (`ai/context.go`), over the
// messages that exist at that moment. Every tool result appended during the
// loop lands after the trim and is never re-checked, so the one lever the
// quota governor has over prompt size does not reach the largest contributor
// to it.
//
// The fix is not to refuse the read. It is to give the tool a window and to
// tell the model, in the result itself, exactly how to ask for the next one.
// A truncation the model cannot act on is worse than no truncation: it turns a
// complete answer into a silently incomplete one. So the notice names the
// literal next call.

// envReadFileMaxChars overrides the per-read ceiling outright.
const envReadFileMaxChars = "ENGINE_READ_FILE_MAX_CHARS"

const (
	// readFileShareNumer/Denom: one file read may take at most a tenth of the
	// run's context budget. On the default 100k-token budget that is 40,000
	// characters — around 1,000 lines of Go, comfortably more than any file
	// worth reading whole — and on the critical tier's 60k it is 24,000.
	readFileShareNumer = 1
	readFileShareDenom = 10

	// A floor, so a misconfigured budget cannot reduce reads to a useless
	// sliver, and a ceiling, so an enormous configured budget does not restore
	// the unbounded behaviour by the back door.
	minReadFileChars = 8000
	maxReadFileChars = 120000
)

// readFileCharBudget is the largest amount of one file a single read may put
// into the prompt.
func readFileCharBudget(projectPath string) int {
	if override := parseIntEnv(envReadFileMaxChars, 0); override > 0 {
		return override
	}
	budget, _, _ := governedBudgetFor(projectPath)
	// Four characters to the token, the same heuristic TokenEstimate uses.
	chars := budget * 4 * readFileShareNumer / readFileShareDenom
	if chars < minReadFileChars {
		chars = minReadFileChars
	}
	if chars > maxReadFileChars {
		chars = maxReadFileChars
	}
	return chars
}

// windowFile returns the requested slice of content and, when anything was
// left out, a notice naming the exact call that continues from where it stops.
//
// startLine is 1-based and 0 means "from the beginning"; maxLines 0 means "as
// much as the budget allows".
func windowFile(path, content string, startLine, maxLines, charBudget int) string {
	lines := strings.Split(content, "\n")
	total := len(lines)

	from := startLine
	if from < 1 {
		from = 1
	}
	if from > total {
		return fmt.Sprintf("[read_file: %s has %d line(s); startLine %d is past the end]",
			filepath.Base(path), total, from)
	}

	to := total
	if maxLines > 0 && from+maxLines-1 < to {
		to = from + maxLines - 1
	}

	// A minified bundle is one enormous line. Taking it whole would be the
	// unbounded behaviour this replaces, and taking nothing would hide the
	// evidence, so the line itself is cut — the one place a split is right,
	// because there is no line boundary to stop at.
	if len(lines[from-1])+1 > charBudget {
		cut := charBudget
		if cut > len(lines[from-1]) {
			cut = len(lines[from-1])
		}
		// Back up to a rune boundary: a cut mid-rune turns the tail into U+FFFD
		// and, in a JSON tool result, can be a decode error rather than text.
		for cut > 0 && cut < len(lines[from-1]) && !utf8.RuneStart(lines[from-1][cut]) {
			cut--
		}
		lines[from-1] = lines[from-1][:cut] + "…[line truncated]"
		to = from
	}

	// Otherwise fill up to the character budget, never splitting a line: half a
	// line of source is a syntax error the model has to work out is not real.
	used := 0
	end := from - 1
	for i := from - 1; i < to; i++ {
		next := len(lines[i]) + 1
		if used > 0 && used+next > charBudget {
			break
		}
		used += next
		end = i + 1
	}

	body := strings.Join(lines[from-1:end], "\n")
	if from == 1 && end == total {
		return body
	}

	var notice strings.Builder
	fmt.Fprintf(&notice, "\n\n[read_file: showed lines %d-%d of %d in %s.",
		from, end, total, filepath.Base(path))
	if end < total {
		fmt.Fprintf(&notice, ` Continue with read_file {"path":"%s","startLine":%d}.`, path, end+1)
		notice.WriteString(" To jump straight to what you need instead, use search_files.")
	}
	notice.WriteString("]")
	return body + notice.String()
}
