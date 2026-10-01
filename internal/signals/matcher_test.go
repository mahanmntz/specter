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
