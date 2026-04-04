package git

import "testing"

func TestParseGitHubRepo(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		remote   string
		owner    string
		repo     string
		expected bool
	}{
		{
			name:     "https remote",
			remote:   "https://github.com/octocat/Hello-World.git",
			owner:    "octocat",
			repo:     "Hello-World",
			expected: true,
		},
		{
			name:     "ssh scp remote",
			remote:   "git@github.com:octocat/Hello-World.git",
			owner:    "octocat",
			repo:     "Hello-World",
			expected: true,
		},
		{
			name:     "ssh url remote",
			remote:   "ssh://git@github.com/octocat/Hello-World.git",
			owner:    "octocat",
			repo:     "Hello-World",
			expected: true,
		},
		{
			name:     "non github remote",
			remote:   "https://gitlab.com/octocat/Hello-World.git",
			expected: false,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			owner, repo, ok := ParseGitHubRepo(testCase.remote)
			if ok != testCase.expected {
				t.Fatalf("ParseGitHubRepo(%q) ok = %v, want %v", testCase.remote, ok, testCase.expected)
			}
			if owner != testCase.owner || repo != testCase.repo {
				t.Fatalf("ParseGitHubRepo(%q) = (%q, %q), want (%q, %q)", testCase.remote, owner, repo, testCase.owner, testCase.repo)
			}
		})
	}
}
