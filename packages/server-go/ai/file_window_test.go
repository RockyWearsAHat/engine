package ai

import (
	"fmt"
	"strings"
	"testing"
)

func numberedLines(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d: some source text that is about sixty characters long\n", i)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// The common case is a small file, and it has to come back byte-identical.
// Every existing caller, test and prompt was written against the raw content;
// a windowing change that alters small reads would be a behaviour change
// disguised as a size fix.
func TestWindowFile_SmallFileIsUnchanged(t *testing.T) {
	content := numberedLines(20)
	if got := windowFile("/p/x.go", content, 0, 0, 100000); got != content {
		t.Fatalf("a 20-line file was altered:\n%s", got)
	}
}

func TestWindowFile_TruncatesAtTheBudgetAndSaysHowToContinue(t *testing.T) {
	content := numberedLines(4000)
	got := windowFile("/p/big.go", content, 0, 0, 8000)

	if len(got) > 8000+400 {
		t.Fatalf("window is %d chars, past the 8000 budget plus notice", len(got))
	}
	if !strings.Contains(got, "line 1:") {
		t.Fatal("the window did not start at the top of the file")
	}
	if !strings.Contains(got, "of 4000") {
		t.Fatalf("the notice does not say how much of the file exists:\n%s", tail(got))
	}
	// A truncation the model cannot act on turns a complete answer into a
	// silently incomplete one, so the result has to name the literal next call.
	if !strings.Contains(got, `read_file {"path":"/p/big.go","startLine":`) {
		t.Fatalf("the notice does not name the continuing call:\n%s", tail(got))
	}
	if !strings.Contains(got, "search_files") {
		t.Fatalf("the notice does not offer the cheaper alternative:\n%s", tail(got))
	}
	// Lines are never split: half a line of source is a syntax error the model
	// has to work out is not real.
	body := got[:strings.Index(got, "\n\n[read_file:")]
	for _, line := range strings.Split(body, "\n") {
		if line != "" && !strings.HasPrefix(line, "line ") {
			t.Fatalf("a line was split: %q", line)
		}
	}
}

// The continuation the notice advertises has to actually work, and the second
// window has to start exactly where the first stopped — no gap, no repeat.
func TestWindowFile_ContinuationIsSeamless(t *testing.T) {
	content := numberedLines(4000)
	first := windowFile("/p/big.go", content, 0, 0, 8000)

	var next int
	if _, err := fmt.Sscanf(first[strings.Index(first, `"startLine":`):], `"startLine":%d}`, &next); err != nil {
		t.Fatalf("could not read the advertised startLine out of %q: %v", tail(first), err)
	}

	firstBody := first[:strings.Index(first, "\n\n[read_file:")]
	lastOfFirst := lastLine(firstBody)
	second := windowFile("/p/big.go", content, next, 0, 8000)
	firstOfSecond := strings.SplitN(second, "\n", 2)[0]

	if !strings.HasPrefix(lastOfFirst, fmt.Sprintf("line %d:", next-1)) {
		t.Fatalf("first window ended at %q, not line %d", lastOfFirst, next-1)
	}
	if !strings.HasPrefix(firstOfSecond, fmt.Sprintf("line %d:", next)) {
		t.Fatalf("second window began at %q, not line %d", firstOfSecond, next)
	}
}

func TestWindowFile_MaxLinesIsHonoured(t *testing.T) {
	got := windowFile("/p/big.go", numberedLines(4000), 100, 5, 100000)
	body := got
	if i := strings.Index(got, "\n\n[read_file:"); i >= 0 {
		body = got[:i]
	}
	lines := strings.Split(body, "\n")
	if len(lines) != 5 {
		t.Fatalf("asked for 5 lines, got %d", len(lines))
	}
	if !strings.HasPrefix(lines[0], "line 100:") || !strings.HasPrefix(lines[4], "line 104:") {
		t.Fatalf("wrong slice: %q … %q", lines[0], lines[4])
	}
}

func TestWindowFile_PastTheEndSaysSo(t *testing.T) {
	got := windowFile("/p/x.go", numberedLines(10), 500, 0, 100000)
	if !strings.Contains(got, "past the end") || !strings.Contains(got, "10 line(s)") {
		t.Fatalf("unhelpful out-of-range result: %q", got)
	}
}

// A minified bundle is one enormous line. Returning nothing would hide the
// evidence; returning all of it is the unbounded behaviour this replaces.
func TestWindowFile_SingleOversizedLineIsTruncatedNotDropped(t *testing.T) {
	content := strings.Repeat("x", 50000) + "\nsecond line"
	got := windowFile("/p/bundle.js", content, 0, 0, 8000)
	if !strings.Contains(got, "line truncated") {
		t.Fatalf("an over-budget single line was not marked as truncated: %q", tail(got))
	}
	if len(got) > 8000+400 {
		t.Fatalf("truncated line still returned %d chars", len(got))
	}
}

func TestReadFileCharBudget_RespectsFloorCeilingAndOverride(t *testing.T) {
	dir := t.TempDir()

	t.Setenv(envReadFileMaxChars, "1234")
	if got := readFileCharBudget(dir); got != 1234 {
		t.Fatalf("explicit override ignored: %d", got)
	}

	t.Setenv(envReadFileMaxChars, "")
	got := readFileCharBudget(dir)
	if got < minReadFileChars || got > maxReadFileChars {
		t.Fatalf("budget %d outside [%d,%d]", got, minReadFileChars, maxReadFileChars)
	}
	t.Logf("read_file budget on this configuration: %d characters (~%d tokens)", got, got/4)
}

func lastLine(s string) string {
	parts := strings.Split(s, "\n")
	return parts[len(parts)-1]
}

func tail(s string) string {
	if len(s) < 300 {
		return s
	}
	return "…" + s[len(s)-300:]
}
