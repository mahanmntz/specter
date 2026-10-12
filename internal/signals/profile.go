package signals

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Profile describes what the user is looking for. Every field is optional;
// omitted fields keep the built-in defaults.
type Profile struct {
	// Skills are scored against each posting to compute the match percentage.
	Skills []string `json:"skills"`
	// Keywords replace the built-in tech keyword list used to tag postings.
	Keywords []string `json:"keywords"`
	// RoleInclude replaces the title terms that mark a posting as relevant.
	RoleInclude []string `json:"role_include"`
	// RoleExclude replaces the title terms that rule a posting out.
	RoleExclude []string `json:"role_exclude"`
}

var profileSkills []string

// LoadProfile reads a JSON profile and applies it process-wide.
// It must be called once at startup, before any matching happens.
func LoadProfile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var p Profile
	if err := json.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("invalid profile %s: %w", path, err)
	}
	ApplyProfile(p)
	return nil
}

// ApplyProfile overrides the matcher defaults with the non-empty fields of p.
func ApplyProfile(p Profile) {
	if s := cleanList(p.Skills); len(s) > 0 {
		profileSkills = s
	}
	if k := cleanList(p.Keywords); len(k) > 0 {
		BackendKeywords = k
		compileKeywords()
	}
	if r := cleanList(p.RoleInclude); len(r) > 0 {
		includeRoleTerms = lowerAll(r)
	}
	if r := cleanList(p.RoleExclude); len(r) > 0 {
		excludeRoleTerms = lowerAll(r)
	}
}

func cleanList(in []string) []string {
	var out []string
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func lowerAll(in []string) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i] = strings.ToLower(v)
	}
	return out
}
