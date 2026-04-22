package discord

import (
	"reflect"
	"testing"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		prefix    string
		ok        bool
		wantCmd   string
		wantParts []string
	}{
		{
			name:      "valid command",
			content:   "!status project-a",
			prefix:    "!",
			ok:        true,
			wantCmd:   "status",
			wantParts: []string{"project-a"},
		},
		{
			name:      "case normalized",
			content:   "!AsK hello world",
			prefix:    "!",
			ok:        true,
			wantCmd:   "ask",
			wantParts: []string{"hello", "world"},
		},
		{
			name:    "missing prefix",
			content: "status",
			prefix:  "!",
			ok:      false,
		},
		{
			name:    "prefix only",
			content: "!",
			prefix:  "!",
			ok:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, parts, ok := parseCommand(tt.content, tt.prefix)
			if ok != tt.ok {
				t.Fatalf("ok mismatch: got %v want %v", ok, tt.ok)
			}
			if cmd != tt.wantCmd {
				t.Fatalf("cmd mismatch: got %q want %q", cmd, tt.wantCmd)
			}
			if !reflect.DeepEqual(parts, tt.wantParts) {
				t.Fatalf("parts mismatch: got %#v want %#v", parts, tt.wantParts)
			}
		})
	}
}

func TestSlug(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "My Editor", want: "my-editor"},
		{in: "Repo___Name", want: "repo-name"},
		{in: "  ", want: "project"},
	}

	for _, tt := range tests {
		if got := slug(tt.in); got != tt.want {
			t.Fatalf("slug(%q) = %q want %q", tt.in, got, tt.want)
		}
	}
}

func TestSplitForDiscord(t *testing.T) {
	parts := splitForDiscord("line1\nline2\nline3", 7)
	if len(parts) < 2 {
		t.Fatalf("expected split output, got %#v", parts)
	}
	if parts[0] == "" || parts[1] == "" {
		t.Fatalf("unexpected empty split result: %#v", parts)
	}
}
