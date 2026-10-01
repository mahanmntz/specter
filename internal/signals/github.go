package signals

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"specter/internal/crawler"
)

// Lead represents a discovered engineering candidate, contributor, or technical contact.
type Lead struct {
	ID             int64     `json:"id,omitempty"`
	CompanyID      int64     `json:"company_id,omitempty"`
	Name           string    `json:"name"`
	Email          string    `json:"email,omitempty"`
	GitHubUsername string    `json:"github_username"`
	ProfileURL     string    `json:"profile_url"`
	RoleTitle      string    `json:"role_title"`
	Source         string    `json:"source"`
	Contacted      bool      `json:"contacted"`
	DiscoveredAt   time.Time `json:"discovered_at"`
}

// GitHubMiner inspects public organization repositories and commit author logs.
type GitHubMiner struct {
	fetcher *crawler.Fetcher
	token   string
}

// NewGitHubMiner initializes the miner with optional GitHub Personal Access Token.
func NewGitHubMiner(f *crawler.Fetcher, token string) *GitHubMiner {
	return &GitHubMiner{
		fetcher: f,
		token:   strings.TrimSpace(token),
	}
}

type ghOrgInfo struct {
	Name    string `json:"name"`
	Blog    string `json:"blog"`
	Email   string `json:"email"`
	Twitter string `json:"twitter_username"`
}

type ghRepoItem struct {
	Name        string `json:"name"`
	Fork        bool   `json:"fork"`
	Language    string `json:"language"`
	Description string `json:"description"`
}

type ghCommitEntry struct {
	SHA    string `json:"sha"`
	Commit struct {
		Author struct {
			Name  string `json:"name"`
			Email string `json:"email"`
			Date  string `json:"date"`
		} `json:"author"`
		Message string `json:"message"`
	} `json:"commit"`
	Author *struct {
		Login     string `json:"login"`
		HTMLURL   string `json:"html_url"`
		AvatarURL string `json:"avatar_url"`
	} `json:"author"`
}

// DiscoverLeads mines technical contributors from the company's public repositories.
func (g *GitHubMiner) DiscoverLeads(ctx context.Context, org string) ([]Lead, error) {
	if org == "" {
		return nil, nil
	}

	headers := make(map[string]string)
	if g.token != "" {
		headers["Authorization"] = "Bearer " + g.token
	}

	// 1. Fetch public repositories
	reposAPI := fmt.Sprintf("https://api.github.com/orgs/%s/repos?type=public&sort=pushed&per_page=15", org)
	res, err := g.fetcher.FetchWithHeaders(ctx, reposAPI, headers)
	if err != nil {
		return nil, fmt.Errorf("failed fetching GitHub repos for org %s: %w", org, err)
	}

	if res.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("GitHub organization %s not found", org)
	}
	if res.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("GitHub API rate limit exceeded (set GITHUB_TOKEN environment variable)")
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", res.StatusCode)
	}

	var repos []ghRepoItem
	if err := json.Unmarshal(res.Body, &repos); err != nil {
		return nil, fmt.Errorf("failed parsing GitHub repos: %w", err)
	}

	leadsMap := make(map[string]Lead)

	// 2. Identify backend-pertinent repositories
	for _, repo := range repos {
		if repo.Fork {
			continue
		}

		lang := strings.ToLower(repo.Language)
		repoName := strings.ToLower(repo.Name)
		isRelevant := lang == "go" || lang == "rust" || lang == "c++" ||
			strings.Contains(repoName, "backend") ||
			strings.Contains(repoName, "server") ||
			strings.Contains(repoName, "service") ||
			strings.Contains(repoName, "infra") ||
			strings.Contains(repoName, "engine")

		if !isRelevant {
			continue
		}

		commitsAPI := fmt.Sprintf("https://api.github.com/repos/%s/%s/commits?per_page=20", org, repo.Name)
		cRes, err := g.fetcher.FetchWithHeaders(ctx, commitsAPI, headers)
		if err != nil || cRes.StatusCode != http.StatusOK {
			continue
		}

		var commits []ghCommitEntry
		if err := json.Unmarshal(cRes.Body, &commits); err != nil {
			continue
		}

		for _, c := range commits {
			name := strings.TrimSpace(c.Commit.Author.Name)
			email := strings.TrimSpace(c.Commit.Author.Email)
			username := ""
			profileURL := ""

			if c.Author != nil {
				username = c.Author.Login
				profileURL = c.Author.HTMLURL
			}

			// Filter out bots and CI accounts
			lowerName := strings.ToLower(name)
			lowerEmail := strings.ToLower(email)
			if strings.Contains(lowerName, "bot") ||
				strings.Contains(lowerName, "actions") ||
				strings.Contains(lowerEmail, "noreply") ||
				strings.Contains(lowerEmail, "dependabot") ||
				strings.Contains(lowerEmail, "greenkeeper") {
				continue
			}

			key := username
			if key == "" {
				key = email
			}
			if key == "" {
				continue
			}

			if _, exists := leadsMap[key]; !exists {
				if profileURL == "" && username != "" {
					profileURL = fmt.Sprintf("https://github.com/%s", username)
				}

				roleTitle := fmt.Sprintf("Contributor: %s", repo.Name)
				if repo.Language != "" {
					roleTitle += fmt.Sprintf(" (%s)", repo.Language)
				}

				leadsMap[key] = Lead{
					Name:           name,
					Email:          email,
					GitHubUsername: username,
					ProfileURL:     profileURL,
					RoleTitle:      roleTitle,
					Source:         fmt.Sprintf("https://github.com/%s/%s", org, repo.Name),
					Contacted:      false,
					DiscoveredAt:   time.Now().UTC(),
				}
			}
		}
	}

	var leads []Lead
	for _, lead := range leadsMap {
		leads = append(leads, lead)
	}

	return leads, nil
}
