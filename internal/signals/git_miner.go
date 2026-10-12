package signals

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
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
	EmailVerified  bool      `json:"email_verified"`
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

	// Personal Skills Synergy
	PersonalScore   int      `json:"personal_score,omitempty"`
	PersonalMatches []string `json:"personal_matches,omitempty"`

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

// Count returns the number of active tokens in the rotator.
func (r *TokenRotator) Count() int {
	if r == nil {
		return 0
	}
	return len(r.tokens)
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
	apiBase      string // GitHub REST API root (overridable in tests)
	webBase      string // github.com web root, used for patch downloads and links
}

// NewGitMiner creates a configured GitMiner.
func NewGitMiner(f *crawler.Fetcher, opts GitMinerOptions) *GitMiner {
	toks := opts.GitHubTokens
	if toks == "" {
		toks = opts.GitHubToken
	}
	if toks == "" {
		toks = os.Getenv("GITHUB_TOKENS")
		if toks == "" {
			toks = os.Getenv("GITHUB_TOKEN")
		}
	}

	if envRepos := os.Getenv("SPECTER_MAX_REPOS"); envRepos != "" {
		if n, err := strconv.Atoi(envRepos); err == nil && n > 0 {
			opts.MaxRepos = n
		}
	}
	if envCommits := os.Getenv("SPECTER_MAX_COMMITS"); envCommits != "" {
		if n, err := strconv.Atoi(envCommits); err == nil && n > 0 {
			opts.MaxCommitsRepo = n
		}
	}

	if toks == "" {
		if opts.MaxRepos <= 0 {
			opts.MaxRepos = 8
		}
		if opts.MaxCommitsRepo <= 0 {
			opts.MaxCommitsRepo = 20
		}
	} else {
		if opts.MaxRepos <= 0 {
			opts.MaxRepos = 20
		}
		if opts.MaxCommitsRepo <= 0 {
			opts.MaxCommitsRepo = 40
		}
	}

	if opts.PolitenessDelay <= 0 {
		opts.PolitenessDelay = 150 * time.Millisecond
	}

	return &GitMiner{
		fetcher:    f,
		opts:       opts,
		throttler:  crawler.NewHostThrottler(opts.PolitenessDelay),
		rotator:    NewTokenRotator(toks),
		yieldStats: make(map[string]*RepoYieldStats),
		apiBase:    "https://api.github.com",
		webBase:    "https://github.com",
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

// MineOrganization discovers backend engineers and extracts valid emails from public Git commits.
func (m *GitMiner) MineOrganization(ctx context.Context, org string, companyDomain string, onProgress func(msg string)) ([]EngineeringLead, error) {
	if org == "" {
		return nil, nil
	}

	if onProgress != nil {
		onProgress(fmt.Sprintf("Querying public repositories for GitHub org: %s...", org))
	}

	// 1. Fetch public repositories
	reposAPI := fmt.Sprintf(m.apiBase+"/orgs/%s/repos?type=public&sort=pushed&per_page=%d", org, m.opts.MaxRepos)
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

	// Filter backend and engineering repositories across modern tech stacks
	var targetRepos []ghRepoDetail
	for _, r := range repos {
		if r.Fork {
			continue
		}
		lang := strings.ToLower(r.Language)
		name := strings.ToLower(r.Name)

		// Skip documentation-only, website-only, or meta repos
		if strings.Contains(name, "docs") || strings.Contains(name, "documentation") ||
			strings.Contains(name, ".github") || strings.Contains(name, "blog") ||
			strings.Contains(name, "website") || strings.Contains(name, "landing") {
			continue
		}

		isEngRepo := lang == "go" || lang == "rust" || lang == "c++" || lang == "c" ||
			lang == "python" || lang == "typescript" || lang == "javascript" ||
			lang == "java" || lang == "kotlin" || lang == "scala" || lang == "elixir" || lang == "c#" ||
			strings.Contains(name, "backend") ||
			strings.Contains(name, "server") ||
			strings.Contains(name, "service") ||
			strings.Contains(name, "infra") ||
			strings.Contains(name, "engine") ||
			strings.Contains(name, "api") ||
			strings.Contains(name, "core") ||
			strings.Contains(name, "distributed") ||
			strings.Contains(name, "storage") ||
			strings.Contains(name, "database") ||
			strings.Contains(name, "data") ||
			strings.Contains(name, "platform") ||
			strings.Contains(name, "cloud") ||
			strings.Contains(name, "proxy") ||
			strings.Contains(name, "worker") ||
			strings.Contains(name, "cli") ||
			strings.Contains(name, "client") ||
			strings.Contains(name, "sdk") ||
			strings.Contains(name, "lib") ||
			strings.Contains(name, "helm") ||
			strings.Contains(name, "operator")

		if isEngRepo {
			targetRepos = append(targetRepos, r)
		}
	}

	if onProgress != nil {
		onProgress(fmt.Sprintf("Identified %d backend-focused repositories in @%s", len(targetRepos), org))
	}

	contributors := make(map[string]*ContributorAggregation)

	// 2. Iterate repositories and inspect recent commits
	for idx, repo := range targetRepos {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		if onProgress != nil {
			onProgress(fmt.Sprintf("Scanning %s/%s (%d/%d)...", org, repo.Name, idx+1, len(targetRepos)))
		}

		repoFullName := fmt.Sprintf("%s/%s", org, repo.Name)
		repoWebURL := fmt.Sprintf(m.webBase+"/%s/%s", org, repo.Name)

		commitsAPI := fmt.Sprintf(m.apiBase+"/repos/%s/%s/commits?per_page=%d", org, repo.Name, m.opts.MaxCommitsRepo)
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

			// Immediate Bot / CI gate before any work. GitHub-masked addresses are not a bot
			// signal on their own (the patch header may reveal the real email below), so
			// judge those by name/handle only.
			gateEmail := email
			if strings.Contains(email, "users.noreply.github.com") {
				gateEmail = ""
			}
			if IsBotOrCI(name, ghHandle, gateEmail) {
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
					commitWebURL = fmt.Sprintf(m.webBase+"/%s/%s/commit/%s", org, repo.Name, c.SHA)
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

		// DNS MX email domain verification
		emailVerified := VerifyEmailDomainContext(ctx, agg.Email)
		if !emailVerified {
			score -= 30
			if score < 0 {
				score = 0
			}
		}

		topLangs := formatTopLanguages(agg.Languages)

		// Personal skill match against user profile (MY_SKILLS)
		userSkills := GetUserSkills()
		leadCorpus := role + " " + topLangs + " " + strings.Join(matchedSignals, " ") + " " + strings.Join(agg.CommitMsgs, " ")
		personalScore, personalMatches := CalculateSkillMatch(leadCorpus, userSkills)

		lead := EngineeringLead{
			Domain:          companyDomain,
			CompanyDomain:   companyDomain,
			Name:            agg.Name,
			Role:            role,
			Email:           agg.Email,
			EmailVerified:   emailVerified,
			Source:          "git_commit",
			GitHubHandle:    agg.GitHubHandle,
			TopLanguages:    topLangs,
			RelevanceScore:  score,
			PersonalScore:   personalScore,
			PersonalMatches: personalMatches,
			DiscoveredAt:    time.Now().UTC(),
			Contacted:       false,
			RepoName:        agg.RepoName,
			RepoURL:         agg.RepoURL,
			CommitSHA:       agg.CommitSHA,
			CommitURL:       agg.CommitURL,
			MatchedSignals:  matchedSignals,
		}

		leads = append(leads, lead)
	}

	// Sort leads descending by relevance score first
	sort.Slice(leads, func(i, j int) bool {
		return leads[i].RelevanceScore > leads[j].RelevanceScore
	})

	// Enrich top leads only (up to 10 per company) to avoid burning API quota and eliminate latency
	enrichLimit := 10
	if len(leads) < enrichLimit {
		enrichLimit = len(leads)
	}
	for i := 0; i < enrichLimit; i++ {
		m.enrichLeadProfile(ctx, &leads[i], org)
	}
	for i := enrichLimit; i < len(leads); i++ {
		if leads[i].LinkedInURL == "" {
			leads[i].LinkedInURL = generateOSINTLinkedIn(leads[i].Name, org, companyDomain)
		}
	}

	return leads, nil
}
