package signals

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProfileOverridesDefaults(t *testing.T) {
	origKw, origInc, origExc, origSkills := BackendKeywords, includeRoleTerms, excludeRoleTerms, profileSkills
	t.Cleanup(func() {
		BackendKeywords, includeRoleTerms, excludeRoleTerms, profileSkills = origKw, origInc, origExc, origSkills
		compileKeywords()
	})

	path := filepath.Join(t.TempDir(), "profile.json")
	body := `{"skills":["Rust"," Tokio "],"keywords":["rust","tokio"],"role_include":["Rust Engineer"],"role_exclude":["sales"]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LoadProfile(path); err != nil {
		t.Fatal(err)
	}

	if got := GetUserSkills(); len(got) != 2 || got[1] != "Tokio" {
		t.Fatalf("skills = %v", got)
	}
	if !IsBackendRole("Senior Rust Engineer", "") {
		t.Error("expected rust role to match")
	}
	if IsBackendRole("Backend Engineer", "") {
		t.Error("default terms should be replaced")
	}
	if kws := MatchBackendKeywords("we use Tokio and Rust"); len(kws) != 2 {
		t.Errorf("keywords = %v", kws)
	}
}

func TestLoadProfileInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	_ = os.WriteFile(path, []byte("{nope"), 0o600)
	if err := LoadProfile(path); err == nil {
		t.Fatal("expected error")
	}
}
