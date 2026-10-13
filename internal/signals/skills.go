package signals

import (
	"math"
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
		sk("Haskell"), sk("Lua"), sk("R"), sk("Julia"), sk("Perl"), sk("Clojure"), sk("Elm"), sk("Groovy"), sk("Objective-C", "objc"),
		sk("Zig"), sk("OCaml"), sk("F#", "fsharp"), sk("Erlang"),
	}},
	{"Backend & APIs", []skillDef{
		sk("Node.js", "nodejs", "node"), sk("NestJS", "nest.js", "nest"), sk("Express", "expressjs", "express.js"),
		sk("FastAPI"), sk("Django"), sk("Flask"), sk("Spring", "spring boot", "springboot"), sk("Rails", "ruby on rails", "ror"),
		sk("Laravel"), sk("gRPC"), sk("GraphQL"), sk("REST", "restful", "rest api"), sk("Microservices", "microservice"),
		sk("Distributed Systems", "distributed"), sk("System Design", "architecture"), sk("WebSockets", "websocket"),
		sk("ASP.NET", "asp.net core", "aspnet", ".net core"), sk("Fastify"), sk("Koa"), sk("tRPC"), sk("Phoenix"), sk("Axum"), sk("Actix"),
		sk("Tokio"), sk("OpenAPI", "swagger"), sk("Protobuf", "protocol buffers"), sk("Serverless", "lambda", "aws lambda"),
		sk("Celery"), sk("Sidekiq"), sk("WebRTC"), sk("Hasura"), sk("API Design", "api development"), sk("Caching"),
		sk("Message Queues", "queues"), sk("Concurrency", "multithreading"), sk("Performance Optimization", "performance tuning", "profiling"),
	}},
	{"Frontend", []skillDef{
		sk("React", "reactjs", "react.js"), sk("Next.js", "nextjs", "next"), sk("Vue", "vue.js", "vuejs"), sk("Nuxt", "nuxtjs"),
		sk("Angular", "angularjs"), sk("Svelte", "sveltekit"), sk("HTML", "html5"), sk("CSS", "css3", "scss", "sass"),
		sk("Tailwind", "tailwindcss", "tailwind css"), sk("Redux"), sk("Webpack"), sk("Vite"), sk("Accessibility", "a11y"),
		sk("Remix"), sk("Astro"), sk("Gatsby"), sk("Storybook"), sk("Three.js", "threejs"), sk("D3", "d3.js"), sk("MUI", "material ui"),
		sk("Bootstrap"), sk("jQuery"), sk("Web Components", "custom elements"), sk("PWA", "progressive web apps"), sk("WebAssembly", "wasm"),
		sk("Electron"), sk("Zustand"), sk("React Query", "tanstack query"), sk("Design Systems", "design system"), sk("Responsive Design"),
	}},
	{"Mobile", []skillDef{
		sk("iOS"), sk("Android"), sk("React Native", "react-native"), sk("Flutter"), sk("SwiftUI"), sk("Jetpack Compose"),
		sk("Kotlin Multiplatform", "kmp"), sk("Ionic"), sk("Capacitor"), sk("Xamarin"), sk(".NET MAUI", "maui"), sk("UIKit"),
		sk("Core Data"), sk("Firebase"), sk("Push Notifications"), sk("App Store Deployment"),
	}},
	{"Databases", []skillDef{
		sk("PostgreSQL", "postgres", "plpgsql"), sk("MySQL", "mariadb"), sk("MongoDB", "mongo"), sk("Redis"), sk("SQLite"),
		sk("Elasticsearch", "opensearch"), sk("Cassandra"), sk("DynamoDB"), sk("ClickHouse"), sk("SQL", "tsql"),
		sk("Oracle", "oracle db"), sk("SQL Server", "mssql"), sk("Neo4j"), sk("CouchDB"), sk("InfluxDB"), sk("TimescaleDB"),
		sk("Memcached"), sk("Supabase"), sk("Prisma"), sk("SQLAlchemy"), sk("Hibernate"), sk("GORM"), sk("TypeORM"), sk("Sequelize"),
		sk("Vector Databases", "pgvector", "pinecone", "weaviate", "qdrant"), sk("Database Design", "data modeling"), sk("Query Optimization"),
	}},
	{"Messaging", []skillDef{
		sk("Kafka", "apache kafka"), sk("RabbitMQ", "rabbit"), sk("NATS"), sk("Event-driven", "event driven", "event sourcing"),
		sk("Pulsar", "apache pulsar"), sk("SQS", "aws sqs"), sk("SNS"), sk("Kinesis"), sk("ActiveMQ"), sk("ZeroMQ", "zmq"), sk("MQTT"),
	}},
	{"Cloud & DevOps", []skillDef{
		sk("Docker", "dockerfile", "containers"), sk("Kubernetes", "k8s"), sk("Terraform", "hcl", "opentofu"), sk("AWS", "amazon web services"),
		sk("GCP", "google cloud"), sk("Azure"), sk("CI/CD", "ci-cd", "cicd"), sk("GitHub Actions", "github-actions"),
		sk("Linux", "ubuntu"), sk("Ansible"), sk("Helm"), sk("Prometheus"), sk("Grafana"), sk("Nginx"), sk("Observability", "opentelemetry"),
		sk("Pulumi"), sk("CloudFormation"), sk("ArgoCD", "argo cd"), sk("Jenkins"), sk("GitLab CI", "gitlab-ci"), sk("CircleCI"),
		sk("Istio", "service mesh"), sk("Envoy"), sk("Vault", "hashicorp vault"), sk("Consul"), sk("Nomad"), sk("Podman"),
		sk("Cloudflare"), sk("Datadog"), sk("ELK", "elk stack", "logstash", "kibana"), sk("Sentry"), sk("New Relic"), sk("PagerDuty"),
		sk("GitOps"), sk("Infrastructure as Code", "iac"), sk("Networking", "tcp/ip"), sk("Load Balancing", "load balancer"), sk("DNS"),
		sk("Incident Response"), sk("SRE Practices", "slo", "error budgets"), sk("Cost Optimization", "finops"), sk("eBPF"),
	}},
	{"Data & ML", []skillDef{
		sk("Spark", "apache spark", "pyspark"), sk("Airflow", "apache airflow"), sk("dbt"), sk("Snowflake"), sk("BigQuery"),
		sk("Pandas"), sk("PyTorch", "torch"), sk("TensorFlow", "keras"), sk("Machine Learning", "ml", "machine-learning"),
		sk("LLMs", "llm", "large language models", "genai", "openai"), sk("NLP", "natural language processing"),
		sk("MLOps"), sk("ETL", "data pipelines", "data pipeline"),
		sk("NumPy"), sk("scikit-learn", "sklearn"), sk("Hugging Face", "huggingface", "transformers"), sk("MLflow"), sk("Kubeflow"),
		sk("LangChain"), sk("RAG", "retrieval augmented generation"), sk("Computer Vision", "opencv"), sk("Reinforcement Learning"),
		sk("CUDA"), sk("ONNX"), sk("Jupyter", "jupyter notebook"), sk("Hadoop"), sk("Flink", "apache flink"), sk("Dagster"), sk("Prefect"),
		sk("Databricks"), sk("Delta Lake"), sk("Trino", "presto"), sk("Looker"), sk("Tableau"), sk("Power BI", "powerbi"),
		sk("Data Warehousing", "data warehouse"), sk("Feature Engineering"), sk("A/B Testing", "experimentation"), sk("Statistics"),
	}},
	{"Testing & QA", []skillDef{
		sk("Test Automation", "sdet", "qa automation", "e2e testing"), sk("Selenium"), sk("Cypress"), sk("Playwright"),
		sk("Jest"), sk("Pytest"), sk("JUnit"),
		sk("Mocha"), sk("Vitest"), sk("Testing Library", "react testing library"), sk("Appium"), sk("Postman"), sk("k6"), sk("JMeter"),
		sk("Load Testing", "performance testing"), sk("RSpec"), sk("Cucumber", "bdd"), sk("TestNG"), sk("Mocking", "mock"),
	}},
	{"Security", []skillDef{
		sk("Penetration Testing", "pentest", "pentesting", "ethical hacking"), sk("OWASP"), sk("Cryptography"),
		sk("Reverse Engineering"), sk("Threat Modeling"),
		sk("OAuth", "oauth2", "oidc"), sk("JWT"), sk("SSO", "single sign-on", "saml"), sk("Zero Trust"), sk("Burp Suite", "burp"),
		sk("Metasploit"), sk("Wireshark"), sk("SAST"), sk("DAST"), sk("SIEM", "splunk"), sk("IAM", "identity and access management"),
		sk("Vulnerability Management", "vulnerability scanning"), sk("SOC 2", "soc2", "compliance"), sk("Fuzzing", "fuzz testing"),
		sk("Malware Analysis"), sk("Cloud Security"), sk("Secure Coding"),
	}},
	{"Systems & Embedded", []skillDef{
		sk("Embedded", "embedded systems"), sk("Firmware"), sk("RTOS", "freertos"), sk("Assembly", "asm"),
		sk("FPGA", "verilog", "vhdl"), sk("Linux Kernel", "kernel"),
		sk("Zephyr", "zephyr rtos"), sk("ARM", "arm cortex"), sk("Yocto", "buildroot"), sk("Device Drivers", "drivers"),
		sk("Bare Metal"), sk("Microcontrollers", "stm32", "arduino", "esp32"), sk("CAN Bus", "can"), sk("I2C"), sk("SPI"),
		sk("LLVM"), sk("Compilers", "compiler"), sk("Real-time Systems", "real-time"), sk("Memory Management"), sk("Systems Programming"),
		sk("Robotics", "ros"),
	}},
	{"Web3", []skillDef{
		sk("Solidity"), sk("Ethereum", "evm"), sk("Smart Contracts", "smart contract"), sk("Web3", "web3.js", "ethers"),
		sk("Hardhat"), sk("Foundry"), sk("Solana"), sk("Zero-Knowledge", "zk", "zero knowledge", "zk-snarks"), sk("DeFi"),
		sk("NFT"), sk("Cosmos SDK", "cosmos"), sk("Consensus Protocols", "consensus"), sk("Smart Contract Auditing", "auditing"),
	}},
	{"Games", []skillDef{
		sk("Unity", "unity3d"), sk("Unreal Engine", "unreal"), sk("Godot"), sk("OpenGL", "vulkan"), sk("Game Development", "gamedev", "game dev"),
		sk("Shaders", "glsl", "hlsl"), sk("ECS", "entity component system"), sk("Game Physics", "physics engine"),
		sk("Multiplayer Networking", "netcode"), sk("Rendering", "graphics programming"), sk("Gameplay Programming", "gameplay"),
	}},
	{"Practices", []skillDef{
		sk("Testing", "tdd", "unit testing"), sk("Security", "appsec"), sk("Git"), sk("Agile", "scrum", "kanban"), sk("Code Review"),
		sk("Domain-Driven Design", "ddd"), sk("Clean Architecture"), sk("Design Patterns"), sk("Technical Leadership", "tech lead"),
		sk("Mentoring"), sk("Open Source", "oss"), sk("Technical Writing", "documentation"), sk("Debugging"),
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

// ambiguousSkills are catalog terms that are also ordinary English words. In free text they only
// count when written capitalised ("Go", "Rust", "Swift"), never in lower case ("let's go").
var ambiguousSkills = map[string]bool{
	"go": true, "rust": true, "swift": true, "spring": true, "rails": true, "express": true, "flask": true,
	"nest": true, "next": true, "unity": true, "unreal": true, "git": true, "kernel": true, "distributed": true,
	"architecture": true, "embedded": true, "security": true, "testing": true, "rest": true, "react": true,
}

// neverInFreeText are abbreviations too short or common to trust outside structured fields.
var neverInFreeText = map[string]bool{"c": true, "sh": true, "ts": true, "js": true, "py": true, "ml": true, "node": true}

// skillMentions counts how many times each catalog skill appears in free text (a GitHub bio, README,
// CV or repository description). Matching is word-boundary aware and conservative about ambiguous words.
func skillMentions(text string) map[string]int {
	skillIndexOnce.Do(buildSkillIndex)
	counts := map[string]int{}
	for term, canon := range skillIndex {
		if len(term) < 2 || neverInFreeText[term] {
			continue
		}
		n := 0
		if ambiguousSkills[term] {
			n = countCapitalised(text, term)
		} else {
			n = countTerm(text, term)
		}
		if n > counts[canon] {
			counts[canon] = n // aliases of one skill are alternative spellings, not extra mentions
		}
	}
	for k, v := range counts {
		if v == 0 {
			delete(counts, k)
		}
	}
	return counts
}

// FindSkills returns the canonical catalog skills mentioned in free text.
func FindSkills(text string) []string {
	var found []string
	for skill := range skillMentions(text) {
		found = append(found, skill)
	}
	return found
}

// CountSkills returns how many times each catalog skill is mentioned in free text.
func CountSkills(text string) map[string]int { return skillMentions(text) }

var capRegexes sync.Map // term -> *regexp.Regexp

func capRegex(term string) *regexp.Regexp {
	v, ok := capRegexes.Load(term)
	if !ok {
		cap := strings.ToUpper(term[:1]) + term[1:]
		re := regexp.MustCompile(`(^|[^A-Za-z0-9])` + regexp.QuoteMeta(cap) + `($|[^A-Za-z0-9])`)
		v, _ = capRegexes.LoadOrStore(term, re)
	}
	return v.(*regexp.Regexp)
}

// containsCapitalised matches term only when its first letter is upper case in the text.
func containsCapitalised(text, term string) bool { return capRegex(term).MatchString(text) }

func countCapitalised(text, term string) int { return len(capRegex(term).FindAllStringIndex(text, -1)) }

var termRegexes sync.Map // term -> *regexp.Regexp

// containsTerm reports whether text contains term delimited by non-alphanumeric characters,
// so "java" does not match "javascript" and "c" does not match "cache".
func containsTerm(text, term string) bool { return termRegex(term).MatchString(text) }

func countTerm(text, term string) int { return len(termRegex(term).FindAllStringIndex(text, -1)) }

func termRegex(term string) *regexp.Regexp {
	term = strings.ToLower(term)
	v, ok := termRegexes.Load(term)
	if !ok {
		re := regexp.MustCompile(`(?i)(^|[^a-z0-9])` + regexp.QuoteMeta(term) + `($|[^a-z0-9])`)
		v, _ = termRegexes.LoadOrStore(term, re)
	}
	return v.(*regexp.Regexp)
}

// roleSignatures maps each preset to the catalog skills that characterize it.
var roleSignatures = map[string][]string{
	"backend": {"Go", "Python", "Java", "Rust", "Kotlin", "C#", "Ruby", "PHP", "Scala", "Elixir", "Node.js", "NestJS", "Express",
		"FastAPI", "Django", "Flask", "Spring", "Rails", "Laravel", "gRPC", "Microservices", "Distributed Systems", "PostgreSQL",
		"MySQL", "MongoDB", "Redis", "Kafka", "RabbitMQ", "GraphQL", "REST", "Event-driven", "System Design", "ASP.NET", "Fastify",
		"tRPC", "Phoenix", "Axum", "Actix", "Tokio", "OpenAPI", "Protobuf", "Celery", "Sidekiq", "API Design", "Caching",
		"Message Queues", "Concurrency", "Database Design", "Query Optimization", "SQLAlchemy", "Hibernate", "GORM", "Prisma"},
	"fullstack": {"TypeScript", "JavaScript", "React", "Next.js", "Node.js", "GraphQL", "PostgreSQL", "Vue", "Svelte", "Tailwind",
		"Django", "Rails", "Laravel", "Remix", "Prisma", "tRPC", "Supabase", "Firebase"},
	"frontend": {"TypeScript", "JavaScript", "React", "Vue", "Angular", "Svelte", "Next.js", "Nuxt", "HTML", "CSS", "Tailwind", "Redux",
		"Webpack", "Vite", "Accessibility", "Remix", "Astro", "Gatsby", "Storybook", "Three.js", "D3", "MUI", "Bootstrap", "jQuery",
		"Web Components", "PWA", "WebAssembly", "Zustand", "React Query", "Design Systems", "Responsive Design"},
	"devops": {"Terraform", "Kubernetes", "Docker", "Helm", "Ansible", "AWS", "GCP", "Azure", "CI/CD", "GitHub Actions", "Prometheus",
		"Grafana", "Linux", "Shell", "Nginx", "Observability", "Pulumi", "CloudFormation", "ArgoCD", "Jenkins", "GitLab CI", "CircleCI",
		"Istio", "Envoy", "Vault", "Consul", "Nomad", "Podman", "Cloudflare", "Datadog", "ELK", "PagerDuty", "GitOps",
		"Infrastructure as Code", "Networking", "Load Balancing", "Incident Response", "SRE Practices", "eBPF"},
	"data": {"Spark", "Airflow", "dbt", "Snowflake", "BigQuery", "Pandas", "SQL", "Kafka", "ETL", "ClickHouse", "Hadoop", "Flink", "Dagster",
		"Prefect", "Databricks", "Delta Lake", "Trino", "Looker", "Tableau", "Power BI", "Data Warehousing", "Statistics"},
	"ml": {"PyTorch", "TensorFlow", "Machine Learning", "LLMs", "NLP", "MLOps", "Pandas", "NumPy", "scikit-learn", "Hugging Face", "MLflow",
		"Kubeflow", "LangChain", "RAG", "Computer Vision", "Reinforcement Learning", "CUDA", "ONNX", "Jupyter", "Feature Engineering",
		"A/B Testing", "Statistics", "Vector Databases"},
	"mobile": {"Swift", "Kotlin", "Dart", "iOS", "Android", "React Native", "Flutter", "SwiftUI", "Jetpack Compose", "Kotlin Multiplatform",
		"Ionic", "Capacitor", "Xamarin", ".NET MAUI", "UIKit", "Core Data", "Firebase", "Push Notifications", "App Store Deployment", "Objective-C"},
	"security": {"Security", "Penetration Testing", "OWASP", "Cryptography", "Reverse Engineering", "Threat Modeling", "OAuth", "JWT", "SSO",
		"Zero Trust", "Burp Suite", "Metasploit", "Wireshark", "SAST", "DAST", "SIEM", "IAM", "Vulnerability Management", "SOC 2",
		"Fuzzing", "Malware Analysis", "Cloud Security", "Secure Coding"},
	"qa": {"Testing", "Test Automation", "Selenium", "Cypress", "Playwright", "Jest", "Pytest", "JUnit", "Mocha", "Vitest", "Testing Library",
		"Appium", "Postman", "k6", "JMeter", "Load Testing", "RSpec", "Cucumber", "TestNG", "Mocking"},
	"embedded": {"Embedded", "Firmware", "RTOS", "C", "C++", "Rust", "Assembly", "Linux Kernel", "FPGA", "Zephyr", "ARM", "Yocto",
		"Device Drivers", "Bare Metal", "Microcontrollers", "CAN Bus", "I2C", "SPI", "LLVM", "Compilers", "Real-time Systems",
		"Memory Management", "Systems Programming", "Robotics"},
	"blockchain": {"Solidity", "Ethereum", "Smart Contracts", "Web3", "Hardhat", "Foundry", "Solana", "Zero-Knowledge", "DeFi", "NFT",
		"Cosmos SDK", "Consensus Protocols", "Smart Contract Auditing", "Cryptography"},
	"game": {"Unity", "Unreal Engine", "Godot", "OpenGL", "Game Development", "C#", "C++", "Shaders", "ECS", "Game Physics",
		"Multiplayer Networking", "Rendering", "Gameplay Programming"},
	// "software-engineer" is the generalist role: it has no signature of its own and is chosen
	// when someone's work is spread across several of the roles above.
	"software-engineer": nil,
}

// RoleScores scores every preset against weighted skills (skill name -> weight).
// Weights are square-rooted so breadth across a role's skills beats sheer volume in one language.
func RoleScores(weights map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(roleSignatures))
	for role, sigs := range roleSignatures {
		total := 0.0
		for _, s := range sigs {
			total += math.Sqrt(weights[s])
		}
		out[role] = total
	}
	return out
}
