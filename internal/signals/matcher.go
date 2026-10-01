package signals

import (
	"regexp"
	"strings"
)

// Primary backend keywords to identify relevant hiring signals.
var BackendKeywords = []string{
	"golang", "go", "distributed systems", "distributed", "redis", "fintech",
	"high throughput", "low latency", "kafka", "grpc", "microservices",
	"postgres", "postgresql", "concurrency", "kubernetes", "scalability",
	"event-driven", "consensus", "raft", "paxos", "clickhouse", "storage",
	"database", "indexing", "key-value", "nosql", "sql", "rust", "etcd",
	"cockroachdb", "cassandra", "dynamodb", "spanner", "temporal", "rocksb",
	"pebble", "badger", "boltdb", "multithreading", "linux", "cloud native",
}

var wordBoundaryRegexes = make(map[string]*regexp.Regexp)

func init() {
	for _, kw := range BackendKeywords {
		wordBoundaryRegexes[kw] = regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(kw) + `\b`)
	}
}

// MatchBackendKeywords returns all unique matched backend keywords from text.
func MatchBackendKeywords(text string) []string {
	var matches []string
	seen := make(map[string]bool)
	for kw, re := range wordBoundaryRegexes {
		if re.MatchString(text) {
			normalized := strings.ToLower(kw)
			if !seen[normalized] {
				seen[normalized] = true
				matches = append(matches, kw)
			}
		}
	}
	return matches
}

// IsBackendRole checks if the role title or department matches backend engineering.
func IsBackendRole(title, department string) bool {
	combined := strings.ToLower(title + " " + department)

	excludeTerms := []string{
		"frontend", "front-end", "ui/ux", "sales", "marketing", "recruiter", "talent",
		"account executive", "finance", "legal", "compliance", "product manager",
		"designer", "hr business partner", "customer success", "operations manager",
		"deal desk", "administrative",
	}
	for _, excl := range excludeTerms {
		if strings.Contains(combined, excl) {
			return false
		}
	}

	backendTerms := []string{
		"backend", "back-end", "distributed", "platform", "infrastructure",
		"systems", "system", "core engine", "golang", "go engineer", "data engineer",
		"site reliability", "sre", "cloud engineer", "software engineer",
		"member of technical staff", "technical staff", "mts", "storage",
		"database", "kernel", "architect", "compiler", "protocols", "consensus",
	}

	for _, term := range backendTerms {
		if strings.Contains(combined, term) {
			return true
		}
	}
	return false
}

// ExtractSeniority attempts to determine role seniority.
func ExtractSeniority(title string) string {
	lower := strings.ToLower(title)
	switch {
	case strings.Contains(lower, "staff"), strings.Contains(lower, "principal"):
		return "Staff/Principal"
	case strings.Contains(lower, "lead"), strings.Contains(lower, "manager"):
		return "Lead/Engineering Manager"
	case strings.Contains(lower, "senior"), strings.Contains(lower, "sr."):
		return "Senior"
	case strings.Contains(lower, "junior"), strings.Contains(lower, "jr."):
		return "Junior"
	default:
		return "Mid/Standard"
	}
}
