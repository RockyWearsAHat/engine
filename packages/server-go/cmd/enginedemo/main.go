// Command enginedemo runs a real end-to-end autonomous build via
// ai.RunAutonomousProject, with live progress logging. It is a manual harness
// for exercising the full orchestrator (intake/grill → PRD → plan → critique →
// build → review → validate) against a real backend (e.g. the claudecode
// provider on the Claude subscription).
//
// Usage:
//
//	ENGINE_MODEL_PROVIDER=claudecode ENGINE_MODEL=claude-opus-4-8 \
//	ENGINE_DEMO_DIR=/path/to/output ENGINE_DEMO_BRIEF_FILE=/path/to/brief.txt \
//	GOWORK=off go run ./cmd/enginedemo
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/engine/server/ai"
	"github.com/engine/server/db"
)

func ts() string { return time.Now().Format("15:04:05") }

func logln(format string, a ...any) {
	fmt.Printf("[%s] %s\n", ts(), fmt.Sprintf(format, a...))
}

func main() {
	dir := strings.TrimSpace(os.Getenv("ENGINE_DEMO_DIR"))
	briefFile := strings.TrimSpace(os.Getenv("ENGINE_DEMO_BRIEF_FILE"))
	if dir == "" || briefFile == "" {
		logln("ENGINE_DEMO_DIR and ENGINE_DEMO_BRIEF_FILE are required")
		os.Exit(2)
	}
	briefBytes, err := os.ReadFile(briefFile)
	if err != nil {
		logln("read brief: %v", err)
		os.Exit(2)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		logln("mkdir dir: %v", err)
		os.Exit(2)
	}
	if err := db.Init(dir); err != nil {
		logln("db.Init: %v", err)
		os.Exit(2)
	}

	logln("=== enginedemo starting ===")
	logln("provider=%s model=%s", os.Getenv("ENGINE_MODEL_PROVIDER"), os.Getenv("ENGINE_MODEL"))
	logln("output dir: %s", dir)

	cfg := ai.OrchestratorConfig{
		ProjectPath:        dir,
		Owner:              "local",
		Repo:               "lifelab",
		Brief:              string(briefBytes),
		MaxOuterIterations: 80,
		OnPhase: func(phase, detail string) {
			logln("PHASE %-10s %s", strings.ToUpper(phase), detail)
		},
		OnProgress: func(message string) {
			logln("progress: %s", message)
		},
		OnError: func(message string) {
			logln("ERROR: %s", message)
		},
		OnPlanUpdate: func(state *ai.OrchestrationState) {
			done := 0
			for _, s := range state.Plan {
				if s.Done {
					done++
				}
			}
			logln("plan: %d/%d steps done", done, len(state.Plan))
		},
	}

	start := time.Now()
	state, runErr := ai.RunAutonomousProject(cfg)
	logln("=== run finished in %s ===", time.Since(start).Round(time.Second))
	if runErr != nil {
		logln("RunAutonomousProject error: %v", runErr)
	}
	if state != nil {
		logln("completedAt=%q outerIterations=%d steps=%d", state.CompletedAt, state.OuterIterations, len(state.Plan))
		for _, s := range state.Plan {
			mark := " "
			if s.Done {
				mark = "x"
			}
			logln("  [%s] %d. %s", mark, s.Index, s.Title)
		}
		if strings.TrimSpace(state.LastValidation) != "" {
			logln("last validation:\n%s", state.LastValidation)
		}
	}
	if runErr != nil {
		os.Exit(1)
	}
}
