package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"specter/internal/ats"
	"specter/internal/signals"

	_ "modernc.org/sqlite"
)

// Store encapsulates the SQLite database connection, migrations, and idempotency methods.
type Store struct {
	db *sql.DB
}

// NewStore initializes or opens the SQLite database and executes migrations.
func NewStore(dbPath string) (*Store, error) {
	if dbPath == "" {
		dbPath = "specter.db"
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed opening sqlite database: %w", err)
	}

	// Set connection pool limits for SQLite single-file write safety
	db.SetMaxOpenConns(1)

	schema := `
	CREATE TABLE IF NOT EXISTS companies (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		domain TEXT UNIQUE NOT NULL,
		name TEXT NOT NULL,
		careers_url TEXT,
		github_org TEXT,
		signals TEXT,
		linkedin_url TEXT DEFAULT '',
		hq_location TEXT DEFAULT '',
		scanned_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS roles (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		company_id INTEGER NOT NULL,
		external_id TEXT UNIQUE NOT NULL,
		title TEXT NOT NULL,
		url TEXT NOT NULL,
		location TEXT,
		department TEXT,
		seniority TEXT,
		keywords TEXT,
		discovered_at DATETIME NOT NULL,
		FOREIGN KEY(company_id) REFERENCES companies(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS leads (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		company_id INTEGER NOT NULL,
		name TEXT NOT NULL,
		email TEXT,
		github_username TEXT,
		profile_url TEXT,
		role_title TEXT,
		source TEXT,
		contacted BOOLEAN DEFAULT 0,
		discovered_at DATETIME NOT NULL,
		UNIQUE(company_id, github_username, email),
		FOREIGN KEY(company_id) REFERENCES companies(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS engineering_leads (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		domain TEXT NOT NULL,
		company_domain TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL,
		role TEXT NOT NULL,
		email TEXT NOT NULL,
		source TEXT NOT NULL,
		github_handle TEXT NOT NULL DEFAULT '',
		top_languages TEXT,
		relevance_score INTEGER DEFAULT 0,
		discovered_at DATETIME NOT NULL,
		contacted BOOLEAN DEFAULT 0,
		repo_name TEXT DEFAULT '',
		repo_url TEXT DEFAULT '',
		commit_sha TEXT DEFAULT '',
		commit_url TEXT DEFAULT '',
		bio TEXT DEFAULT '',
		website_url TEXT DEFAULT '',
		linkedin_url TEXT DEFAULT '',
		location TEXT DEFAULT '',
		matched_signals TEXT DEFAULT '[]',
		UNIQUE(company_domain, github_handle)
	);

	CREATE TABLE IF NOT EXISTS crawl_frontier (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		domain TEXT NOT NULL,
		target_url TEXT UNIQUE NOT NULL,
		entity_type TEXT NOT NULL,
		status TEXT NOT NULL,
		last_crawled_at DATETIME NOT NULL,
		error_message TEXT
	);

	CREATE INDEX IF NOT EXISTS idx_roles_company ON roles(company_id);
	CREATE INDEX IF NOT EXISTS idx_leads_company ON leads(company_id);
	CREATE INDEX IF NOT EXISTS idx_leads_contacted ON leads(contacted);
	CREATE INDEX IF NOT EXISTS idx_eng_leads_domain ON engineering_leads(domain);
	CREATE INDEX IF NOT EXISTS idx_eng_leads_comp ON engineering_leads(company_domain);
	CREATE INDEX IF NOT EXISTS idx_eng_leads_score ON engineering_leads(relevance_score);
	CREATE INDEX IF NOT EXISTS idx_frontier_domain ON crawl_frontier(domain);
	CREATE INDEX IF NOT EXISTS idx_frontier_url ON crawl_frontier(target_url);
	`

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed executing schema migration: %w", err)
	}

	// Runtime migrations for existing SQLite databases
	_, _ = db.Exec("ALTER TABLE companies ADD COLUMN linkedin_url TEXT DEFAULT ''")
	_, _ = db.Exec("ALTER TABLE companies ADD COLUMN hq_location TEXT DEFAULT ''")

	leadCols := []string{
		"repo_name TEXT DEFAULT ''",
		"repo_url TEXT DEFAULT ''",
		"commit_sha TEXT DEFAULT ''",
		"commit_url TEXT DEFAULT ''",
		"bio TEXT DEFAULT ''",
		"website_url TEXT DEFAULT ''",
		"linkedin_url TEXT DEFAULT ''",
		"location TEXT DEFAULT ''",
		"matched_signals TEXT DEFAULT '[]'",
	}
	for _, col := range leadCols {
		_, _ = db.Exec("ALTER TABLE engineering_leads ADD COLUMN " + col)
	}

	// Purge any historical bot / CI accounts in persistent store
	_, _ = db.Exec(`DELETE FROM engineering_leads WHERE
		name LIKE '%[bot]%' OR github_handle LIKE '%[bot]%' OR github_handle LIKE '%bot%' OR github_handle LIKE 'bot-%' OR github_handle LIKE 'bot_%' OR
		email LIKE '%teamcity%' OR email LIKE '%actions@%' OR email LIKE '%noreply%' OR email LIKE '%jenkins%' OR email LIKE '%circleci%' OR email LIKE '%buildkite%' OR email LIKE '%dependabot%' OR email LIKE '%sentry%' OR email LIKE '%codecov%'
	`)

	return &Store{db: db}, nil
}

// DB returns the underlying *sql.DB instance.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// SaveCompanyAndRoles idempotently persists company meta and roles.
// Returns company ID and the count of newly inserted roles.
func (s *Store) SaveCompanyAndRoles(ctx context.Context, meta *ats.CompanyMeta) (int64, int, error) {
	now := time.Now().UTC()
	signalsCSV := strings.Join(meta.Signals, ",")

	// Enrich company metadata defaults if not yet populated
	if meta.LinkedInURL == "" {
		if meta.GitHubOrg != "" {
			meta.LinkedInURL = "https://www.linkedin.com/company/" + meta.GitHubOrg
		} else {
			dom := strings.TrimSuffix(meta.Domain, ".com")
			meta.LinkedInURL = "https://www.linkedin.com/company/" + dom
		}
	}
	if meta.HQLocation == "" {
		locCounts := make(map[string]int)
		for _, r := range meta.OpenRoles {
			loc := strings.TrimSpace(r.Location)
			if loc != "" && !strings.EqualFold(loc, "remote") {
				locCounts[loc]++
			}
		}
		bestLoc := ""
		maxC := 0
		for l, c := range locCounts {
			if c > maxC {
				maxC = c
				bestLoc = l
			}
		}
		meta.HQLocation = bestLoc
	}

	// Upsert company
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO companies (domain, name, careers_url, github_org, signals, linkedin_url, hq_location, scanned_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(domain) DO UPDATE SET
			name = excluded.name,
			careers_url = excluded.careers_url,
			github_org = COALESCE(NULLIF(excluded.github_org, ''), companies.github_org),
			signals = excluded.signals,
			linkedin_url = COALESCE(NULLIF(excluded.linkedin_url, ''), companies.linkedin_url),
			hq_location = COALESCE(NULLIF(excluded.hq_location, ''), companies.hq_location),
			scanned_at = excluded.scanned_at
	`, meta.Domain, meta.Name, meta.CareersURL, meta.GitHubOrg, signalsCSV, meta.LinkedInURL, meta.HQLocation, now)
	if err != nil {
		return 0, 0, fmt.Errorf("failed saving company: %w", err)
	}

	companyID, _ := res.LastInsertId()
	if companyID == 0 {
		err = s.db.QueryRowContext(ctx, "SELECT id FROM companies WHERE domain = ?", meta.Domain).Scan(&companyID)
		if err != nil {
			return 0, 0, fmt.Errorf("failed retrieving company id: %w", err)
		}
	}

	newRolesCount := 0
	for _, r := range meta.OpenRoles {
		kwStr := strings.Join(r.Keywords, ",")
		rRes, err := s.db.ExecContext(ctx, `
			INSERT OR IGNORE INTO roles (company_id, external_id, title, url, location, department, seniority, keywords, discovered_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, companyID, r.ID, r.Title, r.URL, r.Location, r.Department, r.Seniority, kwStr, now)
		if err == nil {
			rowsAffected, _ := rRes.RowsAffected()
			if rowsAffected > 0 {
				newRolesCount++
			}
		}
	}

	return companyID, newRolesCount, nil
}

// SaveLeads idempotently inserts discovered legacy leads.
func (s *Store) SaveLeads(ctx context.Context, companyID int64, leads []signals.Lead) (int, error) {
	now := time.Now().UTC()
	newLeadsCount := 0

	for _, l := range leads {
		res, err := s.db.ExecContext(ctx, `
			INSERT OR IGNORE INTO leads (company_id, name, email, github_username, profile_url, role_title, source, contacted, discovered_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?)
		`, companyID, l.Name, l.Email, l.GitHubUsername, l.ProfileURL, l.RoleTitle, l.Source, now)
		if err == nil {
			rowsAffected, _ := res.RowsAffected()
			if rowsAffected > 0 {
				newLeadsCount++
			}
		}
	}

	return newLeadsCount, nil
}

// SaveEngineeringLeads idempotently persists discovered engineering leads.
func (s *Store) SaveEngineeringLeads(ctx context.Context, leads []signals.EngineeringLead) (int, error) {
	now := time.Now().UTC()
	newCount := 0

	for _, l := range leads {
		ghHandle := l.GitHubHandle
		if ghHandle == "" {
			ghHandle = strings.Split(l.Email, "@")[0]
		}
		if signals.IsBotOrCI(l.Name, ghHandle, l.Email) {
			continue
		}

		compDomain := l.CompanyDomain
		if compDomain == "" {
			compDomain = l.Domain
		}
		if compDomain == "" {
			compDomain = "unknown"
		}

		signalsJSON, _ := json.Marshal(l.MatchedSignals)
		if len(signalsJSON) == 0 {
			signalsJSON = []byte("[]")
		}

		res, err := s.db.ExecContext(ctx, `
			INSERT INTO engineering_leads 
				(domain, company_domain, name, role, email, source, github_handle, top_languages, relevance_score, discovered_at, contacted,
				 repo_name, repo_url, commit_sha, commit_url, bio, website_url, linkedin_url, location, matched_signals)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(company_domain, github_handle) DO UPDATE SET
				domain = excluded.domain,
				name = excluded.name,
				role = excluded.role,
				email = excluded.email,
				top_languages = excluded.top_languages,
				relevance_score = MAX(engineering_leads.relevance_score, excluded.relevance_score),
				discovered_at = excluded.discovered_at,
				repo_name = COALESCE(NULLIF(excluded.repo_name, ''), engineering_leads.repo_name),
				repo_url = COALESCE(NULLIF(excluded.repo_url, ''), engineering_leads.repo_url),
				commit_sha = COALESCE(NULLIF(excluded.commit_sha, ''), engineering_leads.commit_sha),
				commit_url = COALESCE(NULLIF(excluded.commit_url, ''), engineering_leads.commit_url),
				bio = COALESCE(NULLIF(excluded.bio, ''), engineering_leads.bio),
				website_url = COALESCE(NULLIF(excluded.website_url, ''), engineering_leads.website_url),
				linkedin_url = COALESCE(NULLIF(excluded.linkedin_url, ''), engineering_leads.linkedin_url),
				location = COALESCE(NULLIF(excluded.location, ''), engineering_leads.location),
				matched_signals = excluded.matched_signals
		`, compDomain, compDomain, l.Name, l.Role, l.Email, l.Source, ghHandle, l.TopLanguages, l.RelevanceScore, now,
			l.RepoName, l.RepoURL, l.CommitSHA, l.CommitURL, l.Bio, l.WebsiteURL, l.LinkedInURL, l.Location, string(signalsJSON))
		if err != nil {
			return newCount, fmt.Errorf("failed saving lead %s: %w", ghHandle, err)
		}
		rowsAffected, _ := res.RowsAffected()
		if rowsAffected > 0 {
			newCount++
		}
	}

	return newCount, nil
}

// ListEngineeringLeads retrieves engineering leads for a domain or all domains.
func (s *Store) ListEngineeringLeads(ctx context.Context, domain string, uncontactedOnly bool) ([]signals.EngineeringLead, error) {
	query := `
		SELECT id, domain, company_domain, name, role, email, source, github_handle, top_languages, relevance_score, discovered_at, contacted,
		       COALESCE(repo_name, ''), COALESCE(repo_url, ''), COALESCE(commit_sha, ''), COALESCE(commit_url, ''),
		       COALESCE(bio, ''), COALESCE(website_url, ''), COALESCE(linkedin_url, ''), COALESCE(location, ''),
		       COALESCE(matched_signals, '[]')
		FROM engineering_leads
		WHERE 1=1
	`
	var args []any
	if domain != "" {
		query += " AND (domain = ? OR company_domain = ?)"
		args = append(args, domain, domain)
	}
	if uncontactedOnly {
		query += " AND contacted = 0"
	}
	query += " ORDER BY relevance_score DESC, discovered_at DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var leads []signals.EngineeringLead
	for rows.Next() {
		var l signals.EngineeringLead
		var disc time.Time
		var sigsJSON string
		if err := rows.Scan(
			&l.ID, &l.Domain, &l.CompanyDomain, &l.Name, &l.Role, &l.Email, &l.Source,
			&l.GitHubHandle, &l.TopLanguages, &l.RelevanceScore, &disc, &l.Contacted,
			&l.RepoName, &l.RepoURL, &l.CommitSHA, &l.CommitURL,
			&l.Bio, &l.WebsiteURL, &l.LinkedInURL, &l.Location,
			&sigsJSON,
		); err != nil {
			return nil, err
		}
		l.DiscoveredAt = disc
		_ = json.Unmarshal([]byte(sigsJSON), &l.MatchedSignals)
		leads = append(leads, l)
	}

	return leads, nil
}

// MarkEngineeringLeadContacted updates the contacted status of an engineering lead.
func (s *Store) MarkEngineeringLeadContacted(ctx context.Context, leadID int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE engineering_leads SET contacted = 1 WHERE id = ?", leadID)
	return err
}

// ListLeads retrieves legacy leads.
func (s *Store) ListLeads(ctx context.Context, uncontactedOnly bool) ([]signals.Lead, error) {
	query := `
		SELECT id, company_id, name, email, github_username, profile_url, role_title, source, contacted, discovered_at
		FROM leads
	`
	if uncontactedOnly {
		query += " WHERE contacted = 0"
	}
	query += " ORDER BY discovered_at DESC"

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var leads []signals.Lead
	for rows.Next() {
		var l signals.Lead
		var disc time.Time
		if err := rows.Scan(
			&l.ID, &l.CompanyID, &l.Name, &l.Email, &l.GitHubUsername,
			&l.ProfileURL, &l.RoleTitle, &l.Source, &l.Contacted, &disc,
		); err != nil {
			return nil, err
		}
		l.DiscoveredAt = disc
		leads = append(leads, l)
	}

	return leads, nil
}

// MarkLeadContacted updates the contacted status of a legacy lead.
func (s *Store) MarkLeadContacted(ctx context.Context, leadID int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE leads SET contacted = 1 WHERE id = ?", leadID)
	return err
}

// GetCompany retrieves a company by domain.
func (s *Store) GetCompany(ctx context.Context, domain string) (*ats.CompanyMeta, error) {
	var name, careersURL, githubOrg, signalsCSV, linkedInURL, hqLocation string
	err := s.db.QueryRowContext(ctx, `
		SELECT name, careers_url, github_org, signals, COALESCE(linkedin_url, ''), COALESCE(hq_location, '')
		FROM companies
		WHERE domain = ? OR domain = ? OR domain LIKE ?
		ORDER BY scanned_at DESC LIMIT 1
	`, domain, strings.TrimPrefix(domain, "www."), "%"+domain+"%").Scan(&name, &careersURL, &githubOrg, &signalsCSV, &linkedInURL, &hqLocation)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var sigList []string
	if signalsCSV != "" {
		sigList = strings.Split(signalsCSV, ",")
	}

	return &ats.CompanyMeta{
		Name:        name,
		Domain:      domain,
		CareersURL:  careersURL,
		GitHubOrg:   githubOrg,
		Signals:     sigList,
		LinkedInURL: linkedInURL,
		HQLocation:  hqLocation,
	}, nil
}

// ListCompanies retrieves all scanned companies.
func (s *Store) ListCompanies(ctx context.Context) ([]ats.CompanyMeta, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, domain, name, careers_url, github_org, signals, COALESCE(linkedin_url, ''), COALESCE(hq_location, '')
		FROM companies
		ORDER BY scanned_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var companies []ats.CompanyMeta
	for rows.Next() {
		var id int64
		var domain, name, careersURL, githubOrg, signalsCSV, linkedInURL, hqLocation string
		if err := rows.Scan(&id, &domain, &name, &careersURL, &githubOrg, &signalsCSV, &linkedInURL, &hqLocation); err != nil {
			return nil, err
		}
		var sigList []string
		if signalsCSV != "" {
			sigList = strings.Split(signalsCSV, ",")
		}
		companies = append(companies, ats.CompanyMeta{
			Name:        name,
			Domain:      domain,
			CareersURL:  careersURL,
			GitHubOrg:   githubOrg,
			Signals:     sigList,
			LinkedInURL: linkedInURL,
			HQLocation:  hqLocation,
		})
	}

	return companies, nil
}

// ListRolesForCompany retrieves open backend positions for a specific company domain.
func (s *Store) ListRolesForCompany(ctx context.Context, domain string) ([]ats.JobPosting, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.id, r.external_id, r.title, r.url, r.location, r.department, r.seniority, r.keywords, r.discovered_at
		FROM roles r
		JOIN companies c ON r.company_id = c.id
		WHERE c.domain = ? OR c.domain LIKE ?
		ORDER BY r.discovered_at DESC
	`, domain, "%"+domain+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var roles []ats.JobPosting
	for rows.Next() {
		var r ats.JobPosting
		var internalID int64
		var kwCSV string
		if err := rows.Scan(&internalID, &r.ID, &r.Title, &r.URL, &r.Location, &r.Department, &r.Seniority, &kwCSV, &r.PostedAt); err != nil {
			return nil, err
		}
		if kwCSV != "" {
			r.Keywords = strings.Split(kwCSV, ",")
		}
		roles = append(roles, r)
	}

	return roles, nil
}

// ListRoles retrieves open roles matching an optional keyword.
func (s *Store) ListRoles(ctx context.Context, keyword string) ([]ats.JobPosting, error) {
	query := `SELECT id, external_id, title, url, location, department, seniority, keywords, discovered_at FROM roles`
	var args []any
	if keyword != "" {
		query += ` WHERE title LIKE ? OR keywords LIKE ?`
		pattern := "%" + keyword + "%"
		args = append(args, pattern, pattern)
	}
	query += ` ORDER BY discovered_at DESC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var roles []ats.JobPosting
	for rows.Next() {
		var r ats.JobPosting
		var internalID int64
		var kwCSV string
		if err := rows.Scan(&internalID, &r.ID, &r.Title, &r.URL, &r.Location, &r.Department, &r.Seniority, &kwCSV, &r.PostedAt); err != nil {
			return nil, err
		}
		if kwCSV != "" {
			r.Keywords = strings.Split(kwCSV, ",")
		}
		roles = append(roles, r)
	}

	return roles, nil
}
