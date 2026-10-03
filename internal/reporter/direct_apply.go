package reporter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"specter/internal/ats"
	"specter/internal/signals"
)

// GenerateDirectApplyReport builds a Markdown dashboard dedicated to one-click applications.
// It prioritizes worldwide, contractor-friendly, and sanction-resilient remote opportunities.
func GenerateDirectApplyReport(roles []ats.JobPosting, outDir string) (string, string, error) {
	if outDir == "" {
		outDir = "reports"
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "", "", fmt.Errorf("failed creating output directory: %w", err)
	}

	dateStr := time.Now().Format("2006-01-02")
	filePath := filepath.Join(outDir, fmt.Sprintf("direct_apply_%s.md", dateStr))

	var sb strings.Builder

	// Calculate counts
	var globalCount, timezoneCount, restrictedCount, newCount int
	for _, r := range roles {
		if r.GlobalRemote {
			globalCount++
		} else if strings.Contains(r.RemotePolicy, "Timezone Flexible") {
			timezoneCount++
		} else {
			restrictedCount++
		}
		if r.IsNew {
			newCount++
		}
	}

	sb.WriteString("# 🎯 Direct One-Click Apply Dashboard\n\n")
	sb.WriteString(fmt.Sprintf("> **Generated:** %s | **Total Positions Tracked:** %d | **Freshly Discovered:** %d\n\n",
		time.Now().Format("2006-01-02 15:04:05 UTC"), len(roles), newCount))

	sb.WriteString("### 🧭 Remote & Jurisdiction Breakdown\n\n")
	sb.WriteString(fmt.Sprintf("- 🟢 **Worldwide / Contractor-Friendly (Sanction-Resilient):** `%d` positions\n", globalCount))
	sb.WriteString(fmt.Sprintf("- 🟡 **Timezone-Flexible (EMEA/UTC Overlap):** `%d` positions\n", timezoneCount))
	sb.WriteString(fmt.Sprintf("- 🔴 **Geo-Restricted (Domestic US/EU / W-2 Only):** `%d` positions\n\n", restrictedCount))

	sb.WriteString("---\n\n")

	// Section 1: 🟢 High-Signal Worldwide & Contractor Opportunities
	sb.WriteString("## 🟢 Worldwide & Contractor-Friendly Positions (Zero Visa / B2B Ready)\n\n")
	sb.WriteString("These companies hire globally via **B2B Contractor / Deel / Remote.com**, avoiding US/EU domestic tax (W-2) and visa sponsorship barriers. Perfect for engineers in MENA, LATAM, Eastern Europe, and South Asia.\n\n")

	sb.WriteString("| Status | Tech Match | Company / Domain | Position Title | Remote Policy | Compensation | Direct Apply Link |\n")
	sb.WriteString("| :---: | :---: | :--- | :--- | :--- | :--- | :---: |\n")

	var globalRoles []ats.JobPosting
	var otherRoles []ats.JobPosting

	for _, r := range roles {
		if r.GlobalRemote {
			globalRoles = append(globalRoles, r)
		} else {
			otherRoles = append(otherRoles, r)
		}
	}

	if len(globalRoles) == 0 {
		sb.WriteString("| - | - | *No worldwide roles currently detected in this batch* | - | - | - | - |\n")
	} else {
		for _, r := range globalRoles {
			statusBadge := "Active"
			if r.IsNew {
				statusBadge = "**🔥 NEW**"
			}

			matchBadge := "🎯 -"
			if r.PersonalScore > 0 {
				matchBadge = fmt.Sprintf("🎯 %d%%", r.PersonalScore)
			}

			comp := r.Compensation
			if comp == "" {
				comp = "Competitive / Market"
			}

			applyURL := r.ApplyURL
			if applyURL == "" {
				applyURL = r.URL
			}

			loc := r.Location
			if loc == "" {
				loc = "Remote (Worldwide)"
			}

			companyName := r.CompanyDomain
			if companyName == "" {
				companyName = extractDomainFromURL(r.URL)
			}

			sb.WriteString(fmt.Sprintf("| %s | %s | `%s` | **%s**<br><sub>📍 %s</sub> | %s | %s | [**⚡ Apply Now ↗**](%s) |\n",
				statusBadge, matchBadge, companyName, r.Title, loc, r.RemotePolicy, comp, applyURL))
		}
	}

	sb.WriteString("\n---\n\n")

	// Section 2: Other Roles (Timezone Flexible or Regional)
	sb.WriteString("## 🟡 Timezone-Flexible & Regional Positions\n\n")
	sb.WriteString("| Status | Tech Match | Company | Position Title | Policy / Location | Direct Apply Link |\n")
	sb.WriteString("| :---: | :---: | :--- | :--- | :--- | :---: |\n")

	if len(otherRoles) == 0 {
		sb.WriteString("| - | - | *No regional roles in this batch* | - | - | - |\n")
	} else {
		for _, r := range otherRoles {
			statusBadge := "Active"
			if r.IsNew {
				statusBadge = "**🔥 NEW**"
			}
			matchBadge := "🎯 -"
			if r.PersonalScore > 0 {
				matchBadge = fmt.Sprintf("🎯 %d%%", r.PersonalScore)
			}
			applyURL := r.ApplyURL
			if applyURL == "" {
				applyURL = r.URL
			}
			companyName := r.CompanyDomain
			if companyName == "" {
				companyName = extractDomainFromURL(r.URL)
			}

			sb.WriteString(fmt.Sprintf("| %s | %s | `%s` | **%s** | %s<br><sub>📍 %s</sub> | [Apply Directly ↗](%s) |\n",
				statusBadge, matchBadge, companyName, r.Title, r.RemotePolicy, r.Location, applyURL))
		}
	}

	sb.WriteString("\n---\n\n")

	// Section 3: Copy-Paste Application Pitch Templates
	sb.WriteString("## ⚡ Fast-Track Outreach & Pitch Snippets\n\n")
	sb.WriteString("When filling the application form, add this targeted note under *'Additional Information'* or *'Cover Letter'* to eliminate contractor/visa concerns:\n\n")

	if len(globalRoles) > 0 {
		sampleRole := globalRoles[0]
		topTech := "Go & Distributed Systems"
		if len(sampleRole.Keywords) > 0 {
			limit := len(sampleRole.Keywords)
			if limit > 3 {
				limit = 3
			}
			topTech = strings.Join(sampleRole.Keywords[:limit], ", ")
		}
		company := sampleRole.CompanyName
		if company == "" {
			company = sampleRole.CompanyDomain
		}
		if company == "" {
			company = extractDomainFromURL(sampleRole.URL)
		}
		pitch := signals.GenerateQuickPitch(sampleRole.Title, company, topTech)
		sb.WriteString("### 📋 Sample Application Pitch (Global Remote / B2B Contractor)\n\n")
		sb.WriteString("```text\n")
		sb.WriteString(pitch)
		sb.WriteString("\n```\n\n")
	}

	sb.WriteString("### 💡 Why B2B Invoicing Solves Sanction & Visa Roadblocks\n\n")
	sb.WriteString("1. **Zero Visa Sponsorship Required:** Companies don't need H-1B, O-1, or local corporate entity setup.\n")
	sb.WriteString("2. **Compliant Global Payouts:** Modern tech companies route contracts through **Deel**, **Remote.com**, or international wire/crypto.\n")
	sb.WriteString("3. **Direct Form Deep-Links:** All links in the table above bypass generic marketing landing pages directly into Greenhouse (`#app`), Lever (`/apply`), or Ashby (`/application`).\n")

	content := sb.String()
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return "", "", fmt.Errorf("failed writing direct apply report: %w", err)
	}

	return filePath, content, nil
}

func extractDomainFromURL(rawURL string) string {
	parts := strings.Split(rawURL, "/")
	if len(parts) >= 3 {
		return parts[2]
	}
	return "unknown"
}
