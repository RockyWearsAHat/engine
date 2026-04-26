// Package ai — pre-commit secret scanner.
// Scans staged diffs for common secret patterns before git_commit is allowed.
// Blocks on match; returns a detailed report of what was found and on which line.
package ai

import (
	"fmt"
	"regexp"
	"strings"
)

// secretPattern defines a detectable credential pattern.
type secretPattern struct {
	Name    string
	Pattern *regexp.Regexp
}

// secretPatterns is the list of patterns checked on every diff.
// Each pattern targets a specific credential type.
// Patterns match the VALUE of the secret, not just surrounding context.
var secretPatterns = []*secretPattern{
	{Name: "GitHub Personal Access Token (classic)", Pattern: regexp.MustCompile(`ghp_[A-Za-z0-9]{36}`)},
	{Name: "GitHub Fine-Grained Token", Pattern: regexp.MustCompile(`github_pat_[A-Za-z0-9_]{82}`)},
	{Name: "GitHub OAuth Token", Pattern: regexp.MustCompile(`gho_[A-Za-z0-9]{36}`)},
	{Name: "GitHub Actions Token", Pattern: regexp.MustCompile(`ghs_[A-Za-z0-9]{36}`)},
	{Name: "Anthropic API Key", Pattern: regexp.MustCompile(`sk-ant-[A-Za-z0-9\-_]{32,}`)},
	{Name: "OpenAI API Key", Pattern: regexp.MustCompile(`sk-[A-Za-z0-9]{48}`)},
	{Name: "AWS Access Key ID", Pattern: regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{Name: "AWS Secret Access Key", Pattern: regexp.MustCompile(`[A-Za-z0-9/+=]{40}`)},
	{Name: "Stripe Secret Key", Pattern: regexp.MustCompile(`sk_live_[A-Za-z0-9]{24,}`)},
	{Name: "Stripe Publishable Key", Pattern: regexp.MustCompile(`pk_live_[A-Za-z0-9]{24,}`)},
	{Name: "Slack Bot Token", Pattern: regexp.MustCompile(`xoxb-[A-Za-z0-9\-]{50,}`)},
	{Name: "Slack User Token", Pattern: regexp.MustCompile(`xoxp-[A-Za-z0-9\-]{50,}`)},
	{Name: "Google API Key", Pattern: regexp.MustCompile(`AIza[A-Za-z0-9\-_]{35}`)},
	{Name: "Private Key (PEM)", Pattern: regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH )?PRIVATE KEY-----`)},
	{Name: "Basic Auth in URL", Pattern: regexp.MustCompile(`https?://[^:@\s]+:[^@\s]+@`)},
	{Name: "Generic High-Entropy Secret", Pattern: regexp.MustCompile(`(?i)(?:secret|password|api.?key|token|credential)[=:]\s*["']?[A-Za-z0-9+/=_\-]{20,}["']?`)},
}

// ScanResult describes a single detected secret.
type ScanResult struct {
	PatternName string
	File        string
	LineNumber  int
	LineContent string // redacted
}

// ScanDiff scans a git diff string for secrets.
// Returns a slice of findings (empty if clean).
// The LineContent in each finding has the matched secret value redacted.
func ScanDiff(diff string) []ScanResult {
	var results []ScanResult
	currentFile := ""
	lineNum := 0

	for _, line := range strings.Split(diff, "\n") {
		// Track current file.
		if strings.HasPrefix(line, "+++ b/") {
			currentFile = strings.TrimPrefix(line, "+++ b/")
			lineNum = 0
			continue
		}
		if strings.HasPrefix(line, "@@") {
			// Extract the starting line number from @@ -a,b +c,d @@
			var newStart int
			fmt.Sscanf(line, "@@ -%*d,%*d +%d", &newStart)
			lineNum = newStart - 1
			continue
		}

		// Only scan added lines (prefixed with +, not +++).
		if !strings.HasPrefix(line, "+") || strings.HasPrefix(line, "+++") {
			if !strings.HasPrefix(line, "-") {
				lineNum++
			}
			continue
		}
		lineNum++
		content := line[1:] // strip the leading +

		for _, sp := range secretPatterns {
			if sp.Pattern.MatchString(content) {
				// Redact the matched value.
				redacted := sp.Pattern.ReplaceAllStringFunc(content, func(m string) string {
					return m[:4] + strings.Repeat("*", len(m)-8) + m[len(m)-4:]
				})
				results = append(results, ScanResult{
					PatternName: sp.Name,
					File:        currentFile,
					LineNumber:  lineNum,
					LineContent: redacted,
				})
			}
		}
	}

	return results
}

// FormatScanReport formats scan results into a user-facing error message.
func FormatScanReport(results []ScanResult) string {
	if len(results) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("🚨 SECRET SCAN BLOCKED: The diff contains potential secrets. Commit aborted.\n\n")
	for _, r := range results {
		sb.WriteString(fmt.Sprintf("  • %s\n    File: %s (line %d)\n    Content: %s\n\n",
			r.PatternName, r.File, r.LineNumber, r.LineContent))
	}
	sb.WriteString("Remove the secrets before committing. Use environment variables or a secrets manager.\n")
	sb.WriteString("If this is a false positive, ask for explicit approval to override the scan.")
	return sb.String()
}
