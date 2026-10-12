// Package scanner orchestrates the per-company pipeline: ATS ingestion,
// optional GitHub contributor mining, and dossier generation.
package scanner

import (
	"context"
	"fmt"
	"strings"
	"time"

	"specter/internal/ats"
	"specter/internal/seeds"
	"specter/internal/signals"
)

// Ingester fetches a company's open roles from its ATS.
type Ingester interface {
	ExtractBySeed(ctx context.Context, seed seeds.SeedTarget) (*ats.CompanyMeta, error)
}

// Miner discovers engineering leads from a GitHub organization.
type Miner interface {
	MineOrganization(ctx context.Context, org, companyDomain string, onProgress func(msg string)) ([]signals.EngineeringLead, error)
}

// Frontier tracks what was crawled recently.
type Frontier interface {
	ShouldCrawl(rawURL string, ttl time.Duration) (bool, error)
	ShouldCrawlDomain(domain string, ttl time.Duration) (bool, error)
	MarkVisited(rawURL, entityType string, crawlErr error) error
}

// Store persists discovered companies, roles and leads.
type Store interface {
	SaveCompanyAndRoles(ctx context.Context, meta *ats.CompanyMeta) (int64, int, error)
	SaveEngineeringLeads(ctx context.Context, leads []signals.EngineeringLead) (int, error)
}

// Observer receives progress events. All methods must be safe for concurrent use.
type Observer interface {
	SetCurrentTarget(name string)
	IncCrawled()
	IncLeads(n int)
	IncEmails(n int)
	Log(format string, args ...any)
}

// Scanner runs the pipeline for individual seeds.
type Scanner struct {
	Ingester Ingester
	Miner    Miner
	Frontier Frontier
	Store    Store
	Observer Observer
	// Report writes the dossier for a domain; optional.
	Report func(ctx context.Context, domain string) (string, error)
	// MineLeads gates GitHub contributor mining (opt-in).
	MineLeads bool
	// TTL is the frontier dedup window; 0 forces a refresh.
	TTL time.Duration
}

// Result summarizes the outcome of processing one seed.
type Result struct {
	Skipped  bool
	NewRoles int
	NewLeads int
}

// ProcessSeed runs the full pipeline for one seed. Non-fatal problems are
// reported through the Observer; the returned error is reserved for failures
// that make the result unusable.
func (s *Scanner) ProcessSeed(ctx context.Context, seed seeds.SeedTarget) (Result, error) {
	var res Result
	s.Observer.SetCurrentTarget(seed.ATSBoardID)
	apiURL := SeedAPIURL(seed)

	crawlDomain, domErr := s.Frontier.ShouldCrawlDomain(seed.Domain, s.TTL)
	crawlAPI, apiErr := s.Frontier.ShouldCrawl(apiURL, s.TTL)
	if domErr != nil || apiErr != nil {
		s.Observer.Log("⚠️  Frontier check failed for %s (domain: %v, api: %v); crawling anyway", seed.Name, domErr, apiErr)
		crawlDomain, crawlAPI = true, true
	}

	if !crawlDomain && !crawlAPI {
		s.Observer.IncCrawled()
		s.Observer.Log("♻️  [FRONTIER] %s (%s) crawled recently. Skipping.", seed.Name, seed.Domain)
		s.report(ctx, seed.Domain)
		res.Skipped = true
		return res, nil
	}

	meta, err := s.Ingester.ExtractBySeed(ctx, seed)
	if mErr := s.Frontier.MarkVisited(apiURL, "ats_"+seed.ATSService, err); mErr != nil {
		s.Observer.Log("⚠️  Frontier update failed for %s: %v", seed.Name, mErr)
	}
	s.Observer.IncCrawled()

	switch {
	case err != nil:
		s.Observer.Log("⚠️  ATS extraction error for %s (%s): %v", seed.Name, seed.ATSBoardID, err)
	case meta != nil:
		if seed.GitHubOrg != "" && meta.GitHubOrg == "" {
			meta.GitHubOrg = seed.GitHubOrg
		}
		_, newRoles, saveErr := s.Store.SaveCompanyAndRoles(ctx, meta)
		if saveErr != nil {
			s.Observer.Log("⚠️  Database write error for %s: %v", seed.Name, saveErr)
		} else {
			res.NewRoles = newRoles
			s.Observer.Log("✅ %s: %d backend roles matching signals (%d new)", seed.Name, len(meta.OpenRoles), newRoles)
		}
	}

	if meta == nil {
		meta = &ats.CompanyMeta{Name: seed.Name, Domain: seed.Domain, GitHubOrg: seed.GitHubOrg}
	}

	org := seed.GitHubOrg
	if org == "" {
		org = meta.GitHubOrg
	}
	if s.MineLeads && org != "" && ctx.Err() == nil {
		res.NewLeads = s.mine(ctx, org, seed.Domain)
	}

	s.report(ctx, seed.Domain)
	return res, nil
}

func (s *Scanner) mine(ctx context.Context, org, domain string) int {
	key := "github:" + org
	shouldMine, err := s.Frontier.ShouldCrawl(key, s.TTL)
	if err != nil {
		s.Observer.Log("⚠️  Frontier check failed for @%s: %v; mining anyway", org, err)
		shouldMine = true
	}
	if !shouldMine {
		s.Observer.Log("♻️  [FRONTIER] GitHub org @%s visited recently.", org)
		return 0
	}

	leads, mineErr := s.Miner.MineOrganization(ctx, org, domain, func(msg string) {
		s.Observer.Log("   [%s] %s", org, msg)
	})
	if mErr := s.Frontier.MarkVisited(key, "github_org", mineErr); mErr != nil {
		s.Observer.Log("⚠️  Frontier update failed for @%s: %v", org, mErr)
	}
	if mineErr != nil {
		s.Observer.Log("⚠️  GitHub mining warning for @%s: %v", org, mineErr)
		return 0
	}

	emails := 0
	for _, l := range leads {
		if l.Email != "" {
			emails++
		}
	}
	newLeads, saveErr := s.Store.SaveEngineeringLeads(ctx, leads)
	if saveErr != nil {
		s.Observer.Log("⚠️  Database write error for leads of @%s: %v", org, saveErr)
	}
	s.Observer.IncLeads(len(leads))
	s.Observer.IncEmails(emails)
	if len(leads) > 0 {
		s.Observer.Log("🎯 @%s: Discovered %d leads (%d verified emails, %d new saved)", org, len(leads), emails, newLeads)
	}
	return newLeads
}

func (s *Scanner) report(ctx context.Context, domain string) {
	if s.Report == nil {
		return
	}
	path, err := s.Report(ctx, domain)
	if err != nil {
		s.Observer.Log("⚠️  Dossier generation failed for %s: %v", domain, err)
		return
	}
	if path != "" {
		s.Observer.Log("📄 Saved dossier: %s", path)
	}
}

// SeedAPIURL returns the canonical API URL used as the frontier key for a seed.
func SeedAPIURL(seed seeds.SeedTarget) string {
	switch strings.ToLower(seed.ATSService) {
	case "greenhouse":
		return fmt.Sprintf("https://boards-api.greenhouse.io/v1/boards/%s/jobs", seed.ATSBoardID)
	case "lever":
		return fmt.Sprintf("https://api.lever.co/v0/postings/%s", seed.ATSBoardID)
	case "ashby":
		return fmt.Sprintf("https://api.ashbyhq.com/posting-api/job-board/%s", seed.ATSBoardID)
	default:
		return "https://" + seed.Domain
	}
}
