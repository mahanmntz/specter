package signals

import "testing"

func resetProfile(t *testing.T) {
	t.Helper()
	profileMu.Lock()
	origKw, origInc, origExc, origSkills := BackendKeywords, includeRoleTerms, excludeRoleTerms, profileSkills
	profileMu.Unlock()
	t.Cleanup(func() {
		profileMu.Lock()
		BackendKeywords, includeRoleTerms, excludeRoleTerms, profileSkills = origKw, origInc, origExc, origSkills
		compileKeywords()
		profileMu.Unlock()
	})
}

func TestPresetsAreComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range Presets() {
		if p.ID == "" || p.Label == "" || seen[p.ID] {
			t.Fatalf("bad or duplicate preset %+v", p)
		}
		seen[p.ID] = true
		// ApplyProfile only overrides non-empty fields, so every preset must fill all four.
		pr := p.Profile
		if len(pr.Skills) == 0 || len(pr.Keywords) == 0 || len(pr.RoleInclude) == 0 || len(pr.RoleExclude) == 0 {
			t.Errorf("preset %s leaves a profile field empty", p.ID)
		}
	}
	if _, ok := PresetByID("BACKEND"); !ok {
		t.Error("lookup should be case-insensitive")
	}
	if _, ok := PresetByID("nope"); ok {
		t.Error("unknown preset must not resolve")
	}
}

func TestSwitchingPresetChangesMatching(t *testing.T) {
	resetProfile(t)

	apply := func(id string) {
		prof, ok := ProfileFor(id, nil)
		if !ok {
			t.Fatalf("no preset %s", id)
		}
		ApplyProfile(prof)
	}

	apply("backend")
	if !IsBackendRole("Senior Backend Engineer", "Engineering") || IsBackendRole("iOS Engineer", "Mobile") {
		t.Error("backend preset misclassifies")
	}

	apply("mobile")
	if !IsBackendRole("Senior iOS Engineer", "Mobile") || IsBackendRole("Backend Engineer", "Platform") {
		t.Error("mobile preset misclassifies")
	}
	if kws := MatchBackendKeywords("Build SwiftUI apps"); len(kws) == 0 {
		t.Error("mobile keywords not applied")
	}

	apply("devops")
	if !IsBackendRole("Site Reliability Engineer", "") || IsBackendRole("Marketing Manager", "Growth") {
		t.Error("devops preset misclassifies")
	}
}

func TestProfileForMergesUserSkills(t *testing.T) {
	prof, _ := ProfileFor("frontend", []string{" Elm ", "React", ""})
	if len(prof.Skills) != 2 || prof.Skills[0] != "Elm" {
		t.Errorf("skills = %v", prof.Skills)
	}
	hasElm, reactCount := false, 0
	for _, k := range prof.Keywords {
		if k == "elm" {
			hasElm = true
		}
		if k == "react" {
			reactCount++
		}
	}
	if !hasElm || reactCount != 1 {
		t.Errorf("custom skills must be added once as keywords: elm=%v react=%d", hasElm, reactCount)
	}
	// The preset itself must not be mutated.
	orig, _ := PresetByID("frontend")
	if orig.Profile.Skills[0] != "React" {
		t.Error("preset was mutated")
	}
}
