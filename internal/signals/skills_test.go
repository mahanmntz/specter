package signals

import (
	"sort"
	"testing"
)

func TestCatalogIsConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, g := range SkillCatalog() {
		if g.Category == "" || len(g.Skills) == 0 {
			t.Fatalf("bad group %+v", g)
		}
		for _, s := range g.Skills {
			if seen[s] {
				t.Errorf("duplicate skill %q", s)
			}
			seen[s] = true
			if c, ok := CanonicalSkill(s); !ok || c != s {
				t.Errorf("skill %q does not resolve to itself (got %q)", s, c)
			}
		}
	}
	// Every role signature must reference real catalog skills.
	for role, sigs := range roleSignatures {
		for _, s := range sigs {
			if !seen[s] {
				t.Errorf("role %s references unknown skill %q", role, s)
			}
		}
		if _, ok := PresetByID(role); !ok {
			t.Errorf("signature for unknown preset %q", role)
		}
	}
}

func TestCanonicalSkillAliases(t *testing.T) {
	cases := map[string]string{"golang": "Go", " K8S ": "Kubernetes", "nodejs": "Node.js", "postgres": "PostgreSQL", "Tailwind CSS": "Tailwind", "C#": "C#"}
	for in, want := range cases {
		if got, ok := CanonicalSkill(in); !ok || got != want {
			t.Errorf("%q → %q,%v want %q", in, got, ok, want)
		}
	}
	if _, ok := CanonicalSkill("cobol-9000"); ok {
		t.Error("unknown skill must not resolve")
	}
}

func TestFindSkillsWordBoundaries(t *testing.T) {
	got := FindSkills("Building services in Golang with k8s and Postgres. Not a Javascript fan; no cache tricks.")
	sort.Strings(got)
	want := map[string]bool{"Go": true, "Kubernetes": true, "PostgreSQL": true, "JavaScript": true}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for _, s := range got {
		if !want[s] {
			t.Errorf("unexpected skill %q in %v", s, got)
		}
	}
	for _, s := range got {
		if s == "Java" || s == "C" {
			t.Errorf("%q must not match inside other words", s)
		}
	}
}

func TestRoleScores(t *testing.T) {
	sc := RoleScores(map[string]float64{"Go": 5, "Kubernetes": 2, "PostgreSQL": 3, "Terraform": 1})
	if sc["backend"] <= sc["devops"] || sc["devops"] <= 0 || sc["frontend"] != 0 {
		t.Errorf("scores = %v", sc)
	}
}

func TestSkillMatchUsesWordBoundaries(t *testing.T) {
	if s, m := CalculateSkillMatch("We use JavaScript and Typescript", []string{"Java"}); s != 0 || m != nil {
		t.Errorf("Java must not match JavaScript: %d %v", s, m)
	}
	if s, _ := CalculateSkillMatch("Senior Rust engineer (C++ a plus)", []string{"C++", "Rust"}); s == 0 {
		t.Error("expected C++/Rust to match")
	}
	if s, _ := CalculateSkillMatch("Great cache design", []string{"C"}); s != 0 {
		t.Error("C must not match inside 'cache'")
	}
}
