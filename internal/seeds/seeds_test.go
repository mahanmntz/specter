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

	foundHashi := false
	for _, s := range seeds {
		if s.Name == "HashiCorp" && s.ATSBoardID == "hashicorp" {
			foundHashi = true
			break
		}
	}

	if !foundHashi {
		t.Errorf("expected to find HashiCorp in embedded seeds catalog")
	}
}
