package reporter

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunManager(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "specter_runs_test_*")
	if err != nil {
		t.Fatalf("failed creating temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	rm := NewRunManager(tempDir)

	t1 := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	runID1, runDir1, err := rm.InitRunDir(t1)
	if err != nil {
		t.Fatalf("InitRunDir 1 failed: %v", err)
	}
	if runID1 != "2026-10-01_10-00-00" {
		t.Errorf("unexpected runID1: %s", runID1)
	}
	if _, err := os.Stat(runDir1); os.IsNotExist(err) {
		t.Fatalf("runDir1 does not exist: %s", runDir1)
	}

	t2 := time.Date(2026, 10, 1, 11, 0, 0, 0, time.UTC)
	runID2, runDir2, err := rm.InitRunDir(t2)
	if err != nil {
		t.Fatalf("InitRunDir 2 failed: %v", err)
	}

	// Link latest run
	if err := rm.LinkLatestRun(runDir2); err != nil {
		t.Fatalf("LinkLatestRun failed: %v", err)
	}

	// Verify latest pointer exists
	latestLink := filepath.Join(tempDir, "latest")
	latestPointer := filepath.Join(tempDir, "LATEST")
	_, errLink := os.Stat(latestLink)
	_, errPointer := os.Stat(latestPointer)
	if errLink != nil && errPointer != nil {
		t.Fatalf("neither latest symlink nor LATEST file exists")
	}

	// List past runs
	runs, err := rm.ListPastRuns()
	if err != nil {
		t.Fatalf("ListPastRuns failed: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(runs))
	}
	if runs[0] != runID2 || runs[1] != runID1 {
		t.Errorf("unexpected ordering of runs: %v", runs)
	}
}
