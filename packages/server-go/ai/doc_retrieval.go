package ai

import (
	stdctx "context"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Documentation retrieval: send the part of a document that answers the
// question, not the document.
//
// ComposeDocContext reads every requested `.engine/*.md` layer whole and
// prepends the lot to a prompt. Its own comment claims each role "sees exactly
// the documentation slices it needs — no more, no less", which was never true:
// there are no slices, only files. On a project the engine has actually run,
// engine-LifeLab, that prefix is design.md + vocabulary.md + prd.md + plan.md +
// modules.md — about 24 KB, roughly 6,000 tokens — and it is paid again on
// every build step and every review of every step, in full, whether or not the
// step has anything to do with what those documents say.
//
// That is the wrong shape twice over. It is expensive, and it is *diluting*: a
// step about one module arrives buried in a plan for eleven others.
//
// What replaces it is retrieval. The step itself — title, body, acceptance —
// is the query. Sections that answer it are kept, sections that do not are
// dropped, and what was dropped is stated rather than hidden, so the model
// knows there is more and knows where it lives. When the whole set already
// fits the budget nothing changes at all: a small project pays no retrieval
// tax and sees byte-identical context.
//
// The budget is the quota governor's, scaled down from the context ceiling it
// already sets. A conserve tier therefore narrows documentation along with
// everything else instead of being the one input that ignores the window.
//
// And when the project keeps dx documents, those are searched too — for the
// first time. The Go engine has never read a `.dx` file: `dx` appears in this
// codebase only inside the quality linter's list of documentation extensions.
// A project's real design notes live there, and `dx search` returns the
// answering block rather than the document, which is exactly the shape this
// prefix wants.

const (
	// docContextShareNumer/Denom is documentation's share of the run's context
	// ceiling: 2%. On the default 100k budget that is 2,000 tokens, and on the
	// critical tier's 60k it is 1,200 — the tier moves it without a second
	// knob to keep in sync.
	docContextShareNumer = 2
	docContextShareDenom = 100

	// Floors and ceilings on that share. The floor exists because a
	// documentation prefix below a few hundred tokens is not a prefix, it is
	// a fragment; the ceiling because past a few thousand tokens the prefix
	// stops being orientation and starts being the whole document again.
	minDocContextTokens = 500
	maxDocContextTokens = 6000

	// dxContextShareNumer/Denom is how much of the documentation budget the
	// project's own dx documents may take. They are searched, so their hits
	// are relevance-ranked evidence rather than a dump — but the `.engine`
	// layers are the contract the orchestrator itself wrote, so they keep the
	// majority.
	dxContextShareNumer = 2
	dxContextShareDenom = 5

	// maxSectionChars bounds one retrieved unit. A heading-delimited section
	// longer than this is split further at blank lines: plan.md is one long
	// checkbox list with no headings at all, and without sub-splitting it
	// would be a single indivisible 10 KB section that either takes the whole
	// budget or is dropped entirely.
	maxSectionChars = 1600

	// maxLayerShareNumer/Denom caps any single layer at 60% of the layer
	// budget, so one verbose document cannot crowd out the others.
	maxLayerShareNumer = 3
	maxLayerShareDenom = 5

	// dxTimeout bounds a shell-out to the dx CLI. This is on the path of
	// every build step; a hung index must not hang a run.
	dxTimeout = 6 * time.Second
)

// envDocRetrieval turns retrieval off and restores the whole-file behaviour.
// It exists because this changes what every builder and reviewer sees, and a
// way back to the previous prompt is what makes that measurable rather than a
// leap of faith.
const envDocRetrieval = "ENGINE_DOC_RETRIEVAL"

// envDxContext turns the dx half off independently of the rest.
const envDxContext = "ENGINE_DX_CONTEXT"

// envDocContextTokens overrides the computed documentation budget outright.
const envDocContextTokens = "ENGINE_DOC_CONTEXT_TOKENS"

func docRetrievalEnabled() bool { return !envFlagOff(envDocRetrieval) }
func dxContextEnabled() bool    { return !envFlagOff(envDxContext) }

// envFlagOff reports whether an environment variable is explicitly switched
// off. Anything else — unset, empty, "1", nonsense — means on, because these
// are optimisations whose default has to be the one that costs less.
func envFlagOff(key string) bool {
	switch strings.TrimSpace(strings.ToLower(os.Getenv(key))) {
	case "0", "false", "no", "off":
		return true
	}
	return false
}

// docContextTokenBudget is how many tokens of documentation one prompt may
// carry, derived from the governed context budget so the quota tier moves it.
func docContextTokenBudget(projectPath string) int {
	if override := parseIntEnv(envDocContextTokens, 0); override > 0 {
		return override
	}
	budget, _, _ := governedBudgetFor(projectPath)
	share := budget * docContextShareNumer / docContextShareDenom
	if share < minDocContextTokens {
		share = minDocContextTokens
	}
	if share > maxDocContextTokens {
		share = maxDocContextTokens
	}
	return share
}

// ComposeDocContextFocused assembles the same prompt prefix ComposeDocContext
// would, narrowed to what answers `query` when the whole set will not fit the
// documentation budget, and extended with the project's own dx documents when
// it keeps any.
//
// An empty query degrades to ComposeDocContext: with nothing to rank against,
// scoring sections would be arbitrary, and an arbitrary selection is worse
// than an honest whole.
func ComposeDocContextFocused(projectPath, query string, layers ...DocLayer) string {
	whole := ComposeDocContext(projectPath, layers...)
	if !docRetrievalEnabled() || strings.TrimSpace(query) == "" {
		return whole
	}

	budget := docContextTokenBudget(projectPath)

	dxPart := ""
	if dxContextEnabled() {
		dxPart = dxAnswerBlocks(projectPath, query, budget*dxContextShareNumer/dxContextShareDenom)
	}

	layerBudget := budget - TokenEstimate(dxPart)
	if layerBudget < minDocContextTokens/2 {
		layerBudget = minDocContextTokens / 2
	}

	layerPart := whole
	if TokenEstimate(whole) > layerBudget {
		layerPart = focusDocLayers(projectPath, query, layerBudget, layers)
		log.Printf("doc context: %d → %d tokens for %q (budget %d)",
			TokenEstimate(whole), TokenEstimate(layerPart), summarise(query, 60), layerBudget)
	}

	switch {
	case layerPart == "":
		return dxPart
	case dxPart == "":
		return layerPart
	default:
		return layerPart + "\n\n" + dxPart
	}
}

// stepQuery is the retrieval query for one plan step.
//
// Title, body and acceptance criteria together, because each says something
// the others do not: the title names the subject, the body names the mechanism,
// and the acceptance names the surface it has to satisfy — often the only place
// a concrete symbol or command appears.
func stepQuery(step *PlanStep) string {
	if step == nil {
		return ""
	}
	return strings.TrimSpace(step.Title + "\n" + step.Body + "\n" + step.Acceptance)
}

// docSection is one retrievable unit of a documentation layer.
type docSection struct {
	layer   DocLayer
	heading string
	body    string // the section verbatim, heading line included
	order   int    // position within its layer, for reassembly in reading order
	score   float64
}

// focusDocLayers picks the sections of each layer that answer the query and
// reassembles them in reading order, stating what it left out.
func focusDocLayers(projectPath, query string, budgetTokens int, layers []DocLayer) string {
	terms := queryTerms(query)

	var all []docSection
	counts := map[DocLayer]int{}
	for _, layer := range layers {
		body := strings.TrimSpace(ReadDoc(projectPath, layer))
		if body == "" {
			continue
		}
		secs := splitMarkdownSections(body)
		counts[layer] = len(secs)
		for i := range secs {
			secs[i].layer = layer
			secs[i].order = i
			secs[i].score = scoreSection(secs[i], terms)
		}
		all = append(all, secs...)
	}
	if len(all) == 0 {
		return ""
	}

	// Highest scoring first; ties break on reading order so the choice is
	// deterministic and a re-run of the same step composes the same prefix.
	// That matters beyond tidiness: an unstable prefix defeats prompt caching,
	// and cache reads bill at a tenth of an input token.
	ranked := make([]docSection, len(all))
	copy(ranked, all)
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		if ranked[i].layer != ranked[j].layer {
			return ranked[i].layer < ranked[j].layer
		}
		return ranked[i].order < ranked[j].order
	})

	perLayerCap := budgetTokens * maxLayerShareNumer / maxLayerShareDenom
	spent := 0
	spentByLayer := map[DocLayer]int{}
	keep := map[DocLayer]map[int]bool{}
	for _, sec := range ranked {
		if sec.score <= 0 {
			continue
		}
		cost := TokenEstimate(sec.body)
		if spent+cost > budgetTokens {
			continue
		}
		if spentByLayer[sec.layer]+cost > perLayerCap {
			continue
		}
		if keep[sec.layer] == nil {
			keep[sec.layer] = map[int]bool{}
		}
		keep[sec.layer][sec.order] = true
		spent += cost
		spentByLayer[sec.layer] += cost
	}

	// A layer that matched nothing still gets its opening section, if that
	// section is short. Orientation is cheap and a layer that vanishes without
	// trace reads as a layer that does not exist.
	for _, layer := range layers {
		if counts[layer] == 0 || len(keep[layer]) > 0 {
			continue
		}
		for _, sec := range all {
			if sec.layer != layer || sec.order != 0 {
				continue
			}
			cost := TokenEstimate(sec.body)
			if cost <= 200 && spent+cost <= budgetTokens {
				if keep[layer] == nil {
					keep[layer] = map[int]bool{}
				}
				keep[layer][0] = true
				spent += cost
			}
			break
		}
	}

	var b strings.Builder
	for _, layer := range layers {
		kept := keep[layer]
		if len(kept) == 0 {
			continue
		}
		fmt.Fprintf(&b, "=== %s ===\n", docDisplayName(layer))
		for _, sec := range all {
			if sec.layer != layer || !kept[sec.order] {
				continue
			}
			b.WriteString(strings.TrimSpace(sec.body))
			b.WriteString("\n\n")
		}
		if omitted := counts[layer] - len(kept); omitted > 0 {
			// Say what was cut and where the rest is. A model that knows a
			// document was narrowed can go and read it; a model handed a
			// silent excerpt will assume it saw everything.
			fmt.Fprintf(&b, "[%d of %d sections of %s omitted as unrelated to this step — read %s for the rest]\n\n",
				omitted, counts[layer], string(layer), filepath.Join(docDir, string(layer)))
		}
	}
	return strings.TrimSpace(b.String())
}

// splitMarkdownSections cuts a document at markdown headings, then splits any
// section still longer than maxSectionChars at blank lines.
func splitMarkdownSections(text string) []docSection {
	lines := strings.Split(text, "\n")
	var out []docSection
	var cur []string
	curHeading := ""

	flush := func() {
		body := strings.TrimSpace(strings.Join(cur, "\n"))
		cur = nil
		if body == "" {
			return
		}
		for _, chunk := range splitOversized(body, curHeading) {
			out = append(out, docSection{heading: curHeading, body: chunk})
		}
	}

	for _, line := range lines {
		if isMarkdownHeading(line) {
			flush()
			curHeading = strings.TrimSpace(strings.TrimLeft(line, "#"))
		}
		cur = append(cur, line)
	}
	flush()
	return out
}

func isMarkdownHeading(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, "#") {
		return false
	}
	hashes := len(trimmed) - len(strings.TrimLeft(trimmed, "#"))
	return hashes >= 1 && hashes <= 6 && len(trimmed) > hashes && trimmed[hashes] == ' '
}

// splitOversized breaks a too-long section at blank lines, repeating the
// heading on each piece so a fragment still says what it belongs to.
func splitOversized(body, heading string) []string {
	if len(body) <= maxSectionChars {
		return []string{body}
	}
	paras := strings.Split(body, "\n\n")
	var out []string
	var cur strings.Builder
	for _, p := range paras {
		if cur.Len() > 0 && cur.Len()+len(p) > maxSectionChars {
			out = append(out, cur.String())
			cur.Reset()
			if heading != "" {
				cur.WriteString("(" + heading + ", continued)\n")
			}
		}
		if cur.Len() > 0 {
			cur.WriteString("\n\n")
		}
		cur.WriteString(p)
	}
	if strings.TrimSpace(cur.String()) != "" {
		out = append(out, cur.String())
	}
	return out
}

// docStopWords are terms too common in a plan step to discriminate between
// sections. "Implement", "test" and "add" appear in every step of every plan.
var docStopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true,
	"this": true, "from": true, "into": true, "when": true, "then": true,
	"add": true, "use": true, "using": true, "make": true, "should": true,
	"must": true, "will": true, "implement": true, "implementation": true,
	"test": true, "tests": true, "testing": true, "step": true, "code": true,
	"file": true, "files": true, "acceptance": true, "passes": true,
	"ensure": true, "write": true, "create": true, "update": true,
	"support": true, "handle": true, "return": true, "value": true,
	"pass": true, "run": true, "all": true, "new": true, "each": true,
	"its": true, "not": true, "any": true, "one": true, "also": true,
}

// queryTerms reduces a plan step to the distinct words worth matching on.
func queryTerms(query string) map[string]bool {
	terms := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !(r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	}) {
		if len(w) < 3 || docStopWords[w] {
			continue
		}
		terms[w] = true
		// A compound name is also its parts: a step about `parsePlanFromText`
		// should reach a section that only ever says "plan".
		for _, part := range splitIdentifierWords(w) {
			if len(part) >= 3 && !docStopWords[part] {
				terms[part] = true
			}
		}
	}
	return terms
}

// splitIdentifierWords breaks snake_case and a lowercased run of digits off a
// word. Case is already gone by the time this is called, so camelCase cannot
// be split here — that is what the query's own spelling is for.
func splitIdentifierWords(w string) []string {
	if !strings.ContainsAny(w, "_0123456789") {
		return nil
	}
	return strings.FieldsFunc(w, func(r rune) bool {
		return r == '_' || (r >= '0' && r <= '9')
	})
}

// scoreSection ranks a section against the query's terms.
//
// A term in the heading counts for three: a heading is the section's own claim
// about what it covers, and a match there is far better evidence than a match
// in prose that may only be an aside. Long sections are damped, because a long
// section will contain more terms by accident alone and the budget it consumes
// is real.
func scoreSection(sec docSection, terms map[string]bool) float64 {
	if len(terms) == 0 {
		return 0
	}
	body := strings.ToLower(sec.body)
	heading := strings.ToLower(sec.heading)
	raw := 0.0
	for term := range terms {
		if strings.Contains(heading, term) {
			raw += 3
			continue
		}
		if strings.Contains(body, term) {
			raw++
		}
	}
	if raw == 0 {
		return 0
	}
	return raw / math.Sqrt(float64(len(sec.body))/800.0+1.0)
}

// ── dx ────────────────────────────────────────────────────────────────────

var (
	dxOnce sync.Once
	dxPath string
)

// dxBinary resolves the dx CLI once. Absent is the normal case on a machine
// that does not keep dx documents, and it is not an error.
func dxBinary() string {
	dxOnce.Do(func() {
		if p, err := exec.LookPath("dx"); err == nil {
			dxPath = p
		}
	})
	return dxPath
}

// projectHasDxDocuments reports whether the project keeps a dx document store.
// Both halves are required: a `.dx` file is the document, `.doc/` is the index
// that makes it searchable.
func projectHasDxDocuments(projectPath string) bool {
	if projectPath == "" {
		return false
	}
	if st, err := os.Stat(filepath.Join(projectPath, ".doc")); err != nil || !st.IsDir() {
		return false
	}
	matches, err := filepath.Glob(filepath.Join(projectPath, "*.dx"))
	if err != nil || len(matches) == 0 {
		nested, nerr := filepath.Glob(filepath.Join(projectPath, "*", "*.dx"))
		return nerr == nil && len(nested) > 0
	}
	return true
}

// dxHit is one answering block from `dx search`.
type dxHit struct {
	path  string
	block string
}

// dxAnswerBlocks searches the project's dx documents for the query and returns
// the answering blocks, whole, up to the budget.
//
// This is the shape the retrieval is for: `dx search` returns the id of the
// block that answers, and `dx text --section` returns that block and nothing
// else. Reading the document to find the same paragraph costs the document.
func dxAnswerBlocks(projectPath, query string, budgetTokens int) string {
	if budgetTokens <= 0 || strings.TrimSpace(query) == "" {
		return ""
	}
	if dxBinary() == "" || !projectHasDxDocuments(projectPath) {
		return ""
	}

	hits := dxSearch(projectPath, query, 5)
	if len(hits) == 0 {
		return ""
	}

	var b strings.Builder
	spent := 0
	for _, hit := range hits {
		text := dxSection(projectPath, hit.path, hit.block)
		if text == "" {
			continue
		}
		cost := TokenEstimate(text)
		if spent+cost > budgetTokens {
			continue
		}
		if b.Len() == 0 {
			b.WriteString("=== PROJECT DOCUMENTS (dx, searched for this step) ===\n")
		}
		fmt.Fprintf(&b, "[%s#%s]\n%s\n\n", hit.path, hit.block, text)
		spent += cost
	}
	return strings.TrimSpace(b.String())
}

// executeDxSearchTool backs the dx_search tool.
//
// It returns whole answering blocks rather than the previews `dx search`
// prints, because a preview ending in an ellipsis guarantees a second call —
// and two calls for one answer is the cost this tool exists to avoid. The
// budget is the same documentation budget the automatic prefix uses, so an
// agent that searches repeatedly cannot spend more on documents than the
// governor allows a prompt to carry.
func executeDxSearchTool(projectPath, query string, limit int) (string, bool) {
	if strings.TrimSpace(query) == "" {
		return "dx_search: query is required", true
	}
	if dxBinary() == "" {
		return "dx_search: the dx CLI is not installed on this machine — use search_files instead", true
	}
	if !projectHasDxDocuments(projectPath) {
		return "dx_search: this project keeps no dx documents — use search_files instead", true
	}
	out := dxAnswerBlocks(projectPath, query, docContextTokenBudget(projectPath))
	if out == "" {
		return fmt.Sprintf("dx_search: no dx document answers %q. Try search_files for source, or different words.", summarise(query, 80)), false
	}
	return out, false
}

// dxSearch runs `dx search` and returns the document hits, best first.
//
// Source-file hits are dropped. They are real hits — dx searches source
// alongside documents — but their block id is a line range, and pasting a line
// range of Go into a prompt is the whole-file habit this is replacing. The
// model has read_file and search_files for source.
func dxSearch(projectPath, query string, limit int) []dxHit {
	out, ok := runDx(projectPath, "search", summarise(query, 200), projectPath, "--limit", fmt.Sprint(limit))
	if !ok {
		return nil
	}

	var hits []dxHit
	var cur string
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			// `<score>  <path>  <title>` — a new hit.
			fields := strings.Fields(line)
			cur = ""
			if len(fields) >= 2 && strings.HasSuffix(fields[1], ".dx") {
				cur = fields[1]
			}
			continue
		}
		trimmed := strings.TrimSpace(line)
		if cur == "" || !strings.HasPrefix(trimmed, "#") {
			continue
		}
		block := strings.Fields(strings.TrimPrefix(trimmed, "#"))
		if len(block) == 0 {
			continue
		}
		hits = append(hits, dxHit{path: cur, block: block[0]})
		cur = ""
	}
	return hits
}

// dxSection returns one block of one document.
func dxSection(projectPath, docPath, block string) string {
	// --refresh is deliberately not requested: refreshing re-runs approved
	// code blocks, and a build step assembling a prompt is not a reason to
	// execute anything in someone's document.
	out, ok := runDx(projectPath, "text", docPath, "--section", block)
	if !ok {
		return ""
	}
	return strings.TrimSpace(out)
}

// runDx executes the dx CLI in the project directory under a timeout.
// Failure is silent by design: the documentation prefix is an optimisation,
// and an optimisation that can fail a build step is not one.
func runDx(projectPath string, args ...string) (string, bool) {
	bin := dxBinary()
	if bin == "" {
		return "", false
	}
	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), dxTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = projectPath
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}
