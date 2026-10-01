package reporter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"specter/internal/ats"
	"specter/internal/signals"
	"specter/internal/storage"
)

// GenerateDeltaReport compares the current run against the preceding crawl run,
// isolating brand-new open positions and fresh engineering leads.
func GenerateDeltaReport(
	currentRun *storage.CrawlRun,
	prevRun *storage.CrawlRun,
	freshRoles []ats.JobPosting,
	freshLeads []signals.EngineeringLead,
	outDir string,
) (string, string, error) {
	if outDir == "" {
		outDir = "reports"
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "", "", fmt.Errorf("failed creating delta output directory: %w", err)
	}

	dateStr := time.Now().Format("2006-01-02")
	filePath := filepath.Join(outDir, fmt.Sprintf("delta_summary_%s.md", dateStr))

	var sb strings.Builder

	sb.WriteString("# 🔄 Autonomous Recon Delta Report\n\n")
	sb.WriteString(fmt.Sprintf("> **Current Run:** `%s` | **Completed At:** %s\n\n",
		currentRun.RunID, currentRun.CompletedAt.Format("2006-01-02 15:04:05 UTC")))

	sb.WriteString("### 📊 Run Comparison & Yield Delta\n\n")
	sb.WriteString("| Metric | Previous Crawl Run | Current Crawl Run | Delta (Net Growth) |\n")
	sb.WriteString("| :--- | :---: | :---: | :---: |\n")

	if prevRun != nil {
		sb.WriteString(fmt.Sprintf("| **Run Identifier** | `%s` | `%s` | - |\n", prevRun.RunID, currentRun.RunID))
		sb.WriteString(fmt.Sprintf("| **Companies Scanned** | %d | %d | %+d |\n",
			prevRun.TotalCompanies, currentRun.TotalCompanies, currentRun.TotalCompanies-prevRun.TotalCompanies))
		sb.WriteString(fmt.Sprintf("| **Active Roles Tracked** | %d | %d | %+d |\n",
			prevRun.TotalRoles, currentRun.TotalRoles, currentRun.TotalRoles-prevRun.TotalRoles))
		sb.WriteString(fmt.Sprintf("| **Brand New Roles** | %d | %d | **+%d fresh** |\n",
			prevRun.NewRoles, currentRun.NewRoles, currentRun.NewRoles))
		sb.WriteString(fmt.Sprintf("| **Engineering Leads** | %d | %d | %+d |\n",
			prevRun.TotalLeads, currentRun.TotalLeads, currentRun.TotalLeads-prevRun.TotalLeads))
		sb.WriteString(fmt.Sprintf("| **Fresh Leads** | %d | %d | **+%d fresh** |\n\n",
			prevRun.NewLeads, currentRun.NewLeads, currentRun.NewLeads))
	} else {
		sb.WriteString("| **Run Identifier** | *(None - Baseline)* | `" + currentRun.RunID + "` | First Run |\n")
		sb.WriteString(fmt.Sprintf("| **Companies Scanned** | 0 | %d | +%d |\n", currentRun.TotalCompanies, currentRun.TotalCompanies))
		sb.WriteString(fmt.Sprintf("| **Active Roles Tracked** | 0 | %d | +%d |\n", currentRun.TotalRoles, currentRun.TotalRoles))
		sb.WriteString(fmt.Sprintf("| **Brand New Roles** | 0 | %d | **+%d fresh** |\n", currentRun.NewRoles, currentRun.NewRoles))
		sb.WriteString(fmt.Sprintf("| **Engineering Leads** | 0 | %d | +%d |\n", currentRun.TotalLeads, currentRun.TotalLeads))
		sb.WriteString(fmt.Sprintf("| **Fresh Leads** | 0 | %d | **+%d fresh** |\n\n", currentRun.NewLeads, currentRun.NewLeads))
	}

	sb.WriteString("---\n\n")

	// Section 1: Fresh Roles Discovered in this Run
	sb.WriteString(fmt.Sprintf("## 🌟 Newly Discovered Positions (%d Fresh Openings)\n\n", len(freshRoles)))
	if len(freshRoles) == 0 {
		sb.WriteString("*No newly opened positions detected in this scan cycle. All existing positions are already cataloged.*\n\n")
	} else {
		sb.WriteString("| Company | Position Title | Remote Policy | Compensation | Direct Apply Link |\n")
		sb.WriteString("| :--- | :--- | :--- | :--- | :---: |\n")
		for _, r := range freshRoles {
			applyURL := r.ApplyURL
			if applyURL == "" {
				applyURL = r.URL
			}
			comp := r.Compensation
			if comp == "" {
				comp = "Market"
			}
			domain := r.CompanyDomain
			if domain == "" {
				domain = extractDomainFromURL(r.URL)
			}
			sb.WriteString(fmt.Sprintf("| `%s` | **%s**<br><sub>📍 %s</sub> | %s | %s | [**Apply Directly ↗**](%s) |\n",
				domain, r.Title, r.Location, r.RemotePolicy, comp, applyURL))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("---\n\n")

	// Section 2: Fresh Engineering Leads Discovered
	sb.WriteString(fmt.Sprintf("## 👤 Newly Sourced Engineering Leads (%d Leads)\n\n", len(freshLeads)))
	if len(freshLeads) == 0 {
		sb.WriteString("*No newly uncovered engineering leads in this scan cycle.*\n\n")
	} else {
		sb.WriteString("| Name | Role Archetype | GitHub / Bio | Relevance | Source Repo |\n")
		sb.WriteString("| :--- | :--- | :--- | :---: | :--- |\n")
		for _, l := range freshLeads {
			ghLink := fmt.Sprintf("[%s](https://github.com/%s)", l.GitHubHandle, l.GitHubHandle)
			repoLink := fmt.Sprintf("[%s](%s)", l.RepoName, l.RepoURL)
			sb.WriteString(fmt.Sprintf("| **%s** | `%s` | %s | `%d/100` | %s |\n",
				l.Name, l.Role, ghLink, l.RelevanceScore, repoLink))
		}
		sb.WriteString("\n")
	}

	content := sb.String()
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return "", "", fmt.Errorf("failed writing delta report: %w", err)
	}

	return filePath, content, nil
}
