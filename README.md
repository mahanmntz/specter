# Specter ⚡

**Specter** is a high-performance, autonomous CLI reconnaissance and engineering lead discovery engine written in pure Go (1.23+). Designed for engineering leaders, technical recruiters, founders, and global remote software engineers, Specter discovers high-signal backend hiring indicators, maps public ATS boards (Greenhouse, Lever, Ashby, direct portals), detects **worldwide contractor-friendly (sanction-resilient) positions with deep one-click application links**, mines public Git commit metadata and patch headers for verified contact emails, enforces provenance lineage, and produces date-stamped execution archives with delta tracking.

---

## Architecture Diagram

```mermaid
graph TD
    subgraph Inputs ["CLI & Seed Catalog"]
        CLI["CLI Commands (scan / apply / report / leads)"]
        Seeds["Embedded Seed Catalog (75+ Infra / Distributed Systems / Crypto Targets)"]
    end

    subgraph Crawler_Core ["Crawler & Network Engine"]
        Pacer["Adaptive Pacer (1.2 rps GH / 2.0 rps ATS)"]
        Jitter["Jitter Engine (300ms - 1200ms)"]
        Breaker["Canary Circuit Breaker (429 / 403 Backoff)"]
        TokenPool["GitHub Token Pool (Round-Robin, Cooling, Quota Sync)"]
        Fetcher["Resilient HTTP Fetcher (SSRF Guard, Conn Pool)"]
        ETagCache["Conditional ETag / Last-Modified Cache (304 Zero-Body)"]
    end

    subgraph Ingestion ["Ingestion & Detection Engine"]
        Registry["ATS Registry"]
        GH_Adapter["Greenhouse API Adapter"]
        Lever_Adapter["Lever Postings Adapter"]
        Ashby_Adapter["Ashby Board Adapter"]
        Generic_Adapter["Generic DOM ATS Crawler"]
        Matcher["Backend Tech Matcher (Go, Redis, Distributed Systems)"]
        RemotePolicy["Remote Policy & Sanction-Resilient Classifier"]
        DirectApply["Direct Deep-Apply Form Generator (#app, /apply)"]
    end

    subgraph Recon_Mining ["Signals & Reconnaissance"]
        Miner["Git Miner & Commits Engine"]
        BotGate["Strict Bot & CI/CD Gatekeeper"]
        PatchParser["Raw Patch Header Extractor"]
        Archetype["Backend Archetype Classifier"]
        Enrichment["Profile & OSINT Enrichment (LinkedIn, Bio, Web)"]
    end

    subgraph Storage_Frontier ["Persistence & State"]
        Frontier["SQLite Crawl Frontier (TTL Deduplication)"]
        RunTracker["Crawl Runs & Delta Tracker (FirstSeen / LastSeen)"]
        DB[(Pure Go SQLite Store: specter.db)]
    end

    subgraph Outputs ["Reports & Deliverables"]
        TerminalUI["Real-time ANSI Terminal Dashboard"]
        DirectApplyMD["Direct-Apply Dashboard (direct_apply_*.md)"]
        DeltaMD["Recon Delta Summary (delta_summary_*.md)"]
        Dossier["Executive Markdown Dossier (Dossiers/*.md)"]
        Analytics["Source Analytics & Yield Report (sources_analytics_*.md)"]
        RunsManager["Run Directory Manager (reports/runs/YYYY-MM-DD_HH-MM-SS/)"]
        Export["JSON / CSV / Roles-CSV Exporter"]
    end

    CLI --> Seeds
    Seeds --> Registry
    CLI --> Registry

    Registry --> GH_Adapter & Lever_Adapter & Ashby_Adapter & Generic_Adapter
    GH_Adapter & Lever_Adapter & Ashby_Adapter & Generic_Adapter --> Fetcher
    Fetcher --> Pacer --> Jitter --> Breaker
    Fetcher --> TokenPool
    Fetcher --> ETagCache

    Fetcher --> Matcher --> RemotePolicy --> DirectApply --> DB

    DB --> Miner
    Miner --> Fetcher
    Miner --> BotGate
    BotGate --> PatchParser
    PatchParser --> Archetype
    Archetype --> Enrichment
    Enrichment --> DB

    Fetcher <--> Frontier
    Frontier <--> DB
    DB <--> RunTracker

    DB --> TerminalUI
    DB --> DirectApplyMD
    DB --> DeltaMD
    DB --> Dossier
    DB --> Analytics
    DB --> Export
    DirectApplyMD & DeltaMD & Dossier & Analytics --> RunsManager
```

---

## Pipeline Dataflow

```mermaid
sequenceDiagram
    autonumber
    actor User
    participant CLI as Specter CLI
    participant Frontier as Crawl Frontier (SQLite)
    participant Fetcher as Fetcher & Pacer
    participant ATS as ATS Adapters
    participant Policy as Remote Policy Classifier
    participant Miner as Git Miner
    participant Store as SQLite Store
    participant RunMgr as Run & Delta Manager

    User->>CLI: specter scan --all --concurrency 4
    CLI->>RunMgr: InitRunDir(timestamp) -> reports/runs/YYYY-MM-DD_HH-MM-SS/
    CLI->>Store: GetLastCrawlRun() -> prevRun baseline
    loop Every Target Seed (75+ Curated)
        CLI->>Frontier: Check ShouldCrawl(domain, TTL)
        alt Within TTL
            Frontier-->>CLI: Skip (Already Crawled)
        else Expired or Unvisited
            Frontier-->>CLI: Proceed
            CLI->>Fetcher: Fetch ATS Job Board (inject ETag if cached)
            Fetcher->>ATS: Ingest Open Positions
            ATS->>Policy: ClassifyRemotePolicy(location, description)
            Policy-->>ATS: TierGlobalRemote / DirectApplyURL (#app, /apply)
            ATS->>Store: SaveCompanyAndRoles (tracks first_seen_at & is_new)
            CLI->>Miner: Mine GitHub Org Repositories
            loop Commits & Patches
                Miner->>Fetcher: Query Commits (Paced 1.2 rps + Jitter)
                Fetcher-->>Miner: Commit List & Author Info
                Miner->>Miner: Check IsBotOrCI (Drop bots & CI)
                Miner->>Fetcher: Fetch .patch header (extract hidden emails)
                Miner->>Miner: Score Synergy (0-100) & Enrich OSINT
                Miner->>Store: SaveEngineeringLead with provenance
            end
        end
    end
    CLI->>Store: RecordCrawlRun(currentRun)
    CLI->>RunMgr: Generate direct_apply.md, delta_summary.md, dossiers
    CLI->>RunMgr: LinkLatestRun(runDir) -> reports/latest/
    RunMgr-->>User: 🎯 Direct Apply & Delta Reports Ready in reports/latest/
```

---

## Key Capabilities & Features

### 1. 🎯 Direct One-Click Apply Dashboard (`specter apply`)
- **Direct Form Deep-Linking:** Automatically extracts and rewrites job posting URLs into direct application form deep links:
  - **Greenhouse:** Appends `#app` anchor to bypass marketing blurbs and jump straight to the submission form.
  - **Lever:** Points to `/apply` endpoints directly.
  - **Ashby:** Deep-links into `/application` forms.
- **Fast-Track Pitch Generator:** Generates a custom 3-sentence application note emphasizing asynchronous distributed systems experience and pre-configured B2B contractor invoicing (Deel/crypto) to eliminate corporate hesitation.

### 2. 🌍 Global-Remote & Third-World / Sanction-Friendly Classifier
Engineers located in non-US/EU emerging tech markets (MENA, LATAM, Eastern Europe, South Asia) frequently encounter compliance walls (W-2 requirements, US citizenship, strict export controls). Specter classifies roles into 3 distinct tiers:
- 🟢 **Worldwide / Contractor-Friendly (`TierGlobalRemote`):** Explicitly welcomes global candidates, uses contractor/B2B invoicing via **Deel / Remote.com / crypto (USDC)**, has zero domestic tax residency barriers.
- 🟡 **Timezone-Flexible (`TierTimezoneFlexible`):** Remote positions requiring timezone overlap (e.g. EMEA / UTC ± 3 hours).
- 🔴 **Geo-Restricted (`TierGeoRestricted`):** Domestic US/EU only, strict W-2 payroll, or security clearance requirements.

### 3. 🔄 Run-Based Date-Stamped Directories & Delta Tracking
- **Automated Run Isolation:** Every autonomous scan stores output files in an immutable, date-stamped folder:
  ```
  reports/runs/2026-10-01_18-30-00/
  ├── direct_apply_2026-10-01.md
  ├── delta_summary_2026-10-01.md
  ├── sources_analytics_2026-10-01.md
  └── {company}_leads_2026-10-01.md
  ```
- **Live Latest Pointer:** Atomically links `reports/latest` to the most recent run so you never have to search for the newest report.
- **Delta Summary (`delta_summary.md`):** Automatically compares against the previous crawl run from SQLite (`crawl_runs`), highlighting:
  - `+N fresh roles` discovered for the first time (`[NEW]` badge).
  - `+M fresh engineering leads` sourced since the last run.
  - Prevents duplicate candidate outreach and redundant job applications.

### 4. 💎 75+ Embedded High-Signal Curated Seeds
Pre-configured with leading infrastructure, database, and devtools companies known for hiring worldwide contractors and distributed systems engineers:
- **Core Cloud & Systems:** HashiCorp, Cloudflare, Grafana Labs, CockroachDB, Monzo, Fly.io, Meilisearch, Timescale, Supabase, 1Password, Tailscale, Datadog, Docker, Redpanda, ScyllaDB, ClickHouse, Neon.
- **Remote-First Pioneers & Boutique Devtools:** Canonical, Automattic, DuckDuckGo, PostHog, Status.im, Kraken, LiveKit, Railway, Deno, QuestDB, Tinybird, Doppler, Incident.io, Turso, Warp, Depot, Upstash, Resend, Infracost, GitBook, Axiom, Teleport, Qdrant, DragonflyDB.

### 5. 🛡️ Resilient Networking & Bot Filtration
- **Adaptive Pacer:** Per-host token buckets (1.2 rps GitHub, 2.0 rps ATS) with 300–1200ms randomized jitter.
- **Canary Circuit Breaker:** Exponential backoff upon HTTP 429/403 with single-worker canary probes.
- **Multi-Token GitHub Pool:** Automatic round-robin rotation, remaining quota inspection, and cooling periods.
- **Bot Exclusions:** Blocks `[bot]`, CI/CD pipelines, Dependabot, Jenkins, TeamCity, and `noreply.github.com` addresses.

---

## Directory Layout

```
specter/
├── cmd/
│   └── specter/
│       └── main.go                 # CLI entry point (scan, apply, leads, report, roles, companies, export)
├── configs/
│   └── seeds.json                 # Pre-populated catalog of 75+ curated tech companies
├── internal/
│   ├── ats/
│   │   ├── ashby.go               # Ashby job-board API adapter with direct application links
│   │   ├── generic.go             # Generic HTML DOM crawler with embedded ATS detection
│   │   ├── greenhouse.go          # Greenhouse public boards API adapter (#app deep-linking)
│   │   ├── lever.go               # Lever public postings API adapter (/apply deep-linking)
│   │   ├── models.go              # ATS domain models, JobPosting & CompanyMeta
│   │   └── registry.go            # Adapter resolution and dispatch engine
│   ├── crawler/
│   │   ├── fetcher.go             # Resilient HTTP client (SSRF guard, conn pool, ETag injection)
│   │   ├── frontier.go            # Persistent crawl frontier, SQLite TTL cache, conditional headers
│   │   ├── pacer.go               # Per-host token buckets, jitter engine, canary circuit breaker
│   │   ├── token_pool.go          # Multi-token pool, quota tracker, cooling thresholds
│   │   └── scope.go               # Domain boundary & subdomain scoping rules
│   ├── exporter/
│   │   └── export.go              # JSON, CSV, and Roles-CSV exporter
│   ├── reporter/
│   │   ├── direct_apply.go        # One-click direct application dashboard generator
│   │   ├── delta_report.go        # Delta comparison report between crawl runs
│   │   ├── runs_manager.go        # Date-stamped run directory isolation & latest link pointer
│   │   ├── markdown.go            # Executive technical dossier generator with outreach drafts
│   │   └── sources_report.go      # Sources analytics & repository lead yield generator
│   ├── seeds/
│   │   └── seeds.go               # Embedded target seed catalog loader
│   ├── signals/
│   │   ├── git_miner.go           # Public Git miner, email harvester, patch parser, bot gate
│   │   ├── matcher.go             # Backend keyword and technology matcher
│   │   └── remote_policy.go       # Remote policy, sanction resilience, and quick pitch generator
│   ├── storage/
│   │   └── store.go               # Pure Go SQLite store (modernc.org/sqlite) & schema migrations
│   └── ui/
│       └── progress.go            # Real-time ANSI terminal progress tracker
├── reports/                       # Generated dossiers, direct apply dashboards, and run archives
│   ├── latest -> runs/...         # Symlink to the most recent crawl run
│   └── runs/                      # Historical date-stamped execution archives
├── go.mod
├── go.sum
└── README.md
```

---

## Installation & Build

Requires Go 1.23+:

```bash
git clone https://github.com/mahanmntz/specter.git
cd specter
go build -o specter ./cmd/specter
```

---

## CLI Usage Guide

### 1. Autonomous Run Across Seed Catalog
Launch an autonomous discovery run across the curated catalog of 75+ targets:
```bash
./specter scan --all --concurrency=4 --limit=15 --ttl=7
```
Output:
- Saves all dossiers, direct-apply dashboards, and delta summaries to `reports/runs/YYYY-MM-DD_HH-MM-SS/`.
- Updates `reports/latest` symlink.

### 2. Direct One-Click Apply Board (`specter apply`)
Query active roles with direct deep-application links:

```bash
# List all worldwide & contractor-friendly roles (sanction-resilient)
./specter apply --global-only

# Filter for brand-new openings discovered since the last run
./specter apply --global-only --fresh-only

# Filter by company domain and generate a markdown dashboard
./specter apply --domain=canonical.com --export
```

### 3. Targeted Single Scan
Scan a specific company ATS board and GitHub organization:
```bash
# Greenhouse board with GitHub miner
./specter scan --target=boards.greenhouse.io/canonical --github=canonical --ttl=0

# Lever board
./specter scan --target=jobs.lever.co/posthog

# Ashby board
./specter scan --target=jobs.ashbyhq.com/livekit
```

### 4. Discovered Leads Management
List all verified engineering leads sorted by synergy score:
```bash
./specter leads list --uncontacted
```

Filter by domain:
```bash
./specter leads list --domain=cockroachlabs.com
```

Mark lead as contacted:
```bash
./specter leads contact --id=42
```

### 5. Export Datasets
Export all records to JSON or CSV:
```bash
# Export direct apply roles to CSV
./specter export --format=roles-csv --output=direct_apply_roles.csv

# Export full database bundle to JSON
./specter export --format=json --output=specter_full.json
```

---

## Testing & Quality Assurance

Specter is strictly tested with Go's race detector enabled:

```bash
go test -v -race ./...
```

---

## License

MIT License. Designed for ethical engineering reconnaissance, lead discovery, and frictionless global remote opportunities.
