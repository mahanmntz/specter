package signals

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// enrichLeadProfile queries public GitHub user profile and extracts bio, website, and LinkedIn.
func (m *GitMiner) enrichLeadProfile(ctx context.Context, lead *EngineeringLead, companyName string) {
	if lead.GitHubHandle == "" {
		lead.LinkedInURL = generateOSINTLinkedIn(lead.Name, companyName, lead.Domain)
		return
	}

	handle := lead.GitHubHandle

	// If no tokens configured, skip GitHub user profile API calls to preserve unauthenticated rate limits
	if m.rotator == nil || m.rotator.Count() == 0 {
		cleanHandle := strings.TrimPrefix(handle, "@")
		if !strings.ContainsAny(cleanHandle, "._-") && len(cleanHandle) >= 3 && len(cleanHandle) <= 20 {
			lead.LinkedInURL = "https://www.linkedin.com/in/" + cleanHandle
		} else {
			lead.LinkedInURL = generateOSINTLinkedIn(lead.Name, companyName, lead.Domain)
		}
		return
	}

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

	// 1. Extract direct LinkedIn from blog or bio
	searchBlob := profile.Blog + " " + profile.Bio
	if match := linkedInRegex.FindStringSubmatch(searchBlob); len(match) >= 2 {
		lead.LinkedInURL = "https://www.linkedin.com/in/" + strings.Trim(match[1], "/")
	}

	// 2. Query GitHub social accounts API: /users/{username}/social_accounts
	if lead.LinkedInURL == "" && handle != "" {
		socialAPI := fmt.Sprintf("https://api.github.com/users/%s/social_accounts", handle)
		if sRes, sErr := m.fetcher.FetchWithHeaders(ctx, socialAPI, m.authHeaders()); sErr == nil && sRes.StatusCode == http.StatusOK {
			type socialAccount struct {
				Provider string `json:"provider"`
				URL      string `json:"url"`
			}
			var accounts []socialAccount
			if json.Unmarshal(sRes.Body, &accounts) == nil {
				for _, acc := range accounts {
					if strings.Contains(strings.ToLower(acc.Provider), "linkedin") || strings.Contains(strings.ToLower(acc.URL), "linkedin.com/in") {
						lead.LinkedInURL = acc.URL
						break
					}
				}
			}
		}
	}

	// 3. Fallback: if personal handle, provide direct candidate link or OSINT search
	if lead.LinkedInURL == "" {
		cleanHandle := strings.TrimPrefix(handle, "@")
		if !strings.ContainsAny(cleanHandle, "._-") && len(cleanHandle) >= 3 && len(cleanHandle) <= 20 {
			lead.LinkedInURL = "https://www.linkedin.com/in/" + cleanHandle
		} else {
			lead.LinkedInURL = generateOSINTLinkedIn(lead.Name, companyName, lead.Domain)
		}
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
