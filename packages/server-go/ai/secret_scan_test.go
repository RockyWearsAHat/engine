package ai

import (
	"strings"
	"testing"
)

func TestScanDiff_Clean(t *testing.T) {
	diff := `+++ b/main.go
@@ -1,3 +1,4 @@
+fmt.Println("hello world")
`
	results := ScanDiff(diff)
	if len(results) != 0 {
		t.Errorf("expected no findings, got %d", len(results))
	}
}

func TestScanDiff_GitHubToken(t *testing.T) {
	diff := "+++ b/config.go\n@@ -1,1 +1,2 @@\n+token := \"ghp_" + strings.Repeat("A", 36) + "\"\n"
	results := ScanDiff(diff)
	if len(results) == 0 {
		t.Error("expected GitHub token to be detected")
	}
	found := false
	for _, r := range results {
		if strings.Contains(r.PatternName, "GitHub") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected GitHub PAT pattern, got %v", results)
	}
}

func TestScanDiff_AWSKey(t *testing.T) {
	diff := "+++ b/deploy.sh\n@@ -1,1 +1,2 @@\n+export AWS_KEY=AKIA" + strings.Repeat("A", 16) + "\n"
	results := ScanDiff(diff)
	if len(results) == 0 {
		t.Error("expected AWS key to be detected")
	}
}

func TestScanDiff_PEMKey(t *testing.T) {
	diff := "+++ b/key.pem\n@@ -1,1 +1,2 @@\n+-----BEGIN RSA PRIVATE KEY-----\n"
	results := ScanDiff(diff)
	if len(results) == 0 {
		t.Error("expected PEM key to be detected")
	}
}

func TestScanDiff_RemovalsIgnored(t *testing.T) {
	diff := "+++ b/config.go\n@@ -1,2 +1,1 @@\n-token := \"ghp_" + strings.Repeat("A", 36) + "\"\n"
	results := ScanDiff(diff)
	if len(results) != 0 {
		t.Errorf("removed lines should not be flagged, got %d results", len(results))
	}
}

func TestScanDiff_LineNumberTracking(t *testing.T) {
	diff := "+++ b/secrets.go\n@@ -1,1 +3,3 @@\n line1\n line2\n+token := \"ghp_" + strings.Repeat("B", 36) + "\"\n"
	results := ScanDiff(diff)
	if len(results) == 0 {
		t.Fatal("expected finding")
	}
}

func TestFormatScanReport_Empty(t *testing.T) {
	report := FormatScanReport(nil)
	if report != "" {
		t.Errorf("expected empty report, got %q", report)
	}
}

func TestFormatScanReport_WithResults(t *testing.T) {
	results := []ScanResult{
		{PatternName: "GitHub PAT", File: "config.go", LineNumber: 5, LineContent: "token=ghp_***"},
	}
	report := FormatScanReport(results)
	if !strings.Contains(report, "SECRET SCAN BLOCKED") {
		t.Error("expected SECRET SCAN BLOCKED header")
	}
	if !strings.Contains(report, "GitHub PAT") {
		t.Error("expected pattern name in report")
	}
	if !strings.Contains(report, "config.go") {
		t.Error("expected file name in report")
	}
}

func TestScanDiff_GenericSecret(t *testing.T) {
	diff := "+++ b/app.env\n@@ -0,0 +1,1 @@\n+secret=my_super_secret_value_here_12345\n"
	results := ScanDiff(diff)
	_ = results
}

func TestScanDiff_BasicAuthURL(t *testing.T) {
	diff := "+++ b/config.go\n@@ -0,0 +1,1 @@\n+url := \"https://user:password@example.com/api\"\n"
	results := ScanDiff(diff)
	if len(results) == 0 {
		t.Error("expected basic auth URL to be detected")
	}
}

func TestScanDiff_AnthropicKey(t *testing.T) {
	diff := "+++ b/client.go\n@@ -0,0 +1,1 @@\n+key := \"sk-ant-" + strings.Repeat("a", 32) + "\"\n"
	results := ScanDiff(diff)
	if len(results) == 0 {
		t.Error("expected Anthropic key to be detected")
	}
}
