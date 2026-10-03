package signals

import (
	"os"
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
	// Expanded ecosystem: Node, TypeScript, Python, System Architecture
	"nodejs", "node.js", "node", "typescript", "nestjs", "nest.js", "express", "expressjs",
	"python", "fastapi", "django", "rabbitmq", "bullmq", "mongodb", "prisma", "typeorm",
	"system design", "rest", "restful", "rest api", "full stack", "fullstack",
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

// IsBackendRole checks if the role title or department matches backend, software, or fullstack engineering.
func IsBackendRole(title, department string) bool {
	combined := strings.ToLower(title + " " + department)

	isFullStackOrBackend := strings.Contains(combined, "full stack") ||
		strings.Contains(combined, "fullstack") ||
		strings.Contains(combined, "backend") ||
		strings.Contains(combined, "back-end") ||
		strings.Contains(combined, "software engineer") ||
		strings.Contains(combined, "software developer") ||
		strings.Contains(combined, "systems engineer")

	excludeTerms := []string{
		"pure frontend", "ui/ux", "sales", "marketing", "recruiter", "talent",
		"account executive", "finance", "legal", "compliance", "product manager",
		"designer", "hr business partner", "customer success", "operations manager",
		"deal desk", "administrative", "copywriter", "office manager",
	}

	if !isFullStackOrBackend {
		for _, excl := range excludeTerms {
			if strings.Contains(combined, excl) {
				return false
			}
		}
	}

	backendTerms := []string{
		"backend", "back-end", "distributed", "platform", "infrastructure",
		"systems", "system", "core engine", "golang", "go engineer", "data engineer",
		"site reliability", "sre", "cloud engineer", "software engineer",
		"software developer", "full stack", "fullstack", "node", "nodejs",
		"typescript", "python", "api engineer", "core contributor",
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

// GetUserSkills returns user's configured skill profile from MY_SKILLS environment variable.
func GetUserSkills() []string {
	raw := os.Getenv("MY_SKILLS")
	if strings.TrimSpace(raw) == "" {
		raw = "Go, TypeScript, Node.js, NestJS, Express, Python, Backend, Distributed Systems, Microservices, System Design, PostgreSQL, Redis, MongoDB, RabbitMQ, Docker, Kubernetes, Linux, gRPC, REST, CI/CD, Software Engineer"
	}
	var skills []string
	for _, s := range strings.Split(raw, ",") {
		clean := strings.TrimSpace(s)
		if clean != "" {
			skills = append(skills, clean)
		}
	}
	return skills
}

// CalculateSkillMatch matches target text against user's skills and computes a 0-100% synergy score.
func CalculateSkillMatch(targetText string, userSkills []string) (int, []string) {
	if len(userSkills) == 0 {
		return 0, nil
	}
	textLower := " " + strings.ToLower(targetText) + " "
	var matched []string
	seen := make(map[string]bool)

	for _, skill := range userSkills {
		sLower := strings.ToLower(skill)
		matches := false
		switch sLower {
		case "go", "golang":
			matches = strings.Contains(textLower, " go ") || strings.Contains(textLower, "golang") ||
				strings.Contains(textLower, "(go)") || strings.Contains(textLower, "go/") ||
				strings.Contains(textLower, "go,") || strings.Contains(textLower, "go-")
		case "node", "node.js", "nodejs":
			matches = strings.Contains(textLower, "node.js") || strings.Contains(textLower, "nodejs") ||
				strings.Contains(textLower, " node ") || strings.Contains(textLower, "node/express")
		case "typescript", "ts":
			matches = strings.Contains(textLower, "typescript") || strings.Contains(textLower, " ts ") ||
				strings.Contains(textLower, "ts/") || strings.Contains(textLower, "/ts")
		case "nestjs", "nest":
			matches = strings.Contains(textLower, "nestjs") || strings.Contains(textLower, "nest.js") ||
				strings.Contains(textLower, " nest ")
		case "express", "expressjs":
			matches = strings.Contains(textLower, "express") || strings.Contains(textLower, "expressjs") ||
				strings.Contains(textLower, "express.js")
		case "python":
			matches = strings.Contains(textLower, "python") || strings.Contains(textLower, "py/")
		case "backend":
			matches = strings.Contains(textLower, "backend") || strings.Contains(textLower, "back-end")
		case "software engineer":
			matches = strings.Contains(textLower, "software engineer") || strings.Contains(textLower, "software developer") ||
				strings.Contains(textLower, "swe")
		case "system design":
			matches = strings.Contains(textLower, "system design") || strings.Contains(textLower, "systems design") ||
				strings.Contains(textLower, "architecture") || strings.Contains(textLower, "architect")
		case "distributed systems":
			matches = strings.Contains(textLower, "distributed system") || strings.Contains(textLower, "distributed systems") ||
				strings.Contains(textLower, "distributed")
		case "microservices":
			matches = strings.Contains(textLower, "microservice") || strings.Contains(textLower, "microservices")
		case "k8s", "kubernetes":
			matches = strings.Contains(textLower, "kubernetes") || strings.Contains(textLower, "k8s")
		case "docker":
			matches = strings.Contains(textLower, "docker") || strings.Contains(textLower, "container")
		case "postgres", "postgresql":
			matches = strings.Contains(textLower, "postgres") || strings.Contains(textLower, "postgresql") ||
				strings.Contains(textLower, "sql")
		case "redis":
			matches = strings.Contains(textLower, "redis")
		case "mongodb", "mongo":
			matches = strings.Contains(textLower, "mongodb") || strings.Contains(textLower, "mongo")
		case "rabbitmq", "rabbit":
			matches = strings.Contains(textLower, "rabbitmq") || strings.Contains(textLower, "rabbit") ||
				strings.Contains(textLower, "queue") || strings.Contains(textLower, "bullmq")
		case "grpc":
			matches = strings.Contains(textLower, "grpc")
		case "rest", "rest apis":
			matches = strings.Contains(textLower, "rest") || strings.Contains(textLower, "restful") ||
				strings.Contains(textLower, "rest api") || strings.Contains(textLower, "api")
		case "linux":
			matches = strings.Contains(textLower, "linux") || strings.Contains(textLower, "ubuntu")
		case "ci/cd":
			matches = strings.Contains(textLower, "ci/cd") || strings.Contains(textLower, "ci-cd") ||
				strings.Contains(textLower, "github actions") || strings.Contains(textLower, "gitlab ci")
		default:
			matches = strings.Contains(textLower, sLower)
		}

		if matches && !seen[sLower] {
			seen[sLower] = true
			matched = append(matched, skill)
		}
	}

	if len(matched) == 0 {
		return 0, nil
	}

	// Calculate realistic synergy score based on number of matched core technologies
	var pct int
	switch len(matched) {
	case 1:
		pct = 45
	case 2:
		pct = 65
	case 3:
		pct = 80
	case 4:
		pct = 90
	default:
		pct = 95 + (len(matched)-5)*1
		if pct > 100 {
			pct = 100
		}
	}

	return pct, matched
}
