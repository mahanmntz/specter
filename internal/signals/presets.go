package signals

import "strings"

// Preset is a ready-made search profile for a common engineering role.
type Preset struct {
	ID      string  `json:"id"`
	Label   string  `json:"label"`
	Emoji   string  `json:"emoji"`
	Blurb   string  `json:"blurb"`
	Profile Profile `json:"profile"`
}

// nonEngineeringTerms rule a posting out unless its title clearly says engineering.
var nonEngineeringTerms = []string{
	"pure frontend", "ui/ux", "sales", "marketing", "recruiter", "talent",
	"account executive", "finance", "legal", "compliance", "product manager",
	"designer", "hr business partner", "customer success", "operations manager",
	"deal desk", "administrative", "copywriter", "office manager",
}

func preset(id, label, emoji, blurb string, skills, keywords, include []string) Preset {
	return Preset{ID: id, Label: label, Emoji: emoji, Blurb: blurb, Profile: Profile{
		Skills: skills, Keywords: keywords, RoleInclude: include, RoleExclude: nonEngineeringTerms,
	}}
}

var presets = []Preset{
	{ID: "backend", Label: "Backend", Emoji: "⚙️", Blurb: "APIs, services, distributed systems", Profile: Profile{
		Skills: []string{"Go", "Python", "PostgreSQL", "Redis", "Kubernetes", "Distributed Systems", "gRPC", "Docker"},
		Keywords: []string{
			"golang", "go", "distributed systems", "distributed", "redis", "fintech",
			"high throughput", "low latency", "kafka", "grpc", "microservices",
			"postgres", "postgresql", "concurrency", "kubernetes", "scalability",
			"event-driven", "consensus", "raft", "paxos", "clickhouse", "storage",
			"database", "indexing", "key-value", "nosql", "sql", "rust", "etcd",
			"cockroachdb", "cassandra", "dynamodb", "spanner", "temporal", "rocksb",
			"pebble", "badger", "boltdb", "multithreading", "linux", "cloud native",
			"nodejs", "node.js", "node", "typescript", "nestjs", "nest.js", "express", "expressjs",
			"python", "fastapi", "django", "rabbitmq", "bullmq", "mongodb", "prisma", "typeorm",
			"system design", "rest", "restful", "rest api", "full stack", "fullstack",
		},
		RoleInclude: includeRoleTerms, RoleExclude: nonEngineeringTerms,
	}},
	preset("fullstack", "Full-stack", "🧩", "Product engineering across the stack",
		[]string{"TypeScript", "React", "Node.js", "PostgreSQL", "GraphQL", "REST", "Docker"},
		[]string{"typescript", "javascript", "react", "node.js", "nodejs", "next.js", "nextjs", "nestjs", "express", "graphql",
			"postgres", "postgresql", "python", "django", "rails", "ruby", "rest", "redis", "docker", "aws", "full stack", "fullstack"},
		[]string{"full stack", "fullstack", "full-stack", "software engineer", "software developer", "web developer", "product engineer", "frontend", "backend"}),
	preset("frontend", "Frontend", "🎨", "UI, web apps, design systems",
		[]string{"React", "TypeScript", "JavaScript", "CSS", "Next.js", "GraphQL", "Accessibility"},
		[]string{"react", "vue", "angular", "svelte", "typescript", "javascript", "css", "html", "next.js", "nextjs", "tailwind",
			"webpack", "vite", "redux", "graphql", "accessibility", "web components", "frontend", "front-end"},
		[]string{"frontend", "front-end", "front end", "ui engineer", "web developer", "web engineer", "design engineer", "product engineer", "full stack", "fullstack", "software engineer"}),
	preset("devops", "DevOps / SRE", "🛠️", "Infrastructure, reliability, platforms",
		[]string{"Kubernetes", "Terraform", "AWS", "Docker", "CI/CD", "Linux", "Prometheus"},
		[]string{"kubernetes", "terraform", "aws", "gcp", "azure", "docker", "ci/cd", "prometheus", "grafana", "ansible", "helm",
			"linux", "observability", "sre", "devops", "infrastructure as code", "argocd", "istio"},
		[]string{"devops", "sre", "site reliability", "platform", "infrastructure", "cloud engineer", "systems engineer", "reliability", "production engineer", "release engineer"}),
	preset("data", "Data Engineering", "📊", "Pipelines, warehouses, analytics",
		[]string{"Python", "SQL", "Spark", "Airflow", "dbt", "Kafka", "Snowflake"},
		[]string{"spark", "airflow", "dbt", "kafka", "snowflake", "bigquery", "sql", "python", "etl", "data pipeline", "redshift",
			"flink", "databricks", "clickhouse", "data warehouse"},
		[]string{"data engineer", "data platform", "analytics engineer", "data infrastructure", "big data", "data engineering"}),
	preset("ml", "ML / AI", "🧠", "Models, LLMs, ML infrastructure",
		[]string{"Python", "PyTorch", "Machine Learning", "LLMs", "MLOps"},
		[]string{"pytorch", "tensorflow", "machine learning", "deep learning", "llm", "nlp", "computer vision", "mlops",
			"transformers", "python", "cuda", "jax", "scikit-learn", "hugging face", "rag"},
		[]string{"machine learning", "ml engineer", "ai engineer", "applied scientist", "research engineer", "mlops", "data scientist", "deep learning", "nlp", "computer vision", "llm"}),
	preset("mobile", "Mobile", "📱", "iOS, Android, cross-platform",
		[]string{"Swift", "Kotlin", "iOS", "Android", "React Native", "Flutter"},
		[]string{"swift", "swiftui", "kotlin", "jetpack compose", "react native", "flutter", "ios", "android", "objective-c", "xcode", "mobile"},
		[]string{"ios engineer", "ios developer", "android engineer", "android developer", "mobile engineer", "mobile developer", "react native", "flutter", "swift", "kotlin", "mobile"}),
}

// Presets returns every built-in role preset (a defensive copy).
func Presets() []Preset {
	out := make([]Preset, len(presets))
	copy(out, presets)
	return out
}

// PresetByID looks up a preset by its ID.
func PresetByID(id string) (Preset, bool) {
	for _, p := range presets {
		if p.ID == strings.ToLower(id) {
			return p, true
		}
	}
	return Preset{}, false
}

// ProfileFor builds a Profile from a preset, replacing its skills with the user's own
// (when given) and keeping those skills as keywords so postings that mention them survive filtering.
func ProfileFor(presetID string, skills []string) (Profile, bool) {
	p, ok := PresetByID(presetID)
	if !ok {
		return Profile{}, false
	}
	prof := p.Profile
	if clean := cleanList(skills); len(clean) > 0 {
		prof.Skills = clean
		kws := append([]string(nil), prof.Keywords...)
		have := make(map[string]bool, len(kws))
		for _, k := range kws {
			have[strings.ToLower(k)] = true
		}
		for _, s := range clean {
			if l := strings.ToLower(s); !have[l] {
				kws = append(kws, l)
				have[l] = true
			}
		}
		prof.Keywords = kws
	}
	return prof, true
}
