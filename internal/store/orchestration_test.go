// Orchestration persistence tests — added to both SQLite and Memory store tests.

package store_test

import (
	"os"
	"testing"
	"time"

	"github.com/goweft/cas/internal/store"
)

func TestOrchestrationRunRoundtripSQLite(t *testing.T) {
	f, _ := os.CreateTemp("", "cas-orch-*.db")
	f.Close()
	defer os.Remove(f.Name())

	s, err := store.NewSQLiteStore(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	testOrchestrationRoundtrip(t, s)
}

func TestOrchestrationRunRoundtripMemory(t *testing.T) {
	testOrchestrationRoundtrip(t, store.NewMemoryStore())
}

func testOrchestrationRoundtrip(t *testing.T, s store.Store) {
	t.Helper()

	run := store.OrchestrationRunRow{
		ID:          "run-1",
		SessionID:   "sess-1",
		Instruction: "read the linear issue and open a github PR",
		Summary:     "Read issue #42 and opened PR #7.",
		StepCount:   2,
		CreatedAt:   time.Now().UTC().Truncate(time.Second),
	}
	if err := s.SaveOrchestrationRun(run); err != nil {
		t.Fatalf("SaveOrchestrationRun: %v", err)
	}

	steps := []store.OrchestrationStepRow{
		{ID: "step-1", RunID: "run-1", StepNumber: 1, WorkspaceID: "ws-linear", Instruction: "list open issues", Output: "Issue #42: fix login bug"},
		{ID: "step-2", RunID: "run-1", StepNumber: 2, WorkspaceID: "ws-github", Instruction: "create PR for issue #42", Output: "PR #7 created"},
	}
	for _, step := range steps {
		if err := s.SaveOrchestrationStep(step); err != nil {
			t.Fatalf("SaveOrchestrationStep: %v", err)
		}
	}

	// Load runs
	runs, err := s.LoadOrchestrationRuns("sess-1")
	if err != nil {
		t.Fatalf("LoadOrchestrationRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	if runs[0].Instruction != run.Instruction {
		t.Errorf("Instruction = %q, want %q", runs[0].Instruction, run.Instruction)
	}
	if runs[0].StepCount != 2 {
		t.Errorf("StepCount = %d, want 2", runs[0].StepCount)
	}
	if runs[0].Summary != run.Summary {
		t.Errorf("Summary = %q, want %q", runs[0].Summary, run.Summary)
	}

	// Load steps
	loaded, err := s.LoadOrchestrationSteps("run-1")
	if err != nil {
		t.Fatalf("LoadOrchestrationSteps: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(loaded))
	}
	if loaded[0].Output != "Issue #42: fix login bug" {
		t.Errorf("step 1 output = %q", loaded[0].Output)
	}
	if loaded[1].WorkspaceID != "ws-github" {
		t.Errorf("step 2 workspace = %q", loaded[1].WorkspaceID)
	}

	// Different session returns empty
	other, _ := s.LoadOrchestrationRuns("sess-other")
	if len(other) != 0 {
		t.Errorf("expected 0 runs for other session, got %d", len(other))
	}
}
