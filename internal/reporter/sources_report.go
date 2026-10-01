package reporter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"specter/internal/signals"
	"specter/internal/storage"
)

// GenerateSourcesReport produces an aggregated source analytics and lead yield report at outDir/sources_analytics_{date}.md.
func GenerateSourcesReport(ctx context.Context, store *storage.Store, yieldStats []*signals.RepoYieldStats, outDir string) (string, error) {
	if outDir == "" {
		outDir = "reports"
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "", fmt.Errorf("failed creating report output directory: %w", err)
	}

	dateStr := time.Now().Format("2006-01-02")
	fileName := fmt.Sprintf("sources_analytics_%s.md", dateStr)
	filePath := filepath.Join(outDir, fileName)

	// Fetch all stored leads to compute lineage and domain coverage metrics
	var leads []signals.EngineeringLead
	if store != nil {
		var err error
		leads, err = store.ListEngineeringLeads(ctx, "", false)
		if err != nil {
			leads = nil
		}
	}

	// Compute totals from yieldStats
	totalRepos := len(yieldStats)
	totalCommitsScanned := 0
	totalYieldLeads := 0
	totalYieldEmails := 0
	for _, s := range yieldStats {
		totalCommitsScanned += s.CommitsScanned
		totalYieldLeads += s.VerifiedLeads
		totalYieldEmails += s.ExtractedEmails
	}

	totalDbLeads := len(leads)
	totalDbEmails := 0
	leadsWithProvenance := 0
	leadsWithLinkedIn := 0
	archetypeCounts := make(map[string]int)
	domainCounts := make(map[string]int)

	for _, l := range leads {
		if l.Email != "" {
			totalDbEmails++
		}
		if l.CommitSHA != "" || l.RepoName != "" {
			leadsWithProvenance++
		}
		if l.LinkedInURL != "" {
			leadsWithLinkedIn++
		}
		role := l.Role
		if role == "" {
			role = "Core Contributor"
		}
		archetypeCounts[role]++

		dom := l.CompanyDomain
		if dom == "" {
			dom = l.Domain
		}
		if dom == "" {
			dom = "unspecified"
		}
		domainCounts[dom]++
	}

	// Effective totals (prefer DB leads if populated, else yield counts)
	effectiveLeads := totalDbLeads
	if effectiveLeads == 0 {
		effectiveLeads = totalYieldLeads
	}
	effectiveEmails := totalDbEmails
	if effectiveEmails == 0 {
		effectiveEmails = totalYieldEmails
	}

	emailYieldRate := 0.0
	if effectiveLeads > 0 {
		emailYieldRate = (float64(effectiveEmails) / float64(effectiveLeads)) * 100.0
	}

	var b strings.Builder

	// Title & Summary
	b.WriteString("# Source Analytics & Engineering Lead Yield Report\n\n")
	b.WriteString(fmt.Sprintf("**Date Generated:** %s  \n", time.Now().Format("January 02, 2006 15:04 MST")))
	b.WriteString(fmt.Sprintf("**Total Repositories Scanned:** %d  \n", totalRepos))
	b.WriteString(fmt.Sprintf("**Total Commits Inspected:** %d  \n", totalCommitsScanned))
	b.WriteString(fmt.Sprintf("**Total Verified Leads:** %d  \n", effectiveLeads))
	b.WriteString(fmt.Sprintf("**Total Direct Contact Emails:** %d (%.1f%% direct reachability)  \n", effectiveEmails, emailYieldRate))
	b.WriteString(fmt.Sprintf("**Full Lineage Provenance Rate:** %d/%d (%.1f%%)  \n",
		leadsWithProvenance, effectiveLeads, safePct(leadsWithProvenance, effectiveLeads)))
	b.WriteString(fmt.Sprintf("**Profile & LinkedIn Enrichment Rate:** %d/%d (%.1f%%)  \n\n",
		leadsWithLinkedIn, effectiveLeads, safePct(leadsWithLinkedIn, effectiveLeads)))

	b.WriteString("---\n\n")

	// Section 1: Top Producing Repositories
	b.WriteString("## 1. Top Producing Repositories\n\n")
	sortedStats := make([]*signals.RepoYieldStats, len(yieldStats))
	copy(sortedStats, yieldStats)
	sort.Slice(sortedStats, func(i, j int) bool {
		if sortedStats[i].VerifiedLeads != sortedStats[j].VerifiedLeads {
			return sortedStats[i].VerifiedLeads > sortedStats[j].VerifiedLeads
		}
		if sortedStats[i].ExtractedEmails != sortedStats[j].ExtractedEmails {
			return sortedStats[i].ExtractedEmails > sortedStats[j].ExtractedEmails
		}
		return sortedStats[i].CommitsScanned > sortedStats[j].CommitsScanned
	})

	topLimit := 5
	if len(sortedStats) < topLimit {
		topLimit = len(sortedStats)
	}

	if topLimit == 0 {
		b.WriteString("_No repositories were scanned in this run._\n\n")
	} else {
		for i := 0; i < topLimit; i++ {
			s := sortedStats[i]
			repoDisplay := s.RepoName
			if s.RepoURL != "" {
				repoDisplay = fmt.Sprintf("[%s](%s)", s.RepoName, s.RepoURL)
			}
			stack := s.PrimaryStack
			if stack == "" {
				stack = "Go / Backend"
			}
			b.WriteString(fmt.Sprintf("%d. **%s** (`%s`): **%d** verified leads, **%d** valid emails (%d commits scanned)\n",
				i+1, repoDisplay, stack, s.VerifiedLeads, s.ExtractedEmails, s.CommitsScanned))
		}
		b.WriteString("\n")
	}

	b.WriteString("---\n\n")

	// Section 2: Repository Yield Table
	b.WriteString("## 2. Repository Yield Breakdown\n\n")
	if len(sortedStats) == 0 {
		b.WriteString("_No repository yield metrics recorded._\n\n")
	} else {
		b.WriteString("| Repository | Total Commits Scanned | Verified Leads | Extracted Emails | Primary Stack | Yield Rate |\n")
		b.WriteString("| :--- | :---: | :---: | :---: | :--- | :---: |\n")
		for _, s := range sortedStats {
			repoDisplay := s.RepoName
			if s.RepoURL != "" {
				repoDisplay = fmt.Sprintf("[%s](%s)", s.RepoName, s.RepoURL)
			}
			stack := s.PrimaryStack
			if stack == "" {
				stack = "Go / Backend"
			}
			yieldRate := 0.0
			if s.CommitsScanned > 0 {
				yieldRate = (float64(s.VerifiedLeads) / float64(s.CommitsScanned)) * 100.0
			}
			b.WriteString(fmt.Sprintf("| %s | %d | %d | %d | `%s` | %.1f%% |\n",
				repoDisplay, s.CommitsScanned, s.VerifiedLeads, s.ExtractedEmails, stack, yieldRate))
		}
		b.WriteString("\n")
	}

	b.WriteString("---\n\n")

	// Section 3: Lead Lineage & Role Archetype Distribution
	b.WriteString("## 3. Lead Lineage & Role Archetype Distribution\n\n")
	if len(archetypeCounts) == 0 {
		b.WriteString("_No role archetype classifications recorded._\n\n")
	} else {
		type rolePair struct {
			role  string
			count int
		}
		var rolesList []rolePair
		for r, c := range archetypeCounts {
			rolesList = append(rolesList, rolePair{role: r, count: c})
		}
		sort.Slice(rolesList, func(i, j int) bool {
			return rolesList[i].count > rolesList[j].count
		})

		b.WriteString("| Archetype Tier | Lead Count | Share of Total |\n")
		b.WriteString("| :--- | :---: | :---: |\n")
		for _, rp := range rolesList {
			share := 0.0
			if effectiveLeads > 0 {
				share = (float64(rp.count) / float64(effectiveLeads)) * 100.0
			}
			b.WriteString(fmt.Sprintf("| `%s` | **%d** | %.1f%% |\n", rp.role, rp.count, share))
		}
		b.WriteString("\n")
	}

	// Section 4: Domain Coverage Summary
	b.WriteString("## 4. Target Domain Coverage\n\n")
	if len(domainCounts) == 0 {
		b.WriteString("_No domain leads stored._\n\n")
	} else {
		type domPair struct {
			domain string
			count  int
		}
		var domList []domPair
		for d, c := range domainCounts {
			domList = append(domList, domPair{domain: d, count: c})
		}
		sort.Slice(domList, func(i, j int) bool {
			return domList[i].count > domList[j].count
		})

		b.WriteString("| Target Domain | Leads Discovered |\n")
		b.WriteString("| :--- | :---: |\n")
		for _, dp := range domList {
			b.WriteString(fmt.Sprintf("| `%s` | **%d** |\n", dp.domain, dp.count))
		}
		b.WriteString("\n")
	}

	if err := os.WriteFile(filePath, []byte(b.String()), 0644); err != nil {
		return "", fmt.Errorf("failed writing sources report file: %w", err)
	}

	return filePath, nil
}

func safePct(part, total int) float64 {
	if total <= 0 {
		return 0.0
	}
	return (float64(part) / float64(total)) * 100.0
}
