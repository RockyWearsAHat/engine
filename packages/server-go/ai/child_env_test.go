package ai

import (
	"slices"
	"testing"
)

func TestChildEnv(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "empty input",
			input: []string{},
			want:  []string{},
		},
		{
			name:  "no PORT entries",
			input: []string{"HOME=/home/user", "PATH=/usr/bin", "SHELL=/bin/bash"},
			want:  []string{"HOME=/home/user", "PATH=/usr/bin", "SHELL=/bin/bash"},
		},
		{
			name:  "removes PORT=24444",
			input: []string{"HOME=/home/user", "PORT=24444", "SHELL=/bin/bash"},
			want:  []string{"HOME=/home/user", "SHELL=/bin/bash"},
		},
		{
			name:  "removes Port=1 case-insensitive",
			input: []string{"HOME=/home/user", "Port=1", "SHELL=/bin/bash"},
			want:  []string{"HOME=/home/user", "SHELL=/bin/bash"},
		},
		{
			name:  "removes port=8080 case-insensitive",
			input: []string{"port=8080", "SHELL=/bin/bash"},
			want:  []string{"SHELL=/bin/bash"},
		},
		{
			name:  "keeps PORTAL and MYPORT",
			input: []string{"PORTAL=x", "PORT=9999", "MYPORT=2"},
			want:  []string{"PORTAL=x", "MYPORT=2"},
		},
		{
			name:  "preserves order",
			input: []string{"A=1", "B=2", "PORT=3", "C=4", "D=5"},
			want:  []string{"A=1", "B=2", "C=4", "D=5"},
		},
		{
			name:  "removes multiple PORT entries",
			input: []string{"PORT=1", "A=2", "port=3", "B=4", "PORT=5"},
			want:  []string{"A=2", "B=4"},
		},
		{
			name:  "preserves value with equals sign",
			input: []string{"KEY=val=ue", "PORT=9", "OTHER=x=y=z"},
			want:  []string{"KEY=val=ue", "OTHER=x=y=z"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save the original to check we don't mutate it
			originalInput := make([]string, len(tt.input))
			copy(originalInput, tt.input)

			got := childEnv(tt.input)

			// Verify input was not mutated
			if !slices.Equal(tt.input, originalInput) {
				t.Errorf("childEnv mutated input: got %v, want %v", tt.input, originalInput)
			}

			// Verify output
			if !slices.Equal(got, tt.want) {
				t.Errorf("childEnv(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
