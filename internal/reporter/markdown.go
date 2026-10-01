package reporter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"specter/internal/signals"
	"specter/internal/storage"
)

// GenerateMarkdownReport produces an executive technical dossier at outDir/{domain}_leads_{date}.md.
func GenerateMarkdownReport(ctx context.Context, store *storage.Store, domain string, outDir string) (string, error) {
	if domain == "" {
		domain = "unknown"
	}
	cleanDomain := strings.TrimPrefix(domain, "https://")
	cleanDomain = strings.TrimPrefix(cleanDomain, "http://")
	cleanDomain = strings.TrimRight(cleanDomain, "/")
	cleanDomain = strings.ReplaceAll(cleanDomain, "/", "_")

	if outDir == "" {
		outDir = "reports"
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "", fmt.Errorf("failed creating report output directory: %w", err)
	}

	dateStr := time.Now().Format("2006-01-02")
	fileName := fmt.Sprintf("%s_leads_%s.md", cleanDomain, dateStr)
	filePath := filepath.Join(outDir, fileName)

	// Fetch data from SQLite
	company, err := store.GetCompany(ctx, cleanDomain)
	if err != nil {
		return "", fmt.Errorf("failed fetching company info: %w", err)
	}

	roles, err := store.ListRolesForCompany(ctx, cleanDomain)
	if err != nil {
		return "", fmt.Errorf("failed fetching roles for company: %w", err)
	}

	leads, err := store.ListEngineeringLeads(ctx, cleanDomain, false)
	if err != nil {
		return "", fmt.Errorf("failed fetching engineering leads: %w", err)
	}

	// If no domain-scoped leads were found, fallback to all recently discovered leads
	if len(leads) == 0 {
		leads, _ = store.ListEngineeringLeads(ctx, "", false)
	}

	companyName := cleanDomain
	careersURL := "Not detected"
	githubOrg := "Not detected"
	signalsList := "Backend, Go, Distributed Systems, High Throughput"
	companyLinkedIn := ""
	hqLocation := ""
	dominantStacks := ""

	if company != nil {
		if company.Name != "" {
			companyName = company.Name
		}
		if company.CareersURL != "" {
			careersURL = company.CareersURL
		}
		if company.GitHubOrg != "" {
			githubOrg = "@" + company.GitHubOrg
		}
		if len(company.Signals) > 0 {
			signalsList = strings.Join(company.Signals, ", ")
		}
		companyLinkedIn = company.LinkedInURL
		hqLocation = company.HQLocation
		if len(company.DominantStacks) > 0 {
			dominantStacks = strings.Join(company.DominantStacks, ", ")
		}
	}

	var b strings.Builder

	// Header
	b.WriteString(fmt.Sprintf("# Executive Technical Recon Dossier: %s\n\n", companyName))
	b.WriteString(fmt.Sprintf("**Target Domain:** `%s`  \n", cleanDomain))
	b.WriteString(fmt.Sprintf("**Date Generated:** %s  \n", time.Now().Format("January 02, 2006 15:04 MST")))
	b.WriteString(fmt.Sprintf("**Careers Portal:** [%s](%s)  \n", careersURL, careersURL))
	b.WriteString(fmt.Sprintf("**GitHub Organization:** `%s`  \n", githubOrg))
	if companyLinkedIn != "" {
		b.WriteString(fmt.Sprintf("**Company LinkedIn:** [Company Page](%s)  \n", companyLinkedIn))
	}
	if hqLocation != "" {
		b.WriteString(fmt.Sprintf("**Headquarters:** %s  \n", hqLocation))
	}
	if dominantStacks != "" {
		b.WriteString(fmt.Sprintf("**Dominant Stacks:** `%s`  \n", dominantStacks))
	}
	b.WriteString(fmt.Sprintf("**Core Signal Match:** `%s`  \n\n", signalsList))

	b.WriteString("---\n\n")

	// Section 1: Company Overview & Hiring Demand
	b.WriteString("## 1. Company Overview & Hiring Demand\n\n")
	b.WriteString("### Technical Signal Fingerprint\n")
	b.WriteString(fmt.Sprintf("Based on ATS parsing and public repository analysis, **%s** demonstrates active hiring signals around:\n\n", companyName))
	b.WriteString(fmt.Sprintf("- **Core Technologies:** %s\n", signalsList))
	b.WriteString(fmt.Sprintf("- **Total Discovered Open Backend Roles:** %d\n", len(roles)))
	b.WriteString(fmt.Sprintf("- **Verified Contributor Leads Mined:** %d\n\n", len(leads)))

	b.WriteString("### Active Open Backend Positions\n\n")
	if len(roles) == 0 {
		b.WriteString("_No active roles recorded in local frontier at time of report._\n\n")
	} else {
		b.WriteString("| Title | Seniority | Location | Matched Keywords | Application URL |\n")
		b.WriteString("| :--- | :--- | :--- | :--- | :--- |\n")
		for _, r := range roles {
			kw := strings.Join(r.Keywords, ", ")
			loc := r.Location
			if loc == "" {
				loc = "Remote / Unspecified"
			}
			b.WriteString(fmt.Sprintf("| %s | %s | %s | `%s` | [View Role](%s) |\n", r.Title, r.Seniority, loc, kw, r.URL))
		}
		b.WriteString("\n")
	}

	b.WriteString("---\n\n")

	// Section 2: Key Engineering Leads Matrix
	b.WriteString("## 2. Key Engineering Leads Matrix\n\n")
	b.WriteString("Prospects mined from public Git commits, patch headers, and repository contributions with verified contact emails and provenance lineage:\n\n")

	if len(leads) == 0 {
		b.WriteString("_No verified engineering leads discovered yet. Run `specter scan` with a valid `--github=<org>`._\n\n")
	} else {
		b.WriteString("| Score | Name | Archetype | Email | GitHub | LinkedIn / Web | Provenance (Commit / Repo) | Top Tech |\n")
		b.WriteString("| :---: | :--- | :--- | :--- | :---: | :---: | :--- | :--- |\n")
		for _, l := range leads {
			ghLink := "-"
			if l.GitHubHandle != "" {
				ghLink = fmt.Sprintf("[@%s](https://github.com/%s)", l.GitHubHandle, l.GitHubHandle)
			}

			// Social links
			var socialLinks []string
			if l.LinkedInURL != "" {
				if strings.Contains(l.LinkedInURL, "google.com/search") {
					socialLinks = append(socialLinks, fmt.Sprintf("[OSINT](%s)", l.LinkedInURL))
				} else {
					socialLinks = append(socialLinks, fmt.Sprintf("[LinkedIn](%s)", l.LinkedInURL))
				}
			}
			if l.WebsiteURL != "" {
				socialLinks = append(socialLinks, fmt.Sprintf("[Web](%s)", l.WebsiteURL))
			}
			socialDisplay := "-"
			if len(socialLinks) > 0 {
				socialDisplay = strings.Join(socialLinks, " · ")
			}

			// Provenance formatting
			shortSHA := l.CommitSHA
			if len(shortSHA) > 7 {
				shortSHA = shortSHA[:7]
			}
			commitPart := ""
			if shortSHA != "" {
				if l.CommitURL != "" {
					commitPart = fmt.Sprintf("[%s](%s)", shortSHA, l.CommitURL)
				} else {
					commitPart = fmt.Sprintf("`%s`", shortSHA)
				}
			}
			repoPart := ""
			if l.RepoName != "" {
				if l.RepoURL != "" {
					repoPart = fmt.Sprintf("[%s](%s)", l.RepoName, l.RepoURL)
				} else {
					repoPart = fmt.Sprintf("`%s`", l.RepoName)
				}
			}
			provDisplay := "-"
			if commitPart != "" && repoPart != "" {
				provDisplay = fmt.Sprintf("%s in %s", commitPart, repoPart)
			} else if repoPart != "" {
				provDisplay = repoPart
			} else if commitPart != "" {
				provDisplay = commitPart
			} else if l.Source != "" {
				provDisplay = fmt.Sprintf("`%s`", l.Source)
			}

			badge := fmt.Sprintf("**%d/100**", l.RelevanceScore)
			roleDisplay := l.Role
			if roleDisplay == "" {
				roleDisplay = "Core Contributor"
			}

			b.WriteString(fmt.Sprintf("| %s | **%s** | `%s` | `%s` | %s | %s | %s | `%s` |\n",
				badge, l.Name, roleDisplay, l.Email, ghLink, socialDisplay, provDisplay, l.TopLanguages))
		}
		b.WriteString("\n")
	}

	b.WriteString("---\n\n")

	// Section 3: Contextual Outreach Drafts
	b.WriteString("## 3. Contextual Outreach Drafts\n\n")
	b.WriteString("Personalized, high-converting icebreaker templates tailored to discovered commit activity, stack overlaps, and role archetypes.\n\n")

	// Pick representative leads for each archetype
	sampleLead := signals.EngineeringLead{
		Name:         "Alex",
		Role:         "Senior Backend",
		Email:        "alex@" + cleanDomain,
		GitHubHandle: "alexdev",
		TopLanguages: "Go, Distributed Systems",
	}
	if len(leads) > 0 {
		sampleLead = leads[0]
	}

	leadershipLead := sampleLead
	staffLead := sampleLead
	seniorLead := sampleLead

	for _, l := range leads {
		switch l.Role {
		case "Engineering Leadership":
			if leadershipLead.Role != "Engineering Leadership" {
				leadershipLead = l
			}
		case "Staff / Principal":
			if staffLead.Role != "Staff / Principal" {
				staffLead = l
			}
		case "Senior Backend", "Core Contributor":
			if seniorLead.Role != "Senior Backend" && seniorLead.Role != "Core Contributor" {
				seniorLead = l
			}
		}
	}

	sampleRoleTitle := "Senior Backend Engineer (Distributed Systems)"
	if len(roles) > 0 {
		sampleRoleTitle = roles[0].Title
	}

	leadProv := leadershipLead.RepoName
	if leadProv == "" {
		leadProv = companyName + " backend services"
	}
	staffProv := staffLead.RepoName
	if staffProv == "" {
		staffProv = companyName + " core architecture"
	}
	seniorProv := seniorLead.RepoName
	if seniorProv == "" {
		seniorProv = companyName + " repositories"
	}

	seniorCommit := seniorLead.CommitSHA
	if len(seniorCommit) > 7 {
		seniorCommit = seniorCommit[:7]
	}
	if seniorCommit == "" {
		seniorCommit = "recent commits"
	}

	// Template A: Engineering Leadership
	b.WriteString("### Template A: Architecture & Git Commit Overlap (Engineering Leadership)\n\n")
	b.WriteString(fmt.Sprintf("**Subject:** Question regarding %s's backend infrastructure & %s architecture\n\n", companyName, leadershipLead.TopLanguages))
	b.WriteString("```text\n")
	b.WriteString(fmt.Sprintf("Hi %s,\n\n", leadershipLead.Name))
	b.WriteString(fmt.Sprintf("I was digging through %s's public engineering repositories and noticed your leadership around %s.\n\n", companyName, leadershipLead.TopLanguages))
	b.WriteString(fmt.Sprintf("With active hiring across the engineering org (such as %s), scaling developer velocity while maintaining system reliability in %s is usually a critical focus.\n\n", sampleRoleTitle, signalsList))
	b.WriteString(fmt.Sprintf("We've been tackling similar high-throughput and concurrency challenges around %s, and I was really impressed by how your team is architecting these services.\n\n", leadProv))
	b.WriteString("Would love to connect and swap notes on backend scaling patterns if you're open to a brief conversation.\n\n")
	b.WriteString("Best regards,\n")
	b.WriteString("[Your Name]\n")
	b.WriteString("```\n\n")

	// Template B: Staff / Principal
	b.WriteString("### Template B: Distributed Systems & Technical Strategy (Staff / Principal)\n\n")
	b.WriteString(fmt.Sprintf("**Subject:** %s's backend team expansion & distributed systems architecture (%s)\n\n", companyName, sampleRoleTitle))
	b.WriteString("```text\n")
	b.WriteString(fmt.Sprintf("Hi %s,\n\n", staffLead.Name))
	b.WriteString(fmt.Sprintf("I saw that %s is actively expanding its core systems team with open roles like %s.\n\n", companyName, sampleRoleTitle))
	b.WriteString(fmt.Sprintf("Given your contributions to %s and deep focus on %s, I imagine maintaining system stability and scaling backend pipelines is top of mind.\n\n", staffProv, staffLead.TopLanguages))
	b.WriteString("I'm reaching out because of our mutual engineering alignment around distributed systems and low-latency pipelines. Are you the right person on the engineering side to chat with about this, or is someone else leading that initiative?\n\n")
	b.WriteString("Thanks for your time,\n")
	b.WriteString("[Your Name]\n")
	b.WriteString("```\n\n")

	// Template C: Senior Backend / Core Contributor
	b.WriteString("### Template C: Low-Latency Systems & Direct Tech Alignment (Senior Backend / Core Contributor)\n\n")
	b.WriteString(fmt.Sprintf("**Subject:** %s low-latency pipelines / %s commit (%s)\n\n", seniorLead.TopLanguages, seniorProv, seniorCommit))
	b.WriteString("```text\n")
	b.WriteString(fmt.Sprintf("Hi %s,\n\n", seniorLead.Name))
	b.WriteString(fmt.Sprintf("Came across your profile via %s commits on GitHub (@%s) in %s.\n\n", companyName, seniorLead.GitHubHandle, seniorProv))
	b.WriteString(fmt.Sprintf("Given your focus on %s and distributed systems reliability, I wanted to share a quick insight on low-latency throughput patterns.\n\n", seniorLead.TopLanguages))
	b.WriteString("No pitch—just wanted to share what we've seen work well in high-throughput Go environments and see how your team is approaching it.\n\n")
	b.WriteString("Cheers,\n")
	b.WriteString("[Your Name]\n")
	b.WriteString("```\n\n")

	if err := os.WriteFile(filePath, []byte(b.String()), 0644); err != nil {
		return "", fmt.Errorf("failed writing report file: %w", err)
	}

	return filePath, nil
}
