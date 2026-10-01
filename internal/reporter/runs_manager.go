package reporter

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// RunManager handles date-stamped execution directories and latest run pointers.
type RunManager struct {
	BaseDir string
}

// NewRunManager initializes a new RunManager for directory structure management.
func NewRunManager(baseDir string) *RunManager {
	if baseDir == "" {
		baseDir = "reports"
	}
	return &RunManager{BaseDir: baseDir}
}

// InitRunDir creates a structured directory: baseDir/runs/YYYY-MM-DD_HH-MM-SS
func (rm *RunManager) InitRunDir(t time.Time) (runID string, runDir string, err error) {
	if t.IsZero() {
		t = time.Now()
	}
	runID = t.Format("2006-01-02_15-04-05")
	runDir = filepath.Join(rm.BaseDir, "runs", runID)

	if err := os.MkdirAll(runDir, 0755); err != nil {
		return "", "", fmt.Errorf("failed creating run directory %s: %w", runDir, err)
	}

	return runID, runDir, nil
}

// LinkLatestRun creates or updates a pointer to the most recent run at baseDir/latest.
// On Unix-like systems it creates a symlink; if symlinking fails it writes a LATEST file.
func (rm *RunManager) LinkLatestRun(runDir string) error {
	latestDir := filepath.Join(rm.BaseDir, "latest")

	// Remove existing symlink or directory
	_ = os.Remove(latestDir)

	// Try relative symlink for portability
	relTarget, err := filepath.Rel(rm.BaseDir, runDir)
	if err != nil {
		relTarget = runDir
	}

	symErr := os.Symlink(relTarget, latestDir)
	if symErr != nil {
		// Fallback: write a LATEST pointer file containing the path
		latestPointerFile := filepath.Join(rm.BaseDir, "LATEST")
		if err := os.WriteFile(latestPointerFile, []byte(runDir), 0644); err != nil {
			return fmt.Errorf("failed creating latest link/pointer: %w", err)
		}
	}
	return nil
}

// ListPastRuns returns a sorted list of past run IDs (newest first).
func (rm *RunManager) ListPastRuns() ([]string, error) {
	runsDir := filepath.Join(rm.BaseDir, "runs")
	entries, err := os.ReadDir(runsDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var runs []string
	for _, e := range entries {
		if e.IsDir() {
			runs = append(runs, e.Name())
		}
	}

	sort.Slice(runs, func(i, j int) bool {
		return runs[i] > runs[j] // Newest first
	})

	return runs, nil
}
