package signals

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"specter/internal/crawler"
)

// EngineeringLead represents an engineering lead / technical contributor discovered via Git recon.
type EngineeringLead struct {
	ID             int64     `json:"id,omitempty"`
	Domain         string    `json:"domain"`
	CompanyDomain  string    `json:"company_domain,omitempty"`
	Name           string    `json:"name"`
	Role           string    `json:"role"`
	Email          string    `json:"email"`
	Source         string    `json:"source"`
	GitHubHandle   string    `json:"github_handle"`
	TopLanguages   string    `json:"top_languages"`
	RelevanceScore int       `json:"relevance_score"`
	DiscoveredAt   time.Time `json:"discovered_at"`
	Contacted      bool      `json:"contacted"`

	// Source Provenance
	RepoName  string `json:"repo_name,omitempty"`
	RepoURL   string `json:"repo_url,omitempty"`
	CommitSHA string `json:"commit_sha,omitempty"`
	CommitURL string `json:"commit_url,omitempty"`

	// Profile Enrichment
	Bio            string   `json:"bio,omitempty"`
	WebsiteURL     string   `json:"website_url,omitempty"`
	LinkedInURL    string   `json:"linkedin_url,omitempty"`
	Location       string   `json:"location,omitempty"`
	MatchedSignals []string `json:"matched_signals,omitempty"`

	// Synchronization State
	SyncedToSheets bool      `json:"synced_to_sheets,omitempty"`
	SyncedAt       time.Time `json:"synced_at,omitempty"`
}

// RepoYieldStats tracks commit scanning and yield statistics per repository.
type RepoYieldStats struct {
	RepoName        string `json:"repo_name"`
	RepoURL         string `json:"repo_url"`
	CommitsScanned  int    `json:"commits_scanned"`
	VerifiedLeads   int    `json:"verified_leads"`
	ExtractedEmails int    `json:"extracted_emails"`
	PrimaryStack    string `json:"primary_stack"`
}

// TokenRotator manages round-robin rotation across multiple GitHub tokens.
type TokenRotator struct {
	tokens []string
	index  atomic.Uint64
}

// NewTokenRotator creates a rotator from comma-separated tokens.
func NewTokenRotator(tokensCSV string) *TokenRotator {
	var tokens []string
	for _, t := range strings.Split(tokensCSV, ",") {
		clean := strings.TrimSpace(t)
		if clean != "" {
			tokens = append(tokens, clean)
		}
	}
	return &TokenRotator{tokens: tokens}
}

// Next returns the next token in round-robin sequence or empty string if none configured.
func (r *TokenRotator) Next() string {
	if r == nil || len(r.tokens) == 0 {
		return ""
	}
	idx := r.index.Add(1) - 1
	return r.tokens[idx%uint64(len(r.tokens))]
}

// GitMinerOptions configures limits and behavior for mining.
type GitMinerOptions struct {
	GitHubToken     string
	GitHubTokens    string
	MaxRepos        int
	MaxCommitsRepo  int
	ExtractPatches  bool
	PolitenessDelay time.Duration
}

// GitMiner coordinates repository traversal and commit email harvesting.
type GitMiner struct {
	fetcher      *crawler.Fetcher
	opts         GitMinerOptions
	throttler    *crawler.HostThrottler
	rotator      *TokenRotator
	profileCache sync.Map // map[string]*ghUserProfile
	yieldStats   map[string]*RepoYieldStats
	statsMu      sync.Mutex
}

// NewGitMiner creates a configured GitMiner.
func NewGitMiner(f *crawler.Fetcher, opts GitMinerOptions) *GitMiner {
	toks := opts.GitHubTokens
	if toks == "" {
		toks = opts.GitHubToken
	}

	if toks == "" {
		if opts.MaxRepos <= 0 || opts.MaxRepos > 5 {
			opts.MaxRepos = 4
		}
		if opts.MaxCommitsRepo <= 0 || opts.MaxCommitsRepo > 15 {
			opts.MaxCommitsRepo = 10
		}
	} else {
		if opts.MaxRepos <= 0 {
			opts.MaxRepos = 15
		}
		if opts.MaxCommitsRepo <= 0 {
			opts.MaxCommitsRepo = 25
		}
	}

	if opts.PolitenessDelay <= 0 {
		opts.PolitenessDelay = 200 * time.Millisecond
	}

	return &GitMiner{
		fetcher:    f,
		opts:       opts,
		throttler:  crawler.NewHostThrottler(opts.PolitenessDelay),
		rotator:    NewTokenRotator(toks),
		yieldStats: make(map[string]*RepoYieldStats),
	}
}

// GetYieldStats returns the current repository yield statistics sorted by verified leads descending.
func (m *GitMiner) GetYieldStats() []*RepoYieldStats {
	m.statsMu.Lock()
	defer m.statsMu.Unlock()

	var stats []*RepoYieldStats
	for _, s := range m.yieldStats {
		copied := *s
		stats = append(stats, &copied)
	}
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].VerifiedLeads != stats[j].VerifiedLeads {
			return stats[i].VerifiedLeads > stats[j].VerifiedLeads
		}
		return stats[i].CommitsScanned > stats[j].CommitsScanned
	})
	return stats
}

// Regexp matchers for email parsing, patch extraction, and profile enrichment.
var (
	emailRegex      = regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)
	patchFromRegex  = regexp.MustCompile(`(?m)^From:\s+([^<]+)<([^>]+)>`)
	linkedInRegex   = regexp.MustCompile(`(?i)(?:https?://)?(?:www\.)?linkedin\.com/in/([a-zA-Z0-9_\-\.%]+)`)
	genericPrefixes = map[string]bool{
		"info": true, "support": true, "sales": true, "contact": true,
		"admin": true, "help": true, "marketing": true, "security": true,
		"billing": true, "jobs": true, "careers": true, "team": true,
		"hi": true, "hello": true, "press": true, "media": true, "legal": true,
		"privacy": true, "compliance": true, "noreply": true, "no-reply": true,
	}

	backendSignalsList = []string{
		"raft", "paxos", "consensus", "storage", "indexing", "pebble", "badger",
		"lsm", "wal", "distributed", "grpc", "protobuf", "kafka", "redis",
		"postgres", "postgresql", "concurrency", "goroutines", "high-throughput",
		"low-latency", "k8s", "kubernetes", "cloud-native", "multithreading", "linux",
		"cluster", "replication", "sharding", "zero-copy", "btree",
	}
)

type ghRepoDetail struct {
	Name        string `json:"name"`
	Fork        bool   `json:"fork"`
	Language    string `json:"language"`
	Description string `json:"description"`
}

type ghCommitAuthor struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Date  string `json:"date"`
}

type ghCommitDetail struct {
	SHA    string `json:"sha"`
	Commit struct {
		Author    ghCommitAuthor `json:"author"`
		Committer ghCommitAuthor `json:"committer"`
		Message   string         `json:"message"`
	} `json:"commit"`
	Author *struct {
		Login string `json:"login"`
	} `json:"author"`
}

type ghUserProfile struct {
	Bio      string `json:"bio"`
	Blog     string `json:"blog"`
	Location string `json:"location"`
}

// ContributorAggregation aggregates signals across multiple commits for a single individual.
type ContributorAggregation struct {
	Name           string
	GitHubHandle   string
	Email          string
	Languages      map[string]int
	CommitMsgs     []string
	CommitCount    int
	DomainMatch    bool
	RepoName       string
	RepoURL        string
	CommitSHA      string
	CommitURL      string
	MatchedSignals map[string]bool
}

func (m *GitMiner) authHeaders() map[string]string {
	h := make(map[string]string)
	if m.rotator != nil {
		if token := m.rotator.Next(); token != "" {
			h["Authorization"] = "Bearer " + token
		}
	}
	return h
}

// IsBotOrCI checks author name, GitHub handle, and email against strict bot and CI/CD rules.
func IsBotOrCI(name, handle, email string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	h := strings.ToLower(strings.TrimSpace(handle))
	e := strings.ToLower(strings.TrimSpace(email))

	// 1. Email checks
	if e != "" {
		if strings.Contains(e, "noreply.github.com") ||
			strings.Contains(e, "teamcity") ||
			strings.Contains(e, "actions@") ||
			strings.Contains(e, "jenkins") ||
			strings.Contains(e, "circleci") ||
			strings.Contains(e, "buildkite") ||
			strings.Contains(e, "dependabot") ||
			strings.Contains(e, "sentry") ||
			strings.Contains(e, "[bot]") ||
			strings.Contains(e, "bot@") ||
			strings.Contains(e, "automation") ||
			strings.Contains(e, "greenkeeper") ||
			strings.Contains(e, "renovate") ||
			strings.Contains(e, "snyk") ||
			strings.Contains(e, "codecov") {
			return true
		}
	}

	// 2. Name & Handle checks
	isBotString := func(s string) bool {
		if s == "" {
			return false
		}
		if strings.Contains(s, "[bot]") ||
			strings.Contains(s, "automation") ||
			strings.Contains(s, "teamcity") ||
			strings.Contains(s, "jenkins") ||
			strings.Contains(s, "circleci") ||
			strings.Contains(s, "buildkite") ||
			strings.Contains(s, "dependabot") ||
			strings.Contains(s, "sentry") ||
			strings.Contains(s, "github-actions") ||
			strings.Contains(s, "codecov") ||
			strings.HasSuffix(s, "-bot") ||
			strings.HasSuffix(s, "_bot") ||
			strings.HasPrefix(s, "bot-") ||
			strings.HasPrefix(s, "bot_") {
			return true
		}

		tokens := strings.FieldsFunc(s, func(r rune) bool {
			return r == ' ' || r == '-' || r == '_' || r == '.' || r == '/' || r == '[' || r == ']' || r == '(' || r == ')'
		})
		for _, tok := range tokens {
			if tok == "bot" || tok == "ci" || tok == "cd" || tok == "automation" || (strings.HasSuffix(tok, "bot") && len(tok) > 3 && !strings.Contains(tok, "abbott")) {
				return true
			}
		}
		return false
	}

	if isBotString(n) || isBotString(h) {
		return true
	}

	return false
}

// IsValidOutreachEmail validates that an email is legitimate and excludes bots and generic inboxes.
func IsValidOutreachEmail(email string) bool {
	if email == "" {
		return false
	}
	email = strings.TrimSpace(strings.ToLower(email))

	if !emailRegex.MatchString(email) {
		return false
	}

	// Bot & CI noise
	if strings.Contains(email, "users.noreply.github.com") ||
		strings.Contains(email, "noreply.github.com") ||
		strings.Contains(email, "teamcity") ||
		strings.Contains(email, "actions@") ||
		strings.Contains(email, "jenkins") ||
		strings.Contains(email, "circleci") ||
		strings.Contains(email, "buildkite") ||
		strings.Contains(email, "dependabot") ||
		strings.Contains(email, "sentry") ||
		strings.Contains(email, "github-actions") ||
		strings.Contains(email, "greenkeeper") ||
		strings.Contains(email, "snyk") ||
		strings.Contains(email, "renovate") ||
		strings.Contains(email, "bot@") ||
		strings.Contains(email, "[bot]") ||
		strings.Contains(email, "codecov") {
		return false
	}

	// Common test/dummy domains
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return false
	}
	userPart, domainPart := parts[0], parts[1]

	if domainPart == "example.com" || domainPart == "localhost" || domainPart == "test.com" {
		return false
	}

	// Generic company mailboxes
	if genericPrefixes[userPart] {
		return false
	}

	return true
}

// MineOrganization discovers backend engineers and extracts valid emails from public Git commits.
func (m *GitMiner) MineOrganization(ctx context.Context, org string, companyDomain string, onProgress func(msg string)) ([]EngineeringLead, error) {
	if org == "" {
		return nil, nil
	}

	if onProgress != nil {
		onProgress(fmt.Sprintf("Querying public repositories for GitHub org: %s...", org))
	}

	// 1. Fetch public repositories
	reposAPI := fmt.Sprintf("https://api.github.com/orgs/%s/repos?type=public&sort=pushed&per_page=%d", org, m.opts.MaxRepos)
	if err := m.throttler.Wait(ctx, reposAPI); err != nil {
		return nil, err
	}

	res, err := m.fetcher.FetchWithHeaders(ctx, reposAPI, m.authHeaders())
	if err != nil {
		return nil, fmt.Errorf("failed fetching repositories for org %s: %w", org, err)
	}
	if res.StatusCode == http.StatusForbidden || res.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("GitHub API rate limit reached (consider setting GITHUB_TOKENS)")
	}
	if res.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("GitHub organization %q not found", org)
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API repos query returned HTTP %d", res.StatusCode)
	}

	var repos []ghRepoDetail
	if err := json.Unmarshal(res.Body, &repos); err != nil {
		return nil, fmt.Errorf("failed unmarshaling GitHub repos: %w", err)
	}

	// Filter backend-relevant repositories
	var targetRepos []ghRepoDetail
	for _, r := range repos {
		if r.Fork {
			continue
		}
		lang := strings.ToLower(r.Language)
		name := strings.ToLower(r.Name)
		isBackend := lang == "go" || lang == "rust" || lang == "c++" ||
			strings.Contains(name, "backend") ||
			strings.Contains(name, "server") ||
			strings.Contains(name, "service") ||
			strings.Contains(name, "infra") ||
			strings.Contains(name, "engine") ||
			strings.Contains(name, "api") ||
			strings.Contains(name, "core") ||
			strings.Contains(name, "distributed") ||
			strings.Contains(name, "pebble") ||
			strings.Contains(name, "storage") ||
			strings.Contains(name, "helm")

		if isBackend {
			targetRepos = append(targetRepos, r)
		}
	}

	if onProgress != nil {
		onProgress(fmt.Sprintf("Identified %d backend-focused repositories in @%s", len(targetRepos), org))
	}

	contributors := make(map[string]*ContributorAggregation)

	// 2. Iterate repositories and inspect recent commits
	for _, repo := range targetRepos {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		repoFullName := fmt.Sprintf("%s/%s", org, repo.Name)
		repoWebURL := fmt.Sprintf("https://github.com/%s/%s", org, repo.Name)

		commitsAPI := fmt.Sprintf("https://api.github.com/repos/%s/%s/commits?per_page=%d", org, repo.Name, m.opts.MaxCommitsRepo)
		if err := m.throttler.Wait(ctx, commitsAPI); err != nil {
			return nil, err
		}

		cRes, err := m.fetcher.FetchWithHeaders(ctx, commitsAPI, m.authHeaders())
		if err != nil {
			continue
		}
		if cRes.StatusCode == http.StatusForbidden || cRes.StatusCode == http.StatusTooManyRequests {
			if onProgress != nil {
				onProgress(fmt.Sprintf("GitHub rate limit reached while querying commits for %s; returning current prospects.", org))
			}
			break
		}
		if cRes.StatusCode != http.StatusOK {
			continue
		}

		var commits []ghCommitDetail
		if err := json.Unmarshal(cRes.Body, &commits); err != nil {
			continue
		}

		// Update repository yield statistics
		m.statsMu.Lock()
		yStat, exists := m.yieldStats[repoFullName]
		if !exists {
			primaryStack := repo.Language
			if primaryStack == "" {
				primaryStack = "Backend"
			}
			yStat = &RepoYieldStats{
				RepoName:     repoFullName,
				RepoURL:      repoWebURL,
				PrimaryStack: primaryStack,
			}
			m.yieldStats[repoFullName] = yStat
		}
		yStat.CommitsScanned += len(commits)
		m.statsMu.Unlock()

		patchCount := 0
		for _, c := range commits {
			name := strings.TrimSpace(c.Commit.Author.Name)
			email := strings.TrimSpace(c.Commit.Author.Email)
			ghHandle := ""
			if c.Author != nil {
				ghHandle = c.Author.Login
			}

			// Immediate Bot / CI gate before any work
			if IsBotOrCI(name, ghHandle, email) {
				continue
			}

			// If author email is empty or masked as GitHub noreply, attempt patch extraction (cap at 3 per repo)
			if m.opts.ExtractPatches && (email == "" || strings.Contains(email, "users.noreply.github.com")) && c.SHA != "" {
				if patchCount < 3 {
					patchEmail, patchName := m.extractFromPatch(ctx, org, repo.Name, c.SHA, m.authHeaders())
					if patchEmail != "" && !IsBotOrCI(patchName, ghHandle, patchEmail) {
						email = patchEmail
						patchCount++
						if patchName != "" && name == "" {
							name = patchName
						}
					}
				}
			}

			// Re-verify bot gate after patch extraction
			if IsBotOrCI(name, ghHandle, email) {
				continue
			}

			// Validate and filter noise
			if !IsValidOutreachEmail(email) {
				continue
			}

			key := strings.ToLower(email)
			agg, exists := contributors[key]
			if !exists {
				commitWebURL := ""
				if c.SHA != "" {
					commitWebURL = fmt.Sprintf("https://github.com/%s/%s/commit/%s", org, repo.Name, c.SHA)
				}

				agg = &ContributorAggregation{
					Name:           name,
					GitHubHandle:   ghHandle,
					Email:          email,
					Languages:      make(map[string]int),
					CommitMsgs:     []string{},
					RepoName:       repoFullName,
					RepoURL:        repoWebURL,
					CommitSHA:      c.SHA,
					CommitURL:      commitWebURL,
					MatchedSignals: make(map[string]bool),
				}
				contributors[key] = agg

				// Record in yield stats
				m.statsMu.Lock()
				yStat.VerifiedLeads++
				if email != "" {
					yStat.ExtractedEmails++
				}
				m.statsMu.Unlock()
			}

			if agg.Name == "" && name != "" {
				agg.Name = name
			}
			if agg.GitHubHandle == "" && ghHandle != "" {
				agg.GitHubHandle = ghHandle
			}

			if repo.Language != "" {
				agg.Languages[repo.Language]++
			}
			agg.CommitCount++
			if len(c.Commit.Message) > 0 && len(agg.CommitMsgs) < 10 {
				agg.CommitMsgs = append(agg.CommitMsgs, c.Commit.Message)

				// Detect backend signals
				msgLower := strings.ToLower(c.Commit.Message + " " + repo.Name)
				for _, sig := range backendSignalsList {
					if strings.Contains(msgLower, sig) {
						agg.MatchedSignals[sig] = true
					}
				}
			}
			if companyDomain != "" && strings.HasSuffix(strings.ToLower(email), "@"+strings.ToLower(companyDomain)) {
				agg.DomainMatch = true
			}
		}
	}

	// 3. Classify roles, enrich profiles, and compute relevance scores
	var leads []EngineeringLead
	for _, agg := range contributors {
		var matchedSignals []string
		for sig := range agg.MatchedSignals {
			matchedSignals = append(matchedSignals, sig)
		}
		sort.Strings(matchedSignals)

		role, score := ClassifyBackendArchetype(agg.CommitMsgs, agg.Languages, agg.CommitCount, agg.RepoName, matchedSignals)
		if agg.DomainMatch {
			score += 15
			if score > 100 {
				score = 100
			}
		}

		topLangs := formatTopLanguages(agg.Languages)

		lead := EngineeringLead{
			Domain:         companyDomain,
			CompanyDomain:  companyDomain,
			Name:           agg.Name,
			Role:           role,
			Email:          agg.Email,
			Source:         "git_commit",
			GitHubHandle:   agg.GitHubHandle,
			TopLanguages:   topLangs,
			RelevanceScore: score,
			DiscoveredAt:   time.Now().UTC(),
			Contacted:      false,
			RepoName:       agg.RepoName,
			RepoURL:        agg.RepoURL,
			CommitSHA:      agg.CommitSHA,
			CommitURL:      agg.CommitURL,
			MatchedSignals: matchedSignals,
		}

		// Enrich Lead Profile via public GitHub user API & LinkedIn heuristics
		m.enrichLeadProfile(ctx, &lead, org)

		leads = append(leads, lead)
	}

	// Sort leads descending by relevance score
	sort.Slice(leads, func(i, j int) bool {
		return leads[i].RelevanceScore > leads[j].RelevanceScore
	})

	return leads, nil
}

// enrichLeadProfile queries public GitHub user profile and extracts bio, website, and LinkedIn.
func (m *GitMiner) enrichLeadProfile(ctx context.Context, lead *EngineeringLead, companyName string) {
	if lead.GitHubHandle == "" {
		lead.LinkedInURL = generateOSINTLinkedIn(lead.Name, companyName, lead.Domain)
		return
	}

	handle := lead.GitHubHandle
	var profile *ghUserProfile
	if val, ok := m.profileCache.Load(handle); ok {
		profile = val.(*ghUserProfile)
	} else {
		userAPI := fmt.Sprintf("https://api.github.com/users/%s", handle)
		res, err := m.fetcher.FetchWithHeaders(ctx, userAPI, m.authHeaders())
		if err == nil && res.StatusCode == http.StatusOK {
			var p ghUserProfile
			if jsonErr := json.Unmarshal(res.Body, &p); jsonErr == nil {
				profile = &p
			}
		}
		if profile == nil {
			profile = &ghUserProfile{}
		}
		m.profileCache.Store(handle, profile)
	}

	lead.Bio = profile.Bio
	lead.Location = profile.Location
	lead.WebsiteURL = profile.Blog

	// Extract LinkedIn from blog or bio
	searchBlob := profile.Blog + " " + profile.Bio
	if match := linkedInRegex.FindStringSubmatch(searchBlob); len(match) >= 2 {
		lead.LinkedInURL = "https://www.linkedin.com/in/" + strings.Trim(match[1], "/")
	} else {
		lead.LinkedInURL = generateOSINTLinkedIn(lead.Name, companyName, lead.Domain)
	}
}

func generateOSINTLinkedIn(name, companyName, domain string) string {
	comp := companyName
	if comp == "" {
		comp = domain
	}
	comp = strings.TrimSuffix(comp, ".com")
	return fmt.Sprintf("https://www.google.com/search?q=site:linkedin.com/in/+\"%s\"+\"%s\"",
		url.QueryEscape(name), url.QueryEscape(comp))
}

// extractFromPatch fetches the commit patch to extract the raw From: header email.
func (m *GitMiner) extractFromPatch(ctx context.Context, org, repo, sha string, headers map[string]string) (string, string) {
	patchURL := fmt.Sprintf("https://github.com/%s/%s/commit/%s.patch", org, repo, sha)
	patchHeaders := make(map[string]string)
	for k, v := range headers {
		patchHeaders[k] = v
	}
	patchHeaders["Accept"] = "text/plain"

	res, err := m.fetcher.FetchWithHeaders(ctx, patchURL, patchHeaders)
	if err != nil || res.StatusCode != http.StatusOK {
		return "", ""
	}

	matches := patchFromRegex.FindSubmatch(res.Body)
	if len(matches) >= 3 {
		name := strings.TrimSpace(string(matches[1]))
		email := strings.TrimSpace(string(matches[2]))
		if !IsBotOrCI(name, "", email) && IsValidOutreachEmail(email) {
			return email, name
		}
	}
	return "", ""
}

// ClassifyBackendArchetype categorizes prospects into diverse backend archetypes and returns fit score.
func ClassifyBackendArchetype(commitMsgs []string, languages map[string]int, commitCount int, repoName string, signals []string) (string, int) {
	combined := strings.ToLower(strings.Join(commitMsgs, " ") + " " + repoName)

	words := strings.FieldsFunc(combined, func(r rune) bool {
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

	role := "Core Contributor"

	// 1. Engineering Leadership: CTO, VP of Eng, Director of Engineering, Tech Lead, Engineering Manager
	if hasWord("cto") || hasWord("vp") || strings.Contains(combined, "vp of engineering") || strings.Contains(combined, "director of engineering") {
		role = "Engineering Leadership"
	} else if strings.Contains(combined, "engineering manager") || hasWord("em") {
		role = "Engineering Manager"
	} else if strings.Contains(combined, "tech lead") || strings.Contains(combined, "lead engineer") || strings.Contains(combined, "team lead") ||
		strings.Contains(combined, "roadmap") || hasWord("rfc") || strings.Contains(combined, "cut release") || strings.Contains(combined, "release candidate") {
		role = "Tech Lead"
	}

	// 2. Staff / Principal: Principal Engineer, Staff Distributed Systems Engineer, Architect
	if role == "Core Contributor" {
		if hasWord("principal") || hasWord("staff") || hasWord("architect") || strings.Contains(combined, "architecture") ||
			strings.Contains(combined, "distributed systems") || strings.Contains(combined, "consensus") || strings.Contains(combined, "raft") ||
			strings.Contains(combined, "paxos") || strings.Contains(combined, "invariants") || strings.Contains(combined, "design doc") ||
			strings.Contains(combined, "fault tolerance") || (commitCount >= 8 && (strings.Contains(repoName, "core") || strings.Contains(repoName, "storage") || strings.Contains(repoName, "server"))) {
			if strings.Contains(combined, "distributed") || strings.Contains(combined, "raft") || strings.Contains(combined, "consensus") {
				role = "Staff Distributed Systems Engineer"
			} else if strings.Contains(combined, "architect") {
				role = "Principal Systems Architect"
			} else {
				role = "Staff / Principal"
			}
		}
	}

	// 3. Senior Backend: Senior Go Engineer, Infrastructure Engineer, Core Storage Engineer, Platform Engineer
	if role == "Core Contributor" {
		if hasWord("storage") || hasWord("pebble") || hasWord("badger") || hasWord("lsm") || hasWord("indexing") || hasWord("btree") {
			role = "Core Storage Engineer"
		} else if hasWord("k8s") || hasWord("kubernetes") || hasWord("operator") || hasWord("helm") || strings.Contains(combined, "infra") {
			role = "Infrastructure Engineer"
		} else if hasWord("grpc") || hasWord("proto") || hasWord("protobuf") || hasWord("pipeline") || hasWord("kafka") || hasWord("redis") || hasWord("postgres") ||
			hasWord("senior") || hasWord("sr") || hasWord("concurrency") || hasWord("mutex") || hasWord("goroutine") || commitCount >= 3 {
			if languages["Go"] > 0 {
				role = "Senior Go Engineer"
			} else {
				role = "Senior Backend Engineer"
			}
		}
	}

	// 4. Core Contributor: Active backend contributor, Maintainer
	if role == "Core Contributor" {
		if languages["Go"] > 0 {
			role = "Core Contributor (Go)"
		} else if languages["Rust"] > 0 {
			role = "Core Contributor (Rust)"
		} else {
			role = "Active Contributor"
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
	case "Tech Lead", "Engineering Leadership", "Engineering Manager":
		score += 25
	case "Staff/Principal", "Staff / Principal", "Staff Distributed Systems Engineer", "Principal Systems Architect":
		score += 25
	case "Senior Backend", "Senior Go Engineer", "Core Storage Engineer", "Infrastructure Engineer":
		score += 15
	case "Engineer", "Core Contributor", "Core Contributor (Go)", "Core Contributor (Rust)", "Active Contributor":
		score += 5
	}

	hasGo := languages["Go"] > 0
	hasRust := languages["Rust"] > 0
	hasCpp := languages["C++"] > 0

	if hasGo {
		score += 20
	} else if hasRust || hasCpp {
		score += 15
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
