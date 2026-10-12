package signals

import (
	"testing"
)

func TestMatchBackendKeywords(t *testing.T) {
	text := `We are seeking a Senior Backend Engineer proficient in Go / Golang, building high throughput distributed systems backed by Redis, Kafka, and Postgres.`
	matches := MatchBackendKeywords(text)

	expected := map[string]bool{
		"golang":              true,
		"go":                  true,
		"high throughput":     true,
		"distributed systems": true,
		"redis":               true,
		"kafka":               true,
		"postgres":            true,
	}

	for _, m := range matches {
		delete(expected, m)
	}

	if len(expected) > 0 {
		t.Errorf("Expected keywords not matched: %v", expected)
	}
}

func TestIsBackendRole(t *testing.T) {
	cases := []struct {
		title string
		dept  string
		want  bool
	}{
		{"Senior Backend Engineer", "Core Infrastructure", true},
		{"Distributed Systems Engineer", "Platform", true},
		{"Staff Golang Developer", "Payment Gateway", true},
		{"Frontend React Developer", "UI/UX", false},
		{"Product Manager", "Growth", false},
	}

	for _, c := range cases {
		got := IsBackendRole(c.title, c.dept)
		if got != c.want {
			t.Errorf("IsBackendRole(%q, %q) = %v; want %v", c.title, c.dept, got, c.want)
		}
	}
}

func TestExtractSeniority(t *testing.T) {
	if s := ExtractSeniority("Staff Platform Engineer"); s != "Staff/Principal" {
		t.Errorf("expected Staff/Principal, got %s", s)
	}
	if s := ExtractSeniority("Senior Go Developer"); s != "Senior" {
		t.Errorf("expected Senior, got %s", s)
	}
}

func TestCalculateSkillMatch(t *testing.T) {
	text := "Senior Backend Engineer: Golang, PostgreSQL, Kubernetes, gRPC microservices on Linux"
	skills := []string{"Go", "PostgreSQL", "Kubernetes", "gRPC", "Rust"}
	score, matched := CalculateSkillMatch(text, skills)
	if len(matched) != 4 || score != 90 {
		t.Errorf("score=%d matched=%v", score, matched)
	}
	for _, m := range matched {
		if m == "Rust" {
			t.Error("Rust must not match")
		}
	}
	if s, m := CalculateSkillMatch("marketing manager", skills); s != 0 || m != nil {
		t.Errorf("no match expected, got %d %v", s, m)
	}
	if s, _ := CalculateSkillMatch(text, nil); s != 0 {
		t.Error("empty skills must score 0")
	}
	// Five matches score 95, each extra adds 1, and the total never exceeds 100.
	many := []string{"Go", "PostgreSQL", "Kubernetes", "gRPC", "Linux", "Backend", "Microservices", "Docker"}
	if s, _ := CalculateSkillMatch(text+" docker", many); s != 98 {
		t.Errorf("8 matches should score 98, got %d", s)
	}
	extra := append(many, "Redis", "Kafka", "Python", "Terraform", "AWS")
	if s, _ := CalculateSkillMatch(text+" docker redis kafka python terraform aws", extra); s != 100 {
		t.Errorf("score must cap at 100, got %d", s)
	}
}

func TestGetUserSkillsEnvAndDefault(t *testing.T) {
	saved := profileSkills
	profileSkills = nil
	defer func() { profileSkills = saved }()

	t.Setenv("MY_SKILLS", " Rust , ,Tokio ")
	if got := GetUserSkills(); len(got) != 2 || got[0] != "Rust" || got[1] != "Tokio" {
		t.Errorf("got %v", got)
	}
	t.Setenv("MY_SKILLS", "")
	if got := GetUserSkills(); len(got) == 0 {
		t.Error("expected built-in defaults")
	}
}
