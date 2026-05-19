package ai

import "testing"

// Triggers fall into two buckets: explicit slash/bang overrides, and natural
// language directives that the request classifier already routes to the
// workflow path. The detector must catch both.
func TestDetectAutonomousIntent_Triggers(t *testing.T) {
	cases := []string{
		// Slash / bang overrides — always engage autonomous mode.
		"/build",
		"/auto",
		"/autonomous",
		"/ship",
		"!build",
		"!auto",
		"!autonomous",
		"/build the platformer level system",
		"/auto fix the broken physics",

		// Natural workflow directives — classifier picks these up.
		"build",
		"BUILD",
		"build it",
		"build this",
		"build the project",
		"build the platformer",
		"implement the inventory system",
		"create a new player controller",
		"add a level loader",
		"fix the broken physics on slopes",
		"refactor the level module",
		"scaffold the project",
		"write the player tests",
		"test all of it end to end",
		"deploy to staging",
		"build and ship",
		"build and test it all",
	}
	for _, c := range cases {
		if !DetectAutonomousIntent(c) {
			t.Errorf("expected autonomous intent for %q", c)
		}
	}
}

// Non-triggers cover empty input, conversational chat, status checks, tool
// reads, and question-form prompts that contain workflow keywords but are
// asking ABOUT the work rather than directing it.
func TestDetectAutonomousIntent_NonTriggers(t *testing.T) {
	cases := []string{
		"",
		"   ",
		"hello",
		"hi there",
		"how are you",
		"status",
		"status?",
		"what does build do",
		"what does build do?",
		"explain the build process",
		"explain how the scaffolder works",
		"can you explain the level loader",
		"how is the build going",
		"why did the test fail",
		"tell me about the player module",
		"please explain why physics broke",
		"goose",
		"autonomy is interesting",
		"the build failed last night, what happened",
		"is it ready",
		"are we close to done",
		"show me how the level loads",
		"read the file player.go",
		"list files in the repo",
	}
	for _, c := range cases {
		if DetectAutonomousIntent(c) {
			t.Errorf("did not expect autonomous intent for %q", c)
		}
	}
}
