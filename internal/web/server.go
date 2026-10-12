// Package web serves Specter's local job browser: a small JSON API plus an
// embedded single-page UI. It is designed to run on loopback only.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mahanmntz/specter/internal/ats"
	"github.com/mahanmntz/specter/internal/ghprofile"
	"github.com/mahanmntz/specter/internal/seeds"
	"github.com/mahanmntz/specter/internal/signals"
	"github.com/mahanmntz/specter/internal/storage"
)

//go:embed static
var staticFS embed.FS

// Source is the data the UI needs; *storage.Store satisfies it.
type Source interface {
	ListDirectApplyRoles(ctx context.Context, f storage.DirectApplyFilter) ([]ats.JobPosting, error)
	ListRoleStatuses(ctx context.Context) (map[string]string, error)
	SetRoleStatus(ctx context.Context, externalID, status string) error
	GetLastCrawlRun(ctx context.Context) (*storage.CrawlRun, error)
	RecordCrawlRun(ctx context.Context, run *storage.CrawlRun) error
	GetSetting(ctx context.Context, key string) (string, bool, error)
	SetSetting(ctx context.Context, key, value string) error
}

// GitHubAnalyzer reads a public GitHub profile.
type GitHubAnalyzer interface {
	Analyze(ctx context.Context, login string) (*ghprofile.Result, error)
	AvatarDataURL(ctx context.Context, avatarURL string) (string, error)
}

// Options configures a Server.
type Options struct {
	// Skills used for match scores until the user saves a profile.
	Skills []string
	// Run performs a discovery pass; nil disables the Find-jobs button.
	Run RunFunc
	// GitHub reads public GitHub profiles for onboarding; nil disables import.
	GitHub GitHubAnalyzer
	// Seeds returns the company catalog to scan; defaults to the embedded catalog.
	Seeds func() ([]seeds.SeedTarget, error)
}

// Server is the HTTP handler for the local UI.
type Server struct {
	src      Source
	now      func() time.Time
	opts     Options
	progress *Progress
	baseCtx  context.Context
	stopAll  context.CancelFunc

	mu     sync.RWMutex
	prefs  *Prefs // nil until the user completes setup
	skills []string
}

// New creates a Server and restores any saved profile.
func New(src Source, opts Options) *Server {
	if opts.Seeds == nil {
		opts.Seeds = func() ([]seeds.SeedTarget, error) { return seeds.LoadSeeds("") }
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{src: src, now: time.Now, opts: opts, progress: newProgress(), baseCtx: ctx, stopAll: cancel, skills: opts.Skills}
	s.loadPrefs(ctx)
	return s
}

// Close cancels any running discovery pass.
func (s *Server) Close() { s.stopAll() }

// Handler returns the routed, host-checked handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(staticFS, "static")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("GET /api/jobs", s.handleJobs)
	mux.HandleFunc("GET /api/facets", s.handleFacets)
	mux.HandleFunc("POST /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/profile", s.handleGetProfile)
	mux.HandleFunc("PUT /api/profile", s.handlePutProfile)
	mux.HandleFunc("POST /api/github/import", s.handleGitHubImport)
	mux.HandleFunc("GET /api/run", s.handleRunStatus)
	mux.HandleFunc("POST /api/run", s.handleRunStart)
	mux.HandleFunc("POST /api/run/cancel", s.handleRunCancel)
	return guard(mux)
}

// guard blocks DNS-rebinding (Host must be loopback) and cross-site POSTs.
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.Trim(host, "[]")
		if host != "localhost" && host != "127.0.0.1" && host != "::1" {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Specter") != "1" {
			http.Error(w, "missing X-Specter header", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; base-uri 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// Job is the UI representation of a role.
type Job struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Company    string    `json:"company"`
	Domain     string    `json:"domain"`
	Location   string    `json:"location"`
	Department string    `json:"department"`
	Seniority  string    `json:"seniority"`
	Keywords   []string  `json:"keywords"`
	URL        string    `json:"url"`
	ApplyURL   string    `json:"apply_url"`
	Policy     string    `json:"policy"`
	Global     bool      `json:"global_remote"`
	Contractor bool      `json:"contractor_friendly"`
	Comp       string    `json:"compensation,omitempty"`
	FirstSeen  time.Time `json:"first_seen"`
	Score      int       `json:"score"`
	Matches    []string  `json:"matches,omitempty"`
	Status     string    `json:"status,omitempty"`
}

type query struct {
	terms       []string
	global      bool
	contractor  bool
	freshDays   int
	seniorities map[string]bool
	company     string
	keywords    []string
	status      string
	minScore    int
	sortBy      string
	limit       int
	offset      int
}

func parseQuery(r *http.Request) query {
	v := r.URL.Query()
	q := query{
		terms:       strings.Fields(strings.ToLower(v.Get("q"))),
		global:      v.Get("global") == "1",
		contractor:  v.Get("contractor") == "1",
		company:     strings.ToLower(strings.TrimSpace(v.Get("company"))),
		status:      v.Get("status"),
		sortBy:      v.Get("sort"),
		seniorities: map[string]bool{},
	}
	q.freshDays, _ = strconv.Atoi(v.Get("fresh"))
	q.minScore, _ = strconv.Atoi(v.Get("min_score"))
	q.offset, _ = strconv.Atoi(v.Get("offset"))
	q.limit, _ = strconv.Atoi(v.Get("limit"))
	if q.limit <= 0 {
		q.limit = 50
	}
	if q.limit > 200 {
		q.limit = 200
	}
	if q.offset < 0 {
		q.offset = 0
	}
	for _, s := range strings.Split(v.Get("seniority"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			q.seniorities[s] = true
		}
	}
	for _, k := range strings.Split(v.Get("keyword"), ",") {
		if k = strings.ToLower(strings.TrimSpace(k)); k != "" {
			q.keywords = append(q.keywords, k)
		}
	}
	return q
}

func (s *Server) load(ctx context.Context) ([]Job, error) {
	roles, err := s.src.ListDirectApplyRoles(ctx, storage.DirectApplyFilter{})
	if err != nil {
		return nil, err
	}
	statuses, err := s.src.ListRoleStatuses(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	skills, configured := s.skills, s.prefs != nil
	s.mu.RUnlock()

	jobs := make([]Job, 0, len(roles))
	for _, r := range roles {
		// Once a role is chosen, only show postings that fit it (older scans may hold others).
		if configured && !signals.IsBackendRole(r.Title, r.Department) {
			continue
		}
		corpus := r.Title + " " + r.Department + " " + r.Location + " " + r.RemotePolicy + " " + strings.Join(r.Keywords, " ")
		score, matches := signals.CalculateSkillMatch(corpus, skills)
		apply := r.ApplyURL
		if apply == "" {
			apply = r.URL
		}
		jobs = append(jobs, Job{
			ID: r.ID, Title: r.Title, Company: r.CompanyName, Domain: r.CompanyDomain,
			Location: r.Location, Department: r.Department, Seniority: r.Seniority,
			Keywords: r.Keywords, URL: r.URL, ApplyURL: apply, Policy: r.RemotePolicy,
			Global: r.GlobalRemote, Contractor: r.ContractorFriendly, Comp: r.Compensation,
			FirstSeen: r.FirstSeenAt, Score: score, Matches: matches, Status: statuses[r.ID],
		})
	}
	return jobs, nil
}

func (q query) matches(j Job, now time.Time) bool {
	switch q.status {
	case "", "active": // default view hides dismissed jobs
		if j.Status == storage.StatusHidden {
			return false
		}
	case "all":
	case "new": // not yet triaged
		if j.Status != "" {
			return false
		}
	default:
		if j.Status != q.status {
			return false
		}
	}
	if q.global && !j.Global {
		return false
	}
	if q.contractor && !j.Contractor {
		return false
	}
	if q.freshDays > 0 && (j.FirstSeen.IsZero() || now.Sub(j.FirstSeen) > time.Duration(q.freshDays)*24*time.Hour) {
		return false
	}
	if len(q.seniorities) > 0 && !q.seniorities[j.Seniority] {
		return false
	}
	if q.company != "" && !strings.Contains(strings.ToLower(j.Company+" "+j.Domain), q.company) {
		return false
	}
	if q.minScore > 0 && j.Score < q.minScore {
		return false
	}
	if len(q.keywords) > 0 {
		have := make(map[string]bool, len(j.Keywords))
		for _, k := range j.Keywords {
			have[strings.ToLower(k)] = true
		}
		for _, k := range q.keywords {
			if !have[k] {
				return false
			}
		}
	}
	if len(q.terms) > 0 {
		hay := strings.ToLower(j.Title + " " + j.Company + " " + j.Location + " " + j.Department + " " + j.Policy + " " + strings.Join(j.Keywords, " "))
		for _, t := range q.terms {
			if !strings.Contains(hay, t) {
				return false
			}
		}
	}
	return true
}

func sortJobs(jobs []Job, by string) {
	switch by {
	case "newest":
		sort.SliceStable(jobs, func(i, k int) bool { return jobs[i].FirstSeen.After(jobs[k].FirstSeen) })
	case "company":
		sort.SliceStable(jobs, func(i, k int) bool {
			return strings.ToLower(jobs[i].Company) < strings.ToLower(jobs[k].Company)
		})
	default: // "best": worldwide-friendly first, then skill match, then recency
		sort.SliceStable(jobs, func(i, k int) bool {
			a, b := jobs[i], jobs[k]
			if a.Global != b.Global {
				return a.Global
			}
			if a.Contractor != b.Contractor {
				return a.Contractor
			}
			if a.Score != b.Score {
				return a.Score > b.Score
			}
			return a.FirstSeen.After(b.FirstSeen)
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	all, err := s.load(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	q := parseQuery(r)
	now := s.now()
	filtered := all[:0:0]
	for _, j := range all {
		if q.matches(j, now) {
			filtered = append(filtered, j)
		}
	}
	sortJobs(filtered, q.sortBy)

	total := len(filtered)
	start := q.offset
	if start > total {
		start = total
	}
	end := start + q.limit
	if end > total {
		end = total
	}
	writeJSON(w, http.StatusOK, map[string]any{"total": total, "offset": start, "jobs": filtered[start:end]})
}

type facetCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func (s *Server) handleFacets(w http.ResponseWriter, r *http.Request) {
	all, err := s.load(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	sen := map[string]int{}
	kw := map[string]int{}
	statuses := map[string]int{}
	global, contractor, companies := 0, 0, map[string]bool{}
	for _, j := range all {
		if j.Status != "" {
			statuses[j.Status]++
		}
		if j.Status == storage.StatusHidden {
			continue
		}
		sen[j.Seniority]++
		for _, k := range j.Keywords {
			kw[strings.ToLower(k)]++
		}
		if j.Global {
			global++
		}
		if j.Contractor {
			contractor++
		}
		companies[j.Domain] = true
	}
	out := map[string]any{
		"total":      len(all),
		"global":     global,
		"contractor": contractor,
		"companies":  len(companies),
		"seniority":  topCounts(sen, 0),
		"keywords":   topCounts(kw, 30),
		"statuses":   statuses,
	}
	if run, err := s.src.GetLastCrawlRun(r.Context()); err == nil && run != nil {
		out["last_run"] = run.CompletedAt
	}
	writeJSON(w, http.StatusOK, out)
}

func topCounts(m map[string]int, limit int) []facetCount {
	out := make([]facetCount, 0, len(m))
	for k, v := range m {
		if k != "" {
			out = append(out, facetCount{k, v})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if body.ID == "" || (body.Status != "" && !storage.ValidRoleStatus(body.Status)) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id or status"})
		return
	}
	if err := s.src.SetRoleStatus(r.Context(), body.ID, body.Status); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}
