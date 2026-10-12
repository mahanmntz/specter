package signals

import (
	"regexp"
	"strings"
	"sync"
)

// SkillGroup is a labelled set of selectable skills for the UI.
type SkillGroup struct {
	Category string   `json:"category"`
	Skills   []string `json:"skills"`
}

type skillDef struct {
	name    string
	aliases []string
}

type skillCategory struct {
	name   string
	skills []skillDef
}

func sk(name string, aliases ...string) skillDef { return skillDef{name, aliases} }

var skillCatalog = []skillCategory{
	{"Languages", []skillDef{
		sk("Go", "golang"), sk("Python", "py"), sk("TypeScript", "ts"), sk("JavaScript", "js", "ecmascript"),
		sk("Java"), sk("Kotlin"), sk("Rust"), sk("C++", "cpp"), sk("C#", "csharp", ".net", "dotnet"), sk("C"),
		sk("Ruby"), sk("PHP"), sk("Scala"), sk("Elixir"), sk("Swift"), sk("Dart"), sk("Shell", "bash", "zsh", "sh"),
	}},
	{"Backend & APIs", []skillDef{
		sk("Node.js", "nodejs", "node"), sk("NestJS", "nest.js", "nest"), sk("Express", "expressjs", "express.js"),
		sk("FastAPI"), sk("Django"), sk("Flask"), sk("Spring", "spring boot", "springboot"), sk("Rails", "ruby on rails", "ror"),
		sk("Laravel"), sk("gRPC"), sk("GraphQL"), sk("REST", "restful", "rest api"), sk("Microservices", "microservice"),
		sk("Distributed Systems", "distributed"), sk("System Design", "architecture"), sk("WebSockets", "websocket"),
	}},
	{"Frontend", []skillDef{
		sk("React", "reactjs", "react.js"), sk("Next.js", "nextjs", "next"), sk("Vue", "vue.js", "vuejs"), sk("Nuxt", "nuxtjs"),
		sk("Angular", "angularjs"), sk("Svelte", "sveltekit"), sk("HTML", "html5"), sk("CSS", "css3", "scss", "sass"),
		sk("Tailwind", "tailwindcss", "tailwind css"), sk("Redux"), sk("Webpack"), sk("Vite"), sk("Accessibility", "a11y"),
	}},
	{"Mobile", []skillDef{
		sk("iOS"), sk("Android"), sk("React Native", "react-native"), sk("Flutter"), sk("SwiftUI"), sk("Jetpack Compose"),
	}},
	{"Databases", []skillDef{
		sk("PostgreSQL", "postgres", "plpgsql"), sk("MySQL", "mariadb"), sk("MongoDB", "mongo"), sk("Redis"), sk("SQLite"),
		sk("Elasticsearch", "opensearch"), sk("Cassandra"), sk("DynamoDB"), sk("ClickHouse"), sk("SQL", "tsql"),
	}},
	{"Messaging", []skillDef{
		sk("Kafka", "apache kafka"), sk("RabbitMQ", "rabbit"), sk("NATS"), sk("Event-driven", "event driven", "event sourcing"),
	}},
	{"Cloud & DevOps", []skillDef{
		sk("Docker", "dockerfile", "containers"), sk("Kubernetes", "k8s"), sk("Terraform", "hcl", "opentofu"), sk("AWS", "amazon web services"),
		sk("GCP", "google cloud"), sk("Azure"), sk("CI/CD", "ci-cd", "cicd"), sk("GitHub Actions", "github-actions"),
		sk("Linux", "ubuntu"), sk("Ansible"), sk("Helm"), sk("Prometheus"), sk("Grafana"), sk("Nginx"), sk("Observability", "opentelemetry"),
	}},
	{"Data & ML", []skillDef{
		sk("Spark", "apache spark", "pyspark"), sk("Airflow", "apache airflow"), sk("dbt"), sk("Snowflake"), sk("BigQuery"),
		sk("Pandas"), sk("PyTorch", "torch"), sk("TensorFlow", "keras"), sk("Machine Learning", "ml", "machine-learning"),
		sk("LLMs", "llm", "large language models", "genai", "openai"), sk("NLP", "natural language processing"),
		sk("MLOps"), sk("ETL", "data pipelines", "data pipeline"),
	}},
	{"Practices", []skillDef{
		sk("Testing", "tdd", "unit testing"), sk("Security", "appsec"), sk("Git"),
	}},
}

var (
	skillIndexOnce sync.Once
	skillIndex     map[string]string // lowercased name or alias -> canonical name
)

func buildSkillIndex() {
	skillIndex = make(map[string]string)
	for _, c := range skillCatalog {
		for _, s := range c.skills {
			skillIndex[strings.ToLower(s.name)] = s.name
			for _, a := range s.aliases {
				skillIndex[strings.ToLower(a)] = s.name
			}
		}
	}
}

// SkillCatalog returns every selectable skill grouped by category.
func SkillCatalog() []SkillGroup {
	out := make([]SkillGroup, 0, len(skillCatalog))
	for _, c := range skillCatalog {
		g := SkillGroup{Category: c.name}
		for _, s := range c.skills {
			g.Skills = append(g.Skills, s.name)
		}
		out = append(out, g)
	}
	return out
}

// CanonicalSkill resolves a skill name or alias (case-insensitive) to its catalog name.
func CanonicalSkill(s string) (string, bool) {
	skillIndexOnce.Do(buildSkillIndex)
	name, ok := skillIndex[strings.ToLower(strings.TrimSpace(s))]
	return name, ok
}

// FindSkills returns the canonical catalog skills mentioned in free text (word-boundary aware),
// e.g. a GitHub bio or repository description.
func FindSkills(text string) []string {
	skillIndexOnce.Do(buildSkillIndex)
	var found []string
	seen := map[string]bool{}
	for term, canon := range skillIndex {
		if len(term) < 2 || seen[canon] {
			continue
		}
		if containsTerm(text, term) {
			seen[canon] = true
			found = append(found, canon)
		}
	}
	return found
}

var termRegexes sync.Map // term -> *regexp.Regexp

// containsTerm reports whether text contains term delimited by non-alphanumeric characters,
// so "java" does not match "javascript" and "c" does not match "cache".
func containsTerm(text, term string) bool {
	term = strings.ToLower(term)
	v, ok := termRegexes.Load(term)
	if !ok {
		re := regexp.MustCompile(`(?i)(^|[^a-z0-9])` + regexp.QuoteMeta(term) + `($|[^a-z0-9])`)
		v, _ = termRegexes.LoadOrStore(term, re)
	}
	return v.(*regexp.Regexp).MatchString(text)
}

// roleSignatures maps each preset to the catalog skills that characterize it.
var roleSignatures = map[string][]string{
	"backend": {"Go", "Python", "Java", "Rust", "Kotlin", "C#", "Ruby", "PHP", "Scala", "Elixir", "Node.js", "NestJS", "Express",
		"FastAPI", "Django", "Flask", "Spring", "Rails", "Laravel", "gRPC", "Microservices", "Distributed Systems", "PostgreSQL",
		"MySQL", "MongoDB", "Redis", "Kafka", "RabbitMQ", "GraphQL", "REST", "Event-driven", "System Design"},
	"fullstack": {"TypeScript", "JavaScript", "React", "Next.js", "Node.js", "GraphQL", "PostgreSQL", "Vue", "Svelte", "Tailwind", "Django", "Rails", "Laravel"},
	"frontend":  {"TypeScript", "JavaScript", "React", "Vue", "Angular", "Svelte", "Next.js", "Nuxt", "HTML", "CSS", "Tailwind", "Redux", "Webpack", "Vite", "Accessibility"},
	"devops": {"Terraform", "Kubernetes", "Docker", "Helm", "Ansible", "AWS", "GCP", "Azure", "CI/CD", "GitHub Actions", "Prometheus",
		"Grafana", "Linux", "Shell", "Nginx", "Observability"},
	"data":   {"Spark", "Airflow", "dbt", "Snowflake", "BigQuery", "Pandas", "SQL", "Kafka", "ETL", "ClickHouse"},
	"ml":     {"PyTorch", "TensorFlow", "Machine Learning", "LLMs", "NLP", "MLOps", "Pandas"},
	"mobile": {"Swift", "Kotlin", "Dart", "iOS", "Android", "React Native", "Flutter", "SwiftUI", "Jetpack Compose"},
}

// RoleScores scores every preset against weighted skills (skill name -> weight).
func RoleScores(weights map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(roleSignatures))
	for role, sigs := range roleSignatures {
		total := 0.0
		for _, s := range sigs {
			total += weights[s]
		}
		out[role] = total
	}
	return out
}
