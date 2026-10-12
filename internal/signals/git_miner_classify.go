package signals

import (
	"sort"
	"strings"
)

// ClassifyBackendArchetype categorizes prospects into diverse, realistic backend & software engineering archetypes.
func ClassifyBackendArchetype(commitMsgs []string, languages map[string]int, commitCount int, repoName string, signals []string) (string, int) {
	msgsCombined := strings.ToLower(strings.Join(commitMsgs, " "))
	combinedWithRepo := msgsCombined + " " + strings.ToLower(repoName)

	words := strings.FieldsFunc(combinedWithRepo, func(r rune) bool {
		return r == ' ' || r == '/' || r == '-' || r == '_' || r == ':' || r == ',' || r == '.' || r == '(' || r == ')' || r == '[' || r == ']' || r == '"' || r == '\''
	})
	hasWord := func(target string) bool {
		target = strings.ToLower(target)
		for _, w := range words {
			if w == target {
				return true
			}
		}
		return false
	}

	role := ""

	// 1. Engineering Leadership: CTO, VP, Director of Engineering, Engineering Manager, Tech Lead
	if hasWord("cto") || strings.Contains(combinedWithRepo, "vp of engineering") || strings.Contains(combinedWithRepo, "director of engineering") {
		role = "Engineering Leadership"
	} else if strings.Contains(combinedWithRepo, "engineering manager") || hasWord("em") {
		role = "Engineering Manager"
	} else if strings.Contains(combinedWithRepo, "tech lead") || strings.Contains(combinedWithRepo, "lead engineer") || strings.Contains(combinedWithRepo, "team lead") ||
		strings.Contains(msgsCombined, "roadmap") || hasWord("rfc") || strings.Contains(msgsCombined, "cut release") || strings.Contains(msgsCombined, "release candidate") {
		role = "Tech Lead"
	}

	// 2. Staff / Principal Engineers
	if role == "" {
		if hasWord("principal") || hasWord("staff") || hasWord("architect") || strings.Contains(msgsCombined, "architecture") ||
			strings.Contains(msgsCombined, "distributed systems") || strings.Contains(msgsCombined, "consensus") || strings.Contains(msgsCombined, "raft") ||
			strings.Contains(msgsCombined, "paxos") || commitCount >= 10 {
			if strings.Contains(msgsCombined, "distributed") || strings.Contains(msgsCombined, "raft") || strings.Contains(msgsCombined, "consensus") {
				role = "Staff Distributed Systems Engineer"
			} else if strings.Contains(msgsCombined, "architect") {
				role = "Principal Systems Architect"
			} else {
				role = "Staff Software Engineer"
			}
		}
	}

	// 3. Specialized & Domain Engineers (Full Stack, DevOps/Platform, Distributed, Storage)
	if role == "" {
		hasFrontend := hasWord("react") || hasWord("vue") || hasWord("frontend") || hasWord("ui") || hasWord("fullstack") ||
			(languages["TypeScript"] > 0 && (languages["Go"] > 0 || languages["Python"] > 0 || languages["Rust"] > 0))
		hasStorage := strings.Contains(msgsCombined, "storage") || strings.Contains(msgsCombined, "lsm") ||
			strings.Contains(msgsCombined, "wal") || strings.Contains(msgsCombined, "btree") || strings.Contains(msgsCombined, "pebble") || strings.Contains(msgsCombined, "compaction")
		hasInfra := hasWord("k8s") || hasWord("kubernetes") || hasWord("operator") || hasWord("helm") || hasWord("terraform") ||
			hasWord("docker") || strings.Contains(msgsCombined, "ci/cd") || strings.Contains(msgsCombined, "devops") || strings.Contains(msgsCombined, "crd")
		hasDistributed := hasWord("cluster") || hasWord("replication") || hasWord("sharding") || hasWord("partition") ||
			hasWord("consensus") || hasWord("paxos") || hasWord("raft") || hasWord("gossip")

		if hasFrontend {
			role = "Full Stack Engineer"
		} else if hasStorage {
			role = "Core Storage Engineer"
		} else if hasInfra {
			role = "Infrastructure Engineer"
		} else if hasDistributed {
			role = "Distributed Systems Engineer"
		}
	}

	// 4. Senior vs Mid Backend / Software Engineer Archetypes
	if role == "" {
		isSenior := hasWord("senior") || hasWord("sr") || commitCount >= 4 ||
			hasWord("grpc") || hasWord("concurrency") || hasWord("pipeline") || hasWord("database") || hasWord("postgres") || hasWord("kafka")

		if isSenior {
			if languages["Go"] > 0 {
				role = "Senior Go Engineer"
			} else if languages["Rust"] > 0 {
				role = "Senior Systems Engineer (Rust)"
			} else if languages["Python"] > 0 {
				role = "Senior Backend Engineer (Python)"
			} else {
				role = "Senior Backend Engineer"
			}
		} else {
			if languages["Go"] > 0 {
				role = "Backend Engineer (Go)"
			} else if languages["Rust"] > 0 {
				role = "Systems Engineer (Rust)"
			} else if languages["Python"] > 0 {
				role = "Backend Engineer (Python)"
			} else if languages["TypeScript"] > 0 || languages["JavaScript"] > 0 {
				role = "Software Engineer"
			} else {
				role = "Backend Engineer"
			}
		}
	}

	score := CalculateRelevanceScore(role, languages, false, commitCount)
	if len(signals) >= 3 {
		score += 10
	} else if len(signals) >= 1 {
		score += 5
	}
	if score > 100 {
		score = 100
	}

	return role, score
}

// ClassifyRole analyzes commit history and messages to categorize prospects into engineering tiers.
func ClassifyRole(commitMsgs []string, commitCount int) string {
	combined := strings.ToLower(strings.Join(commitMsgs, " "))

	techLeadKeywords := []string{
		"architecture", "arch:", "rfc", "roadmap", "release candidate", "cut release",
		"design doc", "merge pull request", "lead", "v1.0", "v2.0", "co-authored-by",
	}
	for _, kw := range techLeadKeywords {
		if strings.Contains(combined, kw) {
			return "Tech Lead"
		}
	}

	staffKeywords := []string{
		"consensus", "raft", "paxos", "distributed", "protocol", "engine architecture",
		"alloc", "gc", "runtime", "low latency", "optimization", "sharding", "replication",
	}
	for _, kw := range staffKeywords {
		if strings.Contains(combined, kw) {
			return "Staff/Principal"
		}
	}

	seniorKeywords := []string{
		"service", "grpc", "database", "migration", "postgres", "redis", "kafka",
		"concurrency", "mutex", "worker", "pool", "pipeline", "backend",
	}
	for _, kw := range seniorKeywords {
		if strings.Contains(combined, kw) {
			return "Senior Backend"
		}
	}

	if commitCount > 5 {
		return "Senior Backend"
	}

	return "Engineer"
}

// CalculateRelevanceScore evaluates a prospect's fit (0 - 100).
func CalculateRelevanceScore(role string, languages map[string]int, domainMatch bool, commitCount int) int {
	score := 40

	switch role {
	case "Tech Lead", "Engineering Leadership", "Engineering Manager", "VP of Engineering":
		score += 25
	case "Staff/Principal", "Staff / Principal", "Staff Software Engineer", "Staff Distributed Systems Engineer", "Principal Systems Architect":
		score += 25
	case "Senior Backend", "Senior Backend Engineer", "Senior Go Engineer", "Senior Systems Engineer (Rust)", "Senior Backend Engineer (Python)", "Senior Platform Engineer", "Core Storage Engineer", "Infrastructure Engineer", "Distributed Systems Engineer", "Database Systems Engineer":
		score += 15
	case "Backend Engineer", "Software Engineer", "Backend Engineer (Go)", "Backend Engineer (Python)", "Systems Engineer (Rust)", "Full Stack Engineer", "Platform / Cloud Engineer", "DevOps Engineer":
		score += 10
	case "Engineer", "Active Contributor", "Core Contributor", "Core Contributor (Go)", "Core Contributor (Rust)":
		score += 5
	}

	hasGo := languages["Go"] > 0
	hasRust := languages["Rust"] > 0
	hasCpp := languages["C++"] > 0
	hasPython := languages["Python"] > 0
	hasTs := languages["TypeScript"] > 0 || languages["JavaScript"] > 0

	if hasGo {
		score += 20
	} else if hasRust || hasCpp {
		score += 15
	} else if hasPython || hasTs {
		score += 10
	} else if len(languages) > 0 {
		score += 5
	}

	if domainMatch {
		score += 15
	}

	if commitCount > 3 {
		score += 5
	}

	if score > 100 {
		score = 100
	}
	return score
}

func formatTopLanguages(langs map[string]int) string {
	if len(langs) == 0 {
		return "Go"
	}
	type langCount struct {
		name  string
		count int
	}
	var list []langCount
	for k, v := range langs {
		list = append(list, langCount{k, v})
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].count > list[j].count
	})

	var names []string
	for i, item := range list {
		if i >= 3 {
			break
		}
		names = append(names, item.name)
	}
	return strings.Join(names, ", ")
}
