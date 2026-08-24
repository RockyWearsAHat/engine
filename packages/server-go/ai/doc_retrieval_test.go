package ai

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeDocSet lays down a realistic `.engine` documentation set: a term table,
// a module-aware PRD and a module index, each covering several subjects of
// which any one plan step touches one.
func writeDocSet(t *testing.T, subjects []string, parasPerSubject int) string {
	t.Helper()
	dir := t.TempDir()

	var vocab, prd, modules strings.Builder
	vocab.WriteString("# Ubiquitous language\n\nTerms this project uses and what they mean.\n")
	prd.WriteString("# Product requirements\n\nWhat each module owes the rest of the system.\n")
	modules.WriteString("# Module index\n\npath — purpose — public interface\n")

	// Each subject gets its own verb. Real documentation sections are about
	// different things; a fixture where every section repeats the same words
	// would measure tie-breaking rather than retrieval.
	verbs := []string{"flush", "evict", "retry", "reindex", "compact", "snapshot"}
	for i, s := range subjects {
		v := verbs[i%len(verbs)]
		fmt.Fprintf(&vocab, "\n## %s\n\nA %s is the unit the %s subsystem operates on, and to %s one is to %s it exactly once. ", s, s, s, v, v)
		for j := 0; j < parasPerSubject; j++ {
			fmt.Fprintf(&vocab, "The %s vocabulary distinguishes a live %s from a %s-ed one, and the %s-ed form is never reused. ", s, s, v, v)
		}
		fmt.Fprintf(&prd, "\n## %s module\n\nThe %s module exposes Open, %s and Report. ", s, s, upperFirst(v))
		for j := 0; j < parasPerSubject; j++ {
			fmt.Fprintf(&prd, "Callers of %s must not assume ordering across restarts, and the %s %s contract is stated in the vocabulary above. ", s, s, v)
		}
		fmt.Fprintf(&modules, "\n## internal/%s\n\ninternal/%s — owns the %s lifecycle — Open/%s/Report — critical. ", s, s, s, upperFirst(v))
		for j := 0; j < parasPerSubject; j++ {
			fmt.Fprintf(&modules, "Recent changes to %s touched its %s path only. ", s, v)
		}
	}

	engine := filepath.Join(dir, ".engine")
	if err := os.MkdirAll(engine, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for layer, body := range map[DocLayer]string{
		DocVocabulary: vocab.String(),
		DocPRD:        prd.String(),
		DocModules:    modules.String(),
	} {
		if err := os.WriteFile(filepath.Join(engine, string(layer)), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", layer, err)
		}
	}
	return dir
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// A small project must see byte-identical context. Retrieval is an answer to a
// prompt that does not fit; a prompt that fits has no problem to solve, and
// changing it anyway would be a regression dressed as an optimisation.
func TestComposeDocContextFocused_UnchangedWhenItFits(t *testing.T) {
	dir := writeDocSet(t, []string{"ledger"}, 1)
	t.Setenv(envDxContext, "0") // no dx store here; make that explicit
	t.Setenv(envDocContextTokens, "2000")

	whole := ComposeDocContext(dir, DocVocabulary, DocPRD, DocModules)
	got := ComposeDocContextFocused(dir, "Add a flush test to the ledger module", DocVocabulary, DocPRD, DocModules)
	if got != whole {
		t.Fatalf("small doc set was narrowed:\n--- whole ---\n%s\n--- got ---\n%s", whole, got)
	}
}

// The load-bearing case: a doc set the step mostly does not need.
func TestComposeDocContextFocused_KeepsTheRelevantSubject(t *testing.T) {
	subjects := []string{"ledger", "scheduler", "transport", "registry", "compactor", "snapshotter"}
	dir := writeDocSet(t, subjects, 6)
	t.Setenv(envDxContext, "0")
	t.Setenv(envDocContextTokens, "600")

	whole := ComposeDocContext(dir, DocVocabulary, DocPRD, DocModules)
	got := ComposeDocContextFocused(dir,
		"Make the compactor compact on close\nThe compactor must compact before Close returns.\nAcceptance: go test ./internal/compactor/... passes",
		DocVocabulary, DocPRD, DocModules)

	if TokenEstimate(got) >= TokenEstimate(whole) {
		t.Fatalf("no reduction: %d → %d tokens", TokenEstimate(whole), TokenEstimate(got))
	}
	if !strings.Contains(got, "compactor") {
		t.Fatalf("the step's own subject was dropped:\n%s", got)
	}
	// Something must have been left out, and the prefix has to say so — an
	// excerpt that reads as a whole document is how a model concludes a
	// requirement does not exist.
	if !strings.Contains(got, "omitted") {
		t.Fatalf("narrowed context does not disclose what it omitted:\n%s", got)
	}
	for _, unrelated := range []string{"scheduler", "transport", "registry"} {
		if strings.Count(got, unrelated) > 1 {
			t.Errorf("unrelated subject %q survived the narrowing", unrelated)
		}
	}
	t.Logf("doc prefix %d → %d tokens (%.1fx) for a one-subject step over a six-subject doc set",
		TokenEstimate(whole), TokenEstimate(got), float64(TokenEstimate(whole))/float64(TokenEstimate(got)))
}

// The prefix has to be byte-stable for the same step. Prompt caching bills a
// cache read at a tenth of an input token, and a prefix that reshuffles
// between attempts of the same step throws that away — which would give back
// in cache misses more than the narrowing saves.
func TestComposeDocContextFocused_IsDeterministic(t *testing.T) {
	dir := writeDocSet(t, []string{"ledger", "scheduler", "transport", "registry"}, 5)
	t.Setenv(envDxContext, "0")
	t.Setenv(envDocContextTokens, "500")

	query := "Make the registry reindex on close"
	first := ComposeDocContextFocused(dir, query, DocVocabulary, DocPRD, DocModules)
	for i := 0; i < 5; i++ {
		if got := ComposeDocContextFocused(dir, query, DocVocabulary, DocPRD, DocModules); got != first {
			t.Fatalf("run %d differed from run 0", i+1)
		}
	}
}

func TestComposeDocContextFocused_OffRestoresWholeDocuments(t *testing.T) {
	dir := writeDocSet(t, []string{"ledger", "scheduler", "transport", "registry"}, 6)
	t.Setenv(envDocRetrieval, "0")
	t.Setenv(envDocContextTokens, "300")

	whole := ComposeDocContext(dir, DocVocabulary, DocPRD, DocModules)
	if got := ComposeDocContextFocused(dir, "anything at all", DocVocabulary, DocPRD, DocModules); got != whole {
		t.Fatal("ENGINE_DOC_RETRIEVAL=0 did not restore the whole-document prefix")
	}
}

// An empty query cannot rank anything, so it must not pretend to.
func TestComposeDocContextFocused_EmptyQueryFallsBackToWhole(t *testing.T) {
	dir := writeDocSet(t, []string{"ledger", "scheduler", "transport"}, 6)
	t.Setenv(envDocContextTokens, "300")
	whole := ComposeDocContext(dir, DocVocabulary, DocPRD, DocModules)
	if got := ComposeDocContextFocused(dir, "   ", DocVocabulary, DocPRD, DocModules); got != whole {
		t.Fatal("an empty query narrowed the context instead of falling back")
	}
}

// The quota tier has to move the documentation budget, or documentation is the
// one prompt input that ignores the window.
func TestDocContextTokenBudget_TracksTheContextCeiling(t *testing.T) {
	dir := t.TempDir()

	t.Setenv("ENGINE_CONTEXT_MAX_TOKENS", "150000")
	roomyCeiling, _, tier := governedBudgetFor(dir)
	roomy := docContextTokenBudget(dir)
	t.Setenv("ENGINE_CONTEXT_MAX_TOKENS", "20000")
	tightCeiling, _, _ := governedBudgetFor(dir)
	tight := docContextTokenBudget(dir)

	if roomyCeiling == tightCeiling {
		t.Skipf("the %s tier clamps both settings to %d tokens; nothing to compare", tier, roomyCeiling)
	}
	if !(tight < roomy) {
		t.Fatalf("documentation budget did not narrow with the context ceiling: %d then %d", roomy, tight)
	}
	if tight < minDocContextTokens {
		t.Fatalf("documentation budget %d fell below its floor %d", tight, minDocContextTokens)
	}
	t.Logf("context ceiling 150000 → doc budget %d tokens; ceiling 20000 → %d tokens", roomy, tight)
}

func TestSplitMarkdownSections_SplitsHeadingsAndOversizedBodies(t *testing.T) {
	doc := "preamble line\n\n## Alpha\n\nalpha body\n\n## Beta\n\nbeta body\n"
	secs := splitMarkdownSections(doc)
	if len(secs) != 3 {
		t.Fatalf("expected preamble + two headed sections, got %d", len(secs))
	}
	if secs[0].heading != "" || secs[1].heading != "Alpha" || secs[2].heading != "Beta" {
		t.Fatalf("headings wrong: %q %q %q", secs[0].heading, secs[1].heading, secs[2].heading)
	}

	// plan.md is one long checkbox list with no headings at all. Without
	// sub-splitting it is a single indivisible section that either takes the
	// whole budget or is dropped entirely.
	long := "## Plan\n\n" + strings.Repeat("- [ ] do a thing that is described at some length\n\n", 200)
	got := splitMarkdownSections(long)
	if len(got) < 3 {
		t.Fatalf("a %d-char headingless body produced %d section(s); it must be sub-split", len(long), len(got))
	}
	for i, sec := range got {
		if len(sec.body) > maxSectionChars*2 {
			t.Fatalf("section %d is %d chars, past twice the %d cap", i, len(sec.body), maxSectionChars)
		}
	}
}

func TestQueryTerms_DropsBoilerplateAndSplitsIdentifiers(t *testing.T) {
	terms := queryTerms("Implement the flush_path test so that go test passes")
	for _, dead := range []string{"implement", "test", "the", "passes"} {
		if terms[dead] {
			t.Errorf("%q should not discriminate between sections", dead)
		}
	}
	if !terms["flush_path"] || !terms["flush"] || !terms["path"] {
		t.Fatalf("flush_path did not yield its parts: %v", terms)
	}
}

func TestScoreSection_PrefersAHeadingMatchOverAProseMention(t *testing.T) {
	terms := queryTerms("compactor flush")
	headed := docSection{heading: "compactor", body: "## compactor\n\nsome text about the subject at hand"}
	mention := docSection{heading: "scheduler", body: "## scheduler\n\nthe scheduler occasionally calls compactor and flush"}
	if scoreSection(headed, terms) <= scoreSection(mention, terms) {
		t.Fatalf("a passing mention outscored the section that is about the subject: %.3f vs %.3f",
			scoreSection(headed, terms), scoreSection(mention, terms))
	}
}

// A project with no dx store must not shell out, and dx_search must say so
// rather than returning an empty answer that reads like "nothing is known".
func TestDxSearchTool_SaysSoWhenThereIsNoStore(t *testing.T) {
	dir := t.TempDir()
	if projectHasDxDocuments(dir) {
		t.Fatal("an empty directory was reported as having dx documents")
	}
	out, isErr := executeDxSearchTool(dir, "how does the ledger work", 4)
	if !isErr {
		t.Fatal("dx_search reported success against a project with no dx documents")
	}
	if !strings.Contains(out, "search_files") {
		t.Fatalf("the refusal does not name what to use instead: %q", out)
	}
}

// TestDocContextMeasurement reports the before/after on a real project's
// documentation when one is named, and on a representative set otherwise.
// It is a measurement, so it prints numbers rather than only asserting.
func TestDocContextMeasurement(t *testing.T) {
	t.Setenv(envDxContext, "0") // measure the retrieval, not the dx store

	project := os.Getenv("ENGINE_DOC_MEASURE_PROJECT")
	label := "representative doc set"
	if project == "" {
		project = writeDocSet(t, []string{"ledger", "scheduler", "transport", "registry", "compactor", "snapshotter"}, 6)
	} else {
		label = project
	}

	layers := []DocLayer{DocVocabulary, DocPRD, DocModules}
	whole := ComposeDocContext(project, layers...)
	if strings.TrimSpace(whole) == "" {
		t.Skipf("%s has no .engine documentation to measure", label)
	}

	// One query per plan step, so the number is an average over a run rather
	// than the best case of one lucky step. A real project is measured against
	// its own plan — the steps the engine actually ran.
	var queries []string
	if planText := ReadDoc(project, DocPlan); strings.TrimSpace(planText) != "" {
		steps := parsePlanFromText(planText)
		for i := range steps {
			if q := stepQuery(&steps[i]); q != "" {
				queries = append(queries, q)
			}
		}
	}
	if len(queries) == 0 {
		queries = []string{
			"Make the compactor compact on close\nThe compactor must compact before Close returns.\nAcceptance: tests pass",
			"Give the registry a reindex policy\nReindex the oldest entry when full.\nAcceptance: tests pass",
			"Add a transport retry\nRetry a failed send three times.\nAcceptance: tests pass",
		}
	}

	before := TokenEstimate(whole) * len(queries)
	after := 0
	for _, q := range queries {
		after += TokenEstimate(ComposeDocContextFocused(project, q, layers...))
	}

	t.Logf("%s: doc prefix over %d step(s) — %d → %d tokens (%.2fx, %d saved)",
		label, len(queries), before, after,
		float64(before)/float64(max(after, 1)), before-after)

	if after > before {
		t.Fatalf("retrieval made the prefix larger: %d → %d", before, after)
	}
}
