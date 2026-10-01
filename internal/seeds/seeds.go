package seeds

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
)

//go:embed seeds.json
var embeddedSeedsJSON []byte

// SeedTarget defines a curated high-signal tech company for autonomous reconnaissance.
type SeedTarget struct {
	Name       string `json:"name"`
	Domain     string `json:"domain"`
	GitHubOrg  string `json:"github_org"`
	ATSService string `json:"ats_service"`  // "greenhouse", "lever", "ashby", "generic"
	ATSBoardID string `json:"ats_board_id"` // e.g., "hashicorp", "grafanalabs"
	Priority   int    `json:"priority"`     // 1 (high), 2 (medium)
}

// GetEmbeddedSeeds parses the pre-populated embedded seed catalog.
func GetEmbeddedSeeds() ([]SeedTarget, error) {
	if len(embeddedSeedsJSON) == 0 {
		return nil, fmt.Errorf("embedded seeds.json is empty")
	}
	var targets []SeedTarget
	if err := json.Unmarshal(embeddedSeedsJSON, &targets); err != nil {
		return nil, fmt.Errorf("failed decoding embedded seeds.json: %w", err)
	}
	return targets, nil
}

// LoadSeeds loads seed targets from an external JSON file or falls back to embedded seeds if path is empty.
func LoadSeeds(path string) ([]SeedTarget, error) {
	if path == "" {
		return GetEmbeddedSeeds()
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read seeds file %q: %w", path, err)
	}

	var targets []SeedTarget
	if err := json.Unmarshal(data, &targets); err != nil {
		return nil, fmt.Errorf("failed parsing seeds JSON from %q: %w", path, err)
	}
	return targets, nil
}
