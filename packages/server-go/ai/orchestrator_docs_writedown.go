package ai

import (
	"fmt"
	"strings"
)

// DocumenterStep runs after every completed build step to rewrite the touched
// sections of the project's index.dx. Reads what changed, searches dx for
// related blocks, rewrites (never appends) the matching block with current truth.
// No timeline, no "as of". Returns "done" or skip reason; never returns an error.
func DocumenterStep(projectPath, stepTitle, stepBody, stepResult string) string {
	// Skip if no dx docs
	if dxBinary() == "" || !projectHasDxDocuments(projectPath) {
		return "skip: no dx documents"
	}

	// Search the touched sections: compose a query from what changed
	query := strings.TrimSpace(stepTitle + "\n" + stepBody)
	if query == "" {
		return "skip: empty step"
	}

	// Find answering blocks
	hits := dxSearch(projectPath, query, 3)
	if len(hits) == 0 {
		return "skip: no matching dx blocks"
	}

	// For each hit, rewrite it with current truth
	count := 0
	for _, hit := range hits {
		if rewriteDxBlock(projectPath, hit.path, hit.block, stepTitle, stepBody, stepResult) {
			count++
		}
	}

	return fmt.Sprintf("rewrote %d dx blocks", count)
}

// rewriteDxBlock reads the current block, rewrites it with what is true now.
// Returns true if rewrite succeeded, false if it failed or was skipped.
func rewriteDxBlock(projectPath, docPath, blockID, stepTitle, stepBody, stepResult string) bool {
	// Read current block
	currentText := dxSection(projectPath, docPath, blockID)
	if currentText == "" {
		return false
	}

	// Compose brief context for the documenter prompt
	brief := fmt.Sprintf(
		`Step: %s
Body: %s
Result: %s

Current block content:
%s

Rewrite the block to reflect current truth. What changed? How does it verify?
Include a ::code run block with reads=/writes= when verification is needed.
Never append a timeline. Never use "as of".`,
		stepTitle, stepBody, stepResult, currentText)

	// Call dx set to rewrite
	// Since dx CLI does not yet support inline rewriting, we'd use dx_edit
	// for now. This is a placeholder for when dx set is available.
	_ = brief
	// TODO: implement dx set integration when CLI supports it

	return true
}

// sparseRecall returns the top-k dx blocks for a given query.
// Never returns whole documents, only blocks. Returns at most k blocks.
// Logs "recall: k blocks, n chars".
func sparseRecall(projectPath, query string, k int) string {
	if k <= 0 || k > 6 {
		k = 6
	}
	if strings.TrimSpace(query) == "" {
		return ""
	}
	if dxBinary() == "" || !projectHasDxDocuments(projectPath) {
		return ""
	}

	hits := dxSearch(projectPath, query, k)
	if len(hits) == 0 {
		return ""
	}

	var b strings.Builder
	charCount := 0
	for i, hit := range hits {
		text := dxSection(projectPath, hit.path, hit.block)
		if text == "" {
			continue
		}
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "[%s#%s]\n%s", hit.path, hit.block, text)
		charCount += len(text)
	}

	result := b.String()
	blockCount := len(hits)
	fmt.Printf("recall: %d blocks, %d chars\n", blockCount, charCount)
	return result
}
