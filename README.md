# Specter ⚡

**Specter** is a focused CLI reconnaissance, hiring signal detection, and engineering lead discovery engine written in Go (1.23+). It targets backend hiring signals across public ATS feeds (Greenhouse, Lever, Ashby, direct careers pages), mines public Git commit metadata and patch headers for verified engineering contact emails, ensures strict idempotency via SQLite frontier tracking, supports graceful interrupt handling, and generates comprehensive Markdown executive dossiers with contextual outreach drafts.

---

## Architecture Overview

```
specter/
├── cmd/specter/main.go            # CLI entry point (scan, leads, report, roles, companies, export)
├── internal/
│   ├── crawler/
│   │   ├── fetcher.go             # Production HTTP client (SSRF dial guards, connection pooling, body limits)
│   │   ├── frontier.go            # Persistent crawl frontier & in-memory TTL deduplication cache
│   │   ├── ratelimit.go           # In-memory per-host politeness rate limiter
│   │   └── scope.go               # Domain boundary & subdomain scoping rules
│   ├── ats/
│   │   ├── models.go              # Unified ATS models & adapter interface
│   │   ├── greenhouse.go          # Greenhouse public boards API adapter
│   │   ├── lever.go               # Lever public postings API adapter
│   │   ├── ashby.go               # Ashby public job-board API adapter
│   │   ├── generic.go             # Generic HTML DOM crawler with embedded ATS detection
│   │   └── registry.go            # Adapter resolution and dispatch engine
│   ├── signals/
│   │   ├── git_miner.go           # Advanced Git miner, email harvester, patch parser, and role classifier
│   │   └── matcher.go             # Backend keyword matching (Go, Redis, Distributed Systems, Kafka)
│   ├── ui/
│   │   └── progress.go            # Real-time non-blocking ANSI terminal progress reporter
│   ├── reporter/
│   │   └── markdown.go            # Markdown executive dossier generator with cold outreach templates
│   ├── storage/
│   │   └── store.go               # Zero-CGO SQLite engine (modernc.org/sqlite) with strict idempotency
│   └── exporter/
│       └── export.go              # JSON and CSV data exporter
```

---

## Core Capabilities

### 1. State Management & Persistent Frontier
- **Persistent State:** Uses a SQLite `crawl_frontier` table to store visited URLs, domains, entity types, crawl timestamps, and error statuses.
- **Configurable TTL:** Checks an in-memory thread-safe `sync.Map` cache backed by SQLite (`ShouldCrawl(url, ttl)` and `MarkVisited(url, entityType, err)`). If a target has been visited within the TTL (default: 7 days), redundant crawls are skipped immediately.

### 2. Advanced Git Miner & Email Harvester
- **Public Git Commit Mining:** Scrapes commit histories for repositories belonging to the target organization.
- **Raw Patch Header Parsing:** If author email is hidden or masked, fetches commit `.patch` headers (`From: Author <email>`) directly to unmask valid committer addresses.
- **Noise Filtering:** Discards bot/CI addresses (`users.noreply.github.com`, `dependabot`, `actions@github.com`, `snyk`) and generic company mailboxes (`info@`, `support@`, `sales@`, `admin@`).
- **Prospect Categorization:** Categorizes committers into seniority tiers based on message context and architectural impact:
  - `Tech Lead`: Mentions of architecture, RFCs, roadmap, releases, and design documents.
  - `Staff/Principal`: Mentions of consensus, raft, distributed protocols, GC/alloc optimization, sharding, and core engines.
  - `Senior Backend`: Mentions of gRPC, services, Redis caching, Kafka, database migrations, and concurrency.
  - `Engineer`: Feature and maintenance work.
- **Relevance Scoring:** Computes a composite fit score (0–100) weighting language match (Go, Rust, C++), role seniority, and company domain match.

### 3. Real-Time Terminal Progress & Graceful Termination
- **Dynamic Terminal Line:** Uses non-blocking background rendering (`\r\033[K`) displaying active worker count, URLs processed vs total, leads found, emails captured, memory usage, and rate-limit throttle status.
- **Graceful Shutdown (`Ctrl+C`):** Derives execution from `signal.NotifyContext`. Upon interrupt:
  - Immediately stops pulling new work.
  - Drains in-flight requests gracefully with a 3-second deadline.
  - Flushes all pending writes to SQLite.
  - Automatically generates a progress Markdown dossier before exiting.

### 4. Markdown Executive Dossiers & Outreach Drafts
- Generates `reports/{domain}_leads_{date}.md` with three structured sections:
  1. **Company Overview & Hiring Demand:** Detected tech stack (Go, Redis, Distributed Systems) and open backend positions.
  2. **Key Engineering Leads:** Scored table of prospects with roles, emails, GitHub handles, and top languages.
  3. **Contextual Outreach Drafts:** 3 tailored, ready-to-send icebreaker templates:
     - *Template A:* Technical Architecture & Commit Overlap (peer engineering connection).
     - *Template B:* Open Requisition & Team Expansion (referencing specific open roles).
     - *Template C:* Low-Latency & High-Throughput Infrastructure Peer Connect.

---

## Installation & Build

```bash
cd /Users/mahan/development/backend-pr/specter
go build -o specter ./cmd/specter
```

---

## CLI Usage

### 1. Scan ATS or Domain
Scan a Greenhouse board, deduplicate with 7-day TTL, and mine GitHub contributors:
```bash
./specter scan --target=boards.greenhouse.io/stripe --github=stripe --ttl=7
```

Scan a Lever or Ashby board:
```bash
./specter scan --target=jobs.lever.co/netflix
./specter scan --target=jobs.ashbyhq.com/linear
```

### 2. View Discovered Leads
List all mined engineering leads sorted by relevance score:
```bash
./specter leads list
```

Filter by domain and uncontacted status:
```bash
./specter leads list --domain=stripe.com --uncontacted
```

Mark a lead as contacted:
```bash
./specter leads contact --id=1
```

### 3. Generate Markdown Report
Generate a fresh dossier for any previously scanned company:
```bash
./specter report --domain=stripe.com --out-dir=reports
```

### 4. View Backend Roles & Companies
```bash
./specter roles --keyword=redis
./specter companies
```

### 5. Export Data
Export all leads and open roles to JSON or CSV:
```bash
./specter export --format=json --output=leads.json
./specter export --format=csv --output=leads.csv
```

---

## Testing & Race Detection

Run all unit tests with Go's race detector:
```bash
go test -race -v ./...
```
