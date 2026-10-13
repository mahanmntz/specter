package seeds

import (
	"testing"
)

func TestGetEmbeddedSeeds(t *testing.T) {
	seeds, err := GetEmbeddedSeeds()
	if err != nil {
		t.Fatalf("failed loading embedded seeds: %v", err)
	}

	if len(seeds) < 40 {
		t.Errorf("expected at least 40 embedded seeds, got %d", len(seeds))
	}

	foundGitLab := false
	for _, s := range seeds {
		if s.Name == "GitLab" && s.ATSBoardID == "gitlab" {
			foundGitLab = true
			break
		}
	}

	if !foundGitLab {
		t.Errorf("expected to find GitLab in embedded seeds catalog")
	}
}
