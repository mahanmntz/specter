package signals

import "testing"

func resetProfile(t *testing.T) {
	t.Helper()
	profileMu.Lock()
	origKw, origInc, origExc, origSkills := BackendKeywords, includeRoleTerms, excludeRoleTerms, profileSkills
	origBlock := blockRoleTerms
	profileMu.Unlock()
	t.Cleanup(func() {
		profileMu.Lock()
		BackendKeywords, includeRoleTerms, excludeRoleTerms, profileSkills = origKw, origInc, origExc, origSkills
		blockRoleTerms = origBlock
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
		if len(pr.Skills) == 0 || len(pr.Keywords) == 0 || len(pr.RoleInclude) == 0 || len(pr.RoleExclude) == 0 || len(pr.RoleBlock) == 0 {
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

	apply("frontend")
	if !IsBackendRole("Senior Frontend Engineer", "") || !IsBackendRole("Software Engineer, Web", "") {
		t.Error("frontend preset should accept frontend and generic software engineer titles")
	}
	for _, title := range []string{"Backend Software Engineer", "Software Engineer, Backend", "Senior iOS Engineer", "Site Reliability Engineer"} {
		if IsBackendRole(title, "Engineering") {
			t.Errorf("frontend preset must block %q", title)
		}
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

func TestNewRolesMatchTheirTitlesAndBlockOthers(t *testing.T) {
	resetProfile(t)
	cases := []struct {
		preset string
		accept []string
		reject []string
	}{
		{"software-engineer", []string{"Software Engineer", "Senior Software Developer", "Member of Technical Staff", "Backend Engineer", "Frontend Engineer", "Full Stack Developer"},
			[]string{"Sales Engineer", "Solutions Engineer", "Developer Advocate", "Marketing Manager"}},
		{"security", []string{"Application Security Engineer", "Security Researcher", "Detection Engineer"}, []string{"iOS Engineer", "Frontend Engineer"}},
		{"qa", []string{"QA Automation Engineer", "SDET", "Software Engineer in Test"}, []string{"Android Engineer", "DevOps Engineer"}},
		{"embedded", []string{"Embedded Software Engineer", "Firmware Engineer", "Linux Kernel Developer"}, []string{"Frontend Engineer", "Web Developer"}},
		{"blockchain", []string{"Smart Contract Engineer", "Blockchain Engineer", "Protocol Engineer"}, []string{"iOS Engineer", "Data Scientist"}},
		{"game", []string{"Gameplay Programmer", "Graphics Engineer", "Unity Developer"}, []string{"Data Scientist", "DevOps Engineer"}},
	}
	for _, c := range cases {
		prof, ok := ProfileFor(c.preset, nil)
		if !ok {
			t.Fatalf("missing preset %s", c.preset)
		}
		ApplyProfile(prof)
		for _, title := range c.accept {
			if !IsBackendRole(title, "") {
				t.Errorf("%s should accept %q", c.preset, title)
			}
		}
		for _, title := range c.reject {
			if IsBackendRole(title, "") {
				t.Errorf("%s should reject %q", c.preset, title)
			}
		}
	}
}

func TestGeneralistIsFirstPreset(t *testing.T) {
	if got := Presets()[0].ID; got != "software-engineer" {
		t.Errorf("first preset = %s", got)
	}
	if len(Presets()) < 12 {
		t.Errorf("expected a broad role list, got %d", len(Presets()))
	}
}
