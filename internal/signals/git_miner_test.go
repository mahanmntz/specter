package signals

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"specter/internal/crawler"
)

func TestIsValidOutreachEmail(t *testing.T) {
	tests := []struct {
		email string
		valid bool
	}{
		{"jane.doe@stripe.com", true},
		{"alex_chen@gmail.com", true},
		{"dev-ops@netflix.co.uk", true},
		{"123456+user@users.noreply.github.com", false},
		{"user@users.noreply.github.com", false},
		{"dependabot[bot]@users.noreply.github.com", false},
		{"actions@github.com", false},
		{"info@company.com", false},
		{"support@company.com", false},
		{"sales@company.com", false},
		{"help@company.com", false},
		{"not-an-email", false},
		{"test@localhost", false},
		{"user@example.com", false},
		{"", false},
	}

	for _, tt := range tests {
		got := IsValidOutreachEmail(tt.email)
		if got != tt.valid {
			t.Errorf("IsValidOutreachEmail(%q) = %v; want %v", tt.email, got, tt.valid)
		}
	}
}

func TestClassifyRole(t *testing.T) {
	tests := []struct {
		msgs        []string
		commitCount int
		expected    string
	}{
		{[]string{"arch: define rfc for distributed consensus", "cut release v1.0.0"}, 4, "Tech Lead"},
		{[]string{"refactor memory alloc in raft runtime", "optimize gc pause"}, 2, "Staff/Principal"},
		{[]string{"add redis caching to grpc payment service", "postgres migration"}, 3, "Senior Backend"},
		{[]string{"fix typo in readme", "update unit test"}, 1, "Engineer"},
	}

	for _, tt := range tests {
		got := ClassifyRole(tt.msgs, tt.commitCount)
		if got != tt.expected {
			t.Errorf("ClassifyRole(%v, %d) = %q; want %q", tt.msgs, tt.commitCount, got, tt.expected)
		}
	}
}

func TestCalculateRelevanceScore(t *testing.T) {
	langsGo := map[string]int{"Go": 10}
	scoreLead := CalculateRelevanceScore("Tech Lead", langsGo, true, 5)
	if scoreLead < 90 {
		t.Errorf("expected high score (>90) for Tech Lead with Go and domain match, got %d", scoreLead)
	}

	langsOther := map[string]int{"HTML": 1}
	scoreEng := CalculateRelevanceScore("Engineer", langsOther, false, 1)
	if scoreEng > 60 {
		t.Errorf("expected lower score (<60) for Engineer without Go, got %d", scoreEng)
	}
}

func TestExtractFromPatch(t *testing.T) {
	patchContent := `From 3f8a0b1234567890 Mon Sep 17 00:00:00 2001
From: Marcus Aurelius <marcus.aurelius@stoic.io>
Date: Wed, 30 Sep 2026 12:00:00 +0000
Subject: [PATCH] feat: implement lock-free ring buffer for metrics pipeline

---
`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(patchContent))
	}))
	defer server.Close()

	fetcher := crawler.NewFetcher(crawler.FetcherConfig{
		Timeout:              5 * time.Second,
		AllowPrivateNetworks: true,
	})

	miner := NewGitMiner(fetcher, GitMinerOptions{})
	// Mock extraction directly from patch buffer
	matches := patchFromRegex.FindSubmatch([]byte(patchContent))
	if len(matches) < 3 {
		t.Fatalf("expected patch regex match, got none")
	}
	name := string(matches[1])
	email := string(matches[2])

	if email != "marcus.aurelius@stoic.io" || name != "Marcus Aurelius " {
		t.Errorf("unexpected extracted patch email=%q, name=%q", email, name)
	}
	_ = miner
	_ = context.Background()
}

func TestIsBotOrCI(t *testing.T) {
	tests := []struct {
		name   string
		handle string
		email  string
		isBot  bool
	}{
		{"github-actions[bot]", "github-actions[bot]", "teamcity@cockroachlabs.com", true},
		{"Vishal Jaishankar", "VishalJaishankar", "vishal.jaishankar@cockroachlabs.com", false},
		{"Srinath Shrestha", "srinathshrestha", "srinathshrestha9890@gmail.com", false},
		{"Dependabot", "dependabot[bot]", "dependabot@github.com", true},
		{"CI Builder", "ci-runner", "ci@stoic.io", true},
		{"Teamcity Agent", "teamcity-ci", "teamcity@example.com", true},
		{"CircleCI", "circleci", "build@circleci.com", true},
		{"Abbott Miller", "abbottm", "abbott@cockroachlabs.com", false},
		{"Bors", "bors-bot", "bors@users.noreply.github.com", true},
	}

	for _, tt := range tests {
		got := IsBotOrCI(tt.name, tt.handle, tt.email)
		if got != tt.isBot {
			t.Errorf("IsBotOrCI(%q, %q, %q) = %v; want %v", tt.name, tt.handle, tt.email, got, tt.isBot)
		}
	}
}

func TestClassifyBackendArchetype(t *testing.T) {
	role1, score1 := ClassifyBackendArchetype(
		[]string{"define architecture rfc for distributed consensus", "cut release v2.0"},
		map[string]int{"Go": 5}, 10, "cockroachdb/cockroach", []string{"consensus", "raft"},
	)
	if role1 != "Tech Lead" {
		t.Errorf("expected Tech Lead, got %s", role1)
	}
	if score1 < 90 {
		t.Errorf("expected high score >=90, got %d", score1)
	}

	role2, _ := ClassifyBackendArchetype(
		[]string{"refactor pebble lsm tree compaction and btree indexing"},
		map[string]int{"Go": 2}, 4, "cockroachdb/pebble", []string{"storage", "indexing"},
	)
	if role2 != "Core Storage Engineer" {
		t.Errorf("expected Core Storage Engineer, got %s", role2)
	}

	role3, _ := ClassifyBackendArchetype(
		[]string{"update helm release crd and operator deployment"},
		map[string]int{"Go": 2}, 3, "cockroachdb/helm-charts", []string{"k8s"},
	)
	if role3 != "Infrastructure Engineer" {
		t.Errorf("expected Infrastructure Engineer, got %s", role3)
	}
}

